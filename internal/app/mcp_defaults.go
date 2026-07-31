package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// dialectClaudeConfigRel is the instance-relative path of the live, Claude-Code-
// owned .claude.json — the file `cc-dialect remove` erases and that `mcp import`
// reads MCP servers out of.
var dialectClaudeConfigRel = "claude/.claude.json"

// mcpDefaultsFile is the JSON shape `--mcp-config` reads: a top-level mcpServers
// map. A dialect's .claude.json carries the same mcpServers key alongside many
// others, so the same struct decodes both — only the shared defaults file is
// owned by cc-dialect.
type mcpDefaultsFile struct {
	MCPServers map[string]map[string]any `json:"mcpServers"`
}

// loadMCPDefaults reads the shared MCP server defaults. A missing file is the
// normal pre-seed state and is not an error: the caller launches with no flag to
// inject. Any malformed file — invalid JSON, a literal null, a non-object
// mcpServers section, or a non-object server entry — is reported as an error so
// the caller can warn and omit the flag rather than feed Claude Code a bad path
// that breaks every dialect at once. Validation is strict on purpose: if a single
// valid entry alongside a malformed one were silently kept, the result would look
// non-empty and the original, still-malformed file would be passed through
// unchanged for Claude Code to reject on every launch.
func loadMCPDefaults() (mcpDefaultsFile, error) {
	path, err := defaultsMCPPath()
	if err != nil {
		return mcpDefaultsFile{}, err
	}
	data, readErr := os.ReadFile(path)
	if errors.Is(readErr, os.ErrNotExist) {
		return mcpDefaultsFile{}, nil
	}
	if readErr != nil {
		return mcpDefaultsFile{}, readErr
	}
	document := map[string]any{}
	if err := json.Unmarshal(data, &document); err != nil {
		return mcpDefaultsFile{}, fmt.Errorf("parse shared MCP defaults %q: %w", path, err)
	}
	if document == nil {
		return mcpDefaultsFile{}, fmt.Errorf("parse shared MCP defaults %q: must be a JSON object", path)
	}
	file := mcpDefaultsFile{MCPServers: map[string]map[string]any{}}
	section, hasSection := document["mcpServers"]
	if hasSection && section != nil {
		servers, ok := section.(map[string]any)
		if !ok {
			return mcpDefaultsFile{}, fmt.Errorf("parse shared MCP defaults %q: mcpServers must be an object", path)
		}
		// Sort server names so a malformed entry is reported deterministically
		// rather than depending on map iteration order.
		names := make([]string, 0, len(servers))
		for name := range servers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			server, ok := servers[name].(map[string]any)
			if !ok {
				return mcpDefaultsFile{}, fmt.Errorf("parse shared MCP defaults %q: server %q must be an object", path, name)
			}
			file.MCPServers[name] = server
		}
	}
	return file, nil
}

