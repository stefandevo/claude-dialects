package app

import (
	"math"
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
		{"mixed-frontier", 372000, "GPT-5.6 Sol caps Fable 5, Kimi K3, and Grok 4.5"},
		{"glm", 200000, "GLM-5-Turbo (sonnet) and GLM-4.7 (haiku) both at 200K cap GLM-5.3"},
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
func TestApplyContextWindowReplacesAnInheritedValue(t *testing.T) {
	env := []string{"PATH=/usr/bin", autoCompactWindowEnv + "=999", maxContextTokensEnv + "=999", "HOME=/Users/example"}
	got := applyContextWindow(env, 372000)
	for _, key := range contextWindowEnvs {
		if count := countEnv(got, key); count != 1 {
			t.Fatalf("%s appears %d times, want exactly 1: %v", key, count, got)
		}
		if value := lookupEnv(got, key); value != "372000" {
			t.Fatalf("%s = %q, want %q", key, value, "372000")
		}
	}
}

// An unknown window has nothing to calibrate with, so an explicitly exported
// ambient value stays the user's choice rather than being silently dropped.
func TestApplyContextWindowLeavesAmbientValueWhenUnknown(t *testing.T) {
	env := []string{autoCompactWindowEnv + "=250000", maxContextTokensEnv + "=250000"}
	for _, window := range []int{0, -1, maxContextWindow + 1} {
		got := applyContextWindow(env, window)
		for _, key := range contextWindowEnvs {
			if value := lookupEnv(got, key); value != "250000" {
				t.Fatalf("window %d: %s = %q, want the inherited %q", window, key, value, "250000")
			}
		}
	}
}

// Both variables carry the same declared capacity, because Claude Code reads
// them through separate chains: one decides when to compact, the other is the
// denominator its own context readouts — and the statusline — are measured
// against. Declaring only one leaves the other on Claude Code's 200,000-token
// default for a model ID it cannot recognize.
func TestApplyContextWindowAddsBothVariablesWhenAbsent(t *testing.T) {
	got := applyContextWindow([]string{"PATH=/usr/bin"}, 262144)
	for _, key := range contextWindowEnvs {
		if value := lookupEnv(got, key); value != "262144" {
			t.Errorf("%s = %q, want %q", key, value, "262144")
		}
	}
}

// The reported cc-glm session showed `ctx 47%` beside `4% until auto-compact`
// and compacted immediately. Both readings share a numerator; only the
// denominator differed, because Claude Code resolved glm-5.2 to its 200,000
// default while auto-compaction measured against the declared 131,072.
//
// The reserve Claude Code holds back before compacting (a 20,000-token output
// allowance plus a 13,000-token buffer) means the two readings still will not
// be equal — that offset exists against first-party models too. What the second
// variable removes is the false denominator, which is what made them move in
// opposite directions.
//
// The window is the one that session ran against, not whatever the glm preset
// declares today: this reproduces a recorded incident, so its numbers are fixed
// by what was observed. The preset has since moved its haiku tier off
// GLM-4.5-Air and now declares 200,000 — a change to the mapping, not to the
// reconciliation this asserts, which holds for any declared window.
func TestDeclaredWindowReconcilesTheStatuslineWithTheCompactionCountdown(t *testing.T) {
	const claudeCodeDefaultWindow = 200000
	const outputReserve = 20000
	const compactionBuffer = 13000
	const usedTokens = 94149
	const window = 131072

	env := applyContextWindow(nil, window)
	declared := lookupEnv(env, maxContextTokensEnv)
	if declared != strconv.Itoa(window) {
		t.Fatalf("%s = %q, want the declared %q", maxContextTokensEnv, declared, strconv.Itoa(window))
	}

	// The countdown's threshold, as Claude Code derives it from the same window.
	threshold := window - outputReserve - compactionBuffer
	if threshold != 98072 {
		t.Fatalf("auto-compact threshold = %d, want the reported 98072", threshold)
	}
	remaining := percent(threshold-usedTokens, threshold)
	if remaining != 4 {
		t.Fatalf("countdown = %d%% until auto-compact, want the reported 4%%", remaining)
	}

	if stale := percent(usedTokens, claudeCodeDefaultWindow); stale != 47 {
		t.Fatalf("undeclared statusline reading = %d%%, want the reported 47%%", stale)
	}
	fixed := percent(usedTokens, window)
	if fixed != 72 {
		t.Fatalf("declared statusline reading = %d%%, want 72%%", fixed)
	}
	// A session this close to compaction must no longer read as under half full.
	if fixed+remaining < 70 {
		t.Fatalf("statusline %d%% and countdown %d%% still disagree by more than the reserve", fixed, remaining)
	}
}

// percent mirrors the rounding Claude Code applies to both readings.
func percent(part, whole int) int {
	return int(math.Round(float64(part) / float64(whole) * 100))
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
	env := applyContextWindow(nil, window)
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
