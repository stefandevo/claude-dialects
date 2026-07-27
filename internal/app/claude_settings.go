package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// claudeSettingsRel is the instance-relative path of the settings.json Claude
// Code reads for a dialect, which runs with CLAUDE_CONFIG_DIR pointed at
// <instance>/claude.
var claudeSettingsRel = filepath.Join("claude", "settings.json")

// claudeSettingsPath returns the absolute path of a dialect's settings.json.
// File I/O goes through the dialect's own root instead; this is for user-facing
// messages and for the statusLine command Claude Code executes.
func claudeSettingsPath(name string) (string, error) {
	claudeDir, err := claudeConfigDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(claudeDir, "settings.json"), nil
}

// readClaudeSettings decodes a dialect's settings.json through its own root,
// reporting whether the file exists. settingsPath is used for error messages
// only.
//
// A file Claude Code cannot have written is an unusable shape rather than an
// empty one, and callers must leave it alone instead of clobbering it: malformed
// JSON is reported, and so is a literal `null`, which unmarshals into a nil map
// without error and would otherwise panic on the first assignment.
func readClaudeSettings(instance *instanceFS, settingsPath string) (map[string]any, bool, error) {
	data, readErr := instance.ReadFile(claudeSettingsRel)
	if errors.Is(readErr, os.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if readErr != nil {
		return nil, false, readErr
	}
	settings := map[string]any{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", settingsPath, err)
	}
	if settings == nil {
		return nil, true, fmt.Errorf("parse %s: settings must be a JSON object", settingsPath)
	}
	return settings, true, nil
}

// attributionSettled reports whether the settings already carry a commit
// attribution preference: the current `attribution` object, or the deprecated
// `includeCoAuthoredBy` boolean Claude Code still honours. Either one present is
// an answer already given, which seeding must not override.
func attributionSettled(settings map[string]any) bool {
	_, current := settings["attribution"]
	_, deprecated := settings["includeCoAuthoredBy"]
	return current || deprecated
}

// seedAttribution turns off Claude Code's commit and PR attribution for a
// dialect that has not expressed a preference of its own. Dialects run with
// CLAUDE_CONFIG_DIR pointed at their isolated config directory, so a user who
// disabled the co-author trailer in ~/.claude/settings.json is not covered —
// the opt-out has to be seeded per dialect.
//
// Unlike the statusline, where deleting the key is the only way to opt out and a
// script-presence sentinel is needed to tell "removed" from "never seeded",
// attribution has an explicit opt-in value: restoring the trailer means writing
// it, not deleting the key. That keeps the rule stateless — seed whenever
// neither key is present — so it is self-healing across upgrades, with no marker
// file and no interrupted-seed edge case.
func seedAttribution(name string) error {
	if !validName(name) {
		return operationError(ErrorInvalidInput, "invalid dialect name %q", name)
	}
	settingsPath, err := claudeSettingsPath(name)
	if err != nil {
		return err
	}
	// All file I/O goes through this dialect's own os.Root, so neither a symlink
	// out of the tree nor one pointing at a sibling dialect is followed.
	instance, err := openInstanceFS(name)
	if err != nil {
		return err
	}
	defer instance.Close()
	settings, _, err := readClaudeSettings(instance, settingsPath)
	if err != nil {
		return err
	}
	if attributionSettled(settings) {
		return nil
	}
	// The schema documents commit/pr as the attribution text rather than a
	// toggle: an empty string is what hides it.
	settings["attribution"] = map[string]any{"commit": "", "pr": ""}
	merged, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return instance.AtomicWrite(claudeSettingsRel, append(merged, '\n'), 0o600)
}

// attributionDiagnostic reports a dialect that still leaves Claude Code's commit
// attribution enabled, or "" when the dialect has already answered. Settings
// that cannot be read or parsed yield no line either: seeding would refuse them
// too, so there is no fix to point at.
func attributionDiagnostic(name string) string {
	settingsPath, err := claudeSettingsPath(name)
	if err != nil {
		return ""
	}
	instance, err := openInstanceFS(name)
	if err != nil {
		return ""
	}
	defer instance.Close()
	settings, _, err := readClaudeSettings(instance, settingsPath)
	if err != nil || attributionSettled(settings) {
		return ""
	}
	return fmt.Sprintf("✗ %s does not disable Claude commit attribution (run: cc-dialect doctor --fix)", name)
}

// warnAttributionSeed reports a failed attribution seed without failing the
// surrounding operation — the dialect is fully usable without the key.
func warnAttributionSeed(name string, err error) {
	fmt.Fprintf(os.Stderr, "Warning: commit attribution for %q not seeded: %v\n", name, err)
}
