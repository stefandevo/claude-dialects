package app

import (
	"errors"
	"testing"
)

func stubBridgeNodeProbe(t *testing.T, err error) {
	t.Helper()
	previous := bridgeNodeProbe
	bridgeNodeProbe = func(string) error { return err }
	t.Cleanup(func() { bridgeNodeProbe = previous })
}

func TestMissingStartRequirementCursorBridge(t *testing.T) {
	stubBridgeNodeProbe(t, nil)
	dialect := Dialect{Bridge: "cursor", AuthTokenEnv: "CURSOR_API_KEY"}
	t.Setenv("CURSOR_API_KEY", "")
	if got := missingStartRequirement(dialect); got != "CURSOR_API_KEY is not set" {
		t.Fatalf("expected missing CURSOR_API_KEY to be reported, got %q", got)
	}
	t.Setenv("CURSOR_API_KEY", "token")
	if got := missingStartRequirement(dialect); got != "" {
		t.Fatalf("expected no missing requirement when CURSOR_API_KEY is set, got %q", got)
	}
}

func TestMissingStartRequirementBridgeNode(t *testing.T) {
	nodeErr := errors.New("Node.js was not found in PATH")
	stubBridgeNodeProbe(t, nodeErr)

	t.Setenv("CURSOR_API_KEY", "token")
	if got := missingStartRequirement(Dialect{Bridge: "cursor"}); got != nodeErr.Error() {
		t.Fatalf("expected cursor node error to be reported, got %q", got)
	}
	if got := missingStartRequirement(Dialect{Bridge: "copilot"}); got != nodeErr.Error() {
		t.Fatalf("expected copilot node error to be reported, got %q", got)
	}

	stubBridgeNodeProbe(t, nil)
	if got := missingStartRequirement(Dialect{Bridge: "copilot"}); got != "" {
		t.Fatalf("expected no missing requirement for copilot with node available, got %q", got)
	}
}

func TestMissingStartRequirementUpstreamToken(t *testing.T) {
	stubBridgeNodeProbe(t, nil)
	dialect := Dialect{BaseURL: "https://api.z.ai/api/anthropic", AuthTokenEnv: "ZAI_API_KEY"}
	t.Setenv("ZAI_API_KEY", "")
	if got := missingStartRequirement(dialect); got != "ZAI_API_KEY is not set" {
		t.Fatalf("expected missing ZAI_API_KEY to be reported, got %q", got)
	}
	t.Setenv("ZAI_API_KEY", "token")
	if got := missingStartRequirement(dialect); got != "" {
		t.Fatalf("expected no missing requirement when ZAI_API_KEY is set, got %q", got)
	}
}

func TestMissingStartRequirementPlainProxy(t *testing.T) {
	stubBridgeNodeProbe(t, errors.New("node missing"))
	// Non-bridge OAuth dialects need neither env vars nor Node.
	if got := missingStartRequirement(Dialect{}); got != "" {
		t.Fatalf("expected no missing requirement for plain proxy dialects, got %q", got)
	}
}
