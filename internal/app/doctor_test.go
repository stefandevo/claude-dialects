package app

import "testing"

func TestMissingStartEnvCursorBridge(t *testing.T) {
	dialect := Dialect{Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY"}
	t.Setenv("CURSOR_API_KEY", "")
	if got := missingStartEnv(dialect); got != "CURSOR_API_KEY" {
		t.Fatalf("expected CURSOR_API_KEY to be reported missing, got %q", got)
	}
	t.Setenv("CURSOR_API_KEY", "token")
	if got := missingStartEnv(dialect); got != "" {
		t.Fatalf("expected no missing env when CURSOR_API_KEY is set, got %q", got)
	}
}

func TestMissingStartEnvUpstreamToken(t *testing.T) {
	dialect := Dialect{BaseURL: "https://api.z.ai/api/anthropic", AuthTokenEnv: "ZAI_API_KEY"}
	t.Setenv("ZAI_API_KEY", "")
	if got := missingStartEnv(dialect); got != "ZAI_API_KEY" {
		t.Fatalf("expected ZAI_API_KEY to be reported missing, got %q", got)
	}
	t.Setenv("ZAI_API_KEY", "token")
	if got := missingStartEnv(dialect); got != "" {
		t.Fatalf("expected no missing env when ZAI_API_KEY is set, got %q", got)
	}
}

func TestMissingStartEnvOAuthDialect(t *testing.T) {
	// OAuth-based dialects (no upstream token env, no cursor bridge) have no
	// start-time env requirement.
	dialect := Dialect{Bridge: "copilot"}
	if got := missingStartEnv(dialect); got != "" {
		t.Fatalf("expected no missing env for OAuth dialects, got %q", got)
	}
}
