package app

import (
	"strings"
	"testing"
)

// A preset launch must be deterministic: whatever the parent shell exported for
// the auto-compact window, the dialect's own capacity is what Claude Code sees.
func TestClaudeEnvironmentOverridesInheritedAutoCompactWindow(t *testing.T) {
	dialect := presets["codex-sol"]
	dialect.Port = 43170
	dialect.APIKey = "local-secret"

	env := claudeEnvironment([]string{"PATH=/usr/bin", autoCompactWindowEnv + "=8000"}, "/tmp/claude", dialect)

	if count := countEnv(env, autoCompactWindowEnv); count != 1 {
		t.Fatalf("%s appears %d times, want exactly 1", autoCompactWindowEnv, count)
	}
	if value := lookupEnv(env, autoCompactWindowEnv); value != "372000" {
		t.Fatalf("%s = %q, want the codex-sol capacity %q", autoCompactWindowEnv, value, "372000")
	}
}

// Every preset must hand Claude Code a denominator, otherwise the route it
// cannot recognize is left uncalibrated exactly as issue #44 describes.
func TestClaudeEnvironmentSetsAutoCompactWindowForEveryPreset(t *testing.T) {
	for _, name := range presetNames() {
		dialect := presets[name]
		dialect.Port = 43170
		dialect.APIKey = "local-secret"
		env := claudeEnvironment(nil, "/tmp/claude", dialect)
		if value := lookupEnv(env, autoCompactWindowEnv); value == "" {
			t.Errorf("preset %q launches without %s", name, autoCompactWindowEnv)
		}
	}
}

// An unknown capacity cannot be improved on, so an ambient value the user
// exported themselves is preserved rather than dropped.
func TestClaudeEnvironmentKeepsAmbientWindowForUnknownCapacity(t *testing.T) {
	dialect := Dialect{Model: "custom-model", Port: 43170, APIKey: "local-secret"}

	env := claudeEnvironment([]string{autoCompactWindowEnv + "=250000"}, "/tmp/claude", dialect)

	if value := lookupEnv(env, autoCompactWindowEnv); value != "250000" {
		t.Fatalf("%s = %q, want the inherited %q", autoCompactWindowEnv, value, "250000")
	}
}

// ExtraEnv is explicit per-dialect configuration, so it stays the last word.
func TestClaudeEnvironmentLetsExtraEnvOverrideTheWindow(t *testing.T) {
	dialect := presets["codex-sol"]
	dialect.Port = 43170
	dialect.APIKey = "local-secret"
	dialect.ExtraEnv = map[string]string{autoCompactWindowEnv: "123456"}

	env := claudeEnvironment(nil, "/tmp/claude", dialect)

	if value := lookupEnv(env, autoCompactWindowEnv); value != "123456" {
		t.Fatalf("%s = %q, want the explicit extraEnv value %q", autoCompactWindowEnv, value, "123456")
	}
	if count := countEnv(env, autoCompactWindowEnv); count != 1 {
		t.Fatalf("%s appears %d times, want exactly 1", autoCompactWindowEnv, count)
	}
}

// The reproduced session sent 368,812 effective input tokens into a
// 372,000-token window with no compaction event. With the window declared,
// Claude Code has the denominator it needs to compact before exhaustion.
func TestCodexSolLaunchDeclaresTheWindowTheReproducedSessionLacked(t *testing.T) {
	const effectiveInput = 15020 + 353792

	dialect := presets["codex-sol"]
	dialect.Port = 43170
	dialect.APIKey = "local-secret"

	env := claudeEnvironment(nil, "/tmp/claude", dialect)
	value := lookupEnv(env, autoCompactWindowEnv)
	if value != "372000" {
		t.Fatalf("%s = %q, want %q", autoCompactWindowEnv, value, "372000")
	}
	if percent := float64(effectiveInput) / float64(dialect.ContextWindow) * 100; percent < 99 || percent > 100 {
		t.Fatalf("reproduced fill = %.1f%%, want the reported 99.1%% of the configured window", percent)
	}
}

func TestClaudeEnvironmentKeepsExistingRoutingVariables(t *testing.T) {
	dialect := presets["kimi"]
	dialect.Port = 43171
	dialect.APIKey = "local-secret"

	env := claudeEnvironment(nil, "/tmp/claude-config", dialect)

	for key, want := range map[string]string{
		"CLAUDE_CONFIG_DIR":            "/tmp/claude-config",
		"ANTHROPIC_BASE_URL":           "http://127.0.0.1:43171",
		"ANTHROPIC_AUTH_TOKEN":         "local-secret",
		"ANTHROPIC_MODEL":              "kimi-k3",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "kimi-k3",
		"CLAUDE_CODE_SUBAGENT_MODEL":   "kimi-k3",
		autoCompactWindowEnv:           "262144",
	} {
		if value := lookupEnv(env, key); value != want {
			t.Errorf("%s = %q, want %q", key, value, want)
		}
	}
	for _, item := range env {
		if strings.HasPrefix(item, "ANTHROPIC_API_KEY=") {
			t.Error("launch environment must not define ANTHROPIC_API_KEY")
		}
	}
}
