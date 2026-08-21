package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLegacyConfig lays down a configuration file in the pre-contextWindow
// shape so migration is exercised against real on-disk JSON rather than an
// in-memory struct that already knows about the field.
func writeLegacyConfig(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A dialect whose stored mapping still matches its preset is unambiguous, so it
// can adopt that preset's reviewed capacity.
func TestBackfillFillsPresetBackedDialects(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "preset": "codex-sol",
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "effort": true,
          "effortLevel": "auto",
          "concurrency": 3,
          "toolSearch": false,
          "port": 43170,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-codex"].ContextWindow; got != 372000 {
		t.Fatalf("backfilled context window = %d, want 372000", got)
	}
}

// The preset field is younger than the dialects that need migrating: one
// created before it existed stores no label at all, yet is field-for-field the
// preset it was made from. Equality is what makes the reviewed window valid, so
// the missing label must not withhold it — and the label is recorded too, so the
// configuration stops lagging behind the match.
func TestBackfillFillsUnlabeledDialectsIdenticalToAPreset(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "effort": true,
          "effortLevel": "auto",
          "concurrency": 3,
          "toolSearch": false,
          "port": 43170,
          "apiKey": "local-secret"
        },
        "cc-glm": {
          "model": "glm-5.3",
          "subagentModel": "glm-5.3",
          "opusModel": "glm-5.3",
          "sonnetModel": "glm-5-turbo",
          "haikuModel": "glm-4.7",
          "baseUrl": "https://api.z.ai/api/anthropic",
          "authTokenEnv": "ZAI_API_KEY",
          "effort": true,
          "effortLevel": "auto",
          "concurrency": 3,
          "toolSearch": false,
          "port": 43173,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]struct {
		window int
		preset string
	}{
		"cc-codex": {372000, "codex-sol"},
		"cc-glm":   {200000, "glm"},
	} {
		if got := cfg.Dialects[name].ContextWindow; got != want.window {
			t.Errorf("%s context window = %d, want %d", name, got, want.window)
		}
		if got := cfg.Dialects[name].Preset; got != want.preset {
			t.Errorf("%s preset = %q, want the matched %q recorded", name, got, want.preset)
		}
	}
}

// Resolving an unlabeled dialect works only while presets describe distinct
// routes: two presets sharing one makes both ambiguous, and every unlabeled
// dialect on that route silently stops migrating. Nothing else would report
// that — the dialects just stay uncalibrated — so adding a preset that
// duplicates an existing route has to fail the build instead.
func TestEveryPresetIsDistinguishableByRoute(t *testing.T) {
	for name := range presets {
		if got := presetByContextRoute(presets[name]); got != name {
			t.Errorf("preset %s resolves to %q; another preset now shares its route, "+
				"so dialects written before the preset field can no longer be matched to either", name, got)
		}
	}
}

// Matching an unlabeled dialect is equality, not resemblance. One tier pointed
// somewhere else may hold less than the preset's smallest model, which is
// exactly the denominator that must never be inflated.
func TestBackfillLeavesUnlabeledDialectsWithAChangedTierUnknown(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.3-codex-spark",
          "authProvider": "codex",
          "port": 43170,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-codex"].ContextWindow; got != 0 {
		t.Fatalf("a re-pointed tier backfilled to %d, want 0 (unknown)", got)
	}
	if got := cfg.Dialects["cc-codex"].Preset; got != "" {
		t.Fatalf("a re-pointed dialect was labeled %q", got)
	}
}

// Were two presets ever to describe one route, the window an unlabeled dialect
// adopted would depend on map iteration order. Silence is the honest answer.
func TestBackfillLeavesAmbiguousRoutesUnknown(t *testing.T) {
	twin := presets["codex-sol"]
	twin.ContextWindow = 100000
	presets["codex-sol-twin"] = twin
	t.Cleanup(func() { delete(presets, "codex-sol-twin") })

	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "port": 43170,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-codex"].ContextWindow; got != 0 {
		t.Fatalf("an ambiguous route backfilled to %d, want 0 (unknown)", got)
	}
}

// A dialect that names a preset but has been re-pointed at other models is no
// longer unambiguous, so guessing its capacity could hand Claude Code a
// denominator larger than the route really supports.
func TestBackfillLeavesModifiedPresetMappingsUnknown(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "preset": "codex-sol",
          "model": "gpt-5.3-codex-spark",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "port": 43170,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-codex"].ContextWindow; got != 0 {
		t.Fatalf("modified mapping backfilled to %d, want 0 (unknown)", got)
	}
}

// A genuinely custom dialect resembles a preset without being one — here the
// same models reached over no OAuth route at all — so nothing may be inferred
// from the model IDs it happens to share.
func TestBackfillLeavesCustomDialectsUnknown(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-custom": {
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "port": 43170,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-custom"].ContextWindow; got != 0 {
		t.Fatalf("custom dialect backfilled to %d, want 0 (unknown)", got)
	}
}

