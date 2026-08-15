package app

import (
	"strings"
	"testing"
)

// A preset launch must be deterministic: whatever the parent shell exported for
// either capacity variable, the dialect's own capacity is what Claude Code sees.
func TestClaudeEnvironmentOverridesInheritedContextWindow(t *testing.T) {
	dialect := presets["codex-sol"]
	dialect.Port = 43170
	dialect.APIKey = "local-secret"

	env := claudeEnvironment([]string{
		"PATH=/usr/bin", autoCompactWindowEnv + "=8000", maxContextTokensEnv + "=8000",
	}, "/tmp/claude", dialect)

	for _, key := range contextWindowEnvs {
		if count := countEnv(env, key); count != 1 {
			t.Fatalf("%s appears %d times, want exactly 1", key, count)
		}
		if value := lookupEnv(env, key); value != "372000" {
			t.Fatalf("%s = %q, want the codex-sol capacity %q", key, value, "372000")
		}
	}
}

// Every preset must hand Claude Code a denominator through both chains,
// otherwise the route it cannot recognize is left uncalibrated exactly as issue
// #44 describes, or reports its fill level against the 200,000-token default.
func TestClaudeEnvironmentSetsBothCapacityVariablesForEveryPreset(t *testing.T) {
	for _, name := range presetNames() {
		dialect := presets[name]
		dialect.Port = 43170
		dialect.APIKey = "local-secret"
		env := claudeEnvironment(nil, "/tmp/claude", dialect)
		for _, key := range contextWindowEnvs {
			if value := lookupEnv(env, key); value == "" {
				t.Errorf("preset %q launches without %s", name, key)
			}
		}
	}
}

// An unknown capacity cannot be improved on, so an ambient value the user
// exported themselves is preserved rather than dropped.
func TestClaudeEnvironmentKeepsAmbientWindowForUnknownCapacity(t *testing.T) {
	dialect := Dialect{Model: "custom-model", Port: 43170, APIKey: "local-secret"}

	env := claudeEnvironment([]string{
		autoCompactWindowEnv + "=250000", maxContextTokensEnv + "=250000",
	}, "/tmp/claude", dialect)

	for _, key := range contextWindowEnvs {
		if value := lookupEnv(env, key); value != "250000" {
			t.Fatalf("%s = %q, want the inherited %q", key, value, "250000")
		}
	}
}

// ExtraEnv is explicit per-dialect configuration, so it stays the last word for
// each variable independently.
func TestClaudeEnvironmentLetsExtraEnvOverrideEitherWindowVariable(t *testing.T) {
	for _, overridden := range contextWindowEnvs {
		dialect := presets["codex-sol"]
		dialect.Port = 43170
		dialect.APIKey = "local-secret"
		dialect.ExtraEnv = map[string]string{overridden: "123456"}

		env := claudeEnvironment(nil, "/tmp/claude", dialect)

		if value := lookupEnv(env, overridden); value != "123456" {
			t.Errorf("%s = %q, want the explicit extraEnv value %q", overridden, value, "123456")
		}
		if count := countEnv(env, overridden); count != 1 {
			t.Errorf("%s appears %d times, want exactly 1", overridden, count)
		}
		for _, other := range contextWindowEnvs {
			if other == overridden {
				continue
			}
			if value := lookupEnv(env, other); value != "372000" {
				t.Errorf("%s = %q, want the untouched codex-sol capacity %q", other, value, "372000")
			}
		}
	}
}

