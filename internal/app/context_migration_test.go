package app

import (
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

// A genuinely custom dialect names no preset, so nothing may be inferred from
// its model ID.
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

// The revision hash is computed from the normalized configuration, so a
// migrated dialect must not make the revision flip between reads.
func TestBackfillKeepsTheConfigRevisionStable(t *testing.T) {
	writeLegacyConfig(t, `{
      "version": 2,
      "basePort": 43170,
      "dialects": {
        "cc-grok": {
          "preset": "grok",
          "model": "grok-4.5",
          "subagentModel": "grok-4.5",
          "opusModel": "grok-4.5",
          "sonnetModel": "grok-4.5",
          "haikuModel": "grok-4.5",
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
