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
	ExtraEnv      map[string]string `json:"extraEnv,omitempty"`
}

var presets = map[string]Dialect{
	"codex-sol": {
		Model: "gpt-5.6-sol", SubagentModel: "gpt-5.6-sol",
		OpusModel: "gpt-5.6-sol", SonnetModel: "gpt-5.6-terra", HaikuModel: "gpt-5.6-luna",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"codex": {
		Model: "gpt-5.6", SubagentModel: "gpt-5.6",
		OpusModel: "gpt-5.6-sol", SonnetModel: "gpt-5.6-terra", HaikuModel: "gpt-5.6-luna",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"kimi": {
		Model: "kimi-k3", SubagentModel: "kimi-k3",
		OpusModel: "kimi-k3", SonnetModel: "kimi-k2.7-code-highspeed", HaikuModel: "kimi-k2.6",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"gemini": {
		Model: "gemini-3.1-pro-preview", SubagentModel: "gemini-3.1-pro-preview",
		OpusModel: "gemini-3.1-pro-preview", SonnetModel: "gemini-3.5-flash", HaikuModel: "gemini-3.5-flash-low",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"claude": {
		Model: "claude-fable-5", SubagentModel: "claude-fable-5",
		OpusModel: "claude-fable-5", SonnetModel: "claude-sonnet-4-6", HaikuModel: "claude-haiku-4-5",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
	},
	"glm": {
		Model: "glm-5", SubagentModel: "glm-5",
		OpusModel: "glm-5", SonnetModel: "glm-4.7", HaikuModel: "glm-4.6",
		Effort: true, EffortLevel: "auto", Concurrency: 3, ToolSearch: false,
		BaseURL: "https://open.bigmodel.cn/api/anthropic", AuthTokenEnv: "ZAI_API_KEY",
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
	used := make(map[int]bool, len(cfg.Dialects))
	for _, dialect := range cfg.Dialects {
		used[dialect.Port] = true
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
