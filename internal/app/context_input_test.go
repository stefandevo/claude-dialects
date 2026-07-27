package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateDialectAdoptsThePresetContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	result, err := service.CreateDialect(DialectInput{Name: "cc-codex", Preset: "codex-sol"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 372000 {
		t.Fatalf("view context window = %d, want 372000", result.Dialect.ContextWindow)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-codex"].ContextWindow; got != 372000 {
		t.Fatalf("stored context window = %d, want 372000", got)
	}
}

func TestCreateDialectAcceptsAnExplicitContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	result, err := service.CreateDialect(DialectInput{
		Name: "cc-custom", Model: "vendor-model", ContextWindow: 262144,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 262144 {
		t.Fatalf("context window = %d, want 262144", result.Dialect.ContextWindow)
	}
}

// An explicit value is the operator's measured capacity for their own route, so
// it must win over the preset default rather than being silently reset.
func TestExplicitContextWindowOverridesThePreset(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	result, err := service.CreateDialect(DialectInput{
		Name: "cc-codex", Preset: "codex-sol", ContextWindow: 128000,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 128000 {
		t.Fatalf("context window = %d, want the explicit 128000", result.Dialect.ContextWindow)
	}
}

func TestCreateDialectRejectsAnOutOfRangeContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	for _, window := range []int{-1, maxContextWindow + 1} {
		_, err := service.CreateDialect(DialectInput{
			Name: "cc-custom", Model: "vendor-model", ContextWindow: window,
		}, "")
		if err == nil {
			t.Fatalf("context window %d was accepted, want a validation error", window)
		}
		if !strings.Contains(err.Error(), "context-window") {
			t.Fatalf("error %q does not name the offending option", err)
		}
	}
}

// Updating credentials, ports, or models must not silently erase a capacity the
// operator configured earlier.
func TestUpdateDialectPreservesAStoredContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{
		Name: "cc-custom", Model: "vendor-model", ContextWindow: 262144,
	}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateDialect(DialectInput{
		Name: "cc-custom", Model: "vendor-model", Concurrency: 5,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 262144 {
		t.Fatalf("context window = %d, want the preserved 262144", result.Dialect.ContextWindow)
	}
}

// Switching an existing dialect to a preset must adopt that preset's capacity,
// otherwise a re-applied preset would keep the previous model's denominator.
func TestUpdateDialectToAPresetAdoptsItsContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	if _, err := service.CreateDialect(DialectInput{
		Name: "cc-route", Model: "vendor-model", ContextWindow: 32000,
	}, ""); err != nil {
		t.Fatal(err)
	}
	result, err := service.UpdateDialect(DialectInput{Name: "cc-route", Preset: "kimi"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.ContextWindow != 262144 {
		t.Fatalf("context window = %d, want the kimi capacity 262144", result.Dialect.ContextWindow)
	}
}

// The dashboard must be able to read and write the window without ever seeing
// the dialect's private API key or upstream token.
func TestDialectViewSerializesContextWindowWithoutSecrets(t *testing.T) {
	dialect := presets["codex-sol"]
	dialect.Port = 43170
	dialect.APIKey = "local-secret"
	dialect.AuthTokenEnv = "PROVIDER_TOKEN"

	raw, err := json.Marshal(safeDialectView("cc-codex", dialect))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"contextWindow":372000`) {
		t.Fatalf("serialized view does not expose the context window:\n%s", body)
	}
	if strings.Contains(body, "local-secret") {
		t.Fatalf("serialized view leaked the dialect API key:\n%s", body)
	}
}

// "Unknown" must stay distinguishable from a real capacity, so an unconfigured
// dialect omits the field instead of publishing a meaningless zero.
func TestDialectViewOmitsAnUnknownContextWindow(t *testing.T) {
	raw, err := json.Marshal(safeDialectView("cc-custom", Dialect{Model: "vendor-model", Port: 43170}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "contextWindow") {
		t.Fatalf("unknown capacity must be omitted, got:\n%s", raw)
	}
}

func TestPresetTemplatesPublishTheirContextWindow(t *testing.T) {
	for _, name := range presetNames() {
		view := safeDialectView(name, presets[name])
		if view.ContextWindow <= 0 {
			t.Errorf("preset template %q publishes context window %d", name, view.ContextWindow)
		}
	}
}
