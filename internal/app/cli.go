package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
)

const usage = `Claude Dialects — create native Claude Code runners for any model

Usage:
  cc-dialect create <name> --preset <preset> [options]
  cc-dialect run <name> [--] [claude arguments...]
  cc-dialect list
  cc-dialect show <name>
  cc-dialect remove <name>
  cc-dialect detect [preset-or-provider] [--running] [--json] [--quiet]
  cc-dialect models <name>
  cc-dialect presets
  cc-dialect auth <dialect> <codex|claude|kimi|antigravity|xai> [--no-browser]
  cc-dialect cursor <install|status|models>
  cc-dialect shim install <dialect> [--name <command>] [--dir <path>]
  cc-dialect native install <command> [--dangerous] [--dir <path>]
  cc-dialect proxy <dialect> <start|stop|status|logs>
  cc-dialect doctor

Example:
  cc-dialect create cc-codex --preset codex-sol
  cc-dialect auth cc-codex codex
  cc-dialect shim install cc-codex
  cc-codex
`

func Run(args []string, version string) error {
	if len(args) > 0 && args[0] == "__proxy" {
		if len(args) != 2 {
			return errors.New("missing embedded proxy instance name")
		}
		return runEmbeddedProxy(args[1])
	}
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(usage)
	case "version", "--version":
		fmt.Printf("cc-dialect %s (embedded CLIProxyAPI %s)\n", version, embeddedProxyVersion())
	case "presets":
		fmt.Println(strings.Join(presetNames(), "\n"))
	case "create":
		return createDialect(args[1:])
	case "list":
		return listDialects()
	case "show":
		return showDialect(args[1:])
	case "models":
		return listModels(args[1:])
	case "remove":
		return removeDialect(args[1:])
	case "detect":
		return detectCommand(args[1:])
	case "run":
		return runDialect(args[1:])
	case "auth":
		return authCommand(args[1:])
	case "cursor":
		return cursorCommand(args[1:])
	case "proxy":
		return proxyCommand(args[1:])
	case "shim":
		return shimCommand(args[1:])
	case "native":
		return nativeCommand(args[1:])
	case "doctor":
		return doctor()
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
	return nil
}

func embeddedProxyVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return proxyVersionFromBuildInfo(info)
}

func proxyVersionFromBuildInfo(info *debug.BuildInfo) string {
	const module = "github.com/router-for-me/CLIProxyAPI/v7"
	for _, dependency := range info.Deps {
		if dependency.Path != module {
			continue
		}
		if dependency.Replace != nil && dependency.Replace.Version != "" {
			return dependency.Replace.Version
		}
		if dependency.Version != "" {
			return dependency.Version
		}
	}
	return "unknown"
}

