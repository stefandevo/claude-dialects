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
// model's context capacity to the compaction path. Claude Code uses the minimum
// of this value and the window it detects itself, then applies its own
// compaction threshold inside it, so the value is raw model capacity and never a
// pre-discounted trigger point.
//
// Claude Dialects launches Claude Code with provider model IDs it cannot
// recognize, so without this variable there is no dependable denominator and a
// conversation can reach the provider's real limit before compaction runs.
// See https://github.com/anthropics/claude-code/issues/43989.
const autoCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

// maxContextTokensEnv is the second half of the same declaration. Claude Code
// resolves a model's context window through a chain that never consults the
// auto-compact variable: it looks the model up in its own registry and, for an
// ID that is not there and does not begin with "claude-", falls back to this
// variable before defaulting to 200,000 tokens.
//
// The window this resolves to is what every readout measures against — the
// `context_window.used_percentage` the statusline prints, and Claude Code's own
// `/context` view — and, because compaction runs against the smaller of the two
// chains, it also caps when compaction fires. Declaring only the auto-compact
// window therefore leaves the two indicators sharing a numerator over different
// denominators: a dialect declaring 131,072 reported 47% full while its own
// countdown said 4% remained. The "claude-" exclusion is exactly the dialect
// case, so first-party model IDs keep resolving through the registry as before.
const maxContextTokensEnv = "CLAUDE_CODE_MAX_CONTEXT_TOKENS"

