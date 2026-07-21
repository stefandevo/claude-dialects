package app

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
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

func TestNextPortReservesProviderBridgePorts(t *testing.T) {
	base := availablePortRange(t, 4)
	cfg := &Config{
		BasePort: base,
		Dialects: map[string]Dialect{
			"cursorx": {Port: base, Bridge: "cursor", BridgePort: base + 1},
		},
	}
	if got := nextPort(cfg); got != base+2 {
		t.Fatalf("nextPort() = %d, want %d after proxy and bridge reservations", got, base+2)
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

func TestWriteProxyConfigRoutesCursorModelsThroughPrivateBridge(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	const privateKey = "cursor-bridge-secret"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+privateKey {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":[{"id":"composer-2.5"},{"id":"composer-2.5-fast"}]}`))
	}))
	defer server.Close()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	bridgePort, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	cursor := presets["cursor-composer"]
	cursor.Port = availablePortRange(t, 1)
	cursor.BridgePort = bridgePort
	cursor.APIKey = privateKey

	path, err := writeProxyConfig("cc-cursor", cursor)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{
		`name: "cursor"`,
		`base-url: "http://127.0.0.1:` + portText + `/v1"`,
		`api-key: "cursor-bridge-secret"`,
		`name: "composer-2.5"`,
		`name: "composer-2.5-fast"`,
		`name: "composer-2.5-standard"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Cursor proxy config does not contain %q:\n%s", expected, text)
		}
	}
}

func TestWriteProxyConfigRoutesCopilotModelsThroughPrivateBridge(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	const privateKey = "copilot-bridge-secret"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+privateKey {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":[{"id":"mai-code-1-flash"},{"id":"gpt-5.3-codex"}]}`))
	}))
	defer server.Close()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	bridgePort, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	copilot := presets["copilot-mai"]
	copilot.Port = availablePortRange(t, 1)
	copilot.BridgePort = bridgePort
	copilot.APIKey = privateKey

	path, err := writeProxyConfig("cc-copilot-mai", copilot)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{
		`name: "copilot"`,
		`base-url: "http://127.0.0.1:` + portText + `/v1"`,
		`api-key: "copilot-bridge-secret"`,
		`name: "mai-code-1-flash"`,
		`name: "gpt-5.3-codex"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Copilot proxy config does not contain %q:\n%s", expected, text)
		}
	}
}

func TestLoadConfigReturnsDefaultsWithoutWriting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != configVersion || cfg.BasePort != 43170 || cfg.Dialects == nil || cfg.NativeLaunchers == nil {
		t.Fatalf("unexpected default config: %#v", cfg)
	}
	if _, err = os.Stat(filepath.Join(home, "config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loadConfig wrote defaults unexpectedly: %v", err)
	}
	if err = saveConfig(cfg); err != nil {
		t.Fatal(err)
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

func TestXAIPresetsUseDistinctModelsAndOAuth(t *testing.T) {
	tests := map[string]string{
		"grok":       "grok-4.5",
		"grok-build": "grok-build-0.1",
		"composer":   "grok-composer-2.5-fast",
	}
	for name, model := range tests {
		preset := presets[name]
		if preset.Model != model || preset.SubagentModel != model ||
			preset.OpusModel != model || preset.SonnetModel != model || preset.HaikuModel != model {
			t.Errorf("%s preset does not consistently use %s: %#v", name, model, preset)
		}
		if preset.AuthProvider != "xai" {
			t.Errorf("%s preset auth provider = %q, want xai", name, preset.AuthProvider)
		}
	}
}

func TestMiniMaxPresetUsesAnthropicCompatibleEndpoint(t *testing.T) {
	minimax := presets["minimax"]
	if minimax.Model != "MiniMax-M2.7" || minimax.SubagentModel != "MiniMax-M2.7" {
		t.Fatalf("MiniMax preset uses unexpected models: %#v", minimax)
	}
	if minimax.BaseURL != "https://api.minimax.io/anthropic" {
		t.Fatalf("MiniMax preset endpoint = %q", minimax.BaseURL)
	}
	if minimax.AuthTokenEnv != "MINIMAX_API_KEY" {
		t.Fatalf("MiniMax preset token environment = %q", minimax.AuthTokenEnv)
	}
}

func TestDeepSeekPresetUsesProAndFlashModels(t *testing.T) {
	deepseek := presets["deepseek"]
	if deepseek.Model != "deepseek-v4-pro" || deepseek.SubagentModel != "deepseek-v4-pro" || deepseek.OpusModel != "deepseek-v4-pro" {
		t.Fatalf("DeepSeek preset does not use Pro for main, subagent, and opus: %#v", deepseek)
	}
	if deepseek.SonnetModel != "deepseek-v4-flash" || deepseek.HaikuModel != "deepseek-v4-flash" {
		t.Fatalf("DeepSeek preset does not use Flash for lower tiers: %#v", deepseek)
	}
	if deepseek.BaseURL != "https://api.deepseek.com/anthropic" || deepseek.AuthTokenEnv != "DEEPSEEK_API_KEY" {
		t.Fatalf("DeepSeek preset has unexpected connection settings: %#v", deepseek)
	}
}

func TestCursorPresetsUseOfficialSDKBridge(t *testing.T) {
	tests := map[string]string{
		"cursor-composer":      "composer-2.5",
		"cursor-composer-fast": "composer-2.5-fast",
		"cursor-auto":          "auto",
		"cursor-grok":          "grok-4.5",
	}
	for name, model := range tests {
		preset := presets[name]
		if preset.Model != model || preset.SubagentModel != model {
			t.Errorf("%s preset does not use %s for main and subagent: %#v", name, model, preset)
		}
		if preset.Bridge != "cursor" || preset.AuthTokenEnv != "CURSOR_API_KEY" {
			t.Errorf("%s preset does not use the Cursor API bridge: %#v", name, preset)
		}
		if got := providerForDialect(preset); got != "cursor" {
			t.Errorf("%s provider = %q, want cursor", name, got)
		}
	}
	composer := presets["cursor-composer"]
	if composer.OpusModel != "composer-2.5-fast" ||
		composer.SonnetModel != "composer-2.5-standard" ||
		composer.HaikuModel != "composer-2.5-standard" {
		t.Fatalf("Cursor Composer preset does not expose Fast and Standard variants: %#v", composer)
	}
	cursorGrok := presets["cursor-grok"]
	if got := presetForDialect(cursorGrok); got != "cursor-grok" {
		t.Fatalf("Cursor Grok preset detection = %q, want cursor-grok", got)
	}
	if got := providerForDialect(cursorGrok); got != "cursor" {
		t.Fatalf("Cursor Grok provider = %q, want cursor", got)
	}
}

func TestCopilotPresetsUseOfficialSDKBridge(t *testing.T) {
	tests := map[string]string{
		"copilot-auto":   "auto",
		"copilot-mai":    "mai-code-1-flash",
		"copilot-codex":  "gpt-5.3-codex",
		"copilot-claude": "claude-sonnet-4.6",
		"copilot-gemini": "gemini-3.1-pro-preview",
	}
	for name, model := range tests {
		preset := presets[name]
		if preset.Model != model || preset.SubagentModel != model {
			t.Errorf("%s preset does not use %s for main and subagent: %#v", name, model, preset)
		}
		if preset.Bridge != "copilot" || preset.AuthTokenEnv != "" {
			t.Errorf("%s preset does not use Copilot CLI authentication: %#v", name, preset)
		}
		if got := providerForDialect(preset); got != "copilot" {
			t.Errorf("%s provider = %q, want copilot", name, got)
		}
		if got := presetForDialect(preset); got != name {
			t.Errorf("%s preset detection = %q", name, got)
		}
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

func TestCreateNextStepsUsePreferredShimNameByDefault(t *testing.T) {
	got := createNextSteps("gemini", "cc-gemini", presets["gemini"])
	want := []string{
		"Authenticate: cc-dialect auth gemini antigravity",
		"Install the command: cc-dialect shim install gemini",
		"Run: cc-gemini",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("createNextSteps() = %q, want %q", got, want)
	}
}

func TestCreateNextStepsUseCollisionSafeShimName(t *testing.T) {
	got := createNextSteps("gemini", "cc-gemini-dialect", presets["gemini"])
	want := []string{
		"Authenticate: cc-dialect auth gemini antigravity",
		"Install the command: cc-dialect shim install gemini --name cc-gemini-dialect",
		"Run: cc-gemini-dialect",
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

func TestCreateNextStepsInstallCursorBridgeBeforeTokenAndShim(t *testing.T) {
	got := createNextSteps("cc-cursor", "cc-cursor", presets["cursor-composer"])
	want := []string{
		"Install the Cursor bridge: cc-dialect cursor install",
		"Set the provider token: export CURSOR_API_KEY=your_token",
		"Install the command: cc-dialect shim install cc-cursor",
		"Run: cc-cursor",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("createNextSteps() = %q, want %q", got, want)
	}
}

func TestCreateNextStepsInstallAndAuthenticateCopilotBridge(t *testing.T) {
	got := createNextSteps("cc-copilot-mai", "cc-copilot-mai", presets["copilot-mai"])
	want := []string{
		"Install the Copilot bridge: cc-dialect copilot install",
		"Authenticate with GitHub Copilot: cc-dialect copilot login",
		"Install the command: cc-dialect shim install cc-copilot-mai",
		"Run: cc-copilot-mai",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("createNextSteps() = %q, want %q", got, want)
	}
}

func TestPresetForDialectSupportsStoredAndLegacyConfigurations(t *testing.T) {
	stored := presets["codex-sol"]
	stored.Preset = "codex-sol"
	if got := presetForDialect(stored); got != "codex-sol" {
		t.Fatalf("stored preset = %q, want codex-sol", got)
	}
	legacyKimi := presets["kimi"]
	if got := presetForDialect(legacyKimi); got != "kimi" {
		t.Fatalf("legacy Kimi preset = %q, want kimi", got)
	}
	legacyGLM := Dialect{
		Model: "glm-5", BaseURL: "https://open.bigmodel.cn/api/anthropic",
		AuthTokenEnv: "ZAI_API_KEY",
	}
	if got := presetForDialect(legacyGLM); got != "glm" {
		t.Fatalf("legacy GLM preset = %q, want glm", got)
	}
	legacyComposer := Dialect{Model: "grok-composer-2.5-fast", AuthProvider: "xai"}
	if got := presetForDialect(legacyComposer); got != "composer" {
		t.Fatalf("legacy Composer preset = %q, want composer", got)
	}
	legacyMiniMax := Dialect{
		Model: "MiniMax-M2.7", BaseURL: "https://api.minimax.io/anthropic",
		AuthTokenEnv: "MINIMAX_API_KEY",
	}
	if got := presetForDialect(legacyMiniMax); got != "minimax" {
		t.Fatalf("legacy MiniMax preset = %q, want minimax", got)
	}
	if got := presetForDialect(Dialect{Model: "my-private-model"}); got != "" {
		t.Fatalf("custom preset = %q, want empty", got)
	}
}

func TestDetectDialectsMatchesProviderFamilyAndRunningState(t *testing.T) {
	codexSol := presets["codex-sol"]
	codexSol.Preset = "codex-sol"
	codexSol.Port = 43170
	codex := presets["codex"]
	codex.Preset = "codex"
	codex.Port = 43171
	kimi := presets["kimi"]
	kimi.Preset = "kimi"
	kimi.Port = 43172
	grok := presets["grok"]
	grok.Preset = "grok"
	grok.Port = 43173
	composer := presets["composer"]
	composer.Preset = "composer"
	composer.Port = 43174
	cfg := &Config{Dialects: map[string]Dialect{
		"cc-codex-sol": codexSol,
		"cc-codex":     codex,
		"cc-kimi":      kimi,
		"cc-grok":      grok,
		"cc-composer":  composer,
	}}
	healthy := func(dialect Dialect) bool {
		return dialect.Port == 43170 || dialect.Port == 43172
	}

	allCodex := detectDialects(cfg, "codex", false, healthy)
	if len(allCodex) != 2 || allCodex[0].Name != "cc-codex" || allCodex[1].Name != "cc-codex-sol" {
		t.Fatalf("Codex provider detections = %#v", allCodex)
	}
	runningCodex := detectDialects(cfg, "codex", true, healthy)
	if len(runningCodex) != 1 || runningCodex[0].Name != "cc-codex-sol" || !runningCodex[0].Running {
		t.Fatalf("running Codex detections = %#v", runningCodex)
	}
	exactPreset := detectDialects(cfg, "codex-sol", false, healthy)
	if len(exactPreset) != 1 || exactPreset[0].Provider != "codex" || exactPreset[0].Preset != "codex-sol" {
		t.Fatalf("exact preset detections = %#v", exactPreset)
	}
	allXAI := detectDialects(cfg, "xai", false, healthy)
	if len(allXAI) != 2 || allXAI[0].Name != "cc-composer" || allXAI[1].Name != "cc-grok" {
		t.Fatalf("xAI provider detections = %#v", allXAI)
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
	if got := suggestedShimName("gemini"); got != "cc-gemini" {
		t.Fatalf("suggestedShimName(gemini) = %q, want cc-gemini", got)
	}
	if got := suggestedShimName("cc-gemini"); got != "cc-gemini-dialect" {
		t.Fatalf("suggestedShimName(cc-gemini) = %q, want cc-gemini-dialect", got)
	}
}

func TestPreferredShimName(t *testing.T) {
	if got := preferredShimName("gemini"); got != "cc-gemini" {
		t.Fatalf("preferredShimName(gemini) = %q, want cc-gemini", got)
	}
	if got := preferredShimName("cc-gemini"); got != "cc-gemini" {
		t.Fatalf("preferredShimName(cc-gemini) = %q, want cc-gemini", got)
	}
}

func TestParseMajorMinor(t *testing.T) {
	major, minor, ok := parseMajorMinor("22.15.0")
	if !ok || major != 22 || minor != 15 {
		t.Fatalf("parseMajorMinor() = %d, %d, %t", major, minor, ok)
	}
	if _, _, ok = parseMajorMinor("not-a-version"); ok {
		t.Fatal("parseMajorMinor accepted an invalid version")
	}
}

func TestCursorBridgeEnvironmentDoesNotInheritUnrelatedSecrets(t *testing.T) {
	t.Setenv("PATH", "/example/bin")
	t.Setenv("HOME", "/Users/example")
	t.Setenv("CURSOR_API_KEY", "cursor-secret")
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	env := strings.Join(cursorBridgeEnvironment("private-bridge-key"), "\n")
	for _, expected := range []string{
		"PATH=/example/bin",
		"HOME=/Users/example",
		"CURSOR_API_KEY=cursor-secret",
		"CURSOR_DIALECT_BRIDGE_KEY=private-bridge-key",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("Cursor bridge environment does not contain %q: %s", expected, env)
		}
	}
	if strings.Contains(env, "OPENAI_API_KEY") || strings.Contains(env, "must-not-leak") {
		t.Fatalf("Cursor bridge inherited an unrelated secret: %s", env)
	}
}

func TestEmbeddedCursorBridgeDelegatesToolApprovalToClaudeCode(t *testing.T) {
	text := string(cursorBridgeSource)
	for _, expected := range []string{
		`settingSources: []`,
		`sandboxOptions: { enabled: false }`,
		`autoReview: false`,
		`const forwardedTools = aliasTools(toolDefinitions)`,
		`customTools[tool.alias]`,
		`findForwardedTool(forwardedTools`,
		`Use only the custom tools whose names begin with cc_tool_`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("embedded Cursor bridge does not contain %q", expected)
		}
	}
	if strings.Contains(text, `sandboxOptions: { enabled: true }`) {
		t.Fatal("embedded Cursor bridge enables the headless sandbox that blocks custom tool calls")
	}
	if strings.Contains(text, `customTools[tool.name]`) {
		t.Fatal("embedded Cursor bridge exposes names that can collide with Cursor built-in tool schemas")
	}
}

func TestCopilotBridgeEnvironmentDoesNotInheritUnrelatedSecrets(t *testing.T) {
	t.Setenv("PATH", "/example/bin")
	t.Setenv("HOME", "/Users/example")
	t.Setenv("COPILOT_GITHUB_TOKEN", "copilot-token")
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	env := strings.Join(copilotBridgeEnvironment("private-bridge-key", "/private/copilot-home"), "\n")
	for _, expected := range []string{
		"PATH=/example/bin",
		"HOME=/Users/example",
		"COPILOT_GITHUB_TOKEN=copilot-token",
		"COPILOT_DIALECT_BRIDGE_KEY=private-bridge-key",
		"COPILOT_DIALECT_HOME=/private/copilot-home",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("Copilot bridge environment does not contain %q: %s", expected, env)
		}
	}
	if strings.Contains(env, "OPENAI_API_KEY") || strings.Contains(env, "must-not-leak") {
		t.Fatalf("Copilot bridge inherited an unrelated secret: %s", env)
	}
}

func TestEmbeddedCopilotBridgeUsesOfficialSDKAndExternalTools(t *testing.T) {
	text := string(copilotBridgeSource)
	for _, expected := range []string{
		`from "@github/copilot-sdk"`,
		`mode: "empty"`,
		`availableTools: ["custom:*"]`,
		`event.type === "external_tool.requested"`,
		`capabilities?.supports?.reasoningEffort`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("embedded Copilot bridge does not contain %q", expected)
		}
	}
}

func TestUsageShowsRemoveCommand(t *testing.T) {
	if !strings.Contains(usage, "\n  cc-dialect remove <name>\n") {
		t.Fatal("usage does not show the remove command on its own line")
	}
}

func TestUsageShowsDetectCommand(t *testing.T) {
	if !strings.Contains(usage, "\n  cc-dialect detect [preset-or-provider] [--running] [--json] [--quiet]\n") {
		t.Fatal("usage does not show the detect command")
	}
}
