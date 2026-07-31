package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDefaultsFile writes the shared defaults/mcp.json directly under home,
// mirroring the on-disk shape cc-dialect owns outside any instance directory.
func writeDefaultsFile(t *testing.T, home, content string) {
	t.Helper()
	path := filepath.Join(home, "defaults", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeDialectClaudeJSON writes the live, Claude-Code-owned .claude.json for a
// dialect — the file `cc-dialect remove` erases and that `mcp import` reads.
func writeDialectClaudeJSON(t *testing.T, home, name, content string) string {
	t.Helper()
	path := filepath.Join(home, "instances", name, "claude", ".claude.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultsMCPPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	path, err := defaultsMCPPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "defaults", "mcp.json"); path != want {
		t.Fatalf("defaultsMCPPath = %q, want %q", path, want)
	}
}

// A missing defaults file is the common case before a user has added anything:
// loading it must not be an error, and launch must have nothing to inject.
func TestLoadMCPDefaultsMissingFileIsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	defaults, err := loadMCPDefaults()
	if err != nil {
		t.Fatalf("missing defaults file should not error: %v", err)
	}
	if len(defaults.MCPServers) != 0 {
		t.Fatalf("missing defaults file should load empty, got %#v", defaults.MCPServers)
	}
}

func TestLoadMCPDefaultsParsesServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":{"devflow":{"type":"stdio","command":"npx","args":["-y","devflow"],"env":{"TOKEN":"secret"}}}}`)
	defaults, err := loadMCPDefaults()
	if err != nil {
		t.Fatal(err)
	}
	server, ok := defaults.MCPServers["devflow"]
	if !ok {
		t.Fatalf("devflow server not loaded: %#v", defaults.MCPServers)
	}
	if server["command"] != "npx" {
		t.Fatalf("command = %#v, want npx", server["command"])
	}
}

// A malformed defaults file must not break every dialect: load reports the
// error so the caller can omit the flag and warn.
func TestLoadMCPDefaultsRejectsMalformed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, "{not json")
	if _, err := loadMCPDefaults(); err == nil {
		t.Fatal("malformed defaults file should error")
	}
}

// A literal null unmarshals into a zero value without error; load must reject it
// rather than silently report an empty-but-present file.
func TestLoadMCPDefaultsRejectsNull(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, "null\n")
	if _, err := loadMCPDefaults(); err == nil {
		t.Fatal("null defaults file should error")
	}
}

// A non-object mcpServers section is malformed, not empty: returning an error
// keeps the file off every launch instead of silently dropping every server.
func TestLoadMCPDefaultsRejectsMalformedSection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":[]}`)
	if _, err := loadMCPDefaults(); err == nil {
		t.Fatal("non-object mcpServers section should error")
	}
}

// A malformed server entry must fail the whole load, even alongside a valid one:
// silently keeping the valid entry would leave a non-empty result, and the
// original file — still malformed — would then be passed through to Claude Code.
func TestLoadMCPDefaultsRejectsMalformedEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":{"good":{"command":"x"},"bad":"not-an-object"}}`)
	if _, err := loadMCPDefaults(); err == nil {
		t.Fatal("a malformed server entry should error even with a valid sibling")
	}
}

// The defaults file may carry tokens in env, so it must be created 0600 and
// survive a round-trip through load.
func TestWriteMCPDefaultsCreates0600(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	file := mcpDefaultsFile{MCPServers: map[string]map[string]any{
		"devflow": {"type": "stdio", "command": "npx"},
	}}
	if err := writeMCPDefaults(file); err != nil {
		t.Fatal(err)
	}
	path, _ := defaultsMCPPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	defaults, err := loadMCPDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.MCPServers["devflow"]["command"] != "npx" {
		t.Fatalf("written defaults not readable: %#v", defaults.MCPServers)
	}
}

