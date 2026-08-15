package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const configVersion = 2

type Config struct {
	Version         int                       `json:"version"`
	BasePort        int                       `json:"basePort"`
	Dialects        map[string]Dialect        `json:"dialects"`
	NativeLaunchers map[string]NativeLauncher `json:"nativeLaunchers"`
}

type NativeLauncher struct {
	Path       string `json:"path"`
	ClaudePath string `json:"claudePath"`
	Dangerous  bool   `json:"dangerous"`
	SHA256     string `json:"sha256"`
}

type Dialect struct {
	Preset        string `json:"preset,omitempty"`
	Model         string `json:"model"`
	SubagentModel string `json:"subagentModel,omitempty"`
	Effort        bool   `json:"effort"`
	Concurrency   int    `json:"concurrency"`
	ToolSearch    bool   `json:"toolSearch"`
	OpusModel     string `json:"opusModel,omitempty"`
	SonnetModel   string `json:"sonnetModel,omitempty"`
	HaikuModel    string `json:"haikuModel,omitempty"`
	EffortLevel   string `json:"effortLevel,omitempty"`
	// ContextWindow is the smallest context window supported by any model this
	// dialect can select, in tokens. It calibrates Claude Code auto-compaction
	// for provider model IDs Claude Code cannot recognize. Zero means unknown or
	// unconfigured, which is distinct from a real capacity — see context_window.go.
	ContextWindow int               `json:"contextWindow,omitempty"`
	Port          int               `json:"port"`
	APIKey        string            `json:"apiKey"`
	BaseURL       string            `json:"baseUrl,omitempty"`
	AuthTokenEnv  string            `json:"authTokenEnv,omitempty"`
	AuthProvider  string            `json:"authProvider,omitempty"`
	Bridge        string            `json:"bridge,omitempty"`
	BridgePort    int               `json:"bridgePort,omitempty"`
	ExtraEnv      map[string]string `json:"extraEnv,omitempty"`
}

