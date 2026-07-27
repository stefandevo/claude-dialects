package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeInstanceSettings(t *testing.T, home, name, content string) string {
	t.Helper()
	settingsPath := filepath.Join(home, "instances", name, "claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return settingsPath
}

func attributionEntry(t *testing.T, settings map[string]any) map[string]any {
	t.Helper()
	entry, ok := settings["attribution"].(map[string]any)
	if !ok {
		t.Fatalf("settings missing attribution object: %#v", settings)
	}
	return entry
}

// A dialect runs with CLAUDE_CONFIG_DIR pointed at its own directory, so the
// user-level opt-out in ~/.claude never reaches it: the empty attribution text
// has to be written into the dialect's own settings.json.
func TestSeedAttributionWritesEmptyAttribution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := seedAttribution("cc-test"); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, "instances", "cc-test", "claude", "settings.json")
	entry := attributionEntry(t, readSettings(t, settingsPath))
	if entry["commit"] != "" || entry["pr"] != "" {
		t.Fatalf("attribution should hide commit and PR attribution: %#v", entry)
	}
	// The deprecated key is redundant with the current one and must not be written.
	if _, exists := readSettings(t, settingsPath)["includeCoAuthoredBy"]; exists {
		t.Fatal("seeding wrote the deprecated includeCoAuthoredBy key")
	}
	info, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSeedAttributionPreservesExistingSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	// Matches the shape cc-dialect already writes into a live instance.
	settingsPath := writeInstanceSettings(t, home, "cc-test",
		`{"theme":"dark","statusLine":{"type":"command","command":"/custom/statusline.sh"},"skipDangerousModePermissionPrompt":true}`)
	if err := seedAttribution("cc-test"); err != nil {
		t.Fatal(err)
	}
	settings := readSettings(t, settingsPath)
	if settings["theme"] != "dark" {
		t.Fatalf("seeding dropped existing settings: %#v", settings)
	}
	if command := statuslineCommand(t, settings); command != "/custom/statusline.sh" {
		t.Fatalf("seeding replaced the statusLine: %q", command)
	}
	if settings["skipDangerousModePermissionPrompt"] != true {
		t.Fatalf("seeding dropped skipDangerousModePermissionPrompt: %#v", settings)
	}
	attributionEntry(t, settings)
}