// The reproduced session sent 368,812 effective input tokens into a
// 372,000-token window with no compaction event. With the window declared,
// Claude Code has the denominator it needs to compact before exhaustion.
func TestCodexSolLaunchDeclaresTheWindowTheReproducedSessionLacked(t *testing.T) {
	const effectiveInput = 15020 + 353792

	dialect := presets["codex-sol"]
	dialect.Port = 43170
	dialect.APIKey = "local-secret"

	env := claudeEnvironment(nil, "/tmp/claude", dialect)
	for _, key := range contextWindowEnvs {
		if value := lookupEnv(env, key); value != "372000" {
			t.Fatalf("%s = %q, want %q", key, value, "372000")
		}
	}
	if percent := float64(effectiveInput) / float64(dialect.ContextWindow) * 100; percent < 99 || percent > 100 {
		t.Fatalf("reproduced fill = %.1f%%, want the reported 99.1%% of the configured window", percent)
	}
}

func TestClaudeEnvironmentKeepsExistingRoutingVariables(t *testing.T) {
	dialect := presets["kimi"]
	dialect.Port = 43171
	dialect.APIKey = "local-secret"

	env := claudeEnvironment(nil, "/tmp/claude-config", dialect)

	for key, want := range map[string]string{
		"CLAUDE_CONFIG_DIR":            "/tmp/claude-config",
		"ANTHROPIC_BASE_URL":           "http://127.0.0.1:43171",
		"ANTHROPIC_AUTH_TOKEN":         "local-secret",
		"ANTHROPIC_MODEL":              "kimi-k3",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "kimi-k3",
		"CLAUDE_CODE_SUBAGENT_MODEL":   "kimi-k3",
		autoCompactWindowEnv:           "262144",
		maxContextTokensEnv:            "262144",
	} {
		if value := lookupEnv(env, key); value != want {
			t.Errorf("%s = %q, want %q", key, value, want)
		}
	}
	for _, item := range env {
		if strings.HasPrefix(item, "ANTHROPIC_API_KEY=") {
			t.Error("launch environment must not define ANTHROPIC_API_KEY")
		}
	}
}

func TestShellAliasProbeOrderPrefersLoginShell(t *testing.T) {
	order := shellAliasProbeOrder("/bin/bash")
	if len(order) != 2 || order[0].shell != "bash" || order[1].shell != "zsh" {
		t.Fatalf("bash login shell should be probed first, got %#v", order)
	}

	order = shellAliasProbeOrder("/opt/homebrew/bin/zsh")
	if len(order) != 2 || order[0].shell != "zsh" || order[1].shell != "bash" {
		t.Fatalf("zsh login shell should be probed first, got %#v", order)
	}

	order = shellAliasProbeOrder("/usr/bin/fish")
	if len(order) != 2 || order[0].shell != "zsh" || order[1].shell != "bash" {
		t.Fatalf("unknown login shell should keep the default order, got %#v", order)
	}
}

func TestLookupShellAliasFallsBackToTheSecondShell(t *testing.T) {
	var probed []string
	alias, found := lookupShellAlias("cc-x", shellAliasProbeOrder("/bin/zsh"),
		func(shell string) map[string]string {
			probed = append(probed, shell)
			if shell == "zsh" {
				return map[string]string{"other": "other=nope"}
			}
			return map[string]string{"cc-x": "cc-x='something-else'"}
		})
	if !found {
		t.Fatal("expected the bash alias to be found")
	}
	if alias.Shell != "bash" || alias.StartupFile != "~/.bashrc" {
		t.Fatalf("alias should be attributed to bash and ~/.bashrc, got %#v", alias)
	}
	if alias.Definition != "cc-x='something-else'" {
		t.Fatalf("alias definition = %q", alias.Definition)
	}
	if len(probed) != 2 || probed[0] != "zsh" || probed[1] != "bash" {
		t.Fatalf("probes = %v, want zsh then bash", probed)
	}
}

func TestLookupShellAliasStopsAtTheFirstMatch(t *testing.T) {
	probes := 0
	alias, found := lookupShellAlias("cc-x", shellAliasProbeOrder("/bin/zsh"),
		func(string) map[string]string {
			probes++
			return map[string]string{"cc-x": "cc-x=other"}
		})
	if !found || alias.Shell != "zsh" || alias.StartupFile != "~/.zshrc" {
		t.Fatalf("expected a zsh alias, got %#v (found=%v)", alias, found)
	}
	if probes != 1 {
		t.Fatalf("a match should stop probing, read %d alias tables", probes)
	}
}

