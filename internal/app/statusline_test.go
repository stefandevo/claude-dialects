package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{}
	if err = json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return settings
}

func statuslineCommand(t *testing.T, settings map[string]any) string {
	t.Helper()
	entry, ok := settings["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("settings missing statusLine object: %#v", settings)
	}
	if entry["type"] != "command" {
		t.Fatalf("statusLine type = %v, want command", entry["type"])
	}
	command, _ := entry["command"].(string)
	return command
}

func TestCreateDialectSeedsStatusline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	base := availablePortRange(t, 2)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := newAppService().CreateDialect(DialectInput{Name: "cc-test", Preset: "codex", Effort: true}, ""); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(home, "instances", "cc-test", "statusline.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("create did not seed statusline script: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("statusline script mode = %v, want 0755", info.Mode().Perm())
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "cc-test") {
		t.Fatalf("statusline script does not embed the dialect name:\n%s", script)
	}
	settingsPath := filepath.Join(home, "instances", "cc-test", "claude", "settings.json")
	if command, want := statuslineCommand(t, readSettings(t, settingsPath)), "'"+scriptPath+"'"; command != want {
		t.Fatalf("statusLine command = %q, want %q", command, want)
	}
}

func TestSeedStatuslinePreservesExistingSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	settingsPath := filepath.Join(home, "instances", "cc-test", "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"theme":"dark","enabledPlugins":{"example":true}}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedStatusline("cc-test", presets["codex"]); err != nil {
		t.Fatal(err)
	}
	settings := readSettings(t, settingsPath)
	if settings["theme"] != "dark" {
		t.Fatalf("seeding dropped existing settings: %#v", settings)
	}
	if _, ok := settings["enabledPlugins"].(map[string]any); !ok {
		t.Fatalf("seeding dropped nested settings: %#v", settings)
	}
	scriptPath := filepath.Join(home, "instances", "cc-test", "statusline.sh")
	if command, want := statuslineCommand(t, settings), "'"+scriptPath+"'"; command != want {
		t.Fatalf("statusLine command = %q, want %q", command, want)
	}
}

// The default config root (~/Library/Application Support/claude-dialects)
// contains a space, and statusLine.command is executed by a shell — the stored
// command must be quoted or every default install gets a broken statusline.
func TestSeedStatuslineCommandSurvivesSpacesInHome(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	home := filepath.Join(t.TempDir(), "Application Support", "claude-dialects")
	t.Setenv("DIALECT_HOME", home)
	if err := seedStatusline("cc-test", presets["codex"]); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, "instances", "cc-test", "claude", "settings.json")
	command := exec.Command("sh", "-c", statuslineCommand(t, readSettings(t, settingsPath)))
	command.Stdin = strings.NewReader(`{"model":{"display_name":"GPT-5.6 Sol"}}`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("statusLine command failed under a path with spaces: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "cc-test") {
		t.Fatalf("statusLine command output %q missing dialect name", output)
	}
}

func TestSeedStatuslineRejectsInvalidName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := seedStatusline("../evil", presets["codex"]); err == nil {
		t.Fatal("seedStatusline should reject an invalid dialect name")
	}
	if _, err := os.Stat(filepath.Join(home, "evil")); !os.IsNotExist(err) {
		t.Fatalf("invalid name escaped the instances directory: %v", err)
	}
}

func TestSeedStatuslineLeavesCustomStatuslineUntouched(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	settingsPath := filepath.Join(home, "instances", "cc-test", "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"statusLine":{"type":"command","command":"/custom/statusline.sh"}}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedStatusline("cc-test", presets["codex"]); err != nil {
		t.Fatal(err)
	}
	if command := statuslineCommand(t, readSettings(t, settingsPath)); command != "/custom/statusline.sh" {
		t.Fatalf("seeding replaced a user statusLine: %q", command)
	}
}

func TestSeedStatuslineDoesNotReAddRemovedKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := seedStatusline("cc-test", presets["codex"]); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, "instances", "cc-test", "claude", "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedStatusline("cc-test", presets["codex"]); err != nil {
		t.Fatal(err)
	}
	settings := readSettings(t, settingsPath)
	if _, exists := settings["statusLine"]; exists {
		t.Fatalf("backfill re-added a removed statusLine key: %#v", settings)
	}
}

func TestSeedStatuslineRegeneratesOutdatedScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := seedStatusline("cc-test", presets["codex"]); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(home, "instances", "cc-test", "statusline.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n# stale template\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := seedStatusline("cc-test", presets["codex"]); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "jq") {
		t.Fatalf("outdated statusline script was not regenerated:\n%s", script)
	}
}

func TestSeedStatuslineSkipsMalformedSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	settingsPath := filepath.Join(home, "instances", "cc-test", "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedStatusline("cc-test", presets["codex"]); err == nil {
		t.Fatal("seeding with malformed settings.json should fail")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil || string(data) != "{not json" {
		t.Fatalf("malformed settings were modified: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(home, "instances", "cc-test", "statusline.sh")); !os.IsNotExist(err) {
		t.Fatalf("script written despite malformed settings: %v", err)
	}
}

func TestStatuslineScriptRendersStatusJSON(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := seedStatusline("cc-test", presets["codex"]); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", filepath.Join(home, "instances", "cc-test", "statusline.sh"))
	command.Stdin = strings.NewReader(`{"model":{"display_name":"GPT-5.6 Sol"},"effort":{"level":"auto"},"context_window":{"used_percentage":42.4}}`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("statusline script failed: %v\n%s", err, output)
	}
	line := string(output)
	for _, want := range []string{"cc-test", "GPT-5.6 Sol", "effort:auto", "ctx 42%"} {
		if !strings.Contains(line, want) {
			t.Fatalf("statusline output %q missing %q", line, want)
		}
	}
}

func TestStatuslineScriptDegradesWithoutJq(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := seedStatusline("cc-test", presets["codex"]); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", filepath.Join(home, "instances", "cc-test", "statusline.sh"))
	command.Env = append(os.Environ(), "PATH="+t.TempDir())
	command.Stdin = strings.NewReader(`{}`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("statusline script should exit 0 without jq: %v\n%s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("statusline script should stay silent without jq, got %q", output)
	}
}
