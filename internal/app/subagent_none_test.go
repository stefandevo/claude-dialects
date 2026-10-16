package app

import (
	"os"
	"strings"
	"testing"
)

// none is a behavior flag. copilot-auto's subagent model is already "auto",
// which the other tiers also select, so the reviewed 128000 window still
// describes the route.
func TestSubagentModelNoneKeepsTheCopilotAutoWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	result, err := service.CreateDialect(DialectInput{
		Name: "cc-copilot", Preset: "copilot-auto", SubagentModel: subagentModelNone,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.SubagentModel != subagentModelNone {
		t.Fatalf("subagent model = %q, want %q", result.Dialect.SubagentModel, subagentModelNone)
	}
	if result.Dialect.Model != "auto" {
		t.Fatalf("main model = %q, want the preset model", result.Dialect.Model)
	}
	if result.Dialect.ContextWindow != 128000 {
		t.Fatalf("context window = %d, want the copilot-auto capacity 128000", result.Dialect.ContextWindow)
	}
}

// A real subagent model that the rest of the route does not already select is
// a different set of models, so the preset window must not come along.
func TestSubagentModelOverrideClearsThePresetWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	result, err := service.CreateDialect(DialectInput{
		Name: "cc-copilot", Preset: "copilot-auto", SubagentModel: "gpt-5.3-codex",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.SubagentModel != "gpt-5.3-codex" {
		t.Fatalf("subagent model = %q, want the override", result.Dialect.SubagentModel)
	}
	if result.Dialect.ContextWindow != 0 {
		t.Fatalf("context window = %d, want 0 after the subagent model left the preset route", result.Dialect.ContextWindow)
	}
}

// The empty-subagent fill copies the main model. none has to survive that,
// including when the main model itself was overridden.
func TestPrepareDialectKeepsSubagentModelNone(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	service := newAppService()

	result, err := service.CreateDialect(DialectInput{
		Name: "cc-copilot", Preset: "copilot-auto",
		Model: "claude-opus-5.5", SubagentModel: subagentModelNone,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Dialect.Model != "claude-opus-5.5" {
		t.Fatalf("main model = %q, want the override", result.Dialect.Model)
	}
	if result.Dialect.SubagentModel != subagentModelNone {
		t.Fatalf("subagent model = %q, want %q rather than the main model", result.Dialect.SubagentModel, subagentModelNone)
	}
}

func TestSubagentModelNoneIsNotAnAuthProvider(t *testing.T) {
	dialect := Dialect{
		Model: "gpt-6-sol", SubagentModel: subagentModelNone,
		OpusModel: "gpt-6-sol", SonnetModel: "gpt-6-sol", HaikuModel: "gpt-6-sol",
	}
	providers := expectedAuthProviders(dialect)
	if len(providers) != 1 || providers[0] != "codex" {
		t.Fatalf("auth providers = %v, want only codex", providers)
	}
	for _, model := range dialectModels(dialect) {
		if model == subagentModelNone {
			t.Fatalf("dialectModels = %v includes %q", dialectModels(dialect), subagentModelNone)
		}
	}
}

func TestWriteProxyConfigOmitsSubagentModelNone(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	t.Setenv("UPSTREAM_TOKEN", "secret")
	dialect := Dialect{
		Model: "vendor-model", SubagentModel: subagentModelNone,
		OpusModel: "vendor-model", SonnetModel: "vendor-model", HaikuModel: "vendor-model",
		Port: 43180, APIKey: "local-secret",
		BaseURL: "https://provider.example.com/api/anthropic", AuthTokenEnv: "UPSTREAM_TOKEN",
	}

	path, err := writeProxyConfig("cc-custom", dialect)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `name: "vendor-model"`) {
		t.Fatalf("proxy config does not register the real model:\n%s", text)
	}
	if strings.Contains(text, `name: "none"`) || strings.Contains(text, `alias: "none"`) {
		t.Fatalf("proxy config registers the none sentinel:\n%s", text)
	}
}

func TestPresetDriftCommandRestatesSubagentModelNone(t *testing.T) {
	dialect := presets["copilot-auto"]
	dialect.SubagentModel = subagentModelNone
	got := presetDriftCommand("cc-copilot", dialect, presets["copilot-auto"], "copilot-auto")
	want := "cc-dialect create cc-copilot --preset copilot-auto --subagent-model none"
	if got != want {
		t.Fatalf("drift command = %q, want %q", got, want)
	}

	replaced := presets["copilot-auto"]
	replaced.SubagentModel = "gpt-5.3-codex"
	got = presetDriftCommand("cc-copilot", replaced, presets["copilot-auto"], "copilot-auto")
	if strings.Contains(got, "--subagent-model") {
		t.Fatalf("drift command restated a real subagent model: %q", got)
	}
}

func TestContextWindowFixCommandRestatesSubagentModelNone(t *testing.T) {
	dialect := presets["copilot-auto"]
	dialect.Preset = "copilot-auto"
	dialect.SubagentModel = subagentModelNone

	got := contextWindowFixCommand("cc-copilot", dialect)
	for _, expected := range []string{"--preset copilot-auto", "--subagent-model none", "--context-window TOKENS"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("fix command %q does not contain %q", got, expected)
		}
	}
}
