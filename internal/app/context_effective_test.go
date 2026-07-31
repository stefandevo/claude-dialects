package app

import (
	"context"
	"strings"
	"testing"

	proxyusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// ExtraEnv is applied after the calibrated window at launch, so when it replaces
// the auto-compact variable that value — not the stored field — is the window
// Claude Code actually runs with.
func TestEffectiveContextWindowFollowsAnExtraEnvOverride(t *testing.T) {
	dialect := presets["codex-sol"]
	if got := effectiveContextWindow(dialect); got != 372000 {
		t.Fatalf("effective window = %d, want the stored 372000", got)
	}

	dialect.ExtraEnv = map[string]string{autoCompactWindowEnv: "150000"}
	if got := effectiveContextWindow(dialect); got != 150000 {
		t.Fatalf("effective window = %d, want the overriding 150000", got)
	}
}

// An override that is not a usable window leaves the real denominator unknown,
// so nothing is measured against a number that is certainly wrong. Either
// variable can be the one carrying it.
func TestEffectiveContextWindowRejectsAnUnusableOverride(t *testing.T) {
	for _, key := range contextWindowEnvs {
		for _, override := range []string{"not-a-number", "0", "-5", ""} {
			dialect := presets["codex-sol"]
			dialect.ExtraEnv = map[string]string{key: override}
			if got := effectiveContextWindow(dialect); got != 0 {
				t.Errorf("%s override %q gave effective window %d, want 0 (unknown)", key, override, got)
			}
		}
	}
}

// Claude Code compacts against the smaller of the window it resolves for the
// model and the auto-compact window, so a dialect that declares the two
// separately runs against whichever is tighter — not against whichever the
// launcher happened to write last.
func TestEffectiveContextWindowTakesTheSmallestDeclaredCapacity(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		extraEnv map[string]string
		want     int
	}{
		{"max context tokens alone", map[string]string{maxContextTokensEnv: "150000"}, 150000},
		{"auto compact window alone", map[string]string{autoCompactWindowEnv: "150000"}, 150000},
		{
			"both below the stored window",
			map[string]string{autoCompactWindowEnv: "180000", maxContextTokensEnv: "150000"},
			150000,
		},
		{
			"the tighter one is the auto-compact window",
			map[string]string{autoCompactWindowEnv: "150000", maxContextTokensEnv: "500000"},
			150000,
		},
		{"both above the stored window", map[string]string{
			autoCompactWindowEnv: "500000", maxContextTokensEnv: "600000",
		}, 372000},
	} {
		dialect := presets["codex-sol"]
		dialect.ExtraEnv = testCase.extraEnv
		if got := effectiveContextWindow(dialect); got != testCase.want {
			t.Errorf("%s: effective window = %d, want %d", testCase.name, got, testCase.want)
		}
	}
}

// An uncalibrated dialect has no stored window to be capped by, so a single
// override is the whole calibration rather than a ceiling on one.
func TestEffectiveContextWindowAdoptsAnOverrideWhenNothingIsStored(t *testing.T) {
	for _, key := range contextWindowEnvs {
		dialect := Dialect{Model: "vendor-model", ExtraEnv: map[string]string{key: "150000"}}
		if got := effectiveContextWindow(dialect); got != 150000 {
			t.Errorf("%s override gave effective window %d, want 150000", key, got)
		}
	}
}

// Unrelated ExtraEnv entries say nothing about capacity.
func TestEffectiveContextWindowIgnoresUnrelatedExtraEnv(t *testing.T) {
	dialect := presets["codex-sol"]
	dialect.ExtraEnv = map[string]string{"SOME_OTHER_VARIABLE": "1"}
	if got := effectiveContextWindow(dialect); got != 372000 {
		t.Fatalf("effective window = %d, want the stored 372000", got)
	}
}

// Percentages and threshold warnings have to be measured against the window the
// launched process actually received.
func TestMonitorMeasuresAgainstTheOverriddenWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	dialect := presets["codex-sol"]
	dialect.ExtraEnv = map[string]string{autoCompactWindowEnv: "200000"}
	monitor := testContextMonitor(t, "cc-codex", dialect)
	monitor.warn = func(string) {}

	monitor.HandleUsage(context.Background(), proxyusage.Record{
		Model:  "gpt-5.6-sol",
		Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(100000, 0, 0, 10, 0, 0)},
	})

	state, err := readContextUsage("cc-codex")
	if err != nil {
		t.Fatal(err)
	}
	if state.Window != 200000 {
		t.Fatalf("recorded window = %d, want the effective 200000", state.Window)
	}
	if state.UsedPercent < 49.9 || state.UsedPercent > 50.1 {
		t.Fatalf("used percent = %.2f, want 50 against the effective window", state.UsedPercent)
	}
}

// No supported mutation carries ExtraEnv across, so no command or dashboard save
// can record a capacity for such a dialect without dropping it.
func TestContextWindowFixCommandRefusesWhenExtraEnvWouldBeLost(t *testing.T) {
	presetBacked := presets["codex-sol"]
	presetBacked.Preset = "codex-sol"
	presetBacked.ExtraEnv = map[string]string{"ANTHROPIC_BASE_URL": "https://elsewhere.example.com"}
	if got := contextWindowFixCommand("cc-codex", presetBacked); got != "" {
		t.Errorf("preset-backed dialect with ExtraEnv produced command %q", got)
	}

	custom := Dialect{
		Model: "vendor-model", SubagentModel: "vendor-model", OpusModel: "vendor-model",
		SonnetModel: "vendor-model", HaikuModel: "vendor-model",
		ExtraEnv: map[string]string{"SOMETHING": "1"},
	}
	if got := contextWindowFixCommand("cc-custom", custom); got != "" {
		t.Errorf("custom dialect with ExtraEnv produced command %q", got)
	}
}

// The dashboard cannot round-trip ExtraEnv, a preset-less bridge, or a
// preset-less OAuth route either, so pointing at it would be recommending a
// different kind of damage. Those dialects are told to edit the file directly.
func TestContextWindowRemedyDoesNotRecommendALossyDashboardSave(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		dialect Dialect
	}{
		{"extra env", Dialect{Model: "m", ExtraEnv: map[string]string{"X": "1"}}},
		{"preset-less bridge", Dialect{Model: "auto", Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY"}},
		{"preset-less oauth", Dialect{Model: "gpt-5.6-sol", AuthProvider: "codex"}},
	} {
		remedy := contextWindowRemedy("cc-x", testCase.dialect)
		if strings.Contains(remedy, "cc-dialect web") {
			t.Errorf("%s: remedy %q recommends a dashboard save that would drop hidden route state", testCase.name, remedy)
		}
		if !strings.Contains(remedy, "config.json") {
			t.Errorf("%s: remedy %q does not point at the only lossless option", testCase.name, remedy)
		}
	}
}

// A dialect with nothing hidden still gets the copyable command.
func TestContextWindowRemedyStillPrefersACommand(t *testing.T) {
	presetBacked := presets["glm"]
	presetBacked.Preset = "glm"
	if remedy := contextWindowRemedy("cc-glm", presetBacked); !strings.HasPrefix(remedy, "run: cc-dialect create") {
		t.Fatalf("remedy = %q, want a create command", remedy)
	}
}