// writeMCPDefaults atomically replaces the shared defaults file at mode 0600,
// matching the home-level write pattern of saveConfig. The file may carry tokens
// in a server's env, so it must never be world- or group-readable.
func writeMCPDefaults(file mcpDefaultsFile) error {
	path, err := defaultsMCPPath()
	if err != nil {
		return err
	}
	if file.MCPServers == nil {
		file.MCPServers = map[string]map[string]any{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o600)
}

// formatMCPDefaults renders the shared servers for `mcp list`, sorted by name.
// env is intentionally omitted: it may hold tokens. The command prepends the
// file path so the user knows what to edit.
func formatMCPDefaults(file mcpDefaultsFile) string {
	names := make([]string, 0, len(file.MCPServers))
	for name := range file.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "No shared MCP servers configured."
	}
	var b strings.Builder
	for _, name := range names {
		server := file.MCPServers[name]
		fmt.Fprintln(&b, name)
		if t, ok := server["type"].(string); ok && t != "" {
			fmt.Fprintf(&b, "  type:    %s\n", t)
		}
		if cmd, ok := server["command"].(string); ok && cmd != "" {
			fmt.Fprintf(&b, "  command: %s\n", cmd)
		}
		if args := server["args"]; args != nil {
			fmt.Fprintf(&b, "  args:    %s\n", formatMCPArgs(args))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatMCPArgs renders a server's args as a space-joined command tail. It
// accepts the []any shape JSON decodes into as well as any scalar fallback.
func formatMCPArgs(args any) string {
	list, ok := args.([]any)
	if !ok {
		return fmt.Sprintf("%v", args)
	}
	parts := make([]string, 0, len(list))
	for _, item := range list {
		parts = append(parts, fmt.Sprintf("%v", item))
	}
	return strings.Join(parts, " ")
}

// readDialectMCPServers pulls the mcpServers section out of a dialect's live
// .claude.json through that dialect's own root, so the read stays confined. A
// missing file — a fresh dialect Claude Code has not yet written — yields no
// servers and no error; import then has nothing to copy.
func readDialectMCPServers(name string) (map[string]map[string]any, error) {
	instance, err := openInstanceFS(name)
	if err != nil {
		return nil, err
	}
	defer instance.Close()
	data, readErr := instance.ReadFile(dialectClaudeConfigRel)
	if errors.Is(readErr, os.ErrNotExist) {
		return map[string]map[string]any{}, nil
	}
	if readErr != nil {
		return nil, readErr
	}
	document := map[string]any{}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s for %q: %w", dialectClaudeConfigRel, name, err)
	}
	servers := map[string]map[string]any{}
	if section, ok := document["mcpServers"].(map[string]any); ok {
		for serverName, config := range section {
			if server, ok := config.(map[string]any); ok {
				servers[serverName] = server
			}
		}
	}
	return servers, nil
}

// importMCPServers copies a dialect's mcpServers into the shared defaults,
// merging by server name. An entry already in the shared defaults is left
// untouched unless force is set, so an accidental re-import cannot silently
// overwrite a shared configuration with one dialect's local copy. It reports the
// servers it added and the ones it declined to overwrite.
func importMCPServers(name string, force bool) (added, skipped []string, err error) {
	// The shared defaults file is updated by a read-modify-write: load, merge,
	// write back. Two concurrent imports that each snapshot the same defaults and
	// apply different servers would have the last writer silently discard the
	// other's import, so the whole transaction runs under withStateLock — the
	// same cross-process lock the rest of the state machine already uses.
	err = withStateLock(func() error {
		servers, err := readDialectMCPServers(name)
		if err != nil {
			return err
		}
		defaults, err := loadMCPDefaults()
		if err != nil {
			return err
		}
		if defaults.MCPServers == nil {
			defaults.MCPServers = map[string]map[string]any{}
		}
		// Sort for deterministic added/skipped order across map iteration.
		names := make([]string, 0, len(servers))
		for serverName := range servers {
			names = append(names, serverName)
		}
		sort.Strings(names)
		for _, serverName := range names {
			if _, exists := defaults.MCPServers[serverName]; exists && !force {
				skipped = append(skipped, serverName)
				continue
			}
			defaults.MCPServers[serverName] = servers[serverName]
			added = append(added, serverName)
		}
		if len(added) == 0 {
			return nil
		}
		return writeMCPDefaults(defaults)
	})
	if err != nil {
		return nil, nil, err
	}
	return added, skipped, nil
}

// sharedMCPConfigArgs returns the `--mcp-config <path>` pair to prepend to
// Claude Code's arguments when shared defaults define at least one server, or
// nil when there is nothing to inject. A caller that already passed
// --mcp-config is left in control: merge semantics mean their file layers on
// top, and prepending ours would duplicate the flag. A malformed or unreadable
// defaults file yields nil plus an error so the caller can warn and proceed
// without the flag — passing a broken path would break every dialect at once.
//
// --strict-mcp-config is deliberately never emitted: merge, not replace, is the
// whole reason the defaults live outside the instance directory.
func sharedMCPConfigArgs(claudeArgs []string) ([]string, error) {
	if hasFlag(claudeArgs, "--mcp-config") {
		return nil, nil
	}
	defaults, err := loadMCPDefaults()
	if err != nil {
		return nil, err
	}
	if len(defaults.MCPServers) == 0 {
		return nil, nil
	}
	path, err := defaultsMCPPath()
	if err != nil {
		return nil, err
	}
	return []string{"--mcp-config", path}, nil
}

// warnMCPDefaults reports a failed defaults load without failing the launch —
// shared MCP servers are a convenience, not a launch requirement.
func warnMCPDefaults(err error) {
	fmt.Fprintf(os.Stderr, "Warning: shared MCP defaults not applied: %v\n", err)
}

// mcpDefaultsSummary is doctor's global line on the shared defaults file: a
// missing or empty file is the pre-seed norm and is silent; servers present
// report a count; an unreadable file is the actionable problem, since launch
// skips it and no dialect would then carry the shared servers.
func mcpDefaultsSummary(defaults mcpDefaultsFile, loadErr error) string {
	path, err := defaultsMCPPath()
	if err != nil {
		return ""
	}
	if loadErr != nil {
		return fmt.Sprintf("✗ shared MCP defaults at %s are unreadable and will be skipped on launch: %v", path, loadErr)
	}
	if len(defaults.MCPServers) == 0 {
		return ""
	}
	return fmt.Sprintf("✓ shared MCP defaults: %d server(s) merged into every dialect via --mcp-config (%s)", len(defaults.MCPServers), path)
}

// mcpDefaultsDuplicateDiagnostic flags a dialect whose .claude.json redefines a
// server the shared defaults already provide. With --mcp-config merging on
// launch, the local copy is redundant: whichever side wins, the server is now
// managed in two places, so the wording avoids claiming a precedence winner
// (which is undocumented and may differ between Claude Code versions).
func mcpDefaultsDuplicateDiagnostic(name string, shared map[string]map[string]any) string {
	if len(shared) == 0 {
		return ""
	}
	local, err := readDialectMCPServers(name)
	if err != nil || len(local) == 0 {
		return ""
	}
	var duplicates []string
	for serverName := range local {
		if _, ok := shared[serverName]; ok {
			duplicates = append(duplicates, serverName)
		}
	}
	sort.Strings(duplicates)
	if len(duplicates) == 0 {
		return ""
	}
	return fmt.Sprintf("○ %s also defines %s locally; the shared default is merged on launch, so the local copy is redundant (edit %s's .claude.json, or run `claude mcp remove` inside it, to drop the duplicate)",
		name, strings.Join(duplicates, ", "), name)
}

// mcpCommand manages the shared MCP server defaults file: `import` copies a
// dialect's mcpServers into it, `list` shows them (without env values), and
// `path` prints the file location for manual editing.
func mcpCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("mcp requires a subcommand: import <dialect> | list | path\n\n%s", usage)
	}
	switch args[0] {
	case "import":
		// --force may appear before or after the dialect name. Go's flag package
		// stops at the first positional argument, which would reject the documented
		// `mcp import <dialect> --force` order, so the args are scanned manually.
		force := false
		var positional []string
		for _, a := range args[1:] {
			switch {
			case a == "--force" || a == "-force" || a == "--force=true" || a == "-force=true":
				force = true
			case a == "--force=false" || a == "-force=false":
				force = false
			case strings.HasPrefix(a, "-"):
				return fmt.Errorf("unknown mcp import flag %q", a)
			default:
				positional = append(positional, a)
			}
		}
		if len(positional) != 1 {
			return errors.New("mcp import requires a dialect name")
		}
		name := positional[0]
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if _, ok := cfg.Dialects[name]; !ok {
			return fmt.Errorf("dialect %q does not exist", name)
		}
		added, skipped, err := importMCPServers(name, force)
		if err != nil {
			return err
		}
		path, err := defaultsMCPPath()
		if err != nil {
			return err
		}
		if len(added) > 0 {
			fmt.Printf("Imported %d server(s) from %s into %s:\n  %s\n", len(added), name, path, strings.Join(added, "\n  "))
		} else {
			fmt.Printf("No new servers to import from %s.\n", name)
		}
		if len(skipped) > 0 {
			fmt.Printf("Skipped (already shared; use --force to overwrite):\n  %s\n", strings.Join(skipped, "\n  "))
		}
		return nil
	case "list":
		defaults, err := loadMCPDefaults()
		if err != nil {
			return err
		}
		path, err := defaultsMCPPath()
		if err != nil {
			return err
		}
		fmt.Println(path)
		fmt.Println(formatMCPDefaults(defaults))
		return nil
	case "path":
		path, err := defaultsMCPPath()
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	default:
		return fmt.Errorf("unknown mcp subcommand %q\n\n%s", args[0], usage)
	}
}