func createDialect(args []string) error {
	if len(args) == 0 || !validName(args[0]) {
		return errors.New("create requires a lowercase command name (letters, numbers, - or _)")
	}
	name := args[0]
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	preset := fs.String("preset", "", "starting preset")
	model := fs.String("model", "", "main model")
	subagent := fs.String("subagent-model", "", "subagent model")
	opusModel := fs.String("opus-model", "", "model selected by /model opus")
	sonnetModel := fs.String("sonnet-model", "", "model selected by /model sonnet")
	haikuModel := fs.String("haiku-model", "", "model selected by /model haiku")
	effortLevel := fs.String("effort-level", "", "initial effort: auto, low, medium, high, xhigh, or max")
	concurrency := fs.Int("concurrency", 0, "maximum tool concurrency")
	port := fs.Int("port", 0, "isolated proxy port (allocated automatically by default)")
	bridgePort := fs.Int("bridge-port", 0, "isolated provider bridge port (allocated automatically by default)")
	baseURL := fs.String("base-url", "", "Anthropic-compatible upstream URL routed through the embedded proxy")
	tokenEnv := fs.String("token-env", "", "environment variable containing the upstream API token")
	effort := fs.Bool("effort", true, "enable Claude Code effort")
	toolSearch := fs.Bool("tool-search", false, "enable Claude Code tool search")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	var dialect Dialect
	if *preset != "" {
		var ok bool
		dialect, ok = presets[*preset]
		if !ok {
			return fmt.Errorf("unknown preset %q (available: %s)", *preset, strings.Join(presetNames(), ", "))
		}
		dialect.Preset = *preset
	}
	if *model != "" {
		dialect.Model = *model
	}
	if dialect.Model == "" {
		return errors.New("choose --preset or provide --model")
	}
	if *subagent != "" {
		dialect.SubagentModel = *subagent
	}
	if dialect.SubagentModel == "" {
		dialect.SubagentModel = dialect.Model
	}
	if *opusModel != "" {
		dialect.OpusModel = *opusModel
	}
	if *sonnetModel != "" {
		dialect.SonnetModel = *sonnetModel
	}
	if *haikuModel != "" {
		dialect.HaikuModel = *haikuModel
	}
	if dialect.OpusModel == "" {
		dialect.OpusModel = dialect.Model
	}
	if dialect.SonnetModel == "" {
		dialect.SonnetModel = dialect.Model
	}
	if dialect.HaikuModel == "" {
		dialect.HaikuModel = dialect.Model
	}
	if *effortLevel != "" {
		dialect.EffortLevel = *effortLevel
	}
	if dialect.EffortLevel == "" {
		dialect.EffortLevel = "auto"
	}
	if !validEffort(dialect.EffortLevel) {
		return fmt.Errorf("invalid effort level %q", dialect.EffortLevel)
	}
	if *concurrency != 0 {
		dialect.Concurrency = *concurrency
	}
	if dialect.Concurrency == 0 {
		dialect.Concurrency = 3
	}
	if *baseURL != "" {
		dialect.BaseURL = *baseURL
	}
	if *tokenEnv != "" {
		dialect.AuthTokenEnv = *tokenEnv
	}
	dialect.Effort = *effort
	dialect.ToolSearch = *toolSearch
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	_, updating := cfg.Dialects[name]
	if existing, ok := cfg.Dialects[name]; ok {
		dialect.Port = existing.Port
		dialect.APIKey = existing.APIKey
		if dialect.Bridge != "" && existing.Bridge == dialect.Bridge {
			dialect.BridgePort = existing.BridgePort
		}
	} else {
		dialect.Port = nextPort(cfg)
		if dialect.Port == 0 {
			return errors.New("no proxy ports available")
		}
		dialect.APIKey, err = newAPIKey()
		if err != nil {
			return err
		}
	}
	if *port != 0 {
		if *port < 1024 || *port > 65535 {
			return errors.New("--port must be between 1024 and 65535")
		}
		for otherName, other := range cfg.Dialects {
			if otherName != name && (other.Port == *port || other.BridgePort == *port) {
				return fmt.Errorf("port %d is already reserved by %q", *port, otherName)
			}
		}
		if existing, ok := cfg.Dialects[name]; !ok || existing.Port != *port {
			if !portAvailable(*port) {
				return fmt.Errorf("port %d is already in use by another process", *port)
			}
		}
		if dialect.Port != *port {
			dialect.Port = *port
		}
	}
	if dialect.Bridge != "" && dialect.BridgePort == 0 {
		dialect.BridgePort = nextAvailablePort(cfg, map[int]bool{dialect.Port: true})
		if dialect.BridgePort == 0 {
			return errors.New("no provider bridge ports available")
		}
	}
	if *bridgePort != 0 {
		if dialect.Bridge == "" {
			return errors.New("--bridge-port requires a preset with a managed provider bridge")
		}
		if *bridgePort < 1024 || *bridgePort > 65535 {
			return errors.New("--bridge-port must be between 1024 and 65535")
		}
		if *bridgePort == dialect.Port {
			return errors.New("--bridge-port must differ from --port")
		}
		for otherName, other := range cfg.Dialects {
			if otherName != name && (other.Port == *bridgePort || other.BridgePort == *bridgePort) {
				return fmt.Errorf("bridge port %d is already reserved by %q", *bridgePort, otherName)
			}
		}
		if existing, ok := cfg.Dialects[name]; !ok || existing.BridgePort != *bridgePort {
			if !portAvailable(*bridgePort) {
				return fmt.Errorf("bridge port %d is already in use by another process", *bridgePort)
			}
		}
		dialect.BridgePort = *bridgePort
	}
	if dialect.BridgePort == dialect.Port {
		return fmt.Errorf("proxy and provider bridge cannot share port %d", dialect.Port)
	}
	if updating {
		_ = stopProxy(name)
	}
	cfg.Dialects[name] = dialect
	if err = saveConfig(cfg); err != nil {
		return err
	}
	if updating {
		fmt.Printf("Updated %q to model %s (isolated port %d)\n", name, dialect.Model, dialect.Port)
		fmt.Println("Authentication, isolated Claude Code state, and installed shims were preserved.")
		if step := missingAuthenticationStep(name, dialect); step != "" {
			fmt.Println("Next:")
			fmt.Println("  1.", step)
		}
	} else {
		fmt.Printf("Created %q using model %s (isolated port %d)\n", name, dialect.Model, dialect.Port)
		home, _ := os.UserHomeDir()
		shimName := preferredShimName(name)
		target := filepath.Join(home, ".local", "bin", shimName)
		if alias, found := zshAlias(shimName); found {
			fmt.Printf("Warning: command name %q is already used by %s.\n", shimName, alias)
			shimName = suggestedShimName(shimName)
		} else if conflicts := commandConflicts(shimName, target); len(conflicts) > 0 {
			fmt.Printf("Warning: command name %q already exists at %s.\n", shimName, strings.Join(conflicts, ", "))
			shimName = suggestedShimName(shimName)
		}
		fmt.Println("Next:")
		for index, step := range createNextSteps(name, shimName, dialect) {
			fmt.Printf("  %d. %s\n", index+1, step)
		}
	}
	return nil
}

