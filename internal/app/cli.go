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
  dialect create <name> --preset <preset> [options]
  dialect run <name> [--] [claude arguments...]
  dialect list | show <name> | remove <name>
  dialect models <name>
  dialect presets
  dialect auth <dialect> <codex|claude|kimi|antigravity|xai> [--no-browser]
  dialect shim install <dialect> [--name <command>] [--dir <path>]
  dialect native install <command> [--dangerous] [--dir <path>]
  dialect proxy <dialect> <start|stop|status|logs>
  dialect doctor

Example:
  dialect create claudex --preset codex-sol
  dialect auth claudex codex
  dialect shim install claudex
  claudex
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
		fmt.Printf("dialect %s (embedded CLIProxyAPI %s)\n", version, embeddedProxyVersion())
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
	case "run":
		return runDialect(args[1:])
	case "auth":
		return authCommand(args[1:])
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
			if otherName != name && other.Port == *port {
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
	} else {
		fmt.Printf("Created %q using model %s (isolated port %d)\n", name, dialect.Model, dialect.Port)
		fmt.Printf("Next: dialect shim install %s\n", name)
	}
	return nil
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
		fmt.Println("No dialects yet. Try: dialect create claudex --preset codex-sol")
		return nil
	}
	for _, name := range names {
		d := cfg.Dialects[name]
		transport := fmt.Sprintf("embedded proxy :%d", d.Port)
		if d.BaseURL != "" {
			transport = fmt.Sprintf("embedded upstream proxy :%d", d.Port)
		}
		fmt.Printf("%-18s %-24s %s\n", name, d.Model, transport)
	}
	return nil
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
		return errors.New("auth requires a dialect and provider: dialect auth <dialect> <provider>")
	}
	valid := map[string]bool{"codex": true, "claude": true, "kimi": true, "antigravity": true, "xai": true}
	if !valid[args[1]] {
		return fmt.Errorf("unsupported OAuth provider %q", args[1])
	}
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "do not open a browser")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	return authenticate(args[0], args[1], *noBrowser)
}

func proxyCommand(args []string) error {
	if len(args) != 2 {
		return errors.New("proxy requires a dialect and action: dialect proxy <dialect> <start|stop|status|logs>")
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
		if proxyHealthy(dialect) {
			fmt.Printf("running (pid %d, port %d)\n", proxyPID(name), dialect.Port)
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
		return errors.New("usage: dialect shim install <dialect> [--name <command>] [--dir <path>]")
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
	name := fs.String("name", dialectName, "installed command name")
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
	if err = os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(*dir, *name)
	var body string
	body = fmt.Sprintf("#!/bin/sh\nexec %q run %q -- \"$@\"\n", exe, dialectName)
	if err = os.WriteFile(path, []byte(body), 0o755); err != nil {
		return err
	}
	if err = os.Chmod(path, 0o755); err != nil {
		return err
	}
	fmt.Println("Installed", path)
	if alias, found := zshAlias(*name); found {
		return fmt.Errorf("zsh alias %q overrides the installed command (%s); remove it from ~/.zshrc and run `unalias %s` in already-open terminals", alias, path, *name)
	}
	if !pathContains(*dir) {
		fmt.Printf("Add %s to PATH to invoke %s directly.\n", *dir, *name)
	}
	return nil
}

func nativeCommand(args []string) error {
	if len(args) < 2 || args[0] != "install" {
		return errors.New("usage: dialect native install <command> [--dangerous] [--dir <path>]")
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
	body := nativeLauncherBody(claudePath, *dangerous)
	if err = os.WriteFile(path, []byte(body), 0o755); err != nil {
		return err
	}
	if err = os.Chmod(path, 0o755); err != nil {
		return err
	}
	fmt.Println("Installed", path)
	if alias, found := zshAlias(name); found {
		return fmt.Errorf("zsh alias %q overrides the installed command (%s); remove it from ~/.zshrc and run `unalias %s` in already-open terminals", alias, path, name)
	}
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
	for name := range cfg.Dialects {
		if alias, found := zshAlias(name); found {
			fmt.Printf("✗ %s is shadowed by %s\n", name, alias)
		}
	}
	names := make([]string, 0, len(cfg.Dialects))
	for name := range cfg.Dialects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dialect := cfg.Dialects[name]
		if proxyHealthy(dialect) {
			fmt.Printf("✓ %s proxy running on 127.0.0.1:%d\n", name, dialect.Port)
		} else {
			fmt.Printf("○ %s proxy stopped (reserved port %d)\n", name, dialect.Port)
		}
	}
	return nil
}
