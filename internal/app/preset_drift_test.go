package app

import (
	"strings"
	"testing"
)

// staleGLMDialect reconstructs the observed case from issue #104: a dialect
// created when the glm preset still declared GLM-5.2 with a GLM-4.5-Air haiku
// tier and a 131,072-token window, stamped as that revision. The current
// preset in this build plays the role of the upgraded one.
func staleGLMDialect() Dialect {
	stale := presets["glm"]
	stale.Model = "glm-5.2"
	stale.SubagentModel = "glm-5.2"
	stale.OpusModel = "glm-5.2"
	stale.HaikuModel = "glm-4.5-air"
	stale.ContextWindow = 131072
	stale.Preset = "glm"
	stale.PresetFingerprint = presetFingerprint(stale)
	return stale
}

// The fingerprint is the only thing separating "the preset moved" from "the
// dialect was edited", so it has to notice every field a preset revision can
// change — the route sharesContextRoute compares, and the window, because a
// revision may move either without the other.
func TestPresetFingerprintCoversRouteAndWindow(t *testing.T) {
	base := presets["glm"]
	if presetFingerprint(base) != presetFingerprint(presets["glm"]) {
		t.Fatal("fingerprint is not stable for an identical route")
	}
	for _, testCase := range []struct {
		name   string
		mutate func(Dialect) Dialect
	}{
		{"model", func(d Dialect) Dialect { d.Model = "glm-5.4"; return d }},
		{"subagent", func(d Dialect) Dialect { d.SubagentModel = "glm-5.4"; return d }},
		{"opus", func(d Dialect) Dialect { d.OpusModel = "glm-5.4"; return d }},
		{"sonnet", func(d Dialect) Dialect { d.SonnetModel = "glm-5.4"; return d }},
		{"haiku", func(d Dialect) Dialect { d.HaikuModel = "glm-5.4"; return d }},
		{"auth provider", func(d Dialect) Dialect { d.AuthProvider = "codex"; return d }},
		{"bridge", func(d Dialect) Dialect { d.Bridge = "cursor"; return d }},
		{"base URL", func(d Dialect) Dialect { d.BaseURL = "https://other.example.com"; return d }},
		{"token env", func(d Dialect) Dialect { d.AuthTokenEnv = "OTHER_KEY"; return d }},
		{"window", func(d Dialect) Dialect { d.ContextWindow = 262144; return d }},
	} {
		if presetFingerprint(testCase.mutate(base)) == presetFingerprint(base) {
			t.Errorf("a %s change left the fingerprint unchanged", testCase.name)
		}
	}
}

func TestPresetDriftReportsAStaleStampedPreset(t *testing.T) {
	lines := presetDriftDiagnostics("cc-glm", staleGLMDialect())

	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(lines), lines)
	}
	for _, expected := range []string{
		"✗ cc-glm was created from an older glm preset",
		"model glm-5.2 → glm-5.3",
		"haiku glm-4.5-air → glm-5-turbo",
		"window 131072 → 200000",
		"(run: cc-dialect create cc-glm --preset glm)",
	} {
		if !strings.Contains(lines[0], expected) {
			t.Errorf("diagnostic %q does not contain %q", lines[0], expected)
		}
	}
	// The sonnet tier never differed, so it must not pad the report.
	if strings.Contains(lines[0], "sonnet") {
		t.Errorf("diagnostic %q names an unchanged field", lines[0])
	}
}

func TestPresetDriftStaysSilentForACurrentDialect(t *testing.T) {
	current := presets["glm"]
	current.Preset = "glm"
	current.PresetFingerprint = presetFingerprint(current)

	if lines := presetDriftDiagnostics("cc-glm", current); len(lines) != 0 {
		t.Fatalf("a dialect matching the current preset reported %v", lines)
	}
	// A missing stamp only ever widens the hedge; it must not invent drift.
	current.PresetFingerprint = ""
	if lines := presetDriftDiagnostics("cc-glm", current); len(lines) != 0 {
		t.Fatalf("an unstamped current dialect reported %v", lines)
	}
}

// A revision may move only the window. The route still matches, so the
// fingerprint — which covers the window too — is what makes that visible.
func TestPresetDriftReportsAWindowOnlyRevision(t *testing.T) {
	stale := presets["glm"]
	stale.ContextWindow = 131072
	stale.Preset = "glm"
	stale.PresetFingerprint = presetFingerprint(stale)

	lines := presetDriftDiagnostics("cc-glm", stale)
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "window 131072 → 200000") {
		t.Errorf("diagnostic %q lost the window change", lines[0])
	}
	if strings.Contains(lines[0], "model ") {
		t.Errorf("diagnostic %q names models that did not change", lines[0])
	}
}