func createNextSteps(name, shimName string, dialect Dialect) []string {
	var steps []string
	if dialect.Bridge == "cursor" {
		steps = append(steps, "Install the Cursor bridge: cc-dialect cursor install")
	}
	if step := authenticationStep(name, dialect); step != "" {
		steps = append(steps, step)
	}
	shim := fmt.Sprintf("Install the command: cc-dialect shim install %s", name)
	if shimName != preferredShimName(name) {
		shim += " --name " + shimName
	}
	steps = append(steps, shim, "Run: "+shimName)
	return steps
}

func authenticationStep(name string, dialect Dialect) string {
	if dialect.AuthProvider != "" {
		return fmt.Sprintf("Authenticate: cc-dialect auth %s %s", name, dialect.AuthProvider)
	}
	if dialect.AuthTokenEnv != "" {
		return fmt.Sprintf("Set the provider token: export %s=your_token", dialect.AuthTokenEnv)
	}
	return ""
}

func missingAuthenticationStep(name string, dialect Dialect) string {
	if dialect.AuthProvider != "" && !hasProviderCredentials(name, dialect.AuthProvider) {
		return authenticationStep(name, dialect)
	}
	if dialect.AuthTokenEnv != "" && os.Getenv(dialect.AuthTokenEnv) == "" {
		return authenticationStep(name, dialect)
	}
	return ""
}

func listDialects() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(cfg.Dialects))
	for name := range cfg.Dialects {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Println("No dialects yet. Try: cc-dialect create cc-codex --preset codex-sol")
		return nil
	}
	for _, name := range names {
		d := cfg.Dialects[name]
		preset := presetForDialect(d)
		if preset == "" {
			preset = "custom"
		}
		transport := fmt.Sprintf("embedded proxy :%d", d.Port)
		if d.BaseURL != "" {
			transport = fmt.Sprintf("embedded upstream proxy :%d", d.Port)
		} else if d.Bridge != "" {
			transport = fmt.Sprintf("embedded proxy :%d → %s bridge :%d", d.Port, d.Bridge, d.BridgePort)
		}
		fmt.Printf("%-18s %-12s %-24s %s\n", name, preset, d.Model, transport)
	}
	return nil
}

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return ""
}