// Migration must never overwrite a capacity the operator measured themselves.
func TestBackfillPreservesAnExplicitContextWindow(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "preset": "codex-sol",
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "contextWindow": 128000,
          "port": 43170,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-codex"].ContextWindow; got != 128000 {
		t.Fatalf("explicit context window became %d, want the stored 128000", got)
	}
}

// An unknown preset name (a dialect created by a newer build, then opened by an
// older one) has no reviewed value to adopt.
func TestBackfillIgnoresUnknownPresets(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-future": {
          "preset": "not-a-preset",
          "model": "future-model",
          "port": 43170,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-future"].ContextWindow; got != 0 {
		t.Fatalf("unknown preset backfilled to %d, want 0", got)
	}
}

// A label this build does not recognize withholds no capacity. The dialect
// still selects exactly a known preset's models over exactly its upstream, and
// the reviewed window describes those models whatever the configuration calls
// them — refusing it would leave the dialect uncalibrated over a name. The name
// itself is what must survive: it may belong to a newer build, so it is neither
// overwritten here nor rewritten by the command doctor offers.
func TestBackfillCalibratesAnUnrecognizedLabelWithoutRenamingIt(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "preset": "codex-sol-v2",
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "port": 43170,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Dialects["cc-codex"].ContextWindow; got != 372000 {
		t.Errorf("context window = %d, want the route's reviewed 372000", got)
	}
	if got := cfg.Dialects["cc-codex"].Preset; got != "codex-sol-v2" {
		t.Errorf("preset = %q, want the stored \"codex-sol-v2\" left untouched", got)
	}
}

// The migrated value must survive to disk with the existing private permissions
// and without disturbing the dialect's credentials or ports.
func TestBackfillPersistsAtomicallyWithPrivatePermissions(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-kimi": {
          "preset": "kimi",
          "model": "kimi-k3",
          "subagentModel": "kimi-k3",
          "opusModel": "kimi-k3",
          "sonnetModel": "kimi-k2.7-code-highspeed",
          "haikuModel": "kimi-k2.6",
          "authProvider": "kimi",
          "port": 43175,
          "apiKey": "local-secret"
        }
      }
    }`)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err = saveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	_, path, _, _, _, _, _, err := paths("")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, `"contextWindow": 262144`) {
		t.Fatalf("persisted config is missing the backfilled window:\n%s", body)
	}
	if !strings.Contains(body, `"apiKey": "local-secret"`) || !strings.Contains(body, `"port": 43175`) {
		t.Fatalf("migration disturbed private state:\n%s", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
}

// `doctor --fix` records the migrated capacity on disk so the configuration
// file stops lagging behind the value already used at launch. The repair stays
// deterministic: only unambiguous preset-backed dialects are written.
func TestPersistContextWindowBackfillWritesOnlyUnambiguousDialects(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "preset": "codex-sol",
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "port": 43170,
          "apiKey": "codex-secret"
        },
        "cc-tweaked": {
          "preset": "codex-sol",
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-sol",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "port": 43171,
          "apiKey": "tweaked-secret"
        },
        "cc-measured": {
          "preset": "kimi",
          "model": "kimi-k3",
          "subagentModel": "kimi-k3",
          "opusModel": "kimi-k3",
          "sonnetModel": "kimi-k2.7-code-highspeed",
          "haikuModel": "kimi-k2.6",
          "authProvider": "kimi",
          "contextWindow": 100000,
          "port": 43172,
          "apiKey": "kimi-secret"
        }
      }
    }`)

	migrated, err := persistContextWindowBackfill()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated) != 1 || migrated[0] != "cc-codex" {
		t.Fatalf("migrated = %v, want only [cc-codex]", migrated)
	}

	_, path, _, _, _, _, _, err := paths("")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Dialects map[string]struct {
			ContextWindow int    `json:"contextWindow"`
			APIKey        string `json:"apiKey"`
			Port          int    `json:"port"`
		} `json:"dialects"`
	}
	if err = json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if got := stored.Dialects["cc-codex"].ContextWindow; got != 372000 {
		t.Errorf("cc-codex context window = %d, want 372000 written to disk", got)
	}
	if got := stored.Dialects["cc-tweaked"].ContextWindow; got != 0 {
		t.Errorf("cc-tweaked context window = %d, want it left unwritten", got)
	}
	if got := stored.Dialects["cc-measured"].ContextWindow; got != 100000 {
		t.Errorf("cc-measured context window = %d, want the operator's 100000 preserved", got)
	}
	for name, want := range map[string]string{"cc-codex": "codex-secret", "cc-tweaked": "tweaked-secret", "cc-measured": "kimi-secret"} {
		if got := stored.Dialects[name].APIKey; got != want {
			t.Errorf("%s api key = %q, want the preserved %q", name, got, want)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
}

// A recognized label can diverge onto another preset's exact route. Doctor may
// calibrate that route without renaming the dialect, because reading a window
// costs the stored label nothing while a create command would rewrite it.
func TestPersistContextWindowBackfillCalibratesExactRouteWithoutChangingDivergedPresetLabel(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-cursor-mix": {
          "preset": "cursor-composer",
          "model": "composer-2.5",
          "subagentModel": "composer-2.5",
          "opusModel": "composer-2.5",
          "sonnetModel": "grok-4.5",
          "haikuModel": "kimi-k3",
          "bridge": "cursor",
          "authTokenEnv": "CURSOR_API_KEY",
          "port": 43170,
          "bridgePort": 43171,
          "apiKey": "local-secret"
        }
      }
    }`)

	migrated, err := persistContextWindowBackfill()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated) != 1 || migrated[0] != "cc-cursor-mix" {
		t.Fatalf("migrated = %v, want only [cc-cursor-mix]", migrated)
	}

	_, path, _, _, _, _, _, err := paths("")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Dialects map[string]struct {
			Preset        string `json:"preset"`
			ContextWindow int    `json:"contextWindow"`
		} `json:"dialects"`
	}
	if err = json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if got := stored.Dialects["cc-cursor-mix"].ContextWindow; got != 200000 {
		t.Errorf("stored context window = %d, want the exact cursor-mix route's 200000", got)
	}
	if got := stored.Dialects["cc-cursor-mix"].Preset; got != "cursor-composer" {
		t.Errorf("stored preset = %q, want the diverged cursor-composer label preserved", got)
	}
}

// The label a route match resolves has to reach disk alongside the window it
// justified. It is what a later create, doctor remedy, or dashboard save reads
// to restate the dialect's OAuth route in one flag, so leaving it in memory
// would keep the file describing a dialect the tool no longer treats as custom.
func TestPersistContextWindowBackfillRecordsTheResolvedPreset(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "port": 43170,
          "apiKey": "local-secret"
        }
      }
    }`)

	migrated, err := persistContextWindowBackfill()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated) != 1 || migrated[0] != "cc-codex" {
		t.Fatalf("migrated = %v, want only [cc-codex]", migrated)
	}

	_, path, _, _, _, _, _, err := paths("")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Dialects map[string]struct {
			Preset        string `json:"preset"`
			ContextWindow int    `json:"contextWindow"`
		} `json:"dialects"`
	}
	if err = json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if got := stored.Dialects["cc-codex"].Preset; got != "codex-sol" {
		t.Errorf("stored preset = %q, want the matched \"codex-sol\" written to disk", got)
	}
	if got := stored.Dialects["cc-codex"].ContextWindow; got != 372000 {
		t.Errorf("stored context window = %d, want 372000", got)
	}
}

