package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	proxyusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// The canonical breakdown already sums uncached, cache-read, and cache-write
// input into one non-overlapping total, so it is used as-is.
func TestRecordInputTokensPrefersTheCanonicalBreakdown(t *testing.T) {
	record := proxyusage.Record{Detail: proxyusage.Detail{
		InputTokens:     15020,
		CacheReadTokens: 353792,
		TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(
			15020, 353792, 0, 1200, 300, 0,
		),
	}}

	if got := recordInputTokens(record); got != 368812 {
		t.Fatalf("input tokens = %d, want 368812", got)
	}
}

// A provider that reports only the legacy fields must still be measured, and
// cache-read tokens must be counted exactly once.
func TestRecordInputTokensFallsBackWithoutDoubleCounting(t *testing.T) {
	record := proxyusage.Record{
		Provider: "codex",
		Detail: proxyusage.Detail{
			InputTokens:     368812,
			CacheReadTokens: 353792,
			OutputTokens:    1500,
		},
	}

	got := recordInputTokens(record)

	if got != 368812 {
		t.Fatalf("input tokens = %d, want 368812 counted once", got)
	}
	if got > 368812 {
		t.Fatalf("cache-read tokens were double counted: %d", got)
	}
}

func TestRecordInputTokensReportsNothingForAnEmptyRecord(t *testing.T) {
	if got := recordInputTokens(proxyusage.Record{}); got != 0 {
		t.Fatalf("input tokens = %d, want 0", got)
	}
}

// The reproduced codex-sol session: 99.1% of the provider window reached in a
// single request. The monitor must compare that per-request input against the
// dialect's configured window, not against cumulative account usage.
func TestMonitorMeasuresTheReproducedCodexExhaustion(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	dialect := presets["codex-sol"]
	monitor := testContextMonitor(t, "cc-codex", dialect)

	monitor.HandleUsage(context.Background(), proxyusage.Record{
		Model: "gpt-5.6-sol",
		Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(
			15020, 353792, 0, 1200, 300, 0,
		)},
	})

	state, err := readContextUsage("cc-codex")
	if err != nil {
		t.Fatal(err)
	}
	if state.InputTokens != 368812 {
		t.Fatalf("observed input = %d, want 368812", state.InputTokens)
	}
	if state.Window != 372000 {
		t.Fatalf("observed window = %d, want 372000", state.Window)
	}
	if state.UsedPercent < 99.0 || state.UsedPercent > 99.2 {
		t.Fatalf("used percent = %.2f, want the reported 99.1", state.UsedPercent)
	}
}

// Each record describes one request, so a later smaller request must replace the
// reading rather than accumulate into it.
func TestMonitorReplacesRatherThanAccumulates(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	monitor := testContextMonitor(t, "cc-codex", presets["codex-sol"])

	for _, input := range []int64{300000, 12000} {
		monitor.HandleUsage(context.Background(), proxyusage.Record{
			Model:  "gpt-5.6-sol",
			Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(input, 0, 0, 10, 0, 0)},
		})
	}

	state, err := readContextUsage("cc-codex")
	if err != nil {
		t.Fatal(err)
	}
	if state.InputTokens != 12000 {
		t.Fatalf("observed input = %d, want the latest request's 12000", state.InputTokens)
	}
}

// A warning on every tool call would be noise, so each threshold band warns once
// and only re-arms after usage falls back below it.
func TestMonitorWarnsOncePerThresholdBand(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	monitor := testContextMonitor(t, "cc-codex", presets["codex-sol"])
	var warnings []string
	monitor.warn = func(message string) { warnings = append(warnings, message) }

	send := func(input int64) {
		monitor.HandleUsage(context.Background(), proxyusage.Record{
			Model:  "gpt-5.6-sol",
			Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(input, 0, 0, 10, 0, 0)},
		})
	}

	send(310000) // ~83% — first warning
	send(312000) // still ~84%, same band — silent
	send(340000) // ~91% — next band warns
	if len(warnings) != 2 {
		t.Fatalf("got %d warnings, want 2: %v", len(warnings), warnings)
	}

	send(100000) // compaction happened; band re-arms silently
	if len(warnings) != 2 {
		t.Fatalf("dropping below the band warned again: %v", warnings)
	}
	send(310000) // back above the band — warns again
	if len(warnings) != 3 {
		t.Fatalf("got %d warnings after re-entering the band, want 3: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "cc-codex") {
		t.Errorf("warning %q does not name the dialect", warnings[0])
	}
}

func TestMonitorStaysQuietBelowTheFirstBand(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	monitor := testContextMonitor(t, "cc-codex", presets["codex-sol"])
	var warnings []string
	monitor.warn = func(message string) { warnings = append(warnings, message) }

	monitor.HandleUsage(context.Background(), proxyusage.Record{
		Model:  "gpt-5.6-sol",
		Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(100000, 0, 0, 10, 0, 0)},
	})

	if len(warnings) != 0 {
		t.Fatalf("warned below the first band: %v", warnings)
	}
}

