package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// autoCompactWindowEnv is the Claude Code environment variable that declares the
// model's context capacity. Claude Code uses the minimum of this value and the
// window it detects itself, then applies its own compaction threshold inside it,
// so the value is raw model capacity and never a pre-discounted trigger point.
//
// Claude Dialects launches Claude Code with provider model IDs it cannot
// recognize, so without this variable there is no dependable denominator and a
// conversation can reach the provider's real limit before compaction runs.
//
// The assignment is deliberately isolated in applyAutoCompactWindow so a future
// Claude Code release that renames or rejects the variable is a single-line
// change, and so doctor can report the incompatibility from one place.
// See https://github.com/anthropics/claude-code/issues/43989.
const autoCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

// maxContextWindow bounds accepted capacity values. It sits an order of
// magnitude above today's largest published window so genuine growth is not
// blocked, while a mistyped value is still rejected instead of disabling
// compaction outright.
const maxContextWindow = 20_000_000

// contextWindowSource records where a preset's context window came from and when
// it was last checked, so a model change cannot silently leave stale capacity
// metadata behind. Every preset needs an entry; TestEveryPresetDeclaresAReviewedContextWindow
// fails the build otherwise.
type contextWindowSource struct {
	// Window is the smallest context window supported by any model the preset
	// can select — main, Opus, Sonnet, Haiku, or subagent. Claude Code receives
	// one process-level window, so the minimum is what keeps `/model` switching
	// and subagents safe.
	Window int
	// Basis explains which model determines the value.
	Basis string
	// Verified is the YYYY-MM-DD date the value was last checked against its source.
	Verified string
}

// presetContextWindows is the single source of truth for preset capacity. The
// init below stamps these onto the presets themselves, so every existing reader
// of the presets map picks the value up without a second table to keep in sync.
//
// "CLIProxyAPI registry" values come from the embedded CLIProxyAPI v7.2.102
// model registry (internal/registry/models/models.json), which is authoritative
// for the OAuth-backed routes because it is the same catalog the proxy serves.
//
// Cursor and GitHub publish no per-route context window, and neither the
// @cursor/sdk model list nor the Copilot SDK carries the field, so those routes
// carry explicit conservative fallbacks rather than borrowing a number from a
// same-named direct provider model. A dynamic `auto` route may select any model
// the vendor offers, so its fallback covers the smallest of them.
var presetContextWindows = map[string]contextWindowSource{
	"codex-sol": {
		Window: 372000, Verified: "2026-07-27",
		Basis: "GPT-5.6 Sol, Terra, and Luna all 372000 (CLIProxyAPI registry)",
	},
	"codex": {
		Window: 372000, Verified: "2026-07-27",
		Basis: "GPT-5.6 tiers Sol, Terra, and Luna all 372000 (CLIProxyAPI registry)",
	},
	"kimi": {
		Window: 262144, Verified: "2026-07-27",
		Basis: "Kimi K3, K2.7 Code Highspeed, and K2.6 all 262144 (CLIProxyAPI registry)",
	},
	"gemini": {
		Window: 1048576, Verified: "2026-07-27",
		Basis: "Gemini Pro Agent and the 3.5 Flash routes all 1048576 (CLIProxyAPI registry)",
	},
	"claude": {
		Window: 200000, Verified: "2026-07-27",
		Basis: "Sonnet 4.6 and Haiku 4.5 at 200000 cap the 1M Fable 5 main model (CLIProxyAPI registry)",
	},
	"mixed-frontier": {
		Window: 262144, Verified: "2026-07-27",
		Basis: "Kimi K3 at 262144 caps Fable 5 (1M), GPT-5.6 Sol (372000), and Grok 4.5 (500000) (CLIProxyAPI registry)",
	},
	"glm": {
		Window: 131072, Verified: "2026-07-27",
		Basis: "GLM-4.5-Air at 131072 caps GLM-5.2 (1M) and GLM-5-Turbo (262144) (Z.ai model documentation)",
	},
	"grok": {
		Window: 500000, Verified: "2026-07-27",
		Basis: "Grok 4.5 (CLIProxyAPI registry)",
	},
	"grok-build": {
		Window: 256000, Verified: "2026-07-27",
		Basis: "Grok Build 0.1 (CLIProxyAPI registry)",
	},
	"composer": {
		Window: 200000, Verified: "2026-07-27",
		Basis: "Grok Composer 2.5 Fast as exposed by xAI (CLIProxyAPI registry)",
	},
	"minimax": {
		Window: 204800, Verified: "2026-07-27",
		Basis: "MiniMax-M2.7 documented 204800 combined input and output window (MiniMax platform documentation)",
	},
	"deepseek": {
		Window: 1000000, Verified: "2026-07-27",
		Basis: "DeepSeek V4 Pro and V4 Flash both 1000000 (DeepSeek API documentation)",
	},
	"cursor-composer": {
		Window: 200000, Verified: "2026-07-27",
		Basis: "conservative fallback: Cursor publishes no per-model window for the Composer 2.5 route",
	},
	"cursor-composer-fast": {
		Window: 200000, Verified: "2026-07-27",
		Basis: "conservative fallback: same Composer 2.5 route family as cursor-composer",
	},
	"cursor-auto": {
		Window: 128000, Verified: "2026-07-27",
		Basis: "conservative fallback: Cursor auto may select any offered model, including 128000-token routes",
	},
	"cursor-grok": {
		Window: 200000, Verified: "2026-07-27",
		Basis: "conservative fallback: served through the Cursor route, whose window is not published and need not match xAI's direct Grok 4.5 window",
	},
	"copilot-auto": {
		Window: 128000, Verified: "2026-07-27",
		Basis: "conservative fallback: Copilot auto may select any offered model, including the small mini routes",
	},
	"copilot-mai": {
		Window: 256000, Verified: "2026-07-27",
		Basis: "MAI-Code-1-Flash documented 256000 window",
	},
	"copilot-codex": {
		Window: 200000, Verified: "2026-07-27",
		Basis: "conservative fallback: GitHub documents no default window for the GPT-5.3-Codex Copilot route",
	},
	"copilot-claude": {
		Window: 200000, Verified: "2026-07-27",
		Basis: "Claude Sonnet 4.6 and Haiku 4.5 base windows of 200000; the 1M window is an extended-context capability, not the route default",
	},
	"copilot-gemini": {
		Window: 1048576, Verified: "2026-07-27",
		Basis: "Gemini 3.1 Pro and 3.5 Flash 1048576",
	},
}