// contextWindowEnvs lists both variables in the order diagnostics report them.
// applyContextWindow writes the same value to both: they describe one capacity,
// and a launch that let them disagree would compact against one number while
// reporting against another. Only a dialect's own ExtraEnv, applied afterwards
// and per variable, can still separate them. Keeping the set here means a future
// Claude Code release that renames or rejects either one is a single-line
// change, and doctor has one place to report the incompatibility from.
var contextWindowEnvs = []string{autoCompactWindowEnv, maxContextTokensEnv}

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
// "CLIProxyAPI registry" values come from the embedded CLIProxyAPI v7.2.122
// model registry (internal/registry/models/models.json), which is authoritative
// for the OAuth-backed routes because it is the same catalog the proxy serves.
// Recalibrating means re-reading that file at the version go.mod pins now, not
// diffing against the version named here: a stamp left behind hides every change
// made since. Each of a preset's models is looked up on its own, under the
// registry key providerForModel maps that model to — per model rather than per
// preset, which is what lets mixed-frontier's four models resolve under four
// different keys and a preset with no AuthProvider resolve at all. The codex
// provider spans the codex-free/team/plus/pro keys, one per account tier. The
// window field then varies by key rather than by model: the Google-native keys
// (gemini, vertex, aistudio, gemini-cli) spell it inputTokenLimit, while every
// other key, antigravity included, spells it context_length.
//
// Cursor and GitHub publish no per-route context window, and neither the
// @cursor/sdk model list nor the Copilot SDK carries the field, so those routes
// carry explicit conservative fallbacks rather than borrowing a number from a
// same-named direct provider model. A dynamic `auto` route may select any model
// the vendor offers, so its fallback covers the smallest of them.
var presetContextWindows = map[string]contextWindowSource{
	"codex-sol": {
		Window: 372000, Verified: "2026-08-11",
		Basis: "GPT-5.6 Sol, Terra, and Luna all 372000 (CLIProxyAPI registry)",
	},
	"codex": {
		Window: 372000, Verified: "2026-08-11",
		Basis: "GPT-5.6 tiers Sol, Terra, and Luna all 372000 (CLIProxyAPI registry)",
	},
	"kimi": {
		Window: 262144, Verified: "2026-08-11",
		Basis: "K2.7 Code Highspeed and K2.6 at 262144 cap the 1048576 Kimi K3 main model (CLIProxyAPI registry)",
	},
	"gemini": {
		Window: 1048576, Verified: "2026-08-11",
		Basis: "Gemini Pro Agent and the 3.5 Flash routes all 1048576 (CLIProxyAPI registry)",
	},
	"claude": {
		Window: 200000, Verified: "2026-08-11",
		Basis: "Sonnet 4.6 and Haiku 4.5 at 200000 cap the 1M Fable 5 main model (CLIProxyAPI registry)",
	},
	"mixed-frontier": {
		Window: 372000, Verified: "2026-08-11",
		Basis: "GPT-5.6 Sol at 372000 caps Fable 5 (1M), Kimi K3 (1048576), and Grok 4.5 (500000) (CLIProxyAPI registry)",
	},
	"glm": {
		Window: 262144, Verified: "2026-07-27",
		Basis: "GLM-5-Turbo at 262144 on both lower tiers caps GLM-5.2 (1M) (Z.ai model documentation)",
	},
	"grok": {
		Window: 500000, Verified: "2026-08-11",
		Basis: "Grok 4.5 (CLIProxyAPI registry)",
	},
	"grok-build": {
		Window: 256000, Verified: "2026-08-11",
		Basis: "Grok Build 0.1 (CLIProxyAPI registry)",
	},
	"composer": {
		Window: 200000, Verified: "2026-08-11",
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
	"cursor-mix": {
		Window: 200000, Verified: "2026-07-30",
		Basis: "conservative fallback: Composer, Grok, and Kimi models served through the Cursor route, whose window is not published and need not match any direct provider window",
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

// applyContextWindow declares the dialect's context capacity to the Claude Code
// process it is about to launch, through both variables Claude Code reads a
// window from.
//
// A known window always replaces whatever the parent shell exported, so a preset
// launch behaves the same in every terminal. An unknown window has nothing to
// calibrate with, so ambient values are left in place: they are the user's own
// explicit choice, and dropping them would only make compaction less informed.
func applyContextWindow(env []string, window int) []string {
	if !validContextWindow(window) {
		return env
	}
	value := strconv.Itoa(window)
	for _, key := range contextWindowEnvs {
		env = setEnv(env, key, value)
	}
	return env
}

// contextWindowOverride reads a dialect's ExtraEnv entry for one capacity
// variable, following this file's convention that zero is not a capacity: an
// entry that does not parse to a usable window reports zero. present separates
// that from an absent entry, which is what diagnostics need to name the one at
// fault.
func contextWindowOverride(dialect Dialect, key string) (window int, present bool) {
	raw, present := dialect.ExtraEnv[key]
	if !present {
		return 0, false
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || !validContextWindow(parsed) {
		return 0, true
	}
	return parsed, true
}

// effectiveContextWindow returns the window the launched Claude Code process
// actually runs against.
//
// Each variable resolves on its own: claudeEnvironment writes the stored window
// to both and then applies ExtraEnv last, so an override replaces the stored
// value for that half of the declaration and leaves the other half alone. The
// stored field therefore cannot cap a pair of overrides that displaced it — it
// is not in the launched environment at all. Claude Code compacts against the
// smaller of the two chains, so the tightest resolved value is what the session
// lives within.
//
// A half that resolves to no usable window leaves the real denominator unknown,
// whether because an override is unparseable or because an uncalibrated dialect
// declared only the other variable. Reporting zero keeps the monitor from
// measuring against a number the session never received, and lets doctor say so.
func effectiveContextWindow(dialect Dialect) int {
	window := 0
	for _, key := range contextWindowEnvs {
		declared := dialect.ContextWindow
		if override, present := contextWindowOverride(dialect, key); present {
			declared = override
		}
		if !validContextWindow(declared) {
			return 0
		}
		if window == 0 || declared < window {
			window = declared
		}
	}
	return window
}

// roundTripsThroughMutations reports whether a dialect's configuration can
// survive being rebuilt by create or by a dashboard save. Neither carries
// ExtraEnv across, and neither has a flag for Bridge or AuthProvider: those
// reach a dialect only from its preset.
//
// A stored preset name is therefore not enough on its own. A dialect whose name
// still says "cursor-composer" but whose bridge or OAuth route was changed by
// hand would have that route silently replaced by the `--preset` command this
// would otherwise bless. Window calibration may resolve another exact route
// without mutating the dialect; a command cannot, so these hidden fields must
// still agree with the preset it restates.
func roundTripsThroughMutations(dialect Dialect) bool {
	if len(dialect.ExtraEnv) > 0 {
		return false
	}
	if preset, presetBacked := presets[presetRestatingRoute(dialect)]; presetBacked {
		return dialect.Bridge == preset.Bridge && dialect.AuthProvider == preset.AuthProvider
	}
	return dialect.Bridge == "" && dialect.AuthProvider == ""
}

// presetCalibratingWindow names the preset whose reviewed window is valid for a
// dialect: the label it carries while that label still describes its route, or
// — when the label has diverged — the preset whose route it matches exactly.
//
// Unlike presetSupplyingRoute, this is not used to phrase a create command, so
// resolving past a diverged label costs the dialect nothing. sharesContextRoute
// compares every route field and refuses ExtraEnv, which makes the matched
// preset's window correct by equality rather than inference.
func presetCalibratingWindow(dialect Dialect) string {
	if preset, labeled := presets[dialect.Preset]; labeled && sharesContextRoute(dialect, preset) {
		return dialect.Preset
	}
	matched := presetByContextRoute(dialect)
	if preset, ok := presets[matched]; ok && sharesContextRoute(dialect, preset) {
		return matched
	}
	return ""
}

// presetSupplyingRoute names the preset that supplies a dialect's route: the
// label it carries, or — for a dialect written before that label existed — the
// preset whose route it matches exactly.
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

// presetRestatingRoute names the preset a create command may restate a dialect
// through, which is narrower than the one supplying its route: a label this
// build does not recognize blocks the match rather than being resolved past.
//
// Such a label may name a preset a newer build owns, and `--preset` rewrites the
// name along with the route. Reading a window through the match costs that name
// nothing, because backfill leaves it untouched; printing a command would trade
// it for a route the dialect already has. So the window still reaches an
// unrecognized label, and the command does not — which is what it got before any
// of this could be resolved.
func presetRestatingRoute(dialect Dialect) string {
	if _, known := presets[dialect.Preset]; !known && dialect.Preset != "" {
		return ""
	}
	return presetSupplyingRoute(dialect)
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
	source := presetRestatingRoute(dialect)
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
	// An override is the calibration for its half of the declaration once it
	// exists, so the stored field cannot rescue it — say which entry is the
	// problem, because either variable can be the one carrying it. Every broken
	// entry is reported, so a doubly broken declaration is correctable in one
	// pass rather than one doctor run per entry.
	//
	// The consequence is phrased against the declaration rather than against
	// compaction: a broken CLAUDE_CODE_MAX_CONTEXT_TOKENS override falsifies
	// what Claude Code reports and only caps compaction indirectly, so naming
	// compaction alone would point the reader at the wrong behavior.
	var overrides []string
	for _, key := range contextWindowEnvs {
		if override, present := contextWindowOverride(dialect, key); !present || override != 0 {
			continue
		}
		overrides = append(overrides, fmt.Sprintf(
			"✗ %s overrides %s with %q, which is not a usable context window; its declared capacity is uncalibrated "+
				"(remove or correct that entry in the %q extraEnv in config.json)",
			name, key, dialect.ExtraEnv[key], name))
	}
	if len(overrides) > 0 {
		return overrides
	}
	problem := fmt.Sprintf("%s has no context window; Claude Code auto-compaction is uncalibrated for %s",
		name, dialect.Model)
	if dialect.ContextWindow != 0 {
		problem = fmt.Sprintf("%s has an invalid context window (%d); auto-compaction is uncalibrated",
			name, dialect.ContextWindow)
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

// contextWindowSupportProbe reports which capacity variables an installed
// Claude Code build no longer recognizes. A variable so tests can stub it.
var contextWindowSupportProbe = claudeMissingContextWindowVars

// contextWindowEnvConsequence describes what a build that ignores each variable
// costs. The two are not interchangeable: the auto-compact window is the only
// one that can delay compaction, while losing the context-token variable drops
// a custom model ID to Claude Code's 200,000-token default. That default skews
// every readout, and because compaction runs against the smaller of the two
// chains it also compacts any larger declared window early — so this is not a
// display-only loss.
var contextWindowEnvConsequence = map[string]string{
	autoCompactWindowEnv: "auto-compaction may be uncalibrated for custom model IDs",
	maxContextTokensEnv: "custom model IDs fall back to Claude Code's 200,000-token default, " +
		"skewing context readouts and compacting any larger declared window early",
}

// contextWindowCompatibilityDiagnostics reports a Claude Code build that no
// longer honors a variable Claude Dialects calibrates with. Dropping the
// auto-compact window silently returns every dialect to the uncalibrated
// behavior issue #44 fixes; dropping the context-token variable silently
// returns the declared window to Claude Code's default, which both falsifies
// the reported percentage and compacts a larger window early. They are separate
// losses, so each gets its own line.
//
// A build that cannot be inspected proves nothing either way, so it produces no
// diagnostic: a false alarm about a working installation is worse than silence.
func contextWindowCompatibilityDiagnostics(claudePath string) []string {
	missing, err := contextWindowSupportProbe(claudePath)
	if err != nil || len(missing) == 0 {
		return nil
	}
	lines := make([]string, 0, len(missing))
	for _, key := range missing {
		lines = append(lines, fmt.Sprintf(
			"✗ Claude Code at %s does not reference %s; %s "+
				"(see https://github.com/anthropics/claude-code/issues/43989)",
			claudePath, key, contextWindowEnvConsequence[key]))
	}
	return lines
}

// contextWindowScanChunk is the read size used when scanning the Claude Code
// executable. Chunks overlap by contextWindowScanOverlap so a match straddling a
// boundary is still found.
const contextWindowScanChunk = 1 << 20

// contextWindowScanLimit bounds the scan so an unexpectedly large or endless
// file cannot stall doctor.
const contextWindowScanLimit = 512 << 20

// contextWindowScanOverlap is how much of each chunk has to be carried into the
// next read so a literal straddling the boundary is still found: one byte short
// of the longest name being searched for.
func contextWindowScanOverlap() int {
	longest := 0
	for _, name := range contextWindowEnvs {
		if len(name) > longest {
			longest = len(name)
		}
	}
	return longest - 1
}

// claudeMissingContextWindowVars scans the resolved Claude Code executable for
// the capacity variable names, reporting the ones it does not contain. Claude
// Code exposes no API for asking which environment variables it honors, so the
// embedded literals are the only available signal; symlinks are resolved first
// so an npm-style launcher shim is inspected as the script it points at rather
// than as the link. All names are searched in a single pass, because the file is
// large enough that a pass per variable would be felt.
func claudeMissingContextWindowVars(claudePath string) ([]string, error) {
	resolved, err := filepath.EvalSymlinks(claudePath)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	needles := make(map[string][]byte, len(contextWindowEnvs))
	for _, name := range contextWindowEnvs {
		needles[name] = []byte(name)
	}
	found := make(map[string]bool, len(contextWindowEnvs))
	overlap := contextWindowScanOverlap()
	buffer := make([]byte, contextWindowScanChunk+overlap)
	filled := 0
	scanned := 0
	for scanned < contextWindowScanLimit {
		read, readErr := io.ReadFull(file, buffer[filled:])
		filled += read
		scanned += read
		for name, needle := range needles {
			if !found[name] && bytes.Contains(buffer[:filled], needle) {
				found[name] = true
			}
		}
		if len(found) == len(contextWindowEnvs) {
			return nil, nil
		}
		if readErr != nil {
			// io.EOF or io.ErrUnexpectedEOF simply means the file ended; any
			// other error means the verdict would be unreliable.
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				break
			}
			return nil, readErr
		}
		// Carry the tail forward so the next chunk can complete a match that
		// began at the end of this one.
		copy(buffer, buffer[filled-overlap:filled])
		filled = overlap
	}
	missing := make([]string, 0, len(contextWindowEnvs))
	for _, name := range contextWindowEnvs {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	return missing, nil
}
