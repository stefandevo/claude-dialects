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
	"slices"
	"sort"
	"strconv"
	"strings"
)

const usage = `Claude Dialects — create native Claude Code runners for any model

Usage:
  cc-dialect create <name> --preset <preset> [options]
  cc-dialect create <name> --model <model> [options]
  cc-dialect run <name> [--] [claude arguments...]
  cc-dialect list
  cc-dialect show <name>
  cc-dialect remove <name>
  cc-dialect detect [preset-or-provider] [--running] [--json] [--quiet]
  cc-dialect models <name>
  cc-dialect presets
  cc-dialect auth <dialect> <codex|claude|kimi|antigravity|xai> [--no-browser]
  cc-dialect cursor <install|status|models>
  cc-dialect copilot <install|login|status|models>
  cc-dialect shim install <dialect> [--name <command>] [--dir <path>]
  cc-dialect native install <command> [--dangerous] [--dir <path>]
  cc-dialect proxy <dialect> <start|stop|restart|status|logs>
  cc-dialect web [--listen 127.0.0.1:0] [--no-browser]
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
	case "copilot":
		return copilotCommand(args[1:])
	case "proxy":
		return proxyCommand(args[1:])
	case "shim":
		return shimCommand(args[1:])
	case "native":
		return nativeCommand(args[1:])
	case "web":
		return webCommand(args[1:], version)
	case "doctor":
		return doctor(args[1:], version)
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
	service := newAppService()
	result, err := service.UpsertDialect(DialectInput{
		Name: name, Preset: *preset, Model: *model, SubagentModel: *subagent,
		OpusModel: *opusModel, SonnetModel: *sonnetModel, HaikuModel: *haikuModel,
		EffortLevel: *effortLevel, Concurrency: *concurrency, Port: *port, BridgePort: *bridgePort,
		BaseURL: *baseURL, AuthTokenEnv: *tokenEnv, Effort: *effort, ToolSearch: *toolSearch,
	}, "")
	if err != nil {
		return err
	}
	dialect := result.Dialect
	if !result.Created {
		fmt.Printf("Updated %q to model %s (isolated port %d)\n", name, dialect.Model, dialect.Port)
		fmt.Println("Authentication, isolated Claude Code state, and installed shims were preserved.")
		cfg, loadErr := loadConfig()
		if loadErr != nil {
			return loadErr
		}
		stored := cfg.Dialects[name]
		if steps := missingAuthenticationSteps(name, stored); len(steps) > 0 {
			fmt.Println("Next:")
			for index, step := range steps {
				fmt.Printf("  %d. %s\n", index+1, step)
			}
		}
		return nil
	}
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
	cfg, loadErr := loadConfig()
	if loadErr != nil {
		return loadErr
	}
	stored := cfg.Dialects[name]
	fmt.Println("Next:")
	for index, step := range createNextSteps(name, shimName, stored) {
		fmt.Printf("  %d. %s\n", index+1, step)
	}
	return nil
}
func createNextSteps(name, shimName string, dialect Dialect) []string {
	var steps []string
	if dialect.Bridge == "cursor" {
		steps = append(steps, "Install the Cursor bridge: cc-dialect cursor install")
	}
	if dialect.Bridge == "copilot" {
		steps = append(steps,
			"Install the Copilot bridge: cc-dialect copilot install",
			"Authenticate with GitHub Copilot: cc-dialect copilot login",
		)
	}
	steps = append(steps, authenticationSteps(name, dialect)...)
	shim := fmt.Sprintf("Install the command: cc-dialect shim install %s", name)
	if shimName != preferredShimName(name) {
		shim += " --name " + shimName
	}
	steps = append(steps, shim, "Run: "+shimName)
	return steps
}

// authenticationSteps lists the login steps a freshly created dialect needs:
// one `cc-dialect auth` line per expected OAuth provider (several for a mixed
// dialect), or a token export for an upstream API-key route.
func authenticationSteps(name string, dialect Dialect) []string {
	if providers := expectedAuthProviders(dialect); len(providers) > 0 {
		steps := make([]string, len(providers))
		for index, provider := range providers {
			steps[index] = fmt.Sprintf("Authenticate: cc-dialect auth %s %s", name, provider)
		}
		return steps
	}
	if dialect.AuthTokenEnv != "" {
		return []string{fmt.Sprintf("Set the provider token: export %s=your_token", dialect.AuthTokenEnv)}
	}
	return nil
}

// notAuthenticatedError reports the OAuth logins a dialect still needs. A single
// missing provider keeps the original one-line message; a mixed dialect lists an
// explicit `cc-dialect auth` command per missing provider so the fix is copyable.
// It is an OperationError so the dashboard forwards the actionable message rather
// than collapsing it into a generic 500.
func notAuthenticatedError(name string, missing []string) error {
	if len(missing) == 1 {
		return operationError(ErrorInvalidInput, "dialect %q is not authenticated; run: cc-dialect auth %s %s", name, name, missing[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "dialect %q is missing authentication for %s; run:", name, strings.Join(missing, ", "))
	for _, provider := range missing {
		fmt.Fprintf(&b, "\n  cc-dialect auth %s %s", name, provider)
	}
	return operationError(ErrorInvalidInput, "%s", b.String())
}

// missingAuthenticationSteps lists only the login steps still outstanding for an
// existing dialect: an entry per unauthenticated OAuth provider, plus a token
// export when the upstream API-key variable is unset.
func missingAuthenticationSteps(name string, dialect Dialect) []string {
	var steps []string
	for _, provider := range missingAuthProviders(name, dialect) {
		steps = append(steps, fmt.Sprintf("Authenticate: cc-dialect auth %s %s", name, provider))
	}
	if len(expectedAuthProviders(dialect)) == 0 && dialect.AuthTokenEnv != "" && os.Getenv(dialect.AuthTokenEnv) == "" {
		steps = append(steps, fmt.Sprintf("Set the provider token: export %s=your_token", dialect.AuthTokenEnv))
	}
	return steps
}

func listDialects() error {
	result, err := newAppService().ListDialects(false)
	if err != nil {
		return err
	}
	if len(result.Dialects) == 0 {
		fmt.Println("No dialects yet. Try: cc-dialect create cc-codex --preset codex-sol")
		return nil
	}
	for _, dialect := range result.Dialects {
		transport := fmt.Sprintf("embedded proxy :%d", dialect.Port)
		if dialect.BaseURL != "" {
			transport = fmt.Sprintf("embedded upstream proxy :%d", dialect.Port)
		} else if dialect.Bridge != "" {
			transport = fmt.Sprintf("embedded proxy :%d → %s bridge :%d", dialect.Port, dialect.Bridge, dialect.BridgePort)
		}
		fmt.Printf("%-18s %-12s %-24s %s\n", dialect.Name, dialect.Preset, dialect.Model, transport)
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
	dialect, _, err := newAppService().Dialect(args[0])
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(dialect, "", "  ")
	if err != nil {
		return err
	}
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
	if missing := missingAuthProviders(args[0], dialect); len(missing) > 0 {
		return notAuthenticatedError(args[0], missing)
	}
	if _, err = newAppService().StartDialect(args[0]); err != nil {
		return err
	}
	cfg, err = loadConfig()
	if err != nil {
		return err
	}
	dialect = cfg.Dialects[args[0]]
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
	return newAppService().RemoveDialect(args[0], "")
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
	if missing := missingAuthProviders(name, d); len(missing) > 0 {
		return notAuthenticatedError(name, missing)
	}
	claudeDir, err := ensureClaudeConfigDir(name)
	if err != nil {
		return err
	}
	env := append([]string{}, os.Environ()...)
	if _, err = newAppService().StartDialect(name); err != nil {
		return err
	}
	cfg, err = loadConfig()
	if err != nil {
		return err
	}
	d = cfg.Dialects[name]
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
	if expected := expectedAuthProviders(dialect); len(expected) > 0 && !slices.Contains(expected, args[1]) {
		return fmt.Errorf("dialect %q expects OAuth for %s; %s is not one of them",
			args[0], strings.Join(expected, ", "), args[1])
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
		return errors.New("proxy requires a dialect and action: cc-dialect proxy <dialect> <start|stop|restart|status|logs>")
	}
	name := args[0]
	service := newAppService()
	dialect, _, err := service.Dialect(name)
	if err != nil {
		return err
	}
	switch args[1] {
	case "start":
		_, err = service.StartDialect(name)
		if err == nil {
			fmt.Printf("%s proxy ready at http://127.0.0.1:%d\n", name, dialect.Port)
		}
		return err
	case "stop":
		_, err = service.StopDialect(name)
		return err
	case "restart":
		_, err = service.RestartDialect(name)
		if err == nil {
			fmt.Printf("%s proxy ready at http://127.0.0.1:%d\n", name, dialect.Port)
		}
		return err
	case "status":
		status, statusErr := service.DialectStatus(name)
		if statusErr != nil {
			return statusErr
		}
		switch status.State {
		case RuntimeRunning:
			fmt.Printf("running (pid %d, port %d", status.Proxy.PID, status.Proxy.Port)
			if status.Bridge != nil {
				fmt.Printf(", %s bridge pid %d, port %d", dialect.Bridge, status.Bridge.PID, status.Bridge.Port)
			}
			fmt.Println(")")
		case RuntimeDegraded:
			fmt.Printf("degraded (proxy %s", status.Proxy.State)
			if status.Bridge != nil {
				fmt.Printf(", %s bridge %s", dialect.Bridge, status.Bridge.State)
			}
			fmt.Println(")")
		default:
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
	if err = atomicWriteFile(path, []byte(body), 0o755); err != nil {
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
	result, err := newAppService().InstallNativeLauncher(NativeLauncherInput{
		Name: name, Directory: *dir, Dangerous: *dangerous,
	}, "")
	if err != nil {
		return err
	}
	fmt.Println("Installed", result.Launcher.Path)
	if !pathContains(filepath.Dir(result.Launcher.Path)) {
		fmt.Printf("Add %s to PATH to invoke %s directly.\n", filepath.Dir(result.Launcher.Path), name)
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

func doctor(args []string, version string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fix := fs.Bool("fix", false, "apply deterministic fixes (e.g. restart stale proxies, install bridge SDKs)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	embeddedVersion := embeddedProxyVersion()

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
	hasCopilot := false
	needsProxyRestart := make([]string, 0)
	needsBridgeRestart := make([]string, 0)
	needsCopilotInstall := false
	needsCursorInstall := false
	for _, dialect := range cfg.Dialects {
		if dialect.Bridge == "cursor" {
			hasCursor = true
		}
		if dialect.Bridge == "copilot" {
			hasCopilot = true
		}
	}
	if hasCopilot {
		if version, copilotErr := copilotRuntimeVersion(); copilotErr == nil {
			if version == copilotSDKVersion {
				fmt.Printf("✓ Copilot bridge runtime (@github/copilot-sdk %s)\n", version)
			} else {
				fmt.Printf("✗ Copilot bridge runtime has @github/copilot-sdk %s; %s is required (run: cc-dialect copilot install)\n",
					version, copilotSDKVersion)
				needsCopilotInstall = true
			}
		} else {
			fmt.Println("✗ Copilot bridge runtime is not installed (run: cc-dialect copilot install)")
			needsCopilotInstall = true
		}
		if status, statusErr := copilotSDKStatus(); statusErr == nil && status.IsAuthenticated {
			fmt.Println("✓ GitHub Copilot authentication")
		} else {
			fmt.Println("✗ GitHub Copilot is not authenticated (run: cc-dialect copilot login)")
		}
	}
	if hasCursor {
		if version, cursorErr := cursorRuntimeVersion(); cursorErr == nil {
			if version == cursorSDKVersion {
				fmt.Printf("✓ Cursor bridge runtime (@cursor/sdk %s)\n", version)
			} else {
				fmt.Printf("✗ Cursor bridge runtime has @cursor/sdk %s; %s is required (run: cc-dialect cursor install)\n",
					version, cursorSDKVersion)
				needsCursorInstall = true
			}
		} else {
			fmt.Println("✗ Cursor bridge runtime is not installed (run: cc-dialect cursor install)")
			needsCursorInstall = true
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
		for _, provider := range missingAuthProviders(name, dialect) {
			fmt.Printf("✗ %s is not authenticated for %s (run: cc-dialect auth %s %s)\n", name, provider, name, provider)
		}
		if dialectHealthy(dialect) {
			proxyVersion := proxySpawnVersion(name)
			if proxyVersion != "" && proxyVersion != embeddedVersion {
				fmt.Printf("✗ %s proxy is running an older cc-dialect build (run: cc-dialect proxy %s restart)\n", name, name)
				needsProxyRestart = append(needsProxyRestart, name)
			} else if proxyVersion == "" {
				fmt.Printf("✗ %s proxy is running an unknown/stale cc-dialect build (run: cc-dialect proxy %s restart)\n", name, name)
				needsProxyRestart = append(needsProxyRestart, name)
			}

			if dialect.Bridge == "cursor" && cursorBridgeHealthy(dialect) {
				bridgeVersion := cursorBridgeSpawnVersion(name)
				if bridgeVersion != "" && bridgeVersion != embeddedVersion {
					fmt.Printf("✗ %s Cursor bridge is running an older cc-dialect build (run: cc-dialect proxy %s restart)\n", name, name)
					needsBridgeRestart = append(needsBridgeRestart, name)
				} else if bridgeVersion == "" {
					fmt.Printf("✗ %s Cursor bridge is running an unknown/stale cc-dialect build (run: cc-dialect proxy %s restart)\n", name, name)
					needsBridgeRestart = append(needsBridgeRestart, name)
				}
			} else if dialect.Bridge == "copilot" && copilotBridgeHealthy(dialect) {
				bridgeVersion := copilotBridgeSpawnVersion(name)
				if bridgeVersion != "" && bridgeVersion != embeddedVersion {
					fmt.Printf("✗ %s Copilot bridge is running an older cc-dialect build (run: cc-dialect proxy %s restart)\n", name, name)
					needsBridgeRestart = append(needsBridgeRestart, name)
				} else if bridgeVersion == "" {
					fmt.Printf("✗ %s Copilot bridge is running an unknown/stale cc-dialect build (run: cc-dialect proxy %s restart)\n", name, name)
					needsBridgeRestart = append(needsBridgeRestart, name)
				}
			}

			if dialect.Bridge != "" {
				fmt.Printf("✓ %s (preset %s) proxy running on 127.0.0.1:%d, %s bridge on 127.0.0.1:%d\n",
					name, displayPreset(dialect), dialect.Port, bridgeDisplayName(dialect.Bridge), dialect.BridgePort)
			} else {
				fmt.Printf("✓ %s (preset %s) proxy running on 127.0.0.1:%d\n", name, displayPreset(dialect), dialect.Port)
			}
		} else {
			if dialect.Bridge != "" {
				fmt.Printf("○ %s (preset %s) stopped (reserved proxy port %d, bridge port %d)\n",
					name, displayPreset(dialect), dialect.Port, dialect.BridgePort)
			} else {
				fmt.Printf("○ %s (preset %s) proxy stopped (reserved port %d)\n", name, displayPreset(dialect), dialect.Port)
			}
		}
	}
	if *fix {
		if needsCopilotInstall {
			fmt.Println("\nApplying fix: installing Copilot bridge SDK...")
			_ = copilotCommand([]string{"install"})
		}
		if needsCursorInstall {
			fmt.Println("\nApplying fix: installing Cursor bridge SDK...")
			_ = cursorCommand([]string{"install"})
		}
		restarts := make(map[string]bool)
		for _, name := range needsProxyRestart {
			restarts[name] = true
		}
		for _, name := range needsBridgeRestart {
			restarts[name] = true
		}
		service := newAppService()
		for name := range restarts {
			fmt.Printf("\nApplying fix: restarting stale dialect %s...\n", name)
			_, err := service.RestartDialect(name)
			if err != nil {
				fmt.Printf("Failed to restart %s: %v\n", name, err)
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

func bridgeDisplayName(bridge string) string {
	switch bridge {
	case "cursor":
		return "Cursor"
	case "copilot":
		return "Copilot"
	default:
		return bridge
	}
}
