package app

import (
	"fmt"
	"strings"
)

// presetDriftDiagnostics reports when a dialect no longer matches the preset
// it claims, so a preset revision that arrived with `cc-dialect upgrade`
// reaches existing dialects instead of applying to newly created ones only.
// Presets are copied into a dialect at create time, so without this check a
// dialect two revisions behind reports as healthy while launching models the
// preset no longer declares.
//
// The binary carries only today's presets, so "matches an older revision of
// the preset" and "the user edited the dialect" look identical on disk. The
// fingerprint stamped at create time is what separates them, and the report
// claims only what the evidence supports:
//
//   - A dialect whose route and window still equal the labeled preset — with
//     or without a stamp — is current and stays silent.
//   - A dialect matching no preset exactly but stamped and untouched since
//     creation was not edited, so a differing preset can only be a revision:
//     that is the one case reported as drift, with a command.
//   - A route that exactly matches some other preset is current under a stale
//     label (the label predates the preset it really is); it is noted as
//     information, never handed the stored label's command, because `create
//     --preset` with that label would replace a route the dialect already has.
//     The matched preset's window is compared too: a route match whose window
//     is behind is the same window-only revision, judged against the preset
//     the dialect actually runs rather than hidden behind the note.
//   - Everything else — no stamp, or a stamp the dialect has since moved off
//     of — cannot be judged, so it is reported as a possibility rather than a
//     finding, and only offered a command when the dialect round-trips
//     through create without losing state.
//
// A label this build does not recognize, or no label at all, stays silent: an
// unrecognized label may belong to a newer build, and an unlabeled dialect
// makes no claim to judge. ExtraEnv keeps its dialect reportable but refuses
// the command, since create would drop it.
//
// Reporting is all doctor does: `--fix` never rewrites a model or a window,
// because nothing distinguishes a value the user chose from one the old
// preset supplied — the same contract the context-window repair keeps.
func presetDriftDiagnostics(name string, dialect Dialect) []string {
	label := dialect.Preset
	if label == "" {
		return nil
	}
	preset, known := presets[label]
	if !known {
		return nil
	}
	// Equality through the fingerprint rather than sharesContextRoute alone:
	// a revision may move only the window, and an ExtraEnv dialect whose
	// stored route matches must not be reported for state create cannot even
	// see.
	if presetFingerprint(dialect) == presetFingerprint(preset) {
		return nil
	}
	// Untouched since creation: the stamp still describes the dialect's own
	// route and window. Route drift is then provably the preset's movement —
	// nothing else writes those fields without clearing the stamp or leaving
	// the dialect a copy of the preset it was restated through.
	untouched := dialect.PresetFingerprint != "" &&
		dialect.PresetFingerprint == presetFingerprint(dialect)
	if sharesContextRoute(dialect, preset) {
		// Only the window differs. A stored window is deliberately never
		// raised on the dialect's behalf, so an untouched stamp — whose window
		// came from the preset and was never overridden — is the one case
		// where reporting the difference is a finding rather than noise about
		// a value the user set on purpose.
		if untouched {
			return []string{presetDriftLine(name, dialect, preset)}
		}
		return nil
	}
	if matched := presetByContextRoute(dialect); matched != "" {
		routePreset := presets[matched]
		if dialect.ContextWindow == routePreset.ContextWindow {
			return []string{fmt.Sprintf("○ %s already matches the current %s preset but is labeled %s",
				name, matched, label)}
		}
		// presetByContextRoute compares routes, so a window left behind by a
		// revision of the matched preset would otherwise hide behind a note
		// claiming a match the capacity does not share. The matched preset is
		// the one the dialect actually runs, so its window is the comparison
		// that means anything here. The route fields cannot differ from it, so
		// the changes below name the window alone.
		changes := strings.Join(presetDriftChanges(dialect, routePreset), ", ")
		if untouched {
			line := fmt.Sprintf("✗ %s matches the current %s route but its window is from an older revision (%s)",
				name, matched, changes)
			if command := presetDriftCommand(name, dialect, routePreset, matched); command != "" {
				return []string{line + " (run: " + command + ")"}
			}
			return []string{line + "; this dialect holds settings that cc-dialect create would drop"}
		}
		line := fmt.Sprintf("○ %s matches the current %s route but is labeled %s and its window differs (%s); this may be an older preset revision or your own customization",
			name, matched, label, changes)
		if command := presetDriftCommand(name, dialect, routePreset, matched); command != "" {
			return []string{line + " (run: " + command + ")"}
		}
		return []string{line + ", and it holds settings that cc-dialect create would drop"}
	}
	if untouched {
		return []string{presetDriftLine(name, dialect, preset)}
	}
	return []string{presetDriftHedgeLine(name, dialect, preset)}
}