// init stamps the documented capacity onto each preset so the table above stays
// the only place a window is written down.
func init() {
	for name, source := range presetContextWindows {
		dialect, ok := presets[name]
		if !ok {
			continue
		}
		dialect.ContextWindow = source.Window
		presets[name] = dialect
	}
}

// presetContextWindow returns the documented capacity for a preset, or 0 when
// the preset is unknown.
func presetContextWindow(preset string) int {
	return presetContextWindows[preset].Window
}

// validContextWindow reports whether a stored or requested capacity is usable.
// Zero means unknown or unconfigured rather than a real capacity, so it is not
// valid: a zero window would tell Claude Code the model holds nothing.
func validContextWindow(value int) bool {
	return value > 0 && value <= maxContextWindow
}

// applyAutoCompactWindow declares the dialect's context capacity to the Claude
// Code process it is about to launch.
//
// A known window always replaces whatever the parent shell exported, so a preset
// launch behaves the same in every terminal. An unknown window has nothing to
// calibrate with, so an ambient value is left in place: it is the user's own
// explicit choice, and dropping it would only make compaction less informed.
func applyAutoCompactWindow(env []string, window int) []string {
	if !validContextWindow(window) {
		return env
	}
	return setEnv(env, autoCompactWindowEnv, strconv.Itoa(window))
}

// effectiveContextWindow returns the window the launched Claude Code process
// actually receives.
//
// claudeEnvironment applies ExtraEnv last, so an entry for the auto-compact
// variable replaces the stored capacity — that override, not the stored field,
// is the denominator the session runs against. An override that is not a usable
// window leaves the real denominator unknown; reporting zero keeps callers from
// measuring against a number that is certainly wrong.
func effectiveContextWindow(dialect Dialect) int {
	override, overridden := dialect.ExtraEnv[autoCompactWindowEnv]
	if !overridden {
		return dialect.ContextWindow
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(override))
	if err != nil || !validContextWindow(parsed) {
		return 0
	}
	return parsed
}

// roundTripsThroughMutations reports whether a dialect's configuration can
// survive being rebuilt by create or by a dashboard save. Neither carries
// ExtraEnv across, and neither has a flag for Bridge or AuthProvider: those
// reach a dialect only from its preset.
//
// A stored preset name is therefore not enough on its own. A dialect whose name
// still says "cursor-composer" but whose bridge or OAuth route was changed by
// hand would have that route silently replaced by the `--preset` command this
// would otherwise bless — the same divergence backfill already refuses through
// sharesContextRoute, so the two must agree.
func roundTripsThroughMutations(dialect Dialect) bool {
	if len(dialect.ExtraEnv) > 0 {
		return false
	}
	if preset, presetBacked := presets[presetSupplyingRoute(dialect)]; presetBacked {
		return dialect.Bridge == preset.Bridge && dialect.AuthProvider == preset.AuthProvider
	}
	return dialect.Bridge == "" && dialect.AuthProvider == ""
}