// Without a window there is no denominator, so nothing is measured or persisted.
func TestMonitorIsInertWithoutAContextWindow(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	monitor := testContextMonitor(t, "cc-custom", Dialect{Model: "vendor-model"})

	monitor.HandleUsage(context.Background(), proxyusage.Record{
		Model:  "vendor-model",
		Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(500000, 0, 0, 10, 0, 0)},
	})

	if _, err := readContextUsage("cc-custom"); err == nil {
		t.Fatal("an uncalibrated dialect persisted a context reading")
	}
}

// Monitoring records numbers only. No prompt, tool result, credential, or
// request body may ever reach disk.
func TestMonitorPersistsOnlyNumericStateWithPrivatePermissions(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	dialect := presets["codex-sol"]
	dialect.APIKey = "local-secret"
	monitor := testContextMonitor(t, "cc-codex", dialect)

	monitor.HandleUsage(context.Background(), proxyusage.Record{
		Model:  "gpt-5.6-sol",
		APIKey: "local-secret",
		Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(200000, 0, 0, 10, 0, 0)},
	})

	instance, err := openInstanceFS("cc-codex")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	path, err := instance.Abs(contextStateFile)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "local-secret") {
		t.Fatalf("persisted state leaked a credential:\n%s", data)
	}
	var fields map[string]any
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for key := range fields {
		switch key {
		case "window", "inputTokens", "usedPercent", "model", "observedAt":
		default:
			t.Errorf("persisted state contains unexpected field %q", key)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("context state permissions = %o, want 600", info.Mode().Perm())
	}
}

// A failed or usage-free request carries no reading, so the last real
// measurement must not be overwritten with zeros.
func TestMonitorIgnoresRecordsWithoutUsage(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	monitor := testContextMonitor(t, "cc-codex", presets["codex-sol"])

	monitor.HandleUsage(context.Background(), proxyusage.Record{
		Model:  "gpt-5.6-sol",
		Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(200000, 0, 0, 10, 0, 0)},
	})
	monitor.HandleUsage(context.Background(), proxyusage.Record{Model: "gpt-5.6-sol", Failed: true})

	state, err := readContextUsage("cc-codex")
	if err != nil {
		t.Fatal(err)
	}
	if state.InputTokens != 200000 {
		t.Fatalf("observed input = %d, want the preserved 200000", state.InputTokens)
	}
}

func TestContextUsageReportSurfacesTheLatestReading(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())

	if line := contextUsageReport("cc-codex"); line != "" {
		t.Fatalf("unobserved dialect reported %q", line)
	}

	monitor := testContextMonitor(t, "cc-codex", presets["codex-sol"])
	monitor.warn = func(string) {}
	monitor.HandleUsage(context.Background(), proxyusage.Record{
		Model:  "gpt-5.6-sol",
		Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(15020, 353792, 0, 1200, 300, 0)},
	})

	line := contextUsageReport("cc-codex")
	for _, expected := range []string{"cc-codex", "99.1%", "372000", "368812", "gpt-5.6-sol"} {
		if !strings.Contains(line, expected) {
			t.Errorf("report %q does not mention %q", line, expected)
		}
	}
	if !strings.HasPrefix(line, "✗") {
		t.Errorf("a near-limit reading must be flagged, got %q", line)
	}
}

func TestContextUsageBandThresholds(t *testing.T) {
	for _, testCase := range []struct {
		percent float64
		band    int
	}{
		{0, 0}, {79.9, 0}, {80, 1}, {89.9, 1}, {90, 2}, {94.9, 2}, {95, 3}, {99.1, 3},
	} {
		if got := contextUsageBand(testCase.percent); got != testCase.band {
			t.Errorf("contextUsageBand(%.1f) = %d, want %d", testCase.percent, got, testCase.band)
		}
	}
}

// Context readings are per-dialect state, so they are subject to the same
// confinement as credentials and PID files. A symlinked instance directory is
// what an unconfined write follows: the pathname still reads as the dialect's
// own while the bytes land wherever the link points.
func TestMonitorWritesStayInsideTheDialectDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	escape := t.TempDir()
	if err := os.Symlink(escape, filepath.Join(home, "instances", "cc-attacker")); err != nil {
		t.Fatal(err)
	}

	monitor := testContextMonitor(t, "cc-attacker", presets["codex-sol"])
	monitor.warn = func(string) {}
	monitor.HandleUsage(context.Background(), proxyusage.Record{
		Model:  "gpt-5.6-sol",
		Detail: proxyusage.Detail{TokenBreakdown: proxyusage.NewIndependentTokenBreakdown(200000, 0, 0, 10, 0, 0)},
	})

	entries, err := os.ReadDir(escape)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a context reading escaped the instances tree through a symlinked dialect: %v", entries)
	}
}

// testContextMonitor builds a monitor over a real pinned instance directory, the
// same way the embedded proxy does, so the tests exercise the confined write
// path rather than a pathname the production code no longer uses.
func testContextMonitor(t *testing.T, name string, dialect Dialect) *contextMonitor {
	t.Helper()
	instance, err := openInstanceFS(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return newContextMonitor(instance, dialect)
}