func TestLookupShellAliasSkipsInvalidNames(t *testing.T) {
	probes := 0
	if _, found := lookupShellAlias("../evil", shellAliasProbeOrder(""),
		func(string) map[string]string {
			probes++
			return map[string]string{"../evil": "../evil=anything"}
		}); found || probes != 0 {
		t.Fatalf("an invalid name must not be probed (found=%v probes=%d)", found, probes)
	}
}

// Zsh and Bash disagree on whether `alias` echoes the keyword back, and both
// forms have to yield the same table.
func TestParseShellAliasesHandlesBothShellFormats(t *testing.T) {
	zsh := parseShellAliases("cc-x='echo hi'\nll='ls -la'\n")
	if got := zsh["cc-x"]; got != "cc-x='echo hi'" {
		t.Fatalf("zsh cc-x = %q", got)
	}
	if got := zsh["ll"]; got != "ll='ls -la'" {
		t.Fatalf("zsh ll = %q", got)
	}

	bash := parseShellAliases("alias cc-x='echo hi'\nalias ll='ls -la'\n")
	if got := bash["cc-x"]; got != "cc-x='echo hi'" {
		t.Fatalf("bash cc-x = %q", got)
	}
	if len(bash) != 2 {
		t.Fatalf("bash table = %#v, want two entries", bash)
	}
}

func TestParseShellAliasesIgnoresNoise(t *testing.T) {
	table := parseShellAliases("\n  \nbash: no job control in this shell\nnot-an-alias-line\ncc-x='ok'\n")
	if len(table) != 1 {
		t.Fatalf("table = %#v, want only the real alias", table)
	}
	if _, ok := table["cc-x"]; !ok {
		t.Fatalf("table = %#v, want cc-x", table)
	}
}

// The probe reads a shell's alias table, which carries no provenance, so the
// remediation may name only where to start looking — never a definition site.
func TestShellAliasShadowErrorPointsAtTheStartupChain(t *testing.T) {
	err := shellAlias{Shell: "bash", StartupFile: "~/.bashrc", Definition: "cc-x='ls'"}.shadowError("cc-x")
	for _, want := range []string{"bash alias", "~/.bashrc", "or whichever file it sources", "unalias cc-x"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err, want)
		}
	}
}

// A lookup memoizes each shell for its own lifetime, so doctor can share one
// across every dialect — but a new lookup must re-read, which is what lets a
// long-lived `cc-dialect web` observe an alias the operator has since removed.
func TestShellAliasLookupCachesPerInstanceOnly(t *testing.T) {
	reads := 0
	newStub := func(table map[string]string) *shellAliasLookup {
		return &shellAliasLookup{
			probes: shellAliasProbeOrder("/bin/zsh"),
			tables: map[string]map[string]string{},
			read: func(string) map[string]string {
				reads++
				return table
			},
		}
	}

	shadowed := newStub(map[string]string{"cc-x": "cc-x='ls'"})
	if _, found := shadowed.find("cc-x"); !found {
		t.Fatal("expected the alias to be found")
	}
	if _, found := shadowed.find("cc-x"); !found {
		t.Fatal("expected the alias to still be found")
	}
	if reads != 1 {
		t.Fatalf("one lookup should read a shell once, got %d reads", reads)
	}

	// The operator removes the alias; a fresh lookup must see that rather than
	// reusing the first lookup's table. A miss falls through to the second
	// shell, so this costs one read per probed shell.
	before := reads
	if _, found := newStub(map[string]string{}).find("cc-x"); found {
		t.Fatal("a new lookup must re-read rather than reuse a stale table")
	}
	if reads-before != len(defaultShellAliasProbes) {
		t.Fatalf("a new lookup should re-read every probed shell on a miss, got %d reads", reads-before)
	}
}