// presetSupplyingRoute names the preset a create command would restate a dialect
// through: the label it carries, or — for a dialect written before that label
// existed — the preset whose route it matches exactly.
//
// A label still wins over an exact match, because a labeled dialect that has
// since diverged has to keep being judged against what it claims to be: the
// divergence is the reason its remedy is refused, and quietly re-resolving it to
// some other preset would bless the very command that would replace its route.
func presetSupplyingRoute(dialect Dialect) string {
	if _, labeled := presets[dialect.Preset]; labeled {
		return dialect.Preset
	}
	return presetByContextRoute(dialect)
}

// shellArg renders a value for a command line the user is expected to copy into
// a shell, quoting only when the value would otherwise be re-interpreted.
// Upstream URLs carry query strings and model IDs are arbitrary strings, so an
// unquoted value can split arguments or run shell syntax the reader never saw.
func shellArg(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return false
		}
		return !strings.ContainsRune("_@%+=:,./-", r)
	}) < 0 {
		return value
	}
	return shellQuote(value)
}

// contextWindowFixCommand renders a command that records a capacity for a
// dialect without losing the rest of its configuration.
//
// `create` rebuilds a dialect from the flags it is given rather than patching
// it, so a command naming only the model would quietly delete tier overrides,
// the upstream URL and token variable, and any non-default behavior settings.
// A dialect a preset can supply is restated through that preset, which restores
// the whole route in one flag; a custom one restates every flag it actually
// differs on. State no mutation can round-trip — ExtraEnv, or a bridge or OAuth
// route no preset supplies — has no faithful command at all, so this returns ""
// rather than print one that would break the dialect.
func contextWindowFixCommand(name string, dialect Dialect) string {
	if !roundTripsThroughMutations(dialect) {
		return ""
	}
	command := "cc-dialect create " + shellArg(name)
	source := presetSupplyingRoute(dialect)
	base, presetBacked := presets[source]
	switch {
	case presetBacked:
		command += " --preset " + shellArg(source)
	default:
		// prepareDialect fills empty tiers from the primary model, so an
		// unmentioned tier round-trips unchanged.
		base = Dialect{
			Model: dialect.Model, SubagentModel: dialect.Model, OpusModel: dialect.Model,
			SonnetModel: dialect.Model, HaikuModel: dialect.Model,
			Effort: true, EffortLevel: "auto", Concurrency: 3,
		}
		command += " --model " + shellArg(dialect.Model)
	}
	for _, field := range []struct{ flag, value, base string }{
		{"--model", dialect.Model, base.Model},
		{"--subagent-model", dialect.SubagentModel, base.SubagentModel},
		{"--opus-model", dialect.OpusModel, base.OpusModel},
		{"--sonnet-model", dialect.SonnetModel, base.SonnetModel},
		{"--haiku-model", dialect.HaikuModel, base.HaikuModel},
		{"--base-url", dialect.BaseURL, base.BaseURL},
		{"--token-env", dialect.AuthTokenEnv, base.AuthTokenEnv},
		{"--effort-level", dialect.EffortLevel, base.EffortLevel},
	} {
		if field.value != "" && field.value != field.base {
			command += " " + field.flag + " " + shellArg(field.value)
		}
	}
	if dialect.Concurrency != 0 && dialect.Concurrency != base.Concurrency {
		command += fmt.Sprintf(" --concurrency %d", dialect.Concurrency)
	}
	// Both are plain booleans on the command line whose flag defaults are fixed,
	// so they have to be restated whenever the dialect disagrees with them.
	if !dialect.Effort {
		command += " --effort=false"
	}
	if dialect.ToolSearch {
		command += " --tool-search=true"
	}
	// TOKENS rather than <tokens>: the remedy is copied into a shell, where angle
	// brackets are redirection rather than an obvious placeholder.
	return command + " --context-window TOKENS"
}