// A stored window is deliberately never raised on the dialect's behalf, so a
// route-current dialect whose window was set by hand stays quiet: only a
// stamped dialect — one whose window came from the preset and was never
// touched — reports the window half of a revision.
func TestPresetDriftKeepsAHandSetWindowQuiet(t *testing.T) {
	custom := presets["glm"]
	custom.ContextWindow = 150000
	custom.Preset = "glm"
	custom.PresetFingerprint = presetFingerprint(presets["glm"])

	if lines := presetDriftDiagnostics("cc-glm", custom); len(lines) != 0 {
		t.Fatalf("a hand-set window was reported as drift: %v", lines)
	}
}

// cc-cursor-mix is field-for-field identical to today's cursor-mix preset but
// carries a cursor-composer label that predates cursor-mix existing. It is
// fully current; judging it against its label would report drift and offer a
// command that destroys the mix.
func TestPresetDriftDoesNotReportARouteExactMatchUnderAStaleLabel(t *testing.T) {
	relabeled := presets["cursor-mix"]
	relabeled.Preset = "cursor-composer"

	lines := presetDriftDiagnostics("cc-cursor-mix", relabeled)
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want the label note alone: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "○ ") {
		t.Errorf("note %q is not informational", lines[0])
	}
	for _, expected := range []string{"cursor-mix", "cursor-composer"} {
		if !strings.Contains(lines[0], expected) {
			t.Errorf("note %q does not mention %q", lines[0], expected)
		}
	}
	if strings.Contains(lines[0], "run:") || strings.Contains(lines[0], "--preset cursor-composer") {
		t.Errorf("note %q offered a route command", lines[0])
	}
}

// Dialects created before fingerprints existed cannot be told apart from
// hand-customized ones, so the report says both possibilities instead of
// asserting an upgrade the binary cannot prove.
func TestPresetDriftHedgesAnUnstampedDivergedRoute(t *testing.T) {
	diverged := presets["kimi"]
	diverged.HaikuModel = "kimi-k2.5"
	diverged.Preset = "kimi"

	lines := presetDriftDiagnostics("cc-kimi", diverged)
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(lines), lines)
	}
	for _, expected := range []string{
		"○ cc-kimi differs from the current kimi preset",
		"haiku kimi-k2.5 → kimi-k2.6",
		"older preset",
		"your own customization",
		"(run: cc-dialect create cc-kimi --preset kimi)",
	} {
		if !strings.Contains(lines[0], expected) {
			t.Errorf("diagnostic %q does not contain %q", lines[0], expected)
		}
	}
}

// ExtraEnv is applied last at launch, so a create command would drop it. The
// hedge still explains the divergence, but must not offer that command.
func TestPresetDriftRefusesACommandWhenExtraEnvWouldBeDropped(t *testing.T) {
	diverged := presets["kimi"]
	diverged.HaikuModel = "kimi-k2.5"
	diverged.Preset = "kimi"
	diverged.ExtraEnv = map[string]string{"CUSTOM_FLAG": "1"}

	lines := presetDriftDiagnostics("cc-kimi", diverged)
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(lines), lines)
	}
	if strings.Contains(lines[0], "run:") {
		t.Errorf("diagnostic %q offered a command that would drop ExtraEnv", lines[0])
	}
	if !strings.Contains(lines[0], "would drop") {
		t.Errorf("diagnostic %q does not say why no command is offered", lines[0])
	}
}

// A stamped dialect whose route no longer matches its stamp was edited after
// creation — the stamp's guarantee is gone, so the hedge applies rather than
// the clean upgrade report.
func TestPresetDriftHedgesAHandEditedStampedDialect(t *testing.T) {
	edited := staleGLMDialect()
	edited.PresetFingerprint = presetFingerprint(presets["glm"])
	edited.SonnetModel = "glm-5.2-flash"

	lines := presetDriftDiagnostics("cc-glm", edited)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "○ ") {
		t.Fatalf("an edited dialect was reported as a clean upgrade: %v", lines)
	}
	if !strings.Contains(lines[0], "sonnet glm-5.2-flash → glm-5-turbo") {
		t.Errorf("diagnostic %q does not name the differing field", lines[0])
	}
}