type DialectDetection struct {
	Name     string `json:"name"`
	Preset   string `json:"preset"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Port     int    `json:"port"`
	Running  bool   `json:"running"`
}

func detectCommand(args []string) error {
	query := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		query = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("detect", flag.ContinueOnError)
	runningOnly := fs.Bool("running", false, "only include dialects with a healthy proxy")
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	quiet := fs.Bool("quiet", false, "print nothing and use only the exit status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cc-dialect detect [preset-or-provider] [--running] [--json] [--quiet]")
	}
	if *quiet && *jsonOutput {
		return errors.New("--quiet and --json cannot be used together")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	detections := detectDialects(cfg, query, *runningOnly, dialectHealthy)
	if !*quiet {
		if *jsonOutput {
			raw, marshalErr := json.MarshalIndent(detections, "", "  ")
			if marshalErr != nil {
				return marshalErr
			}
			fmt.Println(string(raw))
		} else {
			for _, detection := range detections {
				fmt.Printf("%-18s preset=%-12s provider=%-8s model=%-24s running=%-5t port=%d\n",
					detection.Name, detection.Preset, detection.Provider, detection.Model, detection.Running, detection.Port)
			}
		}
	}
	if query != "" && len(detections) == 0 {
		return &ExitError{Code: 1}
	}
	return nil
}

func detectDialects(cfg *Config, query string, runningOnly bool, healthy func(Dialect) bool) []DialectDetection {
	names := make([]string, 0, len(cfg.Dialects))
	for name := range cfg.Dialects {
		names = append(names, name)
	}
	sort.Strings(names)
	detections := make([]DialectDetection, 0, len(names))
	for _, name := range names {
		dialect := cfg.Dialects[name]
		preset := presetForDialect(dialect)
		provider := providerForDialect(dialect)
		if query != "" && query != preset && query != provider {
			continue
		}
		running := healthy(dialect)
		if runningOnly && !running {
			continue
		}
		if preset == "" {
			preset = "custom"
		}
		if provider == "" {
			provider = "custom"
		}
		detections = append(detections, DialectDetection{
			Name: name, Preset: preset, Provider: provider,
			Model: dialect.Model, Port: dialect.Port, Running: running,
		})
	}
	return detections
}

func showDialect(args []string) error {
	if len(args) != 1 {
		return errors.New("show requires a dialect name")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	d, ok := cfg.Dialects[args[0]]
	if !ok {
		return fmt.Errorf("dialect %q does not exist", args[0])
	}
	raw, _ := json.MarshalIndent(d, "", "  ")
	fmt.Println(string(raw))
	return nil
}

func listModels(args []string) error {
	if len(args) != 1 {
		return errors.New("models requires a dialect name")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	dialect, ok := cfg.Dialects[args[0]]
	if !ok {
		return fmt.Errorf("dialect %q does not exist", args[0])
	}
	if dialect.AuthProvider != "" && !hasProviderCredentials(args[0], dialect.AuthProvider) {
		return fmt.Errorf("dialect %q is not authenticated; run: cc-dialect auth %s %s", args[0], args[0], dialect.AuthProvider)
	}
	if err = startProxy(args[0], dialect); err != nil {
		return err
	}
	models, err := fetchModels(dialect)
	if err != nil {
		return err
	}
	sort.Strings(models)
	if len(models) == 0 {
		fmt.Println("No models available. Authenticate this dialect first.")
		return nil
	}
	fmt.Println(strings.Join(models, "\n"))
	return nil
}

func removeDialect(args []string) error {
	if len(args) != 1 {
		return errors.New("remove requires a dialect name")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Dialects[args[0]]; !ok {
		return fmt.Errorf("dialect %q does not exist", args[0])
	}
	_ = stopProxy(args[0])
	delete(cfg.Dialects, args[0])
	if err = saveConfig(cfg); err != nil {
		return err
	}
	home, _, _, _, _, _, _ := paths(args[0])
	return os.RemoveAll(filepath.Join(home, "instances", args[0]))
}

func runDialect(args []string) error {
	if len(args) == 0 {
		return errors.New("run requires a dialect name")
	}
	name := args[0]
	claudeArgs := args[1:]
	if len(claudeArgs) > 0 && claudeArgs[0] == "--" {
		claudeArgs = claudeArgs[1:]
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	d, ok := cfg.Dialects[name]
	if !ok {
		return fmt.Errorf("dialect %q does not exist", name)
	}
	if d.AuthProvider != "" && !hasProviderCredentials(name, d.AuthProvider) {
		return fmt.Errorf("dialect %q is not authenticated; run: cc-dialect auth %s %s", name, name, d.AuthProvider)
	}
	claudeDir, err := ensureClaudeConfigDir(name)
	if err != nil {
		return err
	}
	env := append([]string{}, os.Environ()...)
	if err = startProxy(name, d); err != nil {
		return err
	}
	env = setEnv(env, "CLAUDE_CONFIG_DIR", claudeDir)
	env = setEnv(env, "ANTHROPIC_BASE_URL", fmt.Sprintf("http://127.0.0.1:%d", d.Port))
	env = setEnv(env, "ANTHROPIC_AUTH_TOKEN", d.APIKey)
	env = setEnv(env, "ANTHROPIC_MODEL", d.Model)
	env = setEnv(env, "ANTHROPIC_DEFAULT_OPUS_MODEL", d.OpusModel)
	env = setEnv(env, "ANTHROPIC_DEFAULT_SONNET_MODEL", d.SonnetModel)
	env = setEnv(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL", d.HaikuModel)
	env = setEnv(env, "CLAUDE_CODE_SUBAGENT_MODEL", d.SubagentModel)
	env = setEnv(env, "CLAUDE_CODE_ALWAYS_ENABLE_EFFORT", boolNumber(d.Effort))
	env = setEnv(env, "CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY", strconv.Itoa(d.Concurrency))
	env = setEnv(env, "ENABLE_TOOL_SEARCH", strconv.FormatBool(d.ToolSearch))
	for key, value := range d.ExtraEnv {
		env = setEnv(env, key, value)
	}
	if !hasModelFlag(claudeArgs) {
		claudeArgs = append([]string{"--model", d.Model}, claudeArgs...)
	}
	if d.Effort && d.EffortLevel != "" && d.EffortLevel != "auto" && !hasFlag(claudeArgs, "--effort") {
		claudeArgs = append([]string{"--effort", d.EffortLevel}, claudeArgs...)
	}
	cmd := exec.Command("claude", claudeArgs...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
	}
	return err
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return append(out, prefix+value)
}

func hasModelFlag(args []string) bool {
	return hasFlag(args, "--model")
}

func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func validEffort(value string) bool {
	switch value {
	case "auto", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func boolNumber(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func authCommand(args []string) error {
	if len(args) < 2 {
		return errors.New("auth requires a dialect and provider: cc-dialect auth <dialect> <provider>")
	}
	valid := map[string]bool{"codex": true, "claude": true, "kimi": true, "antigravity": true, "xai": true}
	if !valid[args[1]] {
		return fmt.Errorf("unsupported OAuth provider %q", args[1])
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	dialect, ok := cfg.Dialects[args[0]]
	if !ok {
		return fmt.Errorf("dialect %q does not exist", args[0])
	}
	if dialect.AuthProvider != "" && dialect.AuthProvider != args[1] {
		return fmt.Errorf("dialect %q requires %s authentication, not %s", args[0], dialect.AuthProvider, args[1])
	}
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "do not open a browser")
	if err = fs.Parse(args[2:]); err != nil {
		return err
	}
	return authenticate(args[0], args[1], *noBrowser)
}

func proxyCommand(args []string) error {
	if len(args) != 2 {
		return errors.New("proxy requires a dialect and action: cc-dialect proxy <dialect> <start|stop|status|logs>")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	name := args[0]
	dialect, ok := cfg.Dialects[name]
	if !ok {
		return fmt.Errorf("dialect %q does not exist", name)
	}
	switch args[1] {
	case "start":
		err = startProxy(name, dialect)
		if err == nil {
			fmt.Printf("%s proxy ready at http://127.0.0.1:%d\n", name, dialect.Port)
		}
		return err
	case "stop":
		return stopProxy(name)
	case "status":
		if dialectHealthy(dialect) {
			fmt.Printf("running (pid %d, port %d", proxyPID(name), dialect.Port)
			if dialect.Bridge != "" {
				fmt.Printf(", %s bridge pid %d, port %d", dialect.Bridge, cursorBridgePID(name), dialect.BridgePort)
			}
			fmt.Println(")")
		} else {
			fmt.Println("stopped")
		}
	case "logs":
		return tailLog(name)
	default:
		return fmt.Errorf("unknown proxy command %q", args[1])
	}
	return nil
}

func shimCommand(args []string) error {
	if len(args) < 2 || args[0] != "install" {
		return errors.New("usage: cc-dialect shim install <dialect> [--name <command>] [--dir <path>]")
	}
	dialectName := args[1]
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.Dialects[dialectName]; !ok {
		return fmt.Errorf("dialect %q does not exist", dialectName)
	}
	fs := flag.NewFlagSet("shim install", flag.ContinueOnError)
	name := fs.String("name", preferredShimName(dialectName), "installed command name")
	dir := fs.String("dir", "", "install directory")
	if err = fs.Parse(args[2:]); err != nil {
		return err
	}
	if !validName(*name) {
		return errors.New("invalid shim name")
	}
	if *dir == "" {
		home, _ := os.UserHomeDir()
		*dir = filepath.Join(home, ".local", "bin")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil {
		exe = resolved
	}
	if err = os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(*dir, *name)
	if alias, found := zshAlias(*name); found {
		return fmt.Errorf("zsh alias %q would override the installed command; remove it from ~/.zshrc and run `unalias %s` in already-open terminals", alias, *name)
	}
	if conflicts := commandConflicts(*name, path); len(conflicts) > 0 {
		return fmt.Errorf("command %q already exists at %s; choose another name, for example: cc-dialect shim install %s --name %s",
			*name, strings.Join(conflicts, ", "), dialectName, suggestedShimName(*name))
	}
	body := fmt.Sprintf("#!/bin/sh\nexec %q run %q -- \"$@\"\n", exe, dialectName)
	if err = os.WriteFile(path, []byte(body), 0o755); err != nil {
		return err
	}
	if err = os.Chmod(path, 0o755); err != nil {
		return err
	}
	fmt.Println("Installed", path)
	if !pathContains(*dir) {
		fmt.Printf("Add %s to PATH to invoke %s directly.\n", *dir, *name)
	}
	return nil
}

func nativeCommand(args []string) error {
	if len(args) < 2 || args[0] != "install" {
		return errors.New("usage: cc-dialect native install <command> [--dangerous] [--dir <path>]")
	}
	name := args[1]
	if !validName(name) || name == "claude" {
		return errors.New("native launcher requires a valid command name other than claude")
	}
	fs := flag.NewFlagSet("native install", flag.ContinueOnError)
	dangerous := fs.Bool("dangerous", false, "bypass Claude Code permission checks")
	dir := fs.String("dir", "", "install directory")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if *dir == "" {
		home, _ := os.UserHomeDir()
		*dir = filepath.Join(home, ".local", "bin")
	}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return errors.New("Claude Code not found in PATH")
	}
	claudePath, err = filepath.Abs(claudePath)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(*dir, name)
	if alias, found := zshAlias(name); found {
		return fmt.Errorf("zsh alias %q would override the installed command; remove it from ~/.zshrc and run `unalias %s` in already-open terminals", alias, name)
	}
	if conflicts := commandConflicts(name, path); len(conflicts) > 0 {
		return fmt.Errorf("command %q already exists at %s; remove it or choose another launcher name",
			name, strings.Join(conflicts, ", "))
	}
	body := nativeLauncherBody(claudePath, *dangerous)
	if err = os.WriteFile(path, []byte(body), 0o755); err != nil {
		return err
	}
	if err = os.Chmod(path, 0o755); err != nil {
		return err
	}
	fmt.Println("Installed", path)
	if !pathContains(*dir) {
		fmt.Printf("Add %s to PATH to invoke %s directly.\n", *dir, name)
	}
	return nil
}

func nativeLauncherBody(claudePath string, dangerous bool) string {
	flag := ""
	if dangerous {
		flag = " --dangerously-skip-permissions"
	}
	return fmt.Sprintf("#!/bin/sh\nexec %q%s \"$@\"\n", claudePath, flag)
}

func zshAlias(name string) (string, bool) {
	if !validName(name) {
		return "", false
	}
	output, err := exec.Command("zsh", "-ic", "alias "+name).CombinedOutput()
	if err != nil {
		return "", false
	}
	alias := strings.TrimSpace(string(output))
	return alias, alias != ""
}

func pathContains(dir string) bool {
	absolute, _ := filepath.Abs(dir)
	for _, item := range filepath.SplitList(os.Getenv("PATH")) {
		candidate, _ := filepath.Abs(item)
		if candidate == absolute {
			return true
		}
	}
	return false
}

func commandConflicts(name, target string) []string {
	target = canonicalPath(target)
	seen := map[string]bool{}
	var conflicts []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		displayPath, err := filepath.Abs(candidate)
		if err != nil {
			displayPath = candidate
		}
		canonical := canonicalPath(candidate)
		if canonical == target || seen[canonical] {
			continue
		}
		seen[canonical] = true
		conflicts = append(conflicts, filepath.Clean(displayPath))
	}
	sort.Strings(conflicts)
	return conflicts
}

func canonicalPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func suggestedShimName(name string) string {
	preferred := preferredShimName(name)
	if preferred == name {
		return preferred + "-dialect"
	}
	return preferred
}

func preferredShimName(name string) string {
	if strings.HasPrefix(name, "cc-") {
		return name
	}
	return "cc-" + name
}

func doctor() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	fmt.Println("✓ configuration")
	if path, err := exec.LookPath("claude"); err == nil {
		fmt.Println("✓ Claude Code:", path)
	} else {
		fmt.Println("✗ Claude Code not found in PATH")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		fmt.Printf("✗ unsupported platform %s/%s (requires darwin/arm64)\n", runtime.GOOS, runtime.GOARCH)
	}
	hasCursor := false
	for _, dialect := range cfg.Dialects {
		if dialect.Bridge == "cursor" {
			hasCursor = true
			break
		}
	}
	if hasCursor {
		if version, cursorErr := cursorRuntimeVersion(); cursorErr == nil {
			if version == cursorSDKVersion {
				fmt.Printf("✓ Cursor bridge runtime (@cursor/sdk %s)\n", version)
			} else {
				fmt.Printf("✗ Cursor bridge runtime has @cursor/sdk %s; %s is required (run: cc-dialect cursor install)\n",
					version, cursorSDKVersion)
			}
		} else {
			fmt.Println("✗ Cursor bridge runtime is not installed (run: cc-dialect cursor install)")
		}
		if os.Getenv("CURSOR_API_KEY") == "" {
			fmt.Println("✗ CURSOR_API_KEY is not set")
		} else {
			fmt.Println("✓ CURSOR_API_KEY")
		}
	}
	for name := range cfg.Dialects {
		shimName := preferredShimName(name)
		if alias, found := zshAlias(shimName); found {
			fmt.Printf("✗ %s is shadowed by %s\n", shimName, alias)
		}
		home, _ := os.UserHomeDir()
		target := filepath.Join(home, ".local", "bin", shimName)
		if conflicts := commandConflicts(shimName, target); len(conflicts) > 0 {
			fmt.Printf("✗ %s conflicts with existing command(s): %s (try --name %s)\n",
				shimName, strings.Join(conflicts, ", "), suggestedShimName(shimName))
		}
	}
	names := make([]string, 0, len(cfg.Dialects))
	for name := range cfg.Dialects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dialect := cfg.Dialects[name]
		if dialect.AuthProvider != "" && !hasProviderCredentials(name, dialect.AuthProvider) {
			fmt.Printf("✗ %s is not authenticated (run: cc-dialect auth %s %s)\n", name, name, dialect.AuthProvider)
		}
		if dialectHealthy(dialect) {
			if dialect.Bridge == "cursor" {
				fmt.Printf("✓ %s (preset %s) proxy running on 127.0.0.1:%d, Cursor bridge on 127.0.0.1:%d\n",
					name, displayPreset(dialect), dialect.Port, dialect.BridgePort)
			} else {
				fmt.Printf("✓ %s (preset %s) proxy running on 127.0.0.1:%d\n", name, displayPreset(dialect), dialect.Port)
			}
		} else {
			if dialect.Bridge == "cursor" {
				fmt.Printf("○ %s (preset %s) stopped (reserved proxy port %d, bridge port %d)\n",
					name, displayPreset(dialect), dialect.Port, dialect.BridgePort)
			} else {
				fmt.Printf("○ %s (preset %s) proxy stopped (reserved port %d)\n", name, displayPreset(dialect), dialect.Port)
			}
		}
	}
	return nil
}

func displayPreset(dialect Dialect) string {
	if preset := presetForDialect(dialect); preset != "" {
		return preset
	}
	return "custom"
}