// Repeating the repair must be a no-op rather than rewriting the configuration
// on every doctor run.
func TestPersistContextWindowBackfillIsIdempotent(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-codex": {
          "preset": "codex-sol",
          "model": "gpt-5.6-sol",
          "subagentModel": "gpt-5.6-sol",
          "opusModel": "gpt-5.6-sol",
          "sonnetModel": "gpt-5.6-terra",
          "haikuModel": "gpt-5.6-luna",
          "authProvider": "codex",
          "port": 43170,
          "apiKey": "codex-secret"
        }
      }
    }`)

	if migrated, err := persistContextWindowBackfill(); err != nil || len(migrated) != 1 {
		t.Fatalf("first run: migrated=%v err=%v", migrated, err)
	}
	_, path, _, _, _, _, _, err := paths("")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	migrated, err := persistContextWindowBackfill()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated) != 0 {
		t.Fatalf("second run migrated %v, want nothing left to repair", migrated)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("second run rewrote an already-migrated configuration")
	}
}

// Nothing to repair must not create a configuration file for a user who has no
// dialects yet.
func TestPersistContextWindowBackfillIsQuietWithNothingToRepair(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())

	migrated, err := persistContextWindowBackfill()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated) != 0 {
		t.Fatalf("migrated = %v, want nothing", migrated)
	}
	_, path, _, _, _, _, _, err := paths("")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("repair created a configuration file where none existed")
	}
}

// The revision hash is computed from the normalized configuration, so a
// migrated dialect must not make the revision flip between reads.
func TestBackfillKeepsTheConfigRevisionStable(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-grok": {
          "preset": "grok",
          "model": "grok-4.6",
          "subagentModel": "grok-4.6",
          "opusModel": "grok-4.6",
          "sonnetModel": "grok-4.6",
          "haikuModel": "grok-4.6",
          "authProvider": "xai",
          "port": 43171,
          "apiKey": "local-secret"
        }
      }
    }`)

	first, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	firstRevision, err := configRevision(first)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveConfig(first); err != nil {
		t.Fatal(err)
	}
	second, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	secondRevision, err := configRevision(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstRevision != secondRevision {
		t.Fatalf("revision changed across the migrating write: %s != %s", firstRevision, secondRevision)
	}
}