// `mcp list` shows servers without their env values — env may hold tokens.
func TestFormatMCPDefaultsHidesEnv(t *testing.T) {
	file := mcpDefaultsFile{MCPServers: map[string]map[string]any{
		"devflow": {
			"type":    "stdio",
			"command": "npx",
			"args":    []any{"-y", "devflow"},
			"env":     map[string]any{"TOKEN": "supersecret"},
		},
	}}
	out := formatMCPDefaults(file)
	if !strings.Contains(out, "devflow") || !strings.Contains(out, "npx") {
		t.Fatalf("expected server name and command in output:\n%s", out)
	}
	if strings.Contains(out, "supersecret") {
		t.Fatalf("env secret leaked into list output:\n%s", out)
	}
	if strings.Contains(out, "TOKEN") {
		t.Fatalf("env key leaked into list output:\n%s", out)
	}
}

func TestFormatMCPDefaultsEmpty(t *testing.T) {
	out := formatMCPDefaults(mcpDefaultsFile{})
	if strings.TrimSpace(out) == "" {
		t.Fatalf("empty list output should explain the state, got %q", out)
	}
}

// readDialectMCPServers pulls mcpServers out of a dialect's .claude.json
// without touching the rest of the live file.
func TestReadDialectMCPServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"devflow":{"type":"stdio","command":"npx"}},"other":"kept"}`)
	servers, err := readDialectMCPServers("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	if servers["devflow"]["command"] != "npx" {
		t.Fatalf("devflow not read: %#v", servers)
	}
}

func TestReadDialectMCPServersMissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "instances", "cc-test", "claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	servers, err := readDialectMCPServers("cc-test")
	if err != nil {
		t.Fatalf("missing .claude.json should not error: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("expected empty, got %#v", servers)
	}
}

func TestReadDialectMCPServersRejectsInvalidName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if _, err := readDialectMCPServers("../evil"); err == nil {
		t.Fatal("invalid name should error")
	}
}

// import merges new servers by name and leaves existing shared entries alone.
func TestImportMCPServersMerges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":{"existing":{"command":"old"}}}`)
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"devflow":{"command":"npx"}}}`)
	added, skipped, err := importMCPServers("cc-test", false)
	if err != nil {
		t.Fatal(err)
	}
	defaults, _ := loadMCPDefaults()
	if defaults.MCPServers["devflow"]["command"] != "npx" {
		t.Fatalf("import did not add devflow: %#v", defaults.MCPServers)
	}
	if defaults.MCPServers["existing"]["command"] != "old" {
		t.Fatalf("import clobbered existing: %#v", defaults.MCPServers)
	}
	if !sliceContains(added, "devflow") {
		t.Fatalf("added = %v, want devflow", added)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
}

// A shared entry must not be overwritten by a dialect's local copy without
// --force; the conflict is reported, not silently lost.
func TestImportMCPServersRefusesClobberWithoutForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":{"devflow":{"command":"existing"}}}`)
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"devflow":{"command":"new"}}}`)
	added, skipped, err := importMCPServers("cc-test", false)
	if err != nil {
		t.Fatalf("clobber refusal should not be a hard error: %v", err)
	}
	defaults, _ := loadMCPDefaults()
	if defaults.MCPServers["devflow"]["command"] != "existing" {
		t.Fatalf("import overwrote without --force: %#v", defaults.MCPServers)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v, want none on conflict", added)
	}
	if !sliceContains(skipped, "devflow") {
		t.Fatalf("skipped = %v, want devflow", skipped)
	}
}

func TestImportMCPServersOverwritesWithForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":{"devflow":{"command":"existing"}}}`)
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"devflow":{"command":"new"}}}`)
	if _, _, err := importMCPServers("cc-test", true); err != nil {
		t.Fatal(err)
	}
	defaults, _ := loadMCPDefaults()
	if defaults.MCPServers["devflow"]["command"] != "new" {
		t.Fatalf("--force did not overwrite: %#v", defaults.MCPServers)
	}
}

// sharedMCPConfigArgs decides what (if anything) to prepend for --mcp-config.
func TestSharedMCPConfigArgsMissingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	args, err := sharedMCPConfigArgs(nil)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if args != nil {
		t.Fatalf("missing file should inject nothing, got %v", args)
	}
}

func TestSharedMCPConfigArgsInjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":{"devflow":{"command":"npx"}}}`)
	args, err := sharedMCPConfigArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := defaultsMCPPath()
	if want := []string{"--mcp-config", path}; !equalStrings(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

// A caller that passes --mcp-config is the whole point of merge semantics: their
// value wins and we add nothing, rather than duplicating the flag.
func TestSharedMCPConfigArgsRespectsExistingFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":{"devflow":{"command":"npx"}}}`)
	args, err := sharedMCPConfigArgs([]string{"--mcp-config", "/custom.json"})
	if err != nil {
		t.Fatal(err)
	}
	if args != nil {
		t.Fatalf("existing --mcp-config should suppress injection, got %v", args)
	}
}

func TestSharedMCPConfigArgsSkipsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":{}}`)
	args, err := sharedMCPConfigArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if args != nil {
		t.Fatalf("empty defaults should inject nothing, got %v", args)
	}
}

func TestSharedMCPConfigArgsMalformedIsOmitted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, "{not json")
	args, err := sharedMCPConfigArgs(nil)
	if err == nil {
		t.Fatal("malformed file should error")
	}
	if args != nil {
		t.Fatalf("malformed file should inject nothing, got %v", args)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// mcpCommand is the user-facing surface over the shared defaults. These cover the
// three subcommands and the guard rails (unknown dialect, missing name).
func TestMCPPathCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	out := captureStdout(t, func() error { return mcpCommand([]string{"path"}) })
	path, _ := defaultsMCPPath()
	if !strings.Contains(out, path) {
		t.Fatalf("mcp path output = %q, want it to contain %q", out, path)
	}
}

func TestMCPListCommandHidesEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDefaultsFile(t, home, `{"mcpServers":{"devflow":{"type":"stdio","command":"npx","args":["-y","devflow"],"env":{"TOKEN":"supersecret"}}}}`)
	out := captureStdout(t, func() error { return mcpCommand([]string{"list"}) })
	if !strings.Contains(out, "devflow") || !strings.Contains(out, "npx") {
		t.Fatalf("list output should name the server and command:\n%s", out)
	}
	if strings.Contains(out, "supersecret") {
		t.Fatalf("list output leaked an env secret:\n%s", out)
	}
}

func TestMCPImportCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	base := availablePortRange(t, 2)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := newAppService().CreateDialect(DialectInput{Name: "cc-test", Preset: "codex"}, ""); err != nil {
		t.Fatal(err)
	}
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"devflow":{"type":"stdio","command":"npx"}}}`)
	out := captureStdout(t, func() error { return mcpCommand([]string{"import", "cc-test"}) })
	if !strings.Contains(out, "cc-test") || !strings.Contains(out, "devflow") {
		t.Fatalf("import output should name the dialect and the imported server:\n%s", out)
	}
	defaults, _ := loadMCPDefaults()
	if defaults.MCPServers["devflow"]["command"] != "npx" {
		t.Fatalf("import did not persist the server: %#v", defaults.MCPServers)
	}
}

// The documented `mcp import <dialect> --force` order must work, not just the
// --force-first form Go's flag package would otherwise require.
func TestMCPImportCommandForceAfterName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	base := availablePortRange(t, 2)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := newAppService().CreateDialect(DialectInput{Name: "cc-test", Preset: "codex"}, ""); err != nil {
		t.Fatal(err)
	}
	// A conflicting shared entry that --force must overwrite.
	writeDefaultsFile(t, home, `{"mcpServers":{"devflow":{"command":"existing"}}}`)
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"devflow":{"command":"npx"}}}`)
	if err := mcpCommand([]string{"import", "cc-test", "--force"}); err != nil {
		t.Fatalf("trailing --force should be accepted: %v", err)
	}
	defaults, _ := loadMCPDefaults()
	if defaults.MCPServers["devflow"]["command"] != "npx" {
		t.Fatalf("trailing --force did not overwrite: %#v", defaults.MCPServers)
	}
}

func TestMCPImportCommandUnknownDialect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	base := availablePortRange(t, 2)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	if err := mcpCommand([]string{"import", "nope"}); err == nil {
		t.Fatal("import of an unknown dialect should error")
	}
}

