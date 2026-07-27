package app

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every preset must declare a reviewed context window. A new preset added
// without capacity metadata leaves Claude Code auto-compaction uncalibrated for
// that route, so it fails here rather than silently at runtime.
func TestEveryPresetDeclaresAReviewedContextWindow(t *testing.T) {
	verified := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	for _, name := range presetNames() {
		source, ok := presetContextWindows[name]
		if !ok {
			t.Errorf("preset %q has no entry in presetContextWindows", name)
			continue
		}
		if !validContextWindow(source.Window) {
			t.Errorf("preset %q declares invalid context window %d", name, source.Window)
		}
		if strings.TrimSpace(source.Basis) == "" {
			t.Errorf("preset %q documents no basis for its context window", name)
		}
		if !verified.MatchString(source.Verified) {
			t.Errorf("preset %q has verification date %q, want YYYY-MM-DD", name, source.Verified)
		}
		if got := presets[name].ContextWindow; got != source.Window {
			t.Errorf("presets[%q].ContextWindow = %d, want %d from the documented table", name, got, source.Window)
		}
	}
}

// The documented table must not drift ahead of the presets it describes.
func TestContextWindowTableHasNoOrphanEntries(t *testing.T) {
	for name := range presetContextWindows {
		if _, ok := presets[name]; !ok {
			t.Errorf("presetContextWindows documents unknown preset %q", name)
		}
	}
}

// A preset that spans several models must advertise the smallest window across
// every selectable model, because Claude Code receives one process-level window
// that has to stay safe after `/model` switching and for subagents.
func TestMultiModelPresetsUseTheSmallestSupportedWindow(t *testing.T) {
	for _, testCase := range []struct {
		preset string
		window int
		reason string
	}{
		{"claude", 200000, "Sonnet 4.6 and Haiku 4.5 cap the 1M Fable 5 main model"},
		{"mixed-frontier", 262144, "Kimi K3 caps Fable 5, GPT-5.6 Sol, and Grok 4.5"},
		{"glm", 131072, "GLM-4.5-Air caps GLM-5.2 and GLM-5-Turbo"},
		{"codex-sol", 372000, "every GPT-5.6 Sol/Terra/Luna tier shares one window"},
	} {
		if got := presets[testCase.preset].ContextWindow; got != testCase.window {
			t.Errorf("presets[%q].ContextWindow = %d, want %d (%s)", testCase.preset, got, testCase.window, testCase.reason)
		}
	}
}

func TestValidContextWindowBounds(t *testing.T) {
	for _, testCase := range []struct {
		value int
		valid bool
	}{
		{0, false},
		{-1, false},
		{1, true},
		{372000, true},
		{maxContextWindow, true},
		{maxContextWindow + 1, false},
	} {
		if got := validContextWindow(testCase.value); got != testCase.valid {
			t.Errorf("validContextWindow(%d) = %t, want %t", testCase.value, got, testCase.valid)
		}
	}
}

// The dialect's calculated window must win over whatever the parent shell
// exported, so a preset launch is deterministic in any terminal.
func TestApplyAutoCompactWindowReplacesAnInheritedValue(t *testing.T) {
	env := []string{"PATH=/usr/bin", autoCompactWindowEnv + "=999", "HOME=/Users/example"}
	got := applyAutoCompactWindow(env, 372000)
	if count := countEnv(got, autoCompactWindowEnv); count != 1 {
		t.Fatalf("%s appears %d times, want exactly 1: %v", autoCompactWindowEnv, count, got)
	}
	if value := lookupEnv(got, autoCompactWindowEnv); value != "372000" {
		t.Fatalf("%s = %q, want %q", autoCompactWindowEnv, value, "372000")
	}
}

// An unknown window has nothing to calibrate with, so an explicitly exported
// ambient value stays the user's choice rather than being silently dropped.
func TestApplyAutoCompactWindowLeavesAmbientValueWhenUnknown(t *testing.T) {
	env := []string{autoCompactWindowEnv + "=250000"}
	for _, window := range []int{0, -1, maxContextWindow + 1} {
		got := applyAutoCompactWindow(env, window)
		if value := lookupEnv(got, autoCompactWindowEnv); value != "250000" {
			t.Fatalf("window %d: %s = %q, want the inherited %q", window, autoCompactWindowEnv, value, "250000")
		}
	}
}

func TestApplyAutoCompactWindowAddsTheVariableWhenAbsent(t *testing.T) {
	got := applyAutoCompactWindow([]string{"PATH=/usr/bin"}, 262144)
	if value := lookupEnv(got, autoCompactWindowEnv); value != "262144" {
		t.Fatalf("%s = %q, want %q", autoCompactWindowEnv, value, "262144")
	}
}

// The reproduced codex-sol session reached 368,812 effective input tokens
// against a 372,000-token provider window without compacting. The preset must
// hand Claude Code that same denominator so it can compact before exhaustion.
func TestCodexSolWindowCoversTheReproducedExhaustionCase(t *testing.T) {
	const uncachedInput = 15020
	const cacheReadInput = 353792
	const effectiveInput = uncachedInput + cacheReadInput
	if effectiveInput != 368812 {
		t.Fatalf("effective input = %d, want 368812", effectiveInput)
	}
	window := presets["codex-sol"].ContextWindow
	if window != 372000 {
		t.Fatalf("codex-sol context window = %d, want the 372000-token GPT-5.6 Sol window", window)
	}
	if effectiveInput >= window {
		t.Fatalf("effective input %d must stay below the configured window %d", effectiveInput, window)
	}
	env := applyAutoCompactWindow(nil, window)
	if value := lookupEnv(env, autoCompactWindowEnv); value != strconv.Itoa(window) {
		t.Fatalf("%s = %q, want %q", autoCompactWindowEnv, value, strconv.Itoa(window))
	}
}

func lookupEnv(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func countEnv(env []string, key string) int {
	prefix := key + "="
	count := 0
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			count++
		}
	}
	return count
}
