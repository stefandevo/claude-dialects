package app

import (
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestNextPortKeepsDialectsIsolated(t *testing.T) {
	base := availablePortRange(t, 3)
	cfg := &Config{
		BasePort: base,
		Dialects: map[string]Dialect{
			"claudex": {Port: base},
			"kimi":    {Port: base + 2},
		},
	}
	if got := nextPort(cfg); got != base+1 {
		t.Fatalf("nextPort() = %d, want %d", got, base+1)
	}
}

func availablePortRange(t *testing.T, count int) int {
	t.Helper()
	for base := 45000; base+count < 65535; base += count {
		available := true
		for port := base; port < base+count; port++ {
			if !portAvailable(port) {
				available = false
				break
			}
		}
		if available {
			return base
		}
	}
	t.Fatal("no available local port range")
	return 0
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

func TestWriteProxyConfigRoutesLatestGLMModels(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	t.Setenv("ZAI_API_KEY", "zai-secret")
	glm := presets["glm"]
	glm.Port = 43173
	glm.APIKey = "local-secret"

	path, err := writeProxyConfig("cc-glm", glm)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{
		`base-url: "https://api.z.ai/api/anthropic"`,
		`name: "glm-5.2"`,
		`name: "glm-5-turbo"`,
		`name: "glm-4.5-air"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("GLM proxy config does not contain %q:\n%s", expected, text)
		}
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

func TestProxyVersionComesFromBuildMetadata(t *testing.T) {
	info := &debug.BuildInfo{
		Deps: []*debug.Module{
			{Path: "example.com/other", Version: "v1.0.0"},
			{Path: "github.com/router-for-me/CLIProxyAPI/v7", Version: "v7.2.86"},
		},
	}
	if got := proxyVersionFromBuildInfo(info); got != "v7.2.86" {
		t.Fatalf("proxy version = %q, want v7.2.86", got)
	}
}

func TestProxyVersionUsesReplacementMetadata(t *testing.T) {
	info := &debug.BuildInfo{
		Deps: []*debug.Module{
			{
				Path:    "github.com/router-for-me/CLIProxyAPI/v7",
				Version: "v7.2.86",
				Replace: &debug.Module{Path: "example.com/fork", Version: "v7.3.0-fork"},
			},
		},
	}
	if got := proxyVersionFromBuildInfo(info); got != "v7.3.0-fork" {
		t.Fatalf("proxy replacement version = %q, want v7.3.0-fork", got)
	}
}

func TestKimiPresetUsesK3(t *testing.T) {
	kimi := presets["kimi"]
	if kimi.Model != "kimi-k3" || kimi.SubagentModel != "kimi-k3" || kimi.OpusModel != "kimi-k3" {
		t.Fatalf("Kimi preset does not use K3 for main, subagent, and opus: %#v", kimi)
	}
	if kimi.EffortLevel != "auto" {
		t.Fatalf("Kimi preset effort = %q, want auto so the provider can use K3's supported default", kimi.EffortLevel)
	}
}

func TestGeminiPresetUsesAntigravityModelAliases(t *testing.T) {
	gemini := presets["gemini"]
	if gemini.Model != "gemini-pro-agent" || gemini.SubagentModel != "gemini-pro-agent" || gemini.OpusModel != "gemini-pro-agent" {
		t.Fatalf("Gemini preset does not use the Antigravity Pro alias: %#v", gemini)
	}
	if gemini.SonnetModel != "gemini-3.5-flash-low" || gemini.HaikuModel != "gemini-3.5-flash-extra-low" {
		t.Fatalf("Gemini preset has unsupported Antigravity Flash aliases: %#v", gemini)
	}
	if gemini.AuthProvider != "antigravity" {
		t.Fatalf("Gemini preset auth provider = %q, want antigravity", gemini.AuthProvider)
	}
}

func TestGLMPresetUsesLatestModelsAndEndpoint(t *testing.T) {
	glm := presets["glm"]
	if glm.Model != "glm-5.2" || glm.SubagentModel != "glm-5.2" || glm.OpusModel != "glm-5.2" {
		t.Fatalf("GLM preset does not use GLM-5.2 for main, subagent, and opus: %#v", glm)
	}
	if glm.SonnetModel != "glm-5-turbo" || glm.HaikuModel != "glm-4.5-air" {
		t.Fatalf("GLM preset has unexpected lower-tier model mappings: %#v", glm)
	}
	if glm.BaseURL != "https://api.z.ai/api/anthropic" {
		t.Fatalf("GLM preset endpoint = %q, want current Z.ai Anthropic endpoint", glm.BaseURL)
	}
	if glm.AuthTokenEnv != "ZAI_API_KEY" {
		t.Fatalf("GLM preset token environment = %q, want ZAI_API_KEY", glm.AuthTokenEnv)
	}
}

func TestCreateNextStepsAuthenticateBeforeInstallingShim(t *testing.T) {
	got := createNextSteps("cc-codex", "cc-codex", presets["codex-sol"])
	want := []string{
		"Authenticate: cc-dialect auth cc-codex codex",
		"Install the command: cc-dialect shim install cc-codex",
		"Run: cc-codex",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("createNextSteps() = %q, want %q", got, want)
	}
}

func TestCreateNextStepsUseCollisionSafeShimName(t *testing.T) {
	got := createNextSteps("gemini", "geminix", presets["gemini"])
	want := []string{
		"Authenticate: cc-dialect auth gemini antigravity",
		"Install the command: cc-dialect shim install gemini --name geminix",
		"Run: geminix",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("createNextSteps() = %q, want %q", got, want)
	}
}

func TestCreateNextStepsExplainAPIToken(t *testing.T) {
	got := createNextSteps("glmx", "glmx", presets["glm"])
	if len(got) == 0 || got[0] != "Set the provider token: export ZAI_API_KEY=your_token" {
		t.Fatalf("createNextSteps() = %q, want API token instruction first", got)
	}
}

func TestHasProviderCredentialsMatchesCredentialType(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	_, _, _, authDir, _, _, err := paths("cc-codex")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(authDir, "account.json"), []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasProviderCredentials("cc-codex", "codex") {
		t.Fatal("Codex credentials were not detected")
	}
	if hasProviderCredentials("cc-codex", "kimi") {
		t.Fatal("Codex credentials incorrectly matched Kimi")
	}
}

func TestCommandConflictsFindsOtherExecutables(t *testing.T) {
	targetDir := t.TempDir()
	otherDir := t.TempDir()
	target := filepath.Join(targetDir, "gemini")
	other := filepath.Join(otherDir, "gemini")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{targetDir, otherDir, otherDir}, string(os.PathListSeparator)))

	conflicts := commandConflicts("gemini", target)
	expected, err := filepath.Abs(other)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0] != expected {
		t.Fatalf("commandConflicts() = %q, want [%q]", conflicts, expected)
	}
}

func TestSuggestedShimName(t *testing.T) {
	if got := suggestedShimName("gemini"); got != "geminix" {
		t.Fatalf("suggestedShimName(gemini) = %q, want geminix", got)
	}
	if got := suggestedShimName("claudex"); got != "claudex-dialect" {
		t.Fatalf("suggestedShimName(claudex) = %q, want claudex-dialect", got)
	}
}

func TestUsageShowsRemoveCommand(t *testing.T) {
	if !strings.Contains(usage, "\n  cc-dialect remove <name>\n") {
		t.Fatal("usage does not show the remove command on its own line")
	}
}
