package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Config struct {
	Version  int                `json:"version"`
	BasePort int                `json:"basePort"`
	Dialects map[string]Dialect `json:"dialects"`
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

func paths(name string) (home, configPath, proxyPath, authDir, pidPath, logPath string, err error) {
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
	}
	return
}

func claudeConfigDir(name string) (string, error) {
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
	if err = os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("create isolated Claude config for %q: %w", name, err)
	}
	if err = os.Chmod(path, 0o700); err != nil {
		return "", fmt.Errorf("secure isolated Claude config for %q: %w", name, err)
	}
	return path, nil
}

func loadConfig() (*Config, error) {
	home, path, _, _, _, _, err := paths("")
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := &Config{Version: 1, BasePort: 43170, Dialects: map[string]Dialect{}}
		return cfg, saveConfig(cfg)
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err = json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if cfg.Dialects == nil {
		cfg.Dialects = map[string]Dialect{}
	}
	if cfg.BasePort == 0 {
		cfg.BasePort = 43170
	}
	return &cfg, nil
}

func saveConfig(cfg *Config) error {
	home, path, _, _, _, _, err := paths("")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp := path + ".tmp"
	if err = os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	if err = os.Chmod(temp, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func writeProxyConfig(name string, dialect Dialect) (string, error) {
	home, _, path, authDir, _, _, err := paths(name)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(authDir, 0o700); err != nil {
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
	if dialect.Bridge == "cursor" {
		models, modelsErr := fetchCursorModels(dialect)
		if modelsErr != nil {
			models = nil
		}
		models = mergeModels(models, dialectModels(dialect))
		content += fmt.Sprintf(`openai-compatibility:
  - name: "cursor"
    base-url: %q
    api-key-entries:
      - api-key: %q
    models:
`, fmt.Sprintf("http://127.0.0.1:%d/v1", dialect.BridgePort), dialect.APIKey)
		for _, model := range models {
			content += fmt.Sprintf("      - name: %q\n        alias: %q\n", model, model)
		}
	}
	if err = os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if err = os.WriteFile(path, []byte(content), 0o600); err != nil {
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

func providerForDialect(dialect Dialect) string {
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
	default:
		if dialect.Bridge == "cursor" {
			return "cursor"
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
