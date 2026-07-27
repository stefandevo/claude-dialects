package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	proxyusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// ContextUsage is the latest per-request context reading for a dialect. It holds
// counters only — never a prompt, tool result, request body, or credential — so
// it is safe to persist and to surface in diagnostics.
type ContextUsage struct {
	// Window is the capacity the dialect was calibrated with, in tokens.
	Window int `json:"window"`
	// InputTokens is the canonical input total of the most recent request,
	// counting uncached, cache-read, and cache-write tokens exactly once.
	InputTokens int64 `json:"inputTokens"`
	// UsedPercent is InputTokens as a percentage of Window.
	UsedPercent float64 `json:"usedPercent"`
	// Model is the provider model ID that served the request.
	Model string `json:"model"`
	// ObservedAt is when the reading was taken.
	ObservedAt time.Time `json:"observedAt"`
}

// contextUsageBands are the fill percentages worth warning about. A conversation
// crossing one of these is close enough to the provider limit that a missed
// compaction becomes a failed request.
var contextUsageBands = []float64{80, 90, 95}

// contextStateFile is where a dialect's latest context reading lives, relative
// to that dialect's own directory. It is never joined into an absolute path for
// I/O: every access goes through an instanceFS so a replaced or symlinked
// instance directory cannot redirect the write into another dialect's state.
const contextStateFile = "context.json"

// readContextUsage returns the latest recorded reading for a dialect. It fails
// when nothing has been observed yet.
func readContextUsage(name string) (ContextUsage, error) {
	instance, err := openInstanceFS(name)
	if err != nil {
		return ContextUsage{}, err
	}
	defer instance.Close()
	data, err := instance.ReadFile(contextStateFile)
	if err != nil {
		return ContextUsage{}, err
	}
	var state ContextUsage
	if err = json.Unmarshal(data, &state); err != nil {
		return ContextUsage{}, fmt.Errorf("read context usage for %q: %w", name, err)
	}
	return state, nil
}

// contextMonitor observes how full each provider request leaves the dialect's
// context window. It answers the diagnostic question the reproduced session
// could not — how close a request came to the provider limit — without taking
// on any part of Claude Code's compaction policy.
type contextMonitor struct {
	// instance is the caller's already-pinned dialect directory. Readings are
	// written through it rather than by re-resolving the dialect name, so they
	// inherit the identity the proxy verified before it began serving.
	instance *instanceFS
	name     string
	window   int

	// warn reports a threshold crossing. A field so tests can capture it.
	warn func(message string)

	mu sync.Mutex
	// band is the highest threshold already warned about. It re-arms when usage
	// falls back below that threshold, so a long session warns once per band
	// rather than on every tool call.
	band int
}

// newContextMonitor builds the monitor for one dialect, writing through the
// caller's pinned instance directory. A dialect with no configured window has no
// denominator to measure against, and the monitor is inert for it.
func newContextMonitor(instance *instanceFS, dialect Dialect) *contextMonitor {
	return &contextMonitor{
		instance: instance,
		name:     instance.name,
		// The effective window, not the stored field: ExtraEnv is applied last at
		// launch and can replace it, and a percentage measured against a window
		// the session never received would be misleading rather than diagnostic.
		window: effectiveContextWindow(dialect),
		warn:   func(message string) { fmt.Fprintln(os.Stderr, message) },
	}
}

// HandleUsage implements the CLIProxyAPI usage plugin contract. Records arrive
// on the proxy's dispatcher goroutine, so the band state is guarded.
func (monitor *contextMonitor) HandleUsage(_ context.Context, record proxyusage.Record) {
	if monitor == nil || !validContextWindow(monitor.window) {
		return
	}
	input := recordInputTokens(record)
	if input <= 0 {
		// A failed or usage-free request carries no reading; keep the last real
		// measurement rather than overwriting it with zeros.
		return
	}
	percent := float64(input) / float64(monitor.window) * 100
	state := ContextUsage{
		Window:      monitor.window,
		InputTokens: input,
		UsedPercent: percent,
		Model:       record.Model,
		ObservedAt:  time.Now().UTC(),
	}
	monitor.persist(state)

	band := contextUsageBand(percent)
	monitor.mu.Lock()
	crossed := band > monitor.band
	monitor.band = band
	monitor.mu.Unlock()
	if !crossed {
		return
	}
	monitor.warn(fmt.Sprintf(
		"Warning: %s used %.1f%% of its %d-token context window on the last request (%d input tokens, model %s). "+
			"Claude Code compacts on its own; run /compact if it does not.",
		monitor.name, percent, monitor.window, input, record.Model))
}

// persist writes the reading beside the dialect's other private state. A failed
// write is diagnostic-only and must never disturb the request path.
func (monitor *contextMonitor) persist(state ContextUsage) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = monitor.instance.AtomicWrite(contextStateFile, append(data, '\n'), 0o600)
}

// recordInputTokens returns how many input tokens one request consumed.
//
// The canonical v2 breakdown is authoritative: its Input.TotalTokens is defined
// as uncached plus cache-read plus cache-write, so cached context is counted
// exactly once. Providers that report only the legacy fields are normalized
// through CLIProxyAPI's own EnsureTokenBreakdownForProvider rather than by
// re-adding the fields here, because only it knows whether a given provider's
// cache counters are already a subset of its input total — hand-rolled
// arithmetic is exactly how cache-read tokens get double counted. A provider
// whose semantics it does not know yields no reading rather than a guess.
func recordInputTokens(record proxyusage.Record) int64 {
	if record.Detail.TokenBreakdown.Valid() && record.Detail.TokenBreakdown.Input.TotalTokens > 0 {
		return record.Detail.TokenBreakdown.Input.TotalTokens
	}
	normalized := proxyusage.EnsureTokenBreakdownForProvider(record.Detail, record.Provider, record.ExecutorType)
	return normalized.TokenBreakdown.Input.TotalTokens
}

// contextUsageReport renders the latest reading as a doctor line, or "" when
// nothing has been observed for the dialect yet.
func contextUsageReport(name string) string {
	state, err := readContextUsage(name)
	if err != nil || state.Window <= 0 {
		return ""
	}
	marker := "○"
	if contextUsageBand(state.UsedPercent) > 0 {
		marker = "✗"
	}
	return fmt.Sprintf("%s %s last request used %.1f%% of its %d-token context window (%d input tokens, model %s)",
		marker, name, state.UsedPercent, state.Window, state.InputTokens, state.Model)
}

// contextUsageBand maps a fill percentage to the highest threshold it has
// crossed: 0 below the first band, then one per entry in contextUsageBands.
func contextUsageBand(percent float64) int {
	band := 0
	for index, threshold := range contextUsageBands {
		if percent >= threshold {
			band = index + 1
		}
	}
	return band
}