// An existing attribution value is the user's answer, whatever its shape — a
// partial object must not be completed with empty strings that hide the
// attribution they left enabled.
func TestSeedAttributionLeavesExistingAttributionUntouched(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		settings string
	}{
		{"custom text", `{"attribution":{"commit":"Co-Authored-By: Claude <noreply@anthropic.com>"}}`},
		{"partial object", `{"attribution":{"sessionUrl":true}}`},
		{"empty object", `{"attribution":{}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("DIALECT_HOME", home)
			settingsPath := writeInstanceSettings(t, home, "cc-test", testCase.settings)
			if err := seedAttribution("cc-test"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != testCase.settings {
				t.Fatalf("seeding rewrote an existing attribution: %s", data)
			}
		})
	}
}

// The deprecated key is still honoured by Claude Code, so its presence — at
// either value — is an explicit preference the seed must not override.
func TestSeedAttributionLeavesIncludeCoAuthoredByUntouched(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("DIALECT_HOME", home)
			original := `{"includeCoAuthoredBy":` + value + `}`
			settingsPath := writeInstanceSettings(t, home, "cc-test", original)
			if err := seedAttribution("cc-test"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != original {
				t.Fatalf("seeding overrode the deprecated attribution key: %s", data)
			}
		})
	}
}

func TestSeedAttributionIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := seedAttribution("cc-test"); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, "instances", "cc-test", "claude", "settings.json")
	first, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = seedAttribution("cc-test"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("second seed rewrote settings:\n%s\n%s", first, second)
	}
}

// A settings file Claude Code cannot have written must never be clobbered: the
// error is reported and the dialect is left untouched.
func TestSeedAttributionSkipsMalformedSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	settingsPath := writeInstanceSettings(t, home, "cc-test", "{not json")
	if err := seedAttribution("cc-test"); err == nil {
		t.Fatal("seeding with malformed settings.json should fail")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil || string(data) != "{not json" {
		t.Fatalf("malformed settings were modified: %q, %v", data, err)
	}
}

// settings.json containing literal `null` unmarshals into a nil map without
// error; seeding must reject it rather than panic on the assignment.
func TestSeedAttributionSkipsNullSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	settingsPath := writeInstanceSettings(t, home, "cc-test", "null\n")
	if err := seedAttribution("cc-test"); err == nil {
		t.Fatal("seeding with null settings.json should fail")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil || string(data) != "null\n" {
		t.Fatalf("null settings were modified: %q, %v", data, err)
	}
}

func TestSeedAttributionRejectsInvalidName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := seedAttribution("../evil"); err == nil {
		t.Fatal("seedAttribution should reject an invalid dialect name")
	}
	if _, err := os.Stat(filepath.Join(home, "evil")); !os.IsNotExist(err) {
		t.Fatalf("invalid name escaped the instances directory: %v", err)
	}
}

// validName blocks path traversal but not symlink escape; confining the write
// to the dialect's own os.Root is what refuses a symlinked instance.
func TestSeedAttributionRejectsSymlinkedInstance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "escape")
	if err := os.MkdirAll(filepath.Join(target, "claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, "instances", "cc-test")); err != nil {
		t.Fatal(err)
	}
	if err := seedAttribution("cc-test"); err == nil {
		t.Fatal("seedAttribution should refuse a symlinked instance directory")
	}
	if _, err := os.Stat(filepath.Join(target, "claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("seedAttribution wrote through the symlink into the escape target: %v", err)
	}
}

func TestAttributionDiagnostic(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		settings string
		write    bool
		want     bool
	}{
		{name: "no settings file", want: true},
		{name: "unrelated keys", settings: `{"theme":"dark"}`, write: true, want: true},
		{name: "attribution present", settings: `{"attribution":{"commit":""}}`, write: true},
		{name: "deprecated key present", settings: `{"includeCoAuthoredBy":true}`, write: true},
		// Seeding would refuse a malformed file, so there is no fix to point at.
		{name: "malformed settings", settings: "{not json", write: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("DIALECT_HOME", home)
			if testCase.write {
				writeInstanceSettings(t, home, "cc-test", testCase.settings)
			}
			line := attributionDiagnostic("cc-test")
			if testCase.want {
				if !strings.Contains(line, "cc-test") || !strings.Contains(line, "doctor --fix") {
					t.Fatalf("expected a diagnostic naming the dialect and its fix, got %q", line)
				}
			} else if line != "" {
				t.Fatalf("expected no diagnostic, got %q", line)
			}
		})
	}
}

// doctor --fix is what reconciles dialects created before this seed existed and
// not launched since: upgrade shells out to it.
func TestDoctorBackfillsAttribution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	base := availablePortRange(t, 2)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := newAppService().CreateDialect(DialectInput{Name: "cc-test", Preset: "codex"}, ""); err != nil {
		t.Fatal(err)
	}
	// Roll the dialect back to its pre-seed shape.
	settingsPath := writeInstanceSettings(t, home, "cc-test", `{"theme":"dark"}`)

	report := captureStdout(t, func() error { return doctor(nil, "test") })
	if !strings.Contains(report, "cc-test does not disable Claude commit attribution") {
		t.Fatalf("doctor did not report the missing attribution opt-out:\n%s", report)
	}
	if _, exists := readSettings(t, settingsPath)["attribution"]; exists {
		t.Fatal("doctor without --fix modified the dialect settings")
	}

	fixed := captureStdout(t, func() error { return doctor([]string{"--fix"}, "test") })
	if !strings.Contains(fixed, "disabling Claude commit attribution for cc-test") {
		t.Fatalf("doctor --fix did not announce the attribution fix:\n%s", fixed)
	}
	settings := readSettings(t, settingsPath)
	entry := attributionEntry(t, settings)
	if entry["commit"] != "" || entry["pr"] != "" {
		t.Fatalf("doctor --fix did not seed an empty attribution: %#v", entry)
	}
	if settings["theme"] != "dark" {
		t.Fatalf("doctor --fix dropped existing settings: %#v", settings)
	}
	if report = captureStdout(t, func() error { return doctor(nil, "test") }); strings.Contains(report, "commit attribution") {
		t.Fatalf("doctor still reports attribution after the fix:\n%s", report)
	}
}

func TestCreateDialectSeedsAttribution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	base := availablePortRange(t, 2)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := newAppService().CreateDialect(DialectInput{Name: "cc-test", Preset: "codex"}, ""); err != nil {
		t.Fatal(err)
	}
	settings := readSettings(t, filepath.Join(home, "instances", "cc-test", "claude", "settings.json"))
	entry := attributionEntry(t, settings)
	if entry["commit"] != "" || entry["pr"] != "" {
		t.Fatalf("create did not seed an empty attribution: %#v", entry)
	}
	// The statusline seed runs first at every call site; attribution must merge
	// into its freshly written file rather than replace it.
	if _, exists := settings["statusLine"]; !exists {
		t.Fatalf("attribution seed dropped the statusline wiring: %#v", settings)
	}
}