// contextWindowDiagnostics reports capacity problems doctor should surface for a
// dialect. An uncalibrated dialect is the exact condition issue #44 describes,
// so it is actionable rather than silent.
func contextWindowDiagnostics(name string, dialect Dialect) []string {
	// The effective window, because ExtraEnv is applied last at launch: a stored
	// field that looks healthy says nothing about a process whose window the
	// override replaced with something unusable.
	if validContextWindow(effectiveContextWindow(dialect)) {
		return nil
	}
	var problem string
	switch override, overridden := dialect.ExtraEnv[autoCompactWindowEnv]; {
	case overridden:
		// The override is the calibration once it exists, so the stored field
		// cannot rescue it — say which value is the problem.
		return []string{fmt.Sprintf(
			"✗ %s overrides %s with %q, which is not a usable context window; auto-compaction is uncalibrated "+
				"(remove or correct that entry in the %q extraEnv in config.json)",
			name, autoCompactWindowEnv, override, name)}
	case dialect.ContextWindow != 0:
		problem = fmt.Sprintf("%s has an invalid context window (%d); auto-compaction is uncalibrated",
			name, dialect.ContextWindow)
	default:
		problem = fmt.Sprintf("%s has no context window; Claude Code auto-compaction is uncalibrated for %s",
			name, dialect.Model)
	}
	return []string{"✗ " + problem + " (" + contextWindowRemedy(name, dialect) + ")"}
}

// contextWindowRemedy phrases how to record a capacity for a dialect.
//
// A dashboard save is no safer than a create for the dialects create cannot
// express: its request carries neither ExtraEnv nor the bridge and OAuth fields,
// so saving would drop exactly the state that made the command unsafe. Editing
// the stored configuration is then the only lossless option, so that is what it
// says rather than recommending a different kind of damage.
func contextWindowRemedy(name string, dialect Dialect) string {
	if command := contextWindowFixCommand(name, dialect); command != "" {
		return "run: " + command
	}
	return fmt.Sprintf(
		"add \"contextWindow\" to the %q entry in config.json; this dialect holds settings "+
			"that cc-dialect create and the dashboard would both drop", name)
}

// autoCompactSupportProbe reports whether an installed Claude Code build still
// recognizes the auto-compact window variable. A variable so tests can stub it.
var autoCompactSupportProbe = claudeSupportsAutoCompactWindow

// autoCompactCompatibilityDiagnostic reports a Claude Code build that no longer
// honors the variable Claude Dialects calibrates with, which would silently
// return every dialect to the uncalibrated behavior issue #44 fixes.
//
// A build that cannot be inspected proves nothing either way, so it produces no
// diagnostic: a false alarm about a working installation is worse than silence.
func autoCompactCompatibilityDiagnostic(claudePath string) string {
	supported, err := autoCompactSupportProbe(claudePath)
	if err != nil || supported {
		return ""
	}
	return fmt.Sprintf(
		"✗ Claude Code at %s does not reference %s; auto-compaction may be uncalibrated "+
			"for custom model IDs (see https://github.com/anthropics/claude-code/issues/43989)",
		claudePath, autoCompactWindowEnv)
}

// autoCompactScanChunk is the read size used when scanning the Claude Code
// executable. Chunks overlap by the length of the searched literal so a match
// straddling a boundary is still found.
const autoCompactScanChunk = 1 << 20

// autoCompactScanLimit bounds the scan so an unexpectedly large or endless file
// cannot stall doctor.
const autoCompactScanLimit = 512 << 20

// claudeSupportsAutoCompactWindow scans the resolved Claude Code executable for
// the auto-compact window variable name. Claude Code exposes no API for asking
// which environment variables it honors, so the embedded literal is the only
// available signal; symlinks are resolved first so an npm-style launcher shim is
// inspected as the script it points at rather than as the link.
func claudeSupportsAutoCompactWindow(claudePath string) (bool, error) {
	resolved, err := filepath.EvalSymlinks(claudePath)
	if err != nil {
		return false, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return false, err
	}
	defer file.Close()

	needle := []byte(autoCompactWindowEnv)
	overlap := len(needle) - 1
	buffer := make([]byte, autoCompactScanChunk+overlap)
	filled := 0
	scanned := 0
	for scanned < autoCompactScanLimit {
		read, readErr := io.ReadFull(file, buffer[filled:])
		filled += read
		scanned += read
		if bytes.Contains(buffer[:filled], needle) {
			return true, nil
		}
		if readErr != nil {
			// io.EOF or io.ErrUnexpectedEOF simply means the file ended; any
			// other error means the verdict would be unreliable.
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				return false, nil
			}
			return false, readErr
		}
		// Carry the tail forward so the next chunk can complete a match that
		// began at the end of this one.
		copy(buffer, buffer[filled-overlap:filled])
		filled = overlap
	}
	return false, nil
}
