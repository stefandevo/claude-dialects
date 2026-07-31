package app

import (
	"strings"
	"testing"
)

// Capacity describes a specific set of models. Re-pointing an existing dialect
// at a different model must not carry the old model's window forward: a window
// larger than the new route supports reproduces the very exhaustion this feature
// prevents, whereas an unset one only returns to uncalibrated behavior and says
// so loudly.
func TestUpdatingTheModelClearsAnInheritedContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{
		Name: "cc-custom", Model: "roomy-model", ContextWindow: 1000000,
	}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateDialect(DialectInput{Name: "cc-custom", Model: "cramped-model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 0 {
		t.Fatalf("context window = %d, want 0 after the route moved to another model", result.Dialect.ContextWindow)
	}
}

// Changing something that is not the route must still not uncalibrate a working
// dialect — that was the reason for preserving capacity in the first place.
func TestUpdatingNonRouteSettingsKeepsTheContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{
		Name: "cc-custom", Model: "vendor-model", ContextWindow: 262144,
	}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateDialect(DialectInput{
		Name: "cc-custom", Model: "vendor-model", Concurrency: 5, ToolSearch: true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 262144 {
		t.Fatalf("context window = %d, want the preserved 262144", result.Dialect.ContextWindow)
	}
}

// A tier override moves the smallest selectable model, so the preset's reviewed
// minimum no longer describes the route.
func TestUpdatingATierClearsAnInheritedContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{Name: "cc-codex", Preset: "codex-sol"}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateDialect(DialectInput{
		Name: "cc-codex", Model: "gpt-5.6-sol", HaikuModel: "some-small-model",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 0 {
		t.Fatalf("context window = %d, want 0 after a tier moved off the preset mapping", result.Dialect.ContextWindow)
	}
}

// Selecting a preset but overriding its model leaves a route the preset's
// reviewed capacity does not describe.
func TestPresetWithAModelOverrideDoesNotKeepThePresetCapacity(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	result, err := service.CreateDialect(DialectInput{
		Name: "cc-codex", Preset: "codex", Model: "gpt-5.3-codex-spark",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 0 {
		t.Fatalf("context window = %d, want 0 for a model the preset does not cover", result.Dialect.ContextWindow)
	}
}

// Overrides may move a dialect off the preset it names and onto another preset's
// exact route. That route's reviewed window is still valid, while the stored
// label remains the user's original create input.
func TestCreateDialectAdoptsWindowFromTheExactResolvedRoute(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	result, err := service.CreateDialect(DialectInput{
		Name: "cc-cursor-mix", Preset: "cursor-composer",
		OpusModel: "composer-2.5", SonnetModel: "grok-4.5", HaikuModel: "kimi-k3",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Dialect.ContextWindow; got != 200000 {
		t.Fatalf("context window = %d, want the exact cursor-mix route's 200000", got)
	}
	if got := result.Dialect.Preset; got != "cursor-composer" {
		t.Fatalf("preset = %q, want the requested cursor-composer label preserved", got)
	}
}

// An explicit value is the operator's own measurement and stays authoritative
// however far the route moved.
func TestAnExplicitContextWindowSurvivesAModelChange(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{
		Name: "cc-custom", Model: "roomy-model", ContextWindow: 1000000,
	}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateDialect(DialectInput{
		Name: "cc-custom", Model: "cramped-model", ContextWindow: 128000,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 128000 {
		t.Fatalf("context window = %d, want the explicit 128000", result.Dialect.ContextWindow)
	}
}

// An operator who measured a lower capacity than the preset advertises keeps it
// through an update that leaves the route alone. Naming the same preset again is
// not a request to raise the window: silently increasing it delays compaction
// past the limit they established, which is the failure this metadata prevents.
func TestUnchangedPresetUpdateKeepsAnExplicitContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{
		Name: "cc-codex", Preset: "codex-sol", ContextWindow: 128000,
	}, ""); err != nil {
		t.Fatal(err)
	}
	for _, update := range []DialectInput{
		{Name: "cc-codex", Preset: "codex-sol", Concurrency: 5},
		{Name: "cc-codex", Preset: "codex-sol"},
		{Name: "cc-codex", Preset: "codex-sol", EffortLevel: "high"},
	} {
		result, err := service.UpdateDialect(update, "")
		if err != nil {
			t.Fatal(err)
		}
		if result.Dialect.ContextWindow != 128000 {
			t.Fatalf("context window = %d, want the operator's 128000 preserved across %+v",
				result.Dialect.ContextWindow, update)
		}
	}
}

// The preset still supplies the capacity when the dialect has none of its own,
// which is the ordinary case for a dialect created straight from a preset.
func TestUnchangedPresetUpdateStillSuppliesAMissingContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{Name: "cc-codex", Preset: "codex-sol"}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateDialect(DialectInput{Name: "cc-codex", Preset: "codex-sol", Concurrency: 5}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 372000 {
		t.Fatalf("context window = %d, want the preset capacity 372000", result.Dialect.ContextWindow)
	}
}

// Re-applying a preset restores its mapping, so it also restores its capacity.
func TestReapplyingAPresetRestoresItsContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{
		Name: "cc-codex", Model: "some-model", ContextWindow: 50000,
	}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateDialect(DialectInput{Name: "cc-codex", Preset: "codex-sol"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 372000 {
		t.Fatalf("context window = %d, want the codex-sol capacity 372000", result.Dialect.ContextWindow)
	}
}

// A custom upstream is part of the route: the same model IDs served by another
// endpoint can have another capacity.
func TestChangingTheUpstreamClearsAnInheritedContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	t.Setenv("FIRST_TOKEN", "first")
	t.Setenv("SECOND_TOKEN", "second")
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{
		Name: "cc-custom", Model: "vendor-model", ContextWindow: 262144,
		BaseURL: "https://first.example.com/anthropic", AuthTokenEnv: "FIRST_TOKEN",
	}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateDialect(DialectInput{
		Name: "cc-custom", Model: "vendor-model",
		BaseURL: "https://second.example.com/anthropic", AuthTokenEnv: "SECOND_TOKEN",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 0 {
		t.Fatalf("context window = %d, want 0 after the upstream changed", result.Dialect.ContextWindow)
	}
}

// ExtraEnv is applied last at launch and can replace the base URL, the model
// variables, or the auto-compact window itself, so a dialect carrying it is not
// described by the route its stored fields show. Migration must not hand it a
// preset's capacity.
func TestBackfillSkipsDialectsCarryingExtraEnv(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "preset": "codex-sol",
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "extraEnv": { "ANTHROPIC_MODEL": "some-other-model" },
          "port": 43170,
          "apiKey": "codex-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-codex"].ContextWindow; got != 0 {
		t.Fatalf("context window = %d, want 0 for a dialect whose launch environment is overridden", got)
	}
}

// `create` rebuilds a dialect from the flags it is given rather than patching
// it, so a remediation command must restate the whole route or it will quietly
// delete the parts it omits.
func TestContextWindowFixCommandPreservesTheRoute(t *testing.T) {
	presetBacked := presets["glm"]
	presetBacked.Preset = "glm"
	if got := contextWindowFixCommand("cc-glm", presetBacked); got != "cc-dialect create cc-glm --preset glm --context-window TOKENS" {
		t.Fatalf("preset-backed command = %q", got)
	}

	custom := Dialect{
		Model: "vendor-model", SubagentModel: "vendor-model",
		OpusModel: "vendor-model", SonnetModel: "vendor-small", HaikuModel: "vendor-small",
		BaseURL: "https://provider.example.com/anthropic", AuthTokenEnv: "MY_TOKEN",
		Effort: true, EffortLevel: "auto", Concurrency: 3,
	}
	got := contextWindowFixCommand("cc-custom", custom)
	for _, expected := range []string{
		"--model vendor-model",
		"--sonnet-model vendor-small",
		"--haiku-model vendor-small",
		"--base-url https://provider.example.com/anthropic",
		"--token-env MY_TOKEN",
		"--context-window TOKENS",
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("custom command %q is missing %q", got, expected)
		}
	}
	if strings.Contains(got, "--subagent-model") {
		t.Errorf("command %q restates a tier that already defaults to the primary model", got)
	}
}

// Non-default behavior settings are part of what `create` would otherwise reset.
func TestContextWindowFixCommandPreservesNonDefaultBehavior(t *testing.T) {
	custom := Dialect{
		Model: "vendor-model", SubagentModel: "vendor-model", OpusModel: "vendor-model",
		SonnetModel: "vendor-model", HaikuModel: "vendor-model",
		Effort: false, EffortLevel: "high", Concurrency: 7, ToolSearch: true,
	}

	got := contextWindowFixCommand("cc-custom", custom)

	for _, expected := range []string{"--effort=false", "--effort-level high", "--concurrency 7", "--tool-search=true"} {
		if !strings.Contains(got, expected) {
			t.Errorf("command %q is missing %q", got, expected)
		}
	}
}

// A bridge or OAuth route has no flag to restate it, so no faithful command
// exists and none may be printed.
func TestContextWindowFixCommandRefusesAnInexpressibleRoute(t *testing.T) {
	for _, dialect := range []Dialect{
		{Model: "auto", Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY"},
		{Model: "gpt-5.6-sol", AuthProvider: "codex"},
	} {
		if got := contextWindowFixCommand("cc-x", dialect); got != "" {
			t.Errorf("inexpressible route produced command %q", got)
		}
	}
}

// The diagnostics must never recommend a command that would break the dialect.
func TestContextWindowDiagnosticsOnlyPrintsASafeCommand(t *testing.T) {
	upstream := Dialect{
		Model: "vendor-model", SubagentModel: "vendor-model", OpusModel: "vendor-model",
		SonnetModel: "vendor-model", HaikuModel: "vendor-model",
		BaseURL: "https://provider.example.com/anthropic", AuthTokenEnv: "MY_TOKEN",
	}
	lines := contextWindowDiagnostics("cc-custom", upstream)
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(lines))
	}
	for _, expected := range []string{"--base-url https://provider.example.com/anthropic", "--token-env MY_TOKEN"} {
		if !strings.Contains(lines[0], expected) {
			t.Errorf("diagnostic %q drops %q from the recommended command", lines[0], expected)
		}
	}

	bridge := Dialect{Model: "auto", Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY"}
	lines = contextWindowDiagnostics("cc-cursor", bridge)
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(lines))
	}
	if strings.Contains(lines[0], "run: cc-dialect create") {
		t.Errorf("diagnostic %q recommends a command that would erase the bridge route", lines[0])
	}
}

// Claude Code takes one process-level window, so a launch-time model override it
// cannot be calibrated for has to be called out rather than passed silently.
func TestModelOverrideWarningCoversUnknownModels(t *testing.T) {
	dialect := presets["codex-sol"]

	if warning := modelOverrideWarning("cc-codex", dialect, []string{"--model", "gpt-5.3-codex-spark"}); warning == "" {
		t.Fatal("an unrecognized model override produced no warning")
	} else {
		for _, expected := range []string{"cc-codex", "gpt-5.3-codex-spark", "372000"} {
			if !strings.Contains(warning, expected) {
				t.Errorf("warning %q does not mention %q", warning, expected)
			}
		}
	}
	if warning := modelOverrideWarning("cc-codex", dialect, []string{"--model=gpt-5.3-codex-spark"}); warning == "" {
		t.Error("the --model=value form produced no warning")
	}
	if warning := modelOverrideWarning("cc-codex", dialect, []string{"--model", "gpt-5.6-terra"}); warning != "" {
		t.Errorf("a configured tier model warned: %q", warning)
	}
	if warning := modelOverrideWarning("cc-codex", dialect, nil); warning != "" {
		t.Errorf("no override warned: %q", warning)
	}
	uncalibrated := Dialect{Model: "vendor-model"}
	if warning := modelOverrideWarning("cc-custom", uncalibrated, []string{"--model", "other"}); warning != "" {
		t.Errorf("a dialect with no window warned about calibration: %q", warning)
	}
}
