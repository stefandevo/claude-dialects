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

// Claude Dialects only supplies the capacity; Claude Code owns compaction. If a
// future release stops honoring the variable, that boundary has broken and
// doctor has to say so rather than silently keeping a dead setting.
func TestAutoCompactCompatibilityDiagnosticReportsAnUnsupportedBuild(t *testing.T) {
	stubAutoCompactSupport(t, func(string) (bool, error) { return false, nil })

	line := autoCompactCompatibilityDiagnostic("/usr/local/bin/claude")

	if line == "" {
		t.Fatal("an unsupported Claude Code build produced no diagnostic")
	}
	for _, expected := range []string{autoCompactWindowEnv, "/usr/local/bin/claude", "43989"} {
		if !strings.Contains(line, expected) {
			t.Errorf("diagnostic %q does not mention %q", line, expected)
		}
	}
}

func TestAutoCompactCompatibilityDiagnosticStaysQuietWhenSupported(t *testing.T) {
	stubAutoCompactSupport(t, func(string) (bool, error) { return true, nil })

	if line := autoCompactCompatibilityDiagnostic("/usr/local/bin/claude"); line != "" {
		t.Fatalf("supported build reported %q", line)
	}
}

// An unreadable executable proves nothing either way, so doctor must not raise
// a false alarm about a Claude Code build it could not inspect.
func TestAutoCompactCompatibilityDiagnosticStaysQuietWhenUninspectable(t *testing.T) {
	stubAutoCompactSupport(t, func(string) (bool, error) { return false, errors.New("permission denied") })

	if line := autoCompactCompatibilityDiagnostic("/usr/local/bin/claude"); line != "" {
		t.Fatalf("uninspectable build reported %q", line)
	}
}

func TestClaudeSupportsAutoCompactWindowScansTheExecutable(t *testing.T) {
	dir := t.TempDir()
	supported := filepath.Join(dir, "claude-supported")
	unsupported := filepath.Join(dir, "claude-unsupported")
	if err := os.WriteFile(supported, []byte("prefix\x00"+autoCompactWindowEnv+"\x00suffix"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unsupported, []byte("a build without the variable"), 0o755); err != nil {
		t.Fatal(err)
	}

	if ok, err := claudeSupportsAutoCompactWindow(supported); err != nil || !ok {
		t.Fatalf("supported executable: ok=%t err=%v", ok, err)
	}
	if ok, err := claudeSupportsAutoCompactWindow(unsupported); err != nil || ok {
		t.Fatalf("unsupported executable: ok=%t err=%v", ok, err)
	}
	if _, err := claudeSupportsAutoCompactWindow(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing executable must report an error, not a verdict")
	}
}

// The literal can straddle the boundary between two reads, so the scan must
// carry a tail forward rather than only matching within one chunk. The first
// read fills chunk+overlap bytes, so placing the literal to span that offset
// splits it across the two reads.
func TestClaudeSupportsAutoCompactWindowMatchesAcrossBufferBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude")
	firstReadEnd := autoCompactScanChunk + len(autoCompactWindowEnv) - 1
	start := firstReadEnd - len(autoCompactWindowEnv)/2
	body := strings.Repeat("x", start) + autoCompactWindowEnv + strings.Repeat("x", 1024)
	if start >= firstReadEnd || start+len(autoCompactWindowEnv) <= firstReadEnd {
		t.Fatalf("literal at [%d,%d) does not span the %d-byte read boundary",
			start, start+len(autoCompactWindowEnv), firstReadEnd)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	ok, err := claudeSupportsAutoCompactWindow(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("literal spanning a chunk boundary was not found")
	}
}

func stubAutoCompactSupport(t *testing.T, probe func(string) (bool, error)) {
	t.Helper()
	previous := autoCompactSupportProbe
	autoCompactSupportProbe = probe
	t.Cleanup(func() { autoCompactSupportProbe = previous })
}
