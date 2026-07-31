package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextWindowDiagnosticsReportsAnUnconfiguredDialect(t *testing.T) {
	lines := contextWindowDiagnostics("cc-custom", Dialect{Model: "vendor-model"})
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(lines), lines)
	}
	for _, expected := range []string{"cc-custom", "context window", "--context-window"} {
		if !strings.Contains(lines[0], expected) {
			t.Errorf("diagnostic %q does not mention %q", lines[0], expected)
		}
	}
}

// A hand-edited configuration can hold a value that would disable compaction or
// declare an impossible capacity; both must be called out rather than used.
func TestContextWindowDiagnosticsReportsAnOutOfRangeValue(t *testing.T) {
	lines := contextWindowDiagnostics("cc-custom", Dialect{Model: "vendor-model", ContextWindow: maxContextWindow + 1})
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "invalid") {
		t.Errorf("diagnostic %q does not describe the value as invalid", lines[0])
	}
}

func TestContextWindowDiagnosticsStaysQuietForACalibratedDialect(t *testing.T) {
	if lines := contextWindowDiagnostics("cc-codex", presets["codex-sol"]); len(lines) != 0 {
		t.Fatalf("calibrated dialect reported %v", lines)
	}
}

// An ExtraEnv entry that is not a usable window has to name the variable
// carrying it, because either of the two can be the one at fault and the user
// has to know which line of config.json to correct.
func TestContextWindowDiagnosticsNamesTheOffendingOverride(t *testing.T) {
	for _, key := range contextWindowEnvs {
		dialect := presets["codex-sol"]
		dialect.ExtraEnv = map[string]string{key: "not-a-number"}
		lines := contextWindowDiagnostics("cc-codex", dialect)
		if len(lines) != 1 {
			t.Fatalf("%s: got %d diagnostics, want 1: %v", key, len(lines), lines)
		}
		for _, expected := range []string{"cc-codex", key, "not-a-number"} {
			if !strings.Contains(lines[0], expected) {
				t.Errorf("diagnostic %q does not mention %q", lines[0], expected)
			}
		}
	}
}

// A doubly broken declaration has to be correctable in one pass: reporting only
// the first entry would send the user back to doctor to discover the second.
func TestContextWindowDiagnosticsReportsEveryUnusableOverride(t *testing.T) {
	dialect := presets["codex-sol"]
	dialect.ExtraEnv = map[string]string{
		autoCompactWindowEnv: "not-a-number",
		maxContextTokensEnv:  "0",
	}

	lines := contextWindowDiagnostics("cc-codex", dialect)

	if len(lines) != len(contextWindowEnvs) {
		t.Fatalf("got %d diagnostics, want %d: %v", len(lines), len(contextWindowEnvs), lines)
	}
	for index, key := range contextWindowEnvs {
		if !strings.Contains(lines[index], key) {
			t.Errorf("diagnostic %d (%q) does not name %q", index, lines[index], key)
		}
	}
}

// Claude Dialects only supplies the capacity; Claude Code owns compaction and
// its own context readouts. If a future release stops honoring either variable,
// that boundary has broken and doctor has to say so rather than silently
// keeping a dead setting.
func TestContextWindowCompatibilityDiagnosticsReportsEachDroppedVariable(t *testing.T) {
	for _, missing := range contextWindowEnvs {
		stubContextWindowSupport(t, func(string) ([]string, error) { return []string{missing}, nil })

		lines := contextWindowCompatibilityDiagnostics("/usr/local/bin/claude")

		if len(lines) != 1 {
			t.Fatalf("%s missing: got %d diagnostics, want 1: %v", missing, len(lines), lines)
		}
		for _, expected := range []string{missing, "/usr/local/bin/claude", "43989"} {
			if !strings.Contains(lines[0], expected) {
				t.Errorf("diagnostic %q does not mention %q", lines[0], expected)
			}
		}
		for _, other := range contextWindowEnvs {
			if other != missing && strings.Contains(lines[0], other) {
				t.Errorf("diagnostic %q blames %q, which the build still references", lines[0], other)
			}
		}
	}
}

// A capacity variable added without an explanation of what losing it costs
// would produce a diagnostic that names a variable and then trails off.
func TestEveryCapacityVariableExplainsWhatLosingItCosts(t *testing.T) {
	for _, key := range contextWindowEnvs {
		if strings.TrimSpace(contextWindowEnvConsequence[key]) == "" {
			t.Errorf("%s has no entry in contextWindowEnvConsequence", key)
		}
	}
}

