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
	Preset        string            `json:"preset,omitempty"`
	Model         string            `json:"model"`
	SubagentModel string            `json:"subagentModel,omitempty"`
	Effort        bool              `json:"effort"`
	Concurrency   int               `json:"concurrency"`
	ToolSearch    bool              `json:"toolSearch"`
	OpusModel     string            `json:"opusModel,omitempty"`
	SonnetModel   string            `json:"sonnetModel,omitempty"`
	HaikuModel    string            `json:"haikuModel,omitempty"`
	EffortLevel   string            `json:"effortLevel,omitempty"`
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
	"glm": {
		Model: "glm-5.2", SubagentModel: "glm-5.2",
		OpusModel: "glm-5.2", SonnetModel: "glm-5-turbo", HaikuModel: "glm-4.5-air",
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

func ensureClaudeConfigDir(name string) (string, error) {
	path, err := claudeConfigDir(name)
	if err != nil {
		return "", err
	}
	root, err := instancesRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()
	rel := filepath.Join(name, "claude")
	if err = root.MkdirAll(rel, 0o700); err != nil {
		return "", fmt.Errorf("create isolated Claude config for %q: %w", name, err)
	}
	if err = rootChmod(root, rel, 0o700); err != nil {
		return "", fmt.Errorf("secure isolated Claude config for %q: %w", name, err)
	}
	return path, nil
}

// instancesRoot returns an os.Root confined to <home>/instances, creating the
// directory when it does not yet exist. os.Root refuses to traverse symlinks or
// escape its root, which closes the symlink-escape gap that validName (a
// path-traversal guard) cannot. It is opened per operation so DIALECT_HOME can
// change between calls (as it does under test); callers Close it when done.
func instancesRoot() (*os.Root, error) {
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "instances")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
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

func loadConfig() (*Config, error) {
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

// removeAllAt removes root/rel recursively, like os.RemoveAll but confined to
// the instances root. It lstats each entry so a symlinked instance (or a
// symlink within it) is unlinked rather than followed — os.RemoveAll would
// descend into and delete the symlink's target outside the tree.
func removeAllAt(root *os.Root, rel string) error {
	info, err := root.Lstat(rel)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return root.Remove(rel)
	}
	dir, err := root.Open(rel)
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	dir.Close()
	if readErr != nil {
		return readErr
	}
	for _, entry := range entries {
		if err := removeAllAt(root, filepath.Join(rel, entry.Name())); err != nil {
			return err
		}
	}
	return root.Remove(rel)
}

func writeProxyConfig(name string, dialect Dialect) (string, error) {
	home, _, path, authDir, _, _, _, err := paths(name)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(home, 0o700); err != nil {
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
	// Confine the auth directory and proxy config write to the instances root so
	// a symlinked instance cannot escape the tree. authDir (absolute) is still
	// embedded in the config for the proxy process to read.
	root, err := instancesRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err = root.MkdirAll(filepath.Join(name, "auth"), 0o700); err != nil {
		return "", err
	}
	if err = atomicWriteFileAt(root, filepath.Join(name, "proxy.yaml"), []byte(content), 0o600); err != nil {
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
	case "cursor-composer", "cursor-composer-fast", "cursor-auto", "cursor-grok":
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