var presets = map[string]Dialect{
	"codex-sol": {
		Model: "gpt-5.6-sol", SubagentModel: "gpt-5.6-sol",
		OpusModel: "gpt-5.6-sol", SonnetModel: "gpt-5.6-terra", HaikuModel: "gpt-5.6-luna",
		AuthProvider: "codex",
		Effort:       true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"codex": {
		Model: "gpt-5.6", SubagentModel: "gpt-5.6",
		OpusModel: "gpt-5.6-sol", SonnetModel: "gpt-5.6-terra", HaikuModel: "gpt-5.6-luna",
		AuthProvider: "codex",
		Effort:       true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"kimi": {
		Model: "kimi-k3", SubagentModel: "kimi-k3",
		OpusModel: "kimi-k3", SonnetModel: "kimi-k2.7-code-highspeed", HaikuModel: "kimi-k2.6",
		AuthProvider: "kimi",
		Effort:       true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"gemini": {
		Model: "gemini-pro-agent", SubagentModel: "gemini-pro-agent",
		OpusModel: "gemini-pro-agent", SonnetModel: "gemini-3.5-flash-low", HaikuModel: "gemini-3.5-flash-extra-low",
		AuthProvider: "antigravity",
		Effort:       true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"claude": {
		Model: "claude-fable-5", SubagentModel: "claude-fable-5",
		OpusModel: "claude-fable-5", SonnetModel: "claude-sonnet-4-6", HaikuModel: "claude-haiku-4-5",
		AuthProvider: "claude",
		Effort:       true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"mixed-frontier": {
		Model: "claude-fable-5", SubagentModel: "claude-fable-5",
		OpusModel: "gpt-5.6-sol", SonnetModel: "kimi-k3", HaikuModel: "grok-4.5",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	// The haiku tier is GLM-5-Turbo rather than the cheaper GLM-4.5-Air. Claude
	// Code takes one context window per process, sized to the smallest model the
	// dialect can select, so GLM-4.5-Air's 131,072 tokens capped a session whose
	// main model holds 1M — and it did so to protect a tier Claude Code sends only
	// short auxiliary work to. GLM has by far the widest tier spread of any preset
	// here, which is what makes the trade worth naming.
	"glm": {
		Model: "glm-5.3", SubagentModel: "glm-5.3",
		OpusModel: "glm-5.3", SonnetModel: "glm-5-turbo", HaikuModel: "glm-5-turbo",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
		BaseURL: "https://api.z.ai/api/anthropic", AuthTokenEnv: "ZAI_API_KEY",
	},
	"grok": {
		Model: "grok-4.5", SubagentModel: "grok-4.5",
		OpusModel: "grok-4.5", SonnetModel: "grok-4.5", HaikuModel: "grok-4.5",
		AuthProvider: "xai",
		Effort:       true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"grok-build": {
		Model: "grok-build-0.1", SubagentModel: "grok-build-0.1",
		OpusModel: "grok-build-0.1", SonnetModel: "grok-build-0.1", HaikuModel: "grok-build-0.1",
		AuthProvider: "xai",
		Effort:       true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"composer": {
		Model: "grok-composer-2.5-fast", SubagentModel: "grok-composer-2.5-fast",
		OpusModel: "grok-composer-2.5-fast", SonnetModel: "grok-composer-2.5-fast", HaikuModel: "grok-composer-2.5-fast",
		AuthProvider: "xai",
		Effort:       true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"minimax": {
		Model: "MiniMax-M2.7", SubagentModel: "MiniMax-M2.7",
		OpusModel: "MiniMax-M2.7", SonnetModel: "MiniMax-M2.7", HaikuModel: "MiniMax-M2.7",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
		BaseURL: "https://api.minimax.io/anthropic", AuthTokenEnv: "MINIMAX_API_KEY",
	},
	"deepseek": {
		Model: "deepseek-v4-pro", SubagentModel: "deepseek-v4-pro",
		OpusModel: "deepseek-v4-pro", SonnetModel: "deepseek-v4-flash", HaikuModel: "deepseek-v4-flash",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
		BaseURL: "https://api.deepseek.com/anthropic", AuthTokenEnv: "DEEPSEEK_API_KEY",
	},
	"cursor-composer": {
		Model: "composer-2.5", SubagentModel: "composer-2.5",
		OpusModel: "composer-2.5-fast", SonnetModel: "composer-2.5-standard", HaikuModel: "composer-2.5-standard",
		Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"cursor-composer-fast": {
		Model: "composer-2.5-fast", SubagentModel: "composer-2.5-fast",
		OpusModel: "composer-2.5-fast", SonnetModel: "composer-2.5-fast", HaikuModel: "composer-2.5-fast",
		Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"cursor-auto": {
		Model: "auto", SubagentModel: "auto",
		OpusModel: "auto", SonnetModel: "auto", HaikuModel: "auto",
		Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"cursor-grok": {
		Model: "grok-4.5", SubagentModel: "grok-4.5",
		OpusModel: "grok-4.5", SonnetModel: "grok-4.5", HaikuModel: "grok-4.5",
		Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"cursor-mix": {
		Model: "composer-2.5", SubagentModel: "composer-2.5",
		OpusModel: "composer-2.5", SonnetModel: "grok-4.5", HaikuModel: "kimi-k3",
		Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"copilot-auto": {
		Model: "auto", SubagentModel: "auto",
		OpusModel: "auto", SonnetModel: "auto", HaikuModel: "auto",
		Bridge: "copilot",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"copilot-mai": {
		Model: "mai-code-1-flash", SubagentModel: "mai-code-1-flash",
		OpusModel: "mai-code-1-flash", SonnetModel: "mai-code-1-flash", HaikuModel: "mai-code-1-flash",
		Bridge: "copilot",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"copilot-codex": {
		Model: "gpt-5.3-codex", SubagentModel: "gpt-5.3-codex",
		OpusModel: "gpt-5.3-codex", SonnetModel: "gpt-5.3-codex", HaikuModel: "gpt-5.3-codex",
		Bridge: "copilot",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"copilot-claude": {
		Model: "claude-sonnet-4.6", SubagentModel: "claude-sonnet-4.6",
		OpusModel: "claude-sonnet-4.6", SonnetModel: "claude-sonnet-4.6", HaikuModel: "claude-haiku-4.5",
		Bridge: "copilot",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"copilot-gemini": {
		Model: "gemini-3.1-pro-preview", SubagentModel: "gemini-3.1-pro-preview",
		OpusModel: "gemini-3.1-pro-preview", SonnetModel: "gemini-3.5-flash", HaikuModel: "gemini-3.5-flash",
		Bridge: "copilot",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
}

func homeDir() (string, error) {
	if value := os.Getenv("DIALECT_HOME"); value != "" {
		return filepath.Abs(value)
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "claude-dialects"), nil
}

func paths(name string) (home, configPath, proxyPath, authDir, pidPath, logPath, versionPath string, err error) {
	home, err = homeDir()
	if err != nil {
		return
	}
	configPath = filepath.Join(home, "config.json")
	if name != "" {
		instanceDir := filepath.Join(home, "instances", name)
		proxyPath = filepath.Join(instanceDir, "proxy.yaml")
		authDir = filepath.Join(instanceDir, "auth")
		pidPath = filepath.Join(instanceDir, "proxy.pid")
		logPath = filepath.Join(instanceDir, "proxy.log")
		versionPath = filepath.Join(instanceDir, "proxy.version")
	}
	return
}

func claudeConfigDir(name string) (string, error) {
	if !validName(name) {
		return "", operationError(ErrorInvalidInput, "invalid dialect name %q", name)
	}
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "instances", name, "claude"), nil
}

// defaultsMCPPath is the home-level location of the shared MCP server defaults
// passed to every dialect via `--mcp-config`. It lives outside instances/, so
// `remove` — which erases a whole instance directory — structurally cannot
// delete it, and re-creating a dialect keeps its servers.
func defaultsMCPPath() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "defaults", "mcp.json"), nil
}

func ensureClaudeConfigDir(name string) (string, error) {
	instance, err := openInstanceFS(name)
	if err != nil {
		return "", err
	}
	defer instance.Close()
	if err = instance.MkdirAll("claude", 0o700); err != nil {
		return "", fmt.Errorf("create isolated Claude config for %q: %w", name, err)
	}
	if err = instance.Chmod("claude", 0o700); err != nil {
		return "", fmt.Errorf("secure isolated Claude config for %q: %w", name, err)
	}
	return instance.Abs("claude")
}

// openRootChild opens child, a direct entry of parent, as a root in its own
// right and refuses anything that is not a real directory. os.Root confines
// paths to the root it was opened on but still follows symlinks whose targets
// stay inside it, so a nested root is the only way to make the child itself a
// boundary. The already-open root is matched against the parent's directory
// entry with SameFile, which closes the check/open race: an entry swapped
// between the two calls cannot satisfy both. display names the path in errors.
func openRootChild(parent *os.Root, child, display string) (*os.Root, error) {
	root, err := parent.OpenRoot(child)
	if err != nil {
		return nil, err
	}
	valid := false
	defer func() {
		if !valid {
			_ = root.Close()
		}
	}()
	info, err := parent.Lstat(child)
	if err != nil {
		return nil, err
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(info, rootInfo) {
		return nil, fmt.Errorf("%s must be a stable real directory", display)
	}
	valid = true
	return root, nil
}

// instancesRoot returns an os.Root confined to <home>/instances, creating the
// directory when it does not yet exist. DIALECT_HOME itself may be a symlink,
// but the instances child must be a stable real directory.
//
// This root spans every dialect, so it is the boundary for the tree as a whole,
// not for one dialect: use instanceFS for per-dialect I/O, which roots each
// dialect separately. The root is opened per operation so tests may change
// DIALECT_HOME; callers Close it when done.
func instancesRoot() (*os.Root, error) {
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	homeRoot, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	defer homeRoot.Close()
	if err = homeRoot.MkdirAll("instances", 0o700); err != nil {
		return nil, err
	}
	return openRootChild(homeRoot, "instances", filepath.Join(home, "instances"))
}

func defaultConfig() *Config {
	return &Config{
		Version:         configVersion,
		BasePort:        43170,
		Dialects:        map[string]Dialect{},
		NativeLaunchers: map[string]NativeLauncher{},
	}
}

func normalizeConfig(cfg *Config) {
	if cfg.Version < configVersion {
		cfg.Version = configVersion
	}
	if cfg.BasePort == 0 {
		cfg.BasePort = 43170
	}
	if cfg.Dialects == nil {
		cfg.Dialects = map[string]Dialect{}
	}
	if cfg.NativeLaunchers == nil {
		cfg.NativeLaunchers = map[string]NativeLauncher{}
	}
}

// backfillContextWindows gives dialects written before capacity metadata existed
// the reviewed window of the preset whose route they match exactly.
//
// It fills only where the answer is unambiguous: every model, tier, and upstream
// field must equal one preset, whether the dialect names that preset, carries a
// label that has since diverged, or predates the preset field entirely. A dialect
// that matches no preset exactly is left unknown and reported by doctor instead:
// a capacity guessed from a model name could be larger than the route really
// supports, which is worse than no calibration at all. An explicit stored value
// is never overwritten.
//
// Migration runs once per load rather than inside normalizeConfig, which
// configRevision applies to a shallow copy that still shares the dialect map —
// hashing a configuration must not rewrite it. Loading is the only entry point
// that needs it: every writer starts from a loaded configuration, so the value
// reaches disk on the next saveConfig, which is already atomic and owner-only,
// and the revision stays stable across that write.
// It returns the names it filled, sorted, so doctor can report the repair.
func backfillContextWindows(cfg *Config) []string {
	var migrated []string
	for name, dialect := range cfg.Dialects {
		if dialect.ContextWindow != 0 {
			continue
		}
		source := presetCalibratingWindow(dialect)
		preset, ok := presets[source]
		if !ok || !validContextWindow(preset.ContextWindow) {
			continue
		}
		dialect.ContextWindow = preset.ContextWindow
		// Record the preset alongside the window when the dialect carries no
		// label at all, so the configuration stops lagging behind what this
		// match already established and later operations — the doctor remedy,
		// a dashboard save — restate the route instead of rediscovering it. A
		// label that is merely unrecognized is left alone: it may name a preset
		// a newer build owns, and overwriting it would downgrade the dialect to
		// whatever today's build happens to match.
		if dialect.Preset == "" {
			dialect.Preset = source
		}
		cfg.Dialects[name] = dialect
		migrated = append(migrated, name)
	}
	sort.Strings(migrated)
	return migrated
}

// persistContextWindowBackfill records migrated capacity values on disk, so the
// configuration file stops lagging behind the value already used at launch.
//
// This is a deterministic repair, which is what `doctor --fix` promises: it
// writes only the value a load already resolves in memory, for dialects whose
// route matches a reviewed preset exactly. Custom and unmatched dialects stay
// unwritten and keep being reported, because inventing a capacity for them is
// exactly what makes a window unsafe. It takes the state lock itself, so callers
// must not already hold it.
func persistContextWindowBackfill() ([]string, error) {
	var migrated []string
	err := withStateLock(func() error {
		cfg, err := readStoredConfig()
		if err != nil {
			return err
		}
		migrated = backfillContextWindows(cfg)
		if len(migrated) == 0 {
			// Nothing to record. Returning early also keeps a user with no
			// dialects from having a configuration file created for them.
			return nil
		}
		return saveConfig(cfg)
	})
	if err != nil {
		return nil, err
	}
	return migrated, nil
}

// sharesContextRoute reports whether two dialects select exactly the same models
// over the same upstream, which is what makes one's context capacity valid for
// the other. Any divergence means the smallest supported window may differ.
//
// A dialect carrying ExtraEnv shares no route with anything: claudeEnvironment
// applies that map last, so it can replace the base URL, the model variables, or
// the auto-compact window itself, and the stored fields then no longer describe
// what the dialect actually talks to.
func sharesContextRoute(dialect, other Dialect) bool {
	return dialect.Model == other.Model &&
		dialect.SubagentModel == other.SubagentModel &&
		dialect.OpusModel == other.OpusModel &&
		dialect.SonnetModel == other.SonnetModel &&
		dialect.HaikuModel == other.HaikuModel &&
		dialect.AuthProvider == other.AuthProvider &&
		dialect.Bridge == other.Bridge &&
		dialect.BaseURL == other.BaseURL &&
		dialect.AuthTokenEnv == other.AuthTokenEnv &&
		len(dialect.ExtraEnv) == 0 && len(other.ExtraEnv) == 0
}

// presetByContextRoute names the preset a dialect is field-for-field identical
// to, or "" when none is or more than one is.
//
// The stored preset name is a label, and dialects created before that label
// existed carry none — but a dialect that selects exactly a preset's models over
// exactly its upstream *is* that route, and the preset's reviewed capacity
// describes it as faithfully as if the name had been written down. Equality is
// what makes that true, so this asks for nothing weaker: presetForDialect, which
// exists to display a likely preset, matches on the primary model alone and
// would hand a dialect with one hand-swapped tier a window larger than its
// smallest model supports.
//
// Two presets sharing one route would make the answer arbitrary, so a second
// match yields "" — the dialect stays unknown and doctor keeps reporting it,
// which is the same outcome as before any of this could be resolved.
func presetByContextRoute(dialect Dialect) string {
	var match string
	for name, preset := range presets {
		if !sharesContextRoute(dialect, preset) {
			continue
		}
		if match != "" {
			return ""
		}
		match = name
	}
	return match
}

// readStoredConfig returns the configuration exactly as it sits on disk. Only
// migration needs this unmigrated view, so it can tell what is already recorded
// apart from what a load would have supplied in memory; every other caller wants
// loadConfig.
func readStoredConfig() (*Config, error) {
	_, path, _, _, _, _, _, err := paths("")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err = json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	normalizeConfig(&cfg)
	return &cfg, nil
}

func loadConfig() (*Config, error) {
	cfg, err := readStoredConfig()
	if err != nil {
		return nil, err
	}
	backfillContextWindows(cfg)
	return cfg, nil
}

func configRevision(cfg *Config) (string, error) {
	copy := *cfg
	normalizeConfig(&copy)
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func saveConfig(cfg *Config) error {
	home, path, _, _, _, _, _, err := paths("")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	normalizeConfig(cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0o600)
}

type atomicWriteError struct {
	err       error
	committed bool
}

func (e *atomicWriteError) Error() string   { return e.err.Error() }
func (e *atomicWriteError) Unwrap() error   { return e.err }
func (e *atomicWriteError) Committed() bool { return e.committed }

func atomicWriteCommitted(err error) bool {
	var writeErr *atomicWriteError
	return errors.As(err, &writeErr) && writeErr.committed
}

func syncDirectory(directory *os.File) error {
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil && !errors.Is(syncErr, syscall.EINVAL) && !errors.Is(syncErr, syscall.ENOTSUP) {
		return syncErr
	}
	return closeErr
}

var syncParentDirectory = func(dir string) error {
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	return syncDirectory(directory)
}

var syncParentDirectoryAt = func(root *os.Root, dir string) error {
	directory, err := root.Open(dir)
	if err != nil {
		return err
	}
	return syncDirectory(directory)
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		if err != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err = temp.Chmod(mode); err != nil {
		return err
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tempPath, path); err != nil {
		return err
	}
	if syncErr := syncParentDirectory(dir); syncErr != nil {
		return &atomicWriteError{err: syncErr, committed: true}
	}
	return nil
}

// atomicWriteFileAt is the root-confined counterpart of atomicWriteFile: it
// writes data to root/relPath atomically, refusing to escape the instances root.
// Every filesystem operation, including the parent-directory sync, is resolved
// through root.
//
// os.Root has no CreateTemp, so the temp file is created beside the destination
// with OpenFile using O_RDWR|O_CREATE|O_EXCL and a random suffix, retrying on
// EEXIST to mirror os.CreateTemp's contract. The atomicWriteError/
// atomicWriteCommitted semantics are preserved exactly: seedStatusline relies
// on them for its opt-out logic.
func atomicWriteFileAt(root *os.Root, relPath string, data []byte, mode os.FileMode) (err error) {
	relDir := filepath.Dir(relPath)
	if err = root.MkdirAll(relDir, 0o700); err != nil {
		return err
	}
	base := filepath.Base(relPath)
	var tempName string
	var temp *os.File
	for {
		suffix, sErr := randomSuffix()
		if sErr != nil {
			return sErr
		}
		tempName = filepath.Join(relDir, "."+base+".tmp-"+suffix)
		temp, err = root.OpenFile(tempName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil || !errors.Is(err, os.ErrExist) {
			break
		}
	}
	if err != nil {
		return err
	}
	defer func() {
		_ = temp.Close()
		if err != nil {
			_ = root.Remove(tempName)
		}
	}()
	if err = temp.Chmod(mode); err != nil {
		return err
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = root.Rename(tempName, relPath); err != nil {
		return err
	}
	if syncErr := syncParentDirectoryAt(root, relDir); syncErr != nil {
		return &atomicWriteError{err: syncErr, committed: true}
	}
	return nil
}

// rootChmod changes the mode of root/rel. os.Root has no Chmod method, so the
// entry is opened through the root (which keeps it confined) and chmod'd via
// the file descriptor; on Unix fchmod does not require write access.
func rootChmod(root *os.Root, rel string, mode os.FileMode) error {
	entry, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer entry.Close()
	return entry.Chmod(mode)
}

// A removal that fails is retried, mirroring os.RemoveAll: an entry created
// after the scan fails the Remove with ENOTEMPTY and would orphan the instance,
// which matters because RemoveDialect has already committed the config change
// by then. Retries are bounded so a directory another process is actively
// refilling surfaces the error instead of spinning. The bound is the only stop
// condition — an empty scan is no reason to give up, since the racing write
// that empties-then-refills is exactly the case this exists for.
const maxRemoveAllRescans = 8

// removeAllUnder recursively empties dir within an already-open root, removing
// dir itself unless it is the root (". "). The caller unlinks the root's own
// entry from its parent.
//
// Entries are classified from the readdir result rather than a second Lstat, so
// there is no window between deciding what an entry is and acting on it: a
// symlink is unlinked on the strength of its directory-entry type instead of
// being re-resolved by name. Everything resolves through the caller's root, so
// a link swapped in mid-removal cannot redirect the recursion outside it.
func removeAllUnder(root *os.Root, dir string) error {
	entries, err := readDirAt(root, dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		child := entry.Name()
		if dir != "." {
			child = filepath.Join(dir, child)
		}
		if entry.IsDir() {
			err = removeAllUnder(root, child)
		} else {
			err = root.Remove(child)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if dir == "." {
		return nil
	}
	if err = root.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

var readDirAt = func(root *os.Root, rel string) ([]os.DirEntry, error) {
	dir, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	return entries, errors.Join(readErr, closeErr)
}

func writeProxyConfig(name string, dialect Dialect) (string, error) {
	instance, err := openInstanceFS(name)
	if err != nil {
		return "", err
	}
	defer instance.Close()
	return writeProxyConfigAt(instance, dialect)
}

func writeProxyConfigAt(instance *instanceFS, dialect Dialect) (string, error) {
	name := instance.name
	path, err := instance.Abs("proxy.yaml")
	if err != nil {
		return "", err
	}
	authDir, err := instance.Abs("auth")
	if err != nil {
		return "", err
	}
	content := fmt.Sprintf(`host: "127.0.0.1"
port: %d
auth-dir: %q
api-keys:
  - %q
remote-management:
  allow-remote: false
  secret-key: ""
  disable-control-panel: true
debug: false
logging-to-file: false
usage-statistics-enabled: false
`, dialect.Port, authDir, dialect.APIKey)
	if dialect.BaseURL != "" {
		token := os.Getenv(dialect.AuthTokenEnv)
		if token == "" {
			return "", fmt.Errorf("%s is not set for %q", dialect.AuthTokenEnv, name)
		}
		content += fmt.Sprintf(`claude-api-key:
  - api-key: %q
    base-url: %q
    models:
`, token, dialect.BaseURL)
		seen := map[string]bool{}
		for _, model := range []string{dialect.Model, dialect.OpusModel, dialect.SonnetModel, dialect.HaikuModel, dialect.SubagentModel} {
			if model != "" && !seen[model] {
				content += fmt.Sprintf("      - name: %q\n        alias: %q\n", model, model)
				seen[model] = true
			}
		}
	}
	if dialect.Bridge != "" {
		models, modelsErr := fetchBridgeModels(dialect)
		if modelsErr != nil {
			models = nil
		}
		models = mergeModels(models, dialectModels(dialect))
		content += fmt.Sprintf(`openai-compatibility:
  - name: %q
    base-url: %q
    api-key-entries:
      - api-key: %q
    models:
`, dialect.Bridge, fmt.Sprintf("http://127.0.0.1:%d/v1", dialect.BridgePort), dialect.APIKey)
		for _, model := range models {
			content += fmt.Sprintf("      - name: %q\n        alias: %q\n", model, model)
		}
	}
	// CLIProxyAPI requires an absolute auth-dir string, but directory creation and
	// config persistence remain confined to the instances root.
	if err = instance.MkdirAll("auth", 0o700); err != nil {
		return "", err
	}
	if err = instance.AtomicWrite("proxy.yaml", []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func newAPIKey() (string, error) {
	key := make([]byte, 24)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

// randomSuffix returns a short hex string for a uniquely-named temp file inside
// the instances root, replacing the random component os.CreateTemp would
// normally generate (os.Root has no CreateTemp).
func randomSuffix() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func nextPort(cfg *Config) int {
	return nextAvailablePort(cfg, nil)
}

func nextAvailablePort(cfg *Config, additionallyUsed map[int]bool) int {
	used := make(map[int]bool, len(cfg.Dialects)*2+len(additionallyUsed))
	for _, dialect := range cfg.Dialects {
		used[dialect.Port] = true
		if dialect.BridgePort != 0 {
			used[dialect.BridgePort] = true
		}
	}
	for port := range additionallyUsed {
		used[port] = true
	}
	for port := cfg.BasePort; port < 65535; port++ {
		if !used[port] && portAvailable(port) {
			return port
		}
	}
	return 0
}

func portAvailable(port int) bool {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// portBusy reports whether something is actually listening on a dialect's
// loopback port. It is the liveness signal used when a PID record cannot be
// read: a runtime that is wedged or still starting fails a health probe while
// very much alive, but still holds its port.
//
// This is deliberately not !portAvailable. A port that merely cannot be bound —
// a privileged one, say — says nothing about whether the dialect is alive, and
// treating it as busy would block cleanup forever. Only "address in use"
// counts. It is a var so tests can describe the runtime state they mean instead
// of depending on what happens to be listening on the developer's machine.
var portBusy = func(port int) bool {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		_ = listener.Close()
		return false
	}
	return errors.Is(err, syscall.EADDRINUSE)
}

func presetNames() []string {
	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func presetForDialect(dialect Dialect) string {
	if _, ok := presets[dialect.Preset]; ok {
		return dialect.Preset
	}
	switch dialect.AuthProvider {
	case "codex":
		if dialect.Model == presets["codex-sol"].Model {
			return "codex-sol"
		}
		return "codex"
	case "kimi":
		return "kimi"
	case "antigravity":
		return "gemini"
	case "claude":
		return "claude"
	case "xai":
		switch {
		case strings.HasPrefix(dialect.Model, "grok-composer-"):
			return "composer"
		case strings.HasPrefix(dialect.Model, "grok-build-"):
			return "grok-build"
		default:
			return "grok"
		}
	}
	if dialect.Bridge == "cursor" {
		// cursor-mix selects Composer 2.5 as its primary model — the same ID as
		// cursor-composer — so the primary model cannot tell the two apart. Its
		// distinct Opus/Sonnet/Haiku tier mapping is what identifies it.
		if dialect.OpusModel == "composer-2.5" &&
			dialect.SonnetModel == "grok-4.5" &&
			dialect.HaikuModel == "kimi-k3" {
			return "cursor-mix"
		}
		switch dialect.Model {
		case "composer-2.5":
			return "cursor-composer"
		case "composer-2.5-fast":
			return "cursor-composer-fast"
		case "auto":
			return "cursor-auto"
		case "grok-4.5":
			return "cursor-grok"
		default:
			return ""
		}
	}
	if dialect.Bridge == "copilot" {
		switch dialect.Model {
		case "auto":
			return "copilot-auto"
		case "mai-code-1-flash":
			return "copilot-mai"
		case "gpt-5.3-codex":
			return "copilot-codex"
		case "claude-sonnet-4.6":
			return "copilot-claude"
		case "gemini-3.1-pro-preview":
			return "copilot-gemini"
		default:
			return ""
		}
	}
	if dialect.AuthTokenEnv == "ZAI_API_KEY" ||
		strings.Contains(strings.ToLower(dialect.BaseURL), "z.ai") ||
		strings.Contains(strings.ToLower(dialect.BaseURL), "bigmodel.cn") {
		return "glm"
	}
	if dialect.AuthTokenEnv == "MINIMAX_API_KEY" ||
		strings.Contains(strings.ToLower(dialect.BaseURL), "minimax.io") {
		return "minimax"
	}
	if dialect.AuthTokenEnv == "DEEPSEEK_API_KEY" ||
		strings.Contains(strings.ToLower(dialect.BaseURL), "deepseek.com") {
		return "deepseek"
	}
	switch {
	case strings.HasPrefix(dialect.Model, "gpt-"):
		if strings.Contains(dialect.Model, "-sol") {
			return "codex-sol"
		}
		return "codex"
	case strings.HasPrefix(dialect.Model, "kimi-"):
		return "kimi"
	case strings.HasPrefix(dialect.Model, "gemini-"):
		return "gemini"
	case strings.HasPrefix(dialect.Model, "claude-"):
		return "claude"
	case strings.HasPrefix(dialect.Model, "glm-"):
		return "glm"
	case strings.HasPrefix(dialect.Model, "grok-composer-"):
		return "composer"
	case strings.HasPrefix(dialect.Model, "grok-build-"):
		return "grok-build"
	case strings.HasPrefix(dialect.Model, "grok-"):
		return "grok"
	case strings.HasPrefix(strings.ToLower(dialect.Model), "minimax-"):
		return "minimax"
	case strings.HasPrefix(dialect.Model, "deepseek-"):
		return "deepseek"
	default:
		return ""
	}
}

// providerForModel maps a model ID to the OAuth provider that serves it, or ""
// for models reached through an API-key upstream or a managed bridge instead.
func providerForModel(model string) string {
	switch {
	case strings.HasPrefix(model, "gpt-"):
		return "codex"
	case strings.HasPrefix(model, "kimi-"):
		return "kimi"
	case strings.HasPrefix(model, "gemini-"):
		return "antigravity"
	case strings.HasPrefix(model, "claude-"):
		return "claude"
	case strings.HasPrefix(model, "grok-"):
		return "xai"
	default:
		return ""
	}
}

// expectedAuthProviders returns the OAuth providers a dialect needs credentials
// for, derived from its final tier model mapping so it stays correct when
// individual tiers are overridden. A dialect spanning several providers needs
// each authenticated; bridge and upstream API-key routes carry their own
// credentials and return none.
func expectedAuthProviders(dialect Dialect) []string {
	if dialect.Bridge != "" || dialect.BaseURL != "" {
		return nil
	}
	seen := map[string]bool{}
	var providers []string
	for _, model := range []string{dialect.Model, dialect.SubagentModel, dialect.OpusModel, dialect.SonnetModel, dialect.HaikuModel} {
		if provider := providerForModel(model); provider != "" && !seen[provider] {
			seen[provider] = true
			providers = append(providers, provider)
		}
	}
	// A custom model ID that matches no known prefix would otherwise drop a
	// preset's stored OAuth route, so always keep the explicit provider when it
	// is not already covered by the derived models.
	if dialect.AuthProvider != "" && !seen[dialect.AuthProvider] {
		seen[dialect.AuthProvider] = true
		providers = append(providers, dialect.AuthProvider)
	}
	return providers
}

func providerForDialect(dialect Dialect) string {
	if len(expectedAuthProviders(dialect)) > 1 {
		return "mixed"
	}
	switch presetForDialect(dialect) {
	case "codex", "codex-sol":
		return "codex"
	case "kimi":
		return "kimi"
	case "gemini":
		return "gemini"
	case "claude":
		return "claude"
	case "glm":
		return "glm"
	case "grok", "grok-build", "composer":
		return "xai"
	case "minimax":
		return "minimax"
	case "deepseek":
		return "deepseek"
	case "cursor-composer", "cursor-composer-fast", "cursor-auto", "cursor-grok", "cursor-mix":
		return "cursor"
	case "copilot-auto", "copilot-mai", "copilot-codex", "copilot-claude", "copilot-gemini":
		return "copilot"
	default:
		if dialect.Bridge == "cursor" {
			return "cursor"
		}
		if dialect.Bridge == "copilot" {
			return "copilot"
		}
		return dialect.AuthProvider
	}
}

func dialectModels(dialect Dialect) []string {
	seen := map[string]bool{}
	var models []string
	for _, model := range []string{dialect.Model, dialect.OpusModel, dialect.SonnetModel, dialect.HaikuModel, dialect.SubagentModel} {
		if model != "" && !seen[model] {
			seen[model] = true
			models = append(models, model)
		}
	}
	return models
}

func mergeModels(groups ...[]string) []string {
	seen := map[string]bool{}
	var models []string
	for _, group := range groups {
		for _, model := range group {
			if model != "" && !seen[model] {
				seen[model] = true
				models = append(models, model)
			}
		}
	}
	return models
}

func validName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return !strings.HasPrefix(name, "-")
}