// A label this build does not recognize may name a preset a newer one owns,
// and an unlabeled dialect makes no claim at all — both stay as silent as
// they were before drift reporting existed.
func TestPresetDriftSilencesUnknownAndMissingLabels(t *testing.T) {
	newer := presets["codex-sol"]
	newer.Preset = "codex-sol-v2"
	if lines := presetDriftDiagnostics("cc-codex", newer); len(lines) != 0 {
		t.Fatalf("an unrecognized label was reported: %v", lines)
	}

	unlabeled := staleGLMDialect()
	unlabeled.Preset = ""
	if lines := presetDriftDiagnostics("cc-glm", unlabeled); len(lines) != 0 {
		t.Fatalf("an unlabeled dialect was reported: %v", lines)
	}
}

// create resets effort, tool search, concurrency, and effort level to the
// preset's values, so the offered command has to restate any the dialect
// changed — otherwise adopting the models would silently change behavior.
func TestPresetDriftCommandRestatesNonRouteSettings(t *testing.T) {
	stale := staleGLMDialect()
	stale.Effort = false
	stale.ToolSearch = true
	stale.Concurrency = 5
	stale.EffortLevel = "high"

	lines := presetDriftDiagnostics("cc-glm", stale)
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(lines), lines)
	}
	for _, expected := range []string{"--effort=false", "--tool-search=true", "--concurrency 5", "--effort-level high"} {
		if !strings.Contains(lines[0], expected) {
			t.Errorf("command in %q lost %q", lines[0], expected)
		}
	}
}

// doctor is where a preset revision meets the dialects it left behind —
// upgrade shells out to doctor --fix, so the report lands at exactly the
// moment a revised preset arrives. --fix must record the finding without
// acting on it: nothing distinguishes a window the user set from one the old
// preset supplied, which is the same contract the window repair already keeps.
func TestDoctorReportsPresetDriftWithoutFixingIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	stale := staleGLMDialect()
	stale.Port = 43170
	stale.APIKey = "private"
	cfg := &Config{
		Version: configVersion, BasePort: 43170,
		Dialects:        map[string]Dialect{"cc-glm": stale},
		NativeLaunchers: map[string]NativeLauncher{},
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	drift := "✗ cc-glm was created from an older glm preset"
	report := captureStdout(t, func() error { return doctor(nil, "test") })
	if !strings.Contains(report, drift) {
		t.Fatalf("doctor output missing the drift report:\n%s", report)
	}
	fixed := captureStdout(t, func() error { return doctor([]string{"--fix"}, "test") })
	if !strings.Contains(fixed, drift) {
		t.Fatalf("doctor --fix output missing the drift report:\n%s", fixed)
	}

	after, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	kept := after.Dialects["cc-glm"]
	if kept.Model != "glm-5.2" || kept.ContextWindow != 131072 || kept.PresetFingerprint != stale.PresetFingerprint {
		t.Fatalf("doctor --fix rewrote the dialect: %#v", kept)
	}
}

// The stamp is written when a dialect is created from a preset and cleared the
// moment any per-field flag overrides what the preset supplied — it must mean
// "this dialect is the preset as it was", nothing weaker, because that is the
// only thing separating a preset revision from a user customization.
func TestPrepareDialectStampsAndClearsThePresetFingerprint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	base := availablePortRange(t, 3)
	if err := saveConfig(&Config{Version: configVersion, BasePort: base, Dialects: map[string]Dialect{}, NativeLaunchers: map[string]NativeLauncher{}}); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	service.stopRuntime = func(*instanceFS, Dialect) error { return nil }

	created, err := service.CreateDialect(DialectInput{Name: "cc-test", Preset: "codex", Effort: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Dialects["cc-test"].PresetFingerprint, presetFingerprint(presets["codex"]); got != want {
		t.Fatalf("created fingerprint = %q, want the stamped preset %q", got, want)
	}

	revision := func() string {
		cfg, err := loadConfig()
		if err != nil {
			t.Fatal(err)
		}
		value, err := configRevision(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if _, err := service.UpdateDialect(DialectInput{
		Name: "cc-test", Preset: "codex", Model: "gpt-5.6-sol", Effort: true,
	}, created.Revision); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dialects["cc-test"].PresetFingerprint != "" {
		t.Fatal("a --model override kept the preset fingerprint")
	}

	if _, err := service.UpdateDialect(DialectInput{Name: "cc-test", Preset: "codex", Effort: true}, revision()); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Dialects["cc-test"].PresetFingerprint, presetFingerprint(presets["codex"]); got != want {
		t.Fatalf("restated fingerprint = %q, want the stamped preset %q", got, want)
	}

	if _, err := service.UpdateDialect(DialectInput{
		Name: "cc-test", Preset: "codex", ContextWindow: 300000, Effort: true,
	}, revision()); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dialects["cc-test"].PresetFingerprint != "" {
		t.Fatal("an explicit --context-window kept the preset fingerprint")
	}
}