func TestMCPImportCommandRequiresName(t *testing.T) {
	if err := mcpCommand([]string{"import"}); err == nil {
		t.Fatal("import without a dialect name should error")
	}
}

func TestMCPCommandRequiresSubcommand(t *testing.T) {
	if err := mcpCommand(nil); err == nil {
		t.Fatal("mcp with no subcommand should error")
	}
	if err := mcpCommand([]string{"bogus"}); err == nil {
		t.Fatal("mcp with an unknown subcommand should error")
	}
}

// mcpDefaultsSummary is doctor's global line on the shared defaults file.
func TestMCPDefaultsSummary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	// Missing file is the pre-seed norm: silent.
	defaults, err := loadMCPDefaults()
	if line := mcpDefaultsSummary(defaults, err); line != "" {
		t.Fatalf("missing file should be silent, got %q", line)
	}
	// Present servers report a count.
	writeDefaultsFile(t, home, `{"mcpServers":{"a":{"command":"x"},"b":{"command":"y"}}}`)
	defaults, err = loadMCPDefaults()
	if line := mcpDefaultsSummary(defaults, err); !strings.Contains(line, "2") || !strings.Contains(line, "✓") {
		t.Fatalf("summary should report 2 servers with a check: %q", line)
	}
	// Malformed file is the actionable problem.
	writeDefaultsFile(t, home, "{bad")
	defaults, err = loadMCPDefaults()
	if line := mcpDefaultsSummary(defaults, err); !strings.Contains(line, "✗") || !strings.Contains(line, "unreadable") {
		t.Fatalf("summary should flag an unreadable file: %q", line)
	}
}

// mcpDefaultsDuplicateDiagnostic flags a dialect redefining a shared server.
func TestMCPDefaultsDuplicateDiagnostic(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	shared := map[string]map[string]any{"devflow": {"command": "npx"}}
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"devflow":{"command":"npx"},"other":{"command":"z"}}}`)
	line := mcpDefaultsDuplicateDiagnostic("cc-test", shared)
	if !strings.Contains(line, "cc-test") || !strings.Contains(line, "devflow") || !strings.Contains(line, "redundant") {
		t.Fatalf("duplicate hint should name the dialect, the server, and redundancy: %q", line)
	}
	if strings.Contains(line, "other") {
		t.Fatalf("hint should not list the non-overlapping server: %q", line)
	}
}

func TestMCPDefaultsDuplicateDiagnosticEmptyShared(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"devflow":{"command":"npx"}}}`)
	if line := mcpDefaultsDuplicateDiagnostic("cc-test", nil); line != "" {
		t.Fatalf("empty shared defaults should be silent, got %q", line)
	}
}

func TestMCPDefaultsDuplicateDiagnosticNoOverlap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	shared := map[string]map[string]any{"devflow": {"command": "npx"}}
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"other":{"command":"z"}}}`)
	if line := mcpDefaultsDuplicateDiagnostic("cc-test", shared); line != "" {
		t.Fatalf("no overlap should be silent, got %q", line)
	}
}

// End-to-end: doctor surfaces the shared-defaults summary and flags a dialect
// that redefines a shared server.
func TestDoctorReportsSharedMCPDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	base := availablePortRange(t, 2)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := newAppService().CreateDialect(DialectInput{Name: "cc-test", Preset: "codex"}, ""); err != nil {
		t.Fatal(err)
	}
	writeDefaultsFile(t, home, `{"mcpServers":{"devflow":{"command":"npx"}}}`)
	writeDialectClaudeJSON(t, home, "cc-test", `{"mcpServers":{"devflow":{"command":"npx"}}}`)
	report := captureStdout(t, func() error { return doctor(nil, "test") })
	if !strings.Contains(report, "shared MCP defaults: 1 server") {
		t.Fatalf("doctor did not report the shared defaults summary:\n%s", report)
	}
	if !strings.Contains(report, "cc-test also defines devflow locally") {
		t.Fatalf("doctor did not flag the duplicated server:\n%s", report)
	}
}

func sliceContains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
