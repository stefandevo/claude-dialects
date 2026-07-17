package app

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNextPortKeepsDialectsIsolated(t *testing.T) {
	cfg := &Config{
		BasePort: 43170,
		Dialects: map[string]Dialect{
			"claudex": {Port: 43170},
			"kimi":    {Port: 43172},
		},
	}
	if got := nextPort(cfg); got != 43171 {
		t.Fatalf("nextPort() = %d, want 43171", got)
	}
}

func TestWriteProxyConfigUsesPerDialectPathsAndSecrets(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	first := Dialect{Port: 43170, APIKey: "first-secret"}
	second := Dialect{Port: 43171, APIKey: "second-secret"}

	firstPath, err := writeProxyConfig("claudex", first)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := writeProxyConfig("kimi", second)
	if err != nil {
		t.Fatal(err)
	}
	if firstPath == secondPath {
		t.Fatal("dialects share a proxy config path")
	}
	firstData, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(firstData)
	if !strings.Contains(text, "port: 43170") || !strings.Contains(text, `"first-secret"`) {
		t.Fatalf("unexpected proxy config:\n%s", text)
	}
	if strings.Contains(text, "second-secret") {
		t.Fatal("proxy config leaked another dialect's key")
	}
	info, err := os.Stat(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("proxy config permissions = %o, want 600", info.Mode().Perm())
	}
	if filepath.Dir(firstPath) == filepath.Dir(secondPath) {
		t.Fatal("dialects share an instance directory")
	}
}

func TestLoadConfigCreatesPrivateConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BasePort != 43170 || cfg.Dialects == nil {
		t.Fatalf("unexpected default config: %#v", cfg)
	}
	info, err := os.Stat(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestNextPortSkipsBoundPort(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	bound := listener.Addr().(*net.TCPAddr).Port
	cfg := &Config{BasePort: bound, Dialects: map[string]Dialect{}}
	if got := nextPort(cfg); got == bound {
		t.Fatalf("nextPort selected occupied port %d", bound)
	}
}

func TestSetEnvReplacesInheritedValue(t *testing.T) {
	got := setEnv([]string{"A=old", "B=kept"}, "A", "new")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "A=old") || !strings.Contains(joined, "A=new") || !strings.Contains(joined, "B=kept") {
		t.Fatalf("setEnv returned %q", got)
	}
}

func TestClaudeConfigDirectoriesAreIsolatedAndPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)

	claudex, err := ensureClaudeConfigDir("claudex")
	if err != nil {
		t.Fatal(err)
	}
	kimi, err := ensureClaudeConfigDir("kimi")
	if err != nil {
		t.Fatal(err)
	}
	if claudex == kimi {
		t.Fatal("dialects share a Claude config directory")
	}
	if want := filepath.Join(home, "instances", "claudex", "claude"); claudex != want {
		t.Fatalf("Claude config directory = %q, want %q", claudex, want)
	}
	info, err := os.Stat(claudex)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("Claude config mode = %v, want private directory 0700", info.Mode())
	}
}

func TestNativeDangerousLauncherUsesNormalClaudeConfiguration(t *testing.T) {
	body := nativeLauncherBody("/Users/example/.local/bin/claude", true)
	if !strings.Contains(body, `exec "/Users/example/.local/bin/claude" --dangerously-skip-permissions "$@"`) {
		t.Fatalf("unexpected native launcher:\n%s", body)
	}
	if strings.Contains(body, "CLAUDE_CONFIG_DIR") || strings.Contains(body, "ANTHROPIC_BASE_URL") {
		t.Fatalf("native launcher unexpectedly changes Claude configuration:\n%s", body)
	}
}