// A build that dropped both is two separate losses — one delays compaction, the
// other falsifies the reported fill level — so each is reported on its own line.
func TestContextWindowCompatibilityDiagnosticsReportsBoth(t *testing.T) {
	stubContextWindowSupport(t, func(string) ([]string, error) { return contextWindowEnvs, nil })

	if lines := contextWindowCompatibilityDiagnostics("/usr/local/bin/claude"); len(lines) != len(contextWindowEnvs) {
		t.Fatalf("got %d diagnostics, want %d: %v", len(lines), len(contextWindowEnvs), lines)
	}
}

func TestContextWindowCompatibilityDiagnosticsStaysQuietWhenSupported(t *testing.T) {
	stubContextWindowSupport(t, func(string) ([]string, error) { return nil, nil })

	if lines := contextWindowCompatibilityDiagnostics("/usr/local/bin/claude"); len(lines) != 0 {
		t.Fatalf("supported build reported %v", lines)
	}
}

// An unreadable executable proves nothing either way, so doctor must not raise
// a false alarm about a Claude Code build it could not inspect.
func TestContextWindowCompatibilityDiagnosticsStaysQuietWhenUninspectable(t *testing.T) {
	stubContextWindowSupport(t, func(string) ([]string, error) {
		return contextWindowEnvs, errors.New("permission denied")
	})

	if lines := contextWindowCompatibilityDiagnostics("/usr/local/bin/claude"); len(lines) != 0 {
		t.Fatalf("uninspectable build reported %v", lines)
	}
}

func TestClaudeMissingContextWindowVarsScansTheExecutable(t *testing.T) {
	dir := t.TempDir()
	both := filepath.Join(dir, "claude-both")
	partial := filepath.Join(dir, "claude-partial")
	neither := filepath.Join(dir, "claude-neither")
	if err := os.WriteFile(both, []byte("prefix\x00"+strings.Join(contextWindowEnvs, "\x00")+"\x00suffix"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, []byte("prefix\x00"+autoCompactWindowEnv+"\x00suffix"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(neither, []byte("a build without the variables"), 0o755); err != nil {
		t.Fatal(err)
	}

	if missing, err := claudeMissingContextWindowVars(both); err != nil || len(missing) != 0 {
		t.Fatalf("supporting executable: missing=%v err=%v", missing, err)
	}
	missing, err := claudeMissingContextWindowVars(partial)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != maxContextTokensEnv {
		t.Fatalf("partial executable: missing=%v, want [%s]", missing, maxContextTokensEnv)
	}
	if missing, err := claudeMissingContextWindowVars(neither); err != nil || len(missing) != len(contextWindowEnvs) {
		t.Fatalf("unsupporting executable: missing=%v err=%v", missing, err)
	}
	if _, err := claudeMissingContextWindowVars(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing executable must report an error, not a verdict")
	}
}

// A literal can straddle the boundary between two reads, so the scan must
// carry a tail forward rather than only matching within one chunk. The first
// read fills chunk+overlap bytes, so placing the literal to span that offset
// splits it across the two reads.
func TestClaudeMissingContextWindowVarsMatchesAcrossBufferBoundaries(t *testing.T) {
	for _, name := range contextWindowEnvs {
		path := filepath.Join(t.TempDir(), "claude")
		firstReadEnd := contextWindowScanChunk + contextWindowScanOverlap()
		start := firstReadEnd - len(name)/2
		body := strings.Repeat("x", start) + name + strings.Repeat("x", 1024)
		if start >= firstReadEnd || start+len(name) <= firstReadEnd {
			t.Fatalf("literal at [%d,%d) does not span the %d-byte read boundary",
				start, start+len(name), firstReadEnd)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}

		missing, err := claudeMissingContextWindowVars(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, reported := range missing {
			if reported == name {
				t.Errorf("%s spanning a chunk boundary was not found", name)
			}
		}
	}
}

func stubContextWindowSupport(t *testing.T, probe func(string) ([]string, error)) {
	t.Helper()
	previous := contextWindowSupportProbe
	contextWindowSupportProbe = probe
	t.Cleanup(func() { contextWindowSupportProbe = previous })
}