// presetDriftLine phrases a confirmed revision: the dialect was created from
// the preset and never edited, so the differences named are the revision's,
// and the command rebuilds the dialect on today's preset.
func presetDriftLine(name string, dialect, preset Dialect) string {
	line := fmt.Sprintf("✗ %s was created from an older %s preset (%s)",
		name, dialect.Preset, strings.Join(presetDriftChanges(dialect, preset), ", "))
	if command := presetDriftCommand(name, dialect, preset, dialect.Preset); command != "" {
		return line + " (run: " + command + ")"
	}
	return line + "; this dialect holds settings that cc-dialect create would drop"
}

// presetDriftHedgeLine phrases a divergence whose cause the binary cannot
// know: the dialect may predate fingerprints or have been edited since, so it
// may be an older revision or the user's own customization. It names the same
// differences the confirmed line would, because the reader needs them to
// judge before running a command that rebuilds the dialect either way.
func presetDriftHedgeLine(name string, dialect, preset Dialect) string {
	line := fmt.Sprintf("○ %s differs from the current %s preset (%s); this may be an older preset revision or your own customization",
		name, dialect.Preset, strings.Join(presetDriftChanges(dialect, preset), ", "))
	if command := presetDriftCommand(name, dialect, preset, dialect.Preset); command != "" {
		return line + " (run: " + command + ")"
	}
	return line + ", and it holds settings that cc-dialect create would drop"
}

// presetDriftChanges names every field that differs between a dialect and the
// preset it claims. A bare "is outdated" gives the reader nothing to judge
// before running a command that replaces the route.
func presetDriftChanges(dialect, preset Dialect) []string {
	var changes []string
	for _, field := range []struct{ label, from, to string }{
		{"model", dialect.Model, preset.Model},
		{"subagent", dialect.SubagentModel, preset.SubagentModel},
		{"opus", dialect.OpusModel, preset.OpusModel},
		{"sonnet", dialect.SonnetModel, preset.SonnetModel},
		{"haiku", dialect.HaikuModel, preset.HaikuModel},
		{"auth provider", dialect.AuthProvider, preset.AuthProvider},
		{"bridge", dialect.Bridge, preset.Bridge},
		{"base URL", dialect.BaseURL, preset.BaseURL},
		{"token env", dialect.AuthTokenEnv, preset.AuthTokenEnv},
	} {
		if field.from != field.to {
			changes = append(changes, fmt.Sprintf("%s %s → %s", field.label,
				presetDriftValue(field.from), presetDriftValue(field.to)))
		}
	}
	if dialect.ContextWindow != preset.ContextWindow {
		from := fmt.Sprintf("%d", dialect.ContextWindow)
		if dialect.ContextWindow == 0 {
			from = "none"
		}
		changes = append(changes, fmt.Sprintf("window %s → %d", from, preset.ContextWindow))
	}
	return changes
}

// presetDriftValue renders a field value for a change list, naming an empty
// side rather than printing an invisible one.
func presetDriftValue(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// presetDriftCommand renders the upsert that adopts the current preset — the
// one whose route the report measured the dialect against, which for a stale
// label is the preset it actually runs rather than the stored label's. The
// stamp guarantees the dialect still runs that route unchanged, so `--preset`
// alone restores the whole revised route. A window the preset has since
// revised is restated explicitly with `--context-window`: inheritedContextWindow
// deliberately keeps a stored window while the route is unchanged, so the
// preset flag by itself would re-stamp the dialect as current while leaving
// the old capacity behind — the exact drift this report exists to surface.
//
// create resets effort, tool search, concurrency, and effort level to the
// preset's values, so any the dialect changed is restated as a flag —
// otherwise adopting the models would silently change behavior. Those fields
// carry no route, which is why they neither clear the stamp nor enter it.
//
// A dialect whose state the target preset cannot restate — ExtraEnv, which no
// flag carries, or a bridge or auth provider the preset does not supply —
// gets no command at all, matching what contextWindowFixCommand refuses for
// the same dialects: a printed command that would break the dialect is worse
// than none. The comparison runs against the preset the command names, not
// the stored label: the exact-route report adopts the preset the dialect
// actually runs, and a stale foreign label's hidden fields would otherwise
// veto a command whose target reproduces the dialect's own.
func presetDriftCommand(name string, dialect, preset Dialect, presetName string) string {
	if len(dialect.ExtraEnv) > 0 {
		return ""
	}
	if dialect.Bridge != preset.Bridge || dialect.AuthProvider != preset.AuthProvider {
		return ""
	}
	command := "cc-dialect create " + shellArg(name) + " --preset " + shellArg(presetName)
	if dialect.ContextWindow != preset.ContextWindow {
		command += fmt.Sprintf(" --context-window %d", preset.ContextWindow)
	}
	if dialect.EffortLevel != "" && dialect.EffortLevel != preset.EffortLevel {
		command += " --effort-level " + shellArg(dialect.EffortLevel)
	}
	if dialect.Concurrency != 0 && dialect.Concurrency != preset.Concurrency {
		command += fmt.Sprintf(" --concurrency %d", dialect.Concurrency)
	}
	if !dialect.Effort {
		command += " --effort=false"
	}
	if dialect.ToolSearch {
		command += " --tool-search=true"
	}
	return command
}
