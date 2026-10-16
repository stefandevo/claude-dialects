package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeConfigDirInheritPassesAnExportedDirectoryThrough(t *testing.T) {
	dialect := presets["copilot-auto"]
	dialect.Port = 43170
	dialect.APIKey = "local-secret"
	dialect.ClaudeConfigDir = claudeConfigDirInherit

	env := claudeEnvironment([]string{"CLAUDE_CONFIG_DIR=/shared/claude"}, "/isolated/claude", dialect)
	if got := lookupEnv(env, "CLAUDE_CONFIG_DIR"); got != "/shared/claude" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the exported directory", got)
	}

	env = claudeEnvironment(nil, "/isolated/claude", dialect)
	if got := lookupEnv(env, "CLAUDE_CONFIG_DIR"); got != "/isolated/claude" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the isolated directory when nothing is exported", got)
	}

	env = claudeEnvironment([]string{"CLAUDE_CONFIG_DIR="}, "/isolated/claude", dialect)
	if got := lookupEnv(env, "CLAUDE_CONFIG_DIR"); got != "/isolated/claude" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the isolated directory when the export is empty", got)
	}
}

func TestClaudeConfigDirIsolatedReplacesAnExportedDirectory(t *testing.T) {
	dialect := presets["copilot-auto"]
	dialect.Port = 43170
	dialect.APIKey = "local-secret"

	env := claudeEnvironment([]string{"CLAUDE_CONFIG_DIR=/shared/claude"}, "/isolated/claude", dialect)
	if got := lookupEnv(env, "CLAUDE_CONFIG_DIR"); got != "/isolated/claude" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the isolated directory", got)
	}
}

func TestClaudeConfigDirExtraEnvRemainsTheLastWord(t *testing.T) {
	dialect := presets["copilot-auto"]
	dialect.Port = 43170
	dialect.APIKey = "local-secret"
	dialect.ClaudeConfigDir = claudeConfigDirInherit
	dialect.ExtraEnv = map[string]string{"CLAUDE_CONFIG_DIR": "/from-extra"}

	env := claudeEnvironment([]string{"CLAUDE_CONFIG_DIR=/shared/claude"}, "/isolated/claude", dialect)
	if got := lookupEnv(env, "CLAUDE_CONFIG_DIR"); got != "/from-extra" {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want the extraEnv value", got)
	}
}

func TestCreateDialectInheritKeepsThePresetWindowAndStamp(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	result, err := service.CreateDialect(DialectInput{
		Name: "cc-copilot", Preset: "copilot-auto", ClaudeConfigDir: claudeConfigDirInherit,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ClaudeConfigDir != claudeConfigDirInherit {
		t.Fatalf("claude config dir = %q, want inherit", result.Dialect.ClaudeConfigDir)
	}
	if result.Dialect.ContextWindow != 128000 {
		t.Fatalf("context window = %d, want the copilot-auto capacity 128000", result.Dialect.ContextWindow)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	stored := cfg.Dialects["cc-copilot"]
	if got, want := stored.PresetFingerprint, presetFingerprint(presets["copilot-auto"]); got != want {
		t.Fatalf("preset fingerprint = %q, want %q", got, want)
	}
	if lines := presetDriftDiagnostics("cc-copilot", stored); len(lines) != 0 {
		t.Fatalf("inherit was reported as preset drift: %v", lines)
	}
}

func TestEmptyClaudeConfigDirKeepsTheStoredMode(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.UpsertDialect(DialectInput{
		Name: "cc-copilot", Preset: "copilot-auto", ClaudeConfigDir: claudeConfigDirInherit,
	}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpsertDialect(DialectInput{
		Name: "cc-copilot", Preset: "copilot-auto",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ClaudeConfigDir != claudeConfigDirInherit {
		t.Fatalf("claude config dir = %q, want the stored inherit mode", result.Dialect.ClaudeConfigDir)
	}

	result, err = service.UpsertDialect(DialectInput{
		Name: "cc-copilot", Preset: "copilot-auto", ClaudeConfigDir: claudeConfigDirIsolated,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ClaudeConfigDir != "" {
		t.Fatalf("claude config dir = %q, want isolated", result.Dialect.ClaudeConfigDir)
	}
}

func TestCreateDialectRejectsAnUnknownClaudeConfigDir(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	_, err := newAppService().CreateDialect(DialectInput{
		Name: "cc-copilot", Preset: "copilot-auto", ClaudeConfigDir: "shared",
	}, "")
	if err == nil || !strings.Contains(err.Error(), "--claude-config-dir") {
		t.Fatalf("error = %v, want an invalid --claude-config-dir", err)
	}
}

func TestGeneratedSettingsStayInTheIsolatedDirectory(t *testing.T) {
	home := t.TempDir()
	shared := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", shared)

	if _, err := newAppService().CreateDialect(DialectInput{
		Name: "cc-copilot", Preset: "copilot-auto", ClaudeConfigDir: claudeConfigDirInherit,
	}, ""); err != nil {
		t.Fatal(err)
	}
	isolatedSettings := filepath.Join(home, "instances", "cc-copilot", "claude", "settings.json")
	data, err := os.ReadFile(isolatedSettings)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "statusLine") || !strings.Contains(text, "attribution") {
		t.Fatalf("isolated settings missing generated keys:\n%s", text)
	}
	if _, err = os.Stat(filepath.Join(home, "instances", "cc-copilot", "statusline.sh")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(shared)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("inherit seeding wrote into the exported directory: %v", entries)
	}
}

func TestPresetDriftCommandRestatesClaudeConfigDirInherit(t *testing.T) {
	dialect := presets["kimi"]
	dialect.HaikuModel = "kimi-k2.5"
	dialect.ClaudeConfigDir = claudeConfigDirInherit

	got := presetDriftCommand("cc-kimi", dialect, presets["kimi"], "kimi")
	if !strings.Contains(got, "--claude-config-dir inherit") {
		t.Fatalf("drift command = %q, want it to restate inherit", got)
	}
	dialect.ClaudeConfigDir = ""
	got = presetDriftCommand("cc-kimi", dialect, presets["kimi"], "kimi")
	if strings.Contains(got, "--claude-config-dir") {
		t.Fatalf("drift command restated the default mode: %q", got)
	}
}

func TestContextWindowFixCommandRestatesClaudeConfigDirInherit(t *testing.T) {
	dialect := presets["kimi"]
	dialect.Preset = "kimi"
	dialect.ClaudeConfigDir = claudeConfigDirInherit

	got := contextWindowFixCommand("cc-kimi", dialect)
	if !strings.Contains(got, "--claude-config-dir inherit") {
		t.Fatalf("fix command = %q, want it to restate inherit", got)
	}
}
