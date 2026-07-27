package app

import (
	"strings"
	"testing"
)

// A preset name alone does not prove the dialect still uses that preset's route.
// Bridge and AuthProvider have no create flag, so a dialect whose stored preset
// name survives a hand edit of either would have that route silently replaced by
// the `--preset` command this would otherwise recommend. Migration already
// refuses such a dialect, so the remedy has to agree with it.
func TestRoundTripsThroughMutationsChecksThePresetHiddenRoute(t *testing.T) {
	intact := presets["cursor-composer"]
	intact.Preset = "cursor-composer"
	if !roundTripsThroughMutations(intact) {
		t.Fatal("an unmodified preset-backed dialect was refused")
	}

	for _, testCase := range []struct {
		name    string
		mutate  func(Dialect) Dialect
		explain string
	}{
		{"bridge dropped", func(d Dialect) Dialect { d.Bridge = ""; return d }, "a create --preset would restore the bridge the dialect no longer uses"},
		{"bridge changed", func(d Dialect) Dialect { d.Bridge = "copilot"; return d }, "a create --preset would replace the bridge"},
		{"auth provider added", func(d Dialect) Dialect { d.AuthProvider = "codex"; return d }, "a create --preset would drop the OAuth route"},
	} {
		modified := testCase.mutate(intact)
		if roundTripsThroughMutations(modified) {
			t.Errorf("%s: declared round-trippable, but %s", testCase.name, testCase.explain)
		}
		if command := contextWindowFixCommand("cc-x", modified); command != "" {
			t.Errorf("%s: recommended %q", testCase.name, command)
		}
		if remedy := contextWindowRemedy("cc-x", modified); !strings.Contains(remedy, "config.json") {
			t.Errorf("%s: remedy %q is not the lossless one", testCase.name, remedy)
		}
	}
}

// The remedy is copied into a shell, so every interpolated value has to survive
// that trip verbatim. Model IDs and upstream URLs are arbitrary strings.
func TestContextWindowFixCommandQuotesShellUnsafeValues(t *testing.T) {
	dialect := Dialect{
		Model: "vendor-model", SubagentModel: "vendor-model", OpusModel: "vendor-model",
		SonnetModel: "vendor-model", HaikuModel: "weird; rm -rf ~",
		BaseURL:      "https://provider.example.com/anthropic?tenant=a&region=b",
		AuthTokenEnv: "MY_TOKEN",
	}

	command := contextWindowFixCommand("cc-custom", dialect)

	for _, unsafe := range []string{"weird; rm -rf ~", "?tenant=a&region=b"} {
		if strings.Contains(command, " "+unsafe) {
			t.Errorf("command %q interpolates %q unquoted", command, unsafe)
		}
	}
	if !strings.Contains(command, `'weird; rm -rf ~'`) {
		t.Errorf("command %q does not quote the unsafe model ID", command)
	}
	if !strings.Contains(command, `'https://provider.example.com/anthropic?tenant=a&region=b'`) {
		t.Errorf("command %q does not quote the URL query string", command)
	}
	// A value that needs no quoting stays readable.
	if !strings.Contains(command, "--model vendor-model") {
		t.Errorf("command %q needlessly quoted a safe value", command)
	}
}

// The capacity placeholder is part of the same copied line, so it must not be
// shell syntax either — `<tokens>` is a redirection.
func TestContextWindowFixCommandPlaceholderIsNotShellSyntax(t *testing.T) {
	presetBacked := presets["glm"]
	presetBacked.Preset = "glm"

	command := contextWindowFixCommand("cc-glm", presetBacked)

	if strings.ContainsAny(command, "<>") {
		t.Fatalf("command %q contains shell redirection characters", command)
	}
	if !strings.Contains(command, "--context-window") {
		t.Fatalf("command %q lost the capacity flag", command)
	}
}

func TestShellArgQuotesOnlyWhenNeeded(t *testing.T) {
	for _, testCase := range []struct{ value, want string }{
		{"vendor-model", "vendor-model"},
		{"https://a.example.com/b", "https://a.example.com/b"},
		{"MY_TOKEN", "MY_TOKEN"},
		{"a&b", "'a&b'"},
		{"a b", "'a b'"},
		{"", "''"},
		{"it's", `'it'\''s'`},
	} {
		if got := shellArg(testCase.value); got != testCase.want {
			t.Errorf("shellArg(%q) = %q, want %q", testCase.value, got, testCase.want)
		}
	}
}

// ExtraEnv is applied last at launch, so an override that is not a usable window
// leaves the process uncalibrated however healthy the stored field looks.
func TestContextWindowDiagnosticsReportsAnUnusableOverride(t *testing.T) {
	dialect := presets["codex-sol"]
	dialect.Preset = "codex-sol"
	dialect.ExtraEnv = map[string]string{autoCompactWindowEnv: "not-a-number"}

	lines := contextWindowDiagnostics("cc-codex", dialect)

	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(lines), lines)
	}
	for _, expected := range []string{"cc-codex", autoCompactWindowEnv, "not-a-number"} {
		if !strings.Contains(lines[0], expected) {
			t.Errorf("diagnostic %q does not mention %q", lines[0], expected)
		}
	}
}

// An override that is a usable window is the calibration, so there is nothing to
// report even when it disagrees with the stored field.
func TestContextWindowDiagnosticsAcceptsAUsableOverride(t *testing.T) {
	dialect := Dialect{Model: "vendor-model", ExtraEnv: map[string]string{autoCompactWindowEnv: "150000"}}

	if lines := contextWindowDiagnostics("cc-custom", dialect); len(lines) != 0 {
		t.Fatalf("a dialect calibrated by its override reported %v", lines)
	}
}

// A stored window that only looks valid must not mask an override that does not.
func TestContextWindowDiagnosticsPrefersTheEffectiveWindow(t *testing.T) {
	dialect := presets["codex-sol"]
	dialect.Preset = "codex-sol"
	dialect.ExtraEnv = map[string]string{autoCompactWindowEnv: "0"}

	if lines := contextWindowDiagnostics("cc-codex", dialect); len(lines) == 0 {
		t.Fatal("a zero override was reported as calibrated")
	}
}
