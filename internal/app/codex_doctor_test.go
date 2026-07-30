package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDoctorExplainsRecentCodexUpstreamCooldown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if _, err := newAppService().CreateDialect(DialectInput{Name: "cc-codex", Preset: "codex-sol"}, ""); err != nil {
		t.Fatal(err)
	}

	authDir := filepath.Join(home, "instances", "cc-codex", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "codex.json"), []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	logsDir := filepath.Join(authDir, "logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	errorLog := fmt.Sprintf(`=== REQUEST INFO ===
URL: /v1/messages
Method: POST
Downstream Transport: http
Upstream Transport: http
Timestamp: %s

=== REQUEST BODY ===
{"model":"gpt-5.6-sol","messages":[]}

=== API ERROR RESPONSE ===
HTTP Status: 502

=== RESPONSE ===
Status: 503
`, now.Format(time.RFC3339Nano))
	if err := os.WriteFile(
		filepath.Join(logsDir, "error-v1-messages-2026-07-30T120000-abcdef12.log"),
		[]byte(errorLog),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	proxyLog := fmt.Sprintf(
		"[%s] [error] | abcdef12 | 502 |        1.243s |       127.0.0.1 | POST    \"/v1/messages\"\n"+
			"[%s] [error] | 1234abcd | 503 |           1ms |       127.0.0.1 | POST    \"/v1/messages\"\n",
		now.Format("2006-01-02 15:04:05"),
		now.Format("2006-01-02 15:04:05"),
	)
	if err := os.WriteFile(filepath.Join(home, "instances", "cc-codex", "proxy.log"), []byte(proxyLog), 0o600); err != nil {
		t.Fatal(err)
	}

	report := captureStdout(t, func() error { return doctor(nil, "test") })
	for _, want := range []string{
		"⚠ cc-codex: gpt-5.6-sol upstream returned 502 over HTTP",
		"OAuth credentials are present",
		"auth_unavailable",
		"cooldown",
		"/model sonnet",
		"cc-dialect proxy cc-codex restart",
		"cc-dialect proxy cc-codex logs",
		"Codex CLI uses WebSocket",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("doctor output missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "cc-codex is not authenticated for codex") {
		t.Fatalf("doctor misreported present OAuth credentials:\n%s", report)
	}
}

func TestDoctorIgnoresStaleCodexUpstreamErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if _, err := newAppService().CreateDialect(DialectInput{Name: "cc-codex", Preset: "codex-sol"}, ""); err != nil {
		t.Fatal(err)
	}

	authDir := filepath.Join(home, "instances", "cc-codex", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "codex.json"), []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	logsDir := filepath.Join(authDir, "logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * time.Hour)
	errorLog := fmt.Sprintf("Upstream Transport: http\nTimestamp: %s\nStatus: 502\n", stale.Format(time.RFC3339Nano))
	if err := os.WriteFile(
		filepath.Join(logsDir, "error-v1-messages-2026-07-30T100000-abcdef12.log"),
		[]byte(errorLog),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	report := captureStdout(t, func() error { return doctor(nil, "test") })
	if strings.Contains(report, "upstream returned 502") {
		t.Fatalf("doctor reported a stale upstream failure:\n%s", report)
	}
}

func TestDoctorDoesNotPairAnOlderFastFailureWithANewerUpstreamError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if _, err := newAppService().CreateDialect(DialectInput{Name: "cc-codex", Preset: "codex-sol"}, ""); err != nil {
		t.Fatal(err)
	}

	authDir := filepath.Join(home, "instances", "cc-codex", "auth")
	logsDir := filepath.Join(authDir, "logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "codex.json"), []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	errorLog := fmt.Sprintf(
		"Upstream Transport: http\nTimestamp: %s\nHTTP Status: 502\n",
		now.Format(time.RFC3339Nano),
	)
	if err := os.WriteFile(filepath.Join(logsDir, "error-v1-messages-current.log"), []byte(errorLog), 0o600); err != nil {
		t.Fatal(err)
	}
	older := now.Add(-10 * time.Minute)
	proxyLog := fmt.Sprintf(
		"[%s] [error] | abcdef12 | 503 | 1ms | 127.0.0.1 | POST \"/v1/messages\"\n",
		older.Format("2006-01-02 15:04:05"),
	)
	if err := os.WriteFile(filepath.Join(home, "instances", "cc-codex", "proxy.log"), []byte(proxyLog), 0o600); err != nil {
		t.Fatal(err)
	}

	report := captureStdout(t, func() error { return doctor(nil, "test") })
	if !strings.Contains(report, "upstream returned 502") {
		t.Fatalf("doctor missed the recent upstream failure:\n%s", report)
	}
	if strings.Contains(report, "Retries completing in under 10ms") {
		t.Fatalf("doctor paired an older fast failure with the newer upstream error:\n%s", report)
	}
}

func TestDoctorDoesNotTreatDownstreamStatusAsUpstreamFailure(t *testing.T) {
	home, logsDir := createCodexDoctorFixture(t, DialectInput{Name: "cc-codex", Preset: "codex-sol"})
	now := time.Now()
	errorLog := fmt.Sprintf(
		"Upstream Transport: http\nTimestamp: %s\nStatus: 502\n",
		now.Format(time.RFC3339Nano),
	)
	if err := os.WriteFile(filepath.Join(logsDir, "error-v1-messages-downstream-only.log"), []byte(errorLog), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "instances", "cc-codex", "proxy.log"), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}

	report := captureStdout(t, func() error { return doctor(nil, "test") })
	if strings.Contains(report, "upstream returned 502") {
		t.Fatalf("doctor mislabeled a downstream-only status as upstream:\n%s", report)
	}
}

func TestDoctorRecognizesInferredCodexRoute(t *testing.T) {
	_, logsDir := createCodexDoctorFixture(t, DialectInput{Name: "cc-custom", Model: "gpt-5.6-sol"})
	now := time.Now()
	errorLog := fmt.Sprintf(
		"Upstream Transport: http\nTimestamp: %s\n=== REQUEST BODY ===\n{\"model\":\"gpt-5.6-sol\"}\n\n=== API ERROR RESPONSE ===\nHTTP Status: 502\n",
		now.Format(time.RFC3339Nano),
	)
	if err := os.WriteFile(filepath.Join(logsDir, "error-v1-messages-inferred.log"), []byte(errorLog), 0o600); err != nil {
		t.Fatal(err)
	}

	report := captureStdout(t, func() error { return doctor(nil, "test") })
	if !strings.Contains(report, "⚠ cc-custom: gpt-5.6-sol upstream returned 502 over HTTP") {
		t.Fatalf("doctor missed an inferred authenticated Codex route:\n%s", report)
	}
}

func TestDoctorSuggestsADifferentModelTier(t *testing.T) {
	_, logsDir := createCodexDoctorFixture(t, DialectInput{Name: "cc-codex", Preset: "codex-sol"})
	now := time.Now()
	errorLog := fmt.Sprintf(
		"Upstream Transport: http\nTimestamp: %s\n=== REQUEST BODY ===\n{\"model\":\"gpt-5.6-terra\"}\n\n=== API ERROR RESPONSE ===\nHTTP Status: 502\n",
		now.Format(time.RFC3339Nano),
	)
	if err := os.WriteFile(filepath.Join(logsDir, "error-v1-messages-terra.log"), []byte(errorLog), 0o600); err != nil {
		t.Fatal(err)
	}

	report := captureStdout(t, func() error { return doctor(nil, "test") })
	if !strings.Contains(report, "gpt-5.6-terra upstream returned 502") {
		t.Fatalf("doctor did not name the failing request model:\n%s", report)
	}
	if !strings.Contains(report, "Try: /model haiku (gpt-5.6-luna)") {
		t.Fatalf("doctor did not suggest a different tier:\n%s", report)
	}
	if strings.Contains(report, "Try: /model sonnet") {
		t.Fatalf("doctor recommended the tier that already failed:\n%s", report)
	}
}

func createCodexDoctorFixture(t *testing.T, input DialectInput) (home, logsDir string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if _, err := newAppService().CreateDialect(input, ""); err != nil {
		t.Fatal(err)
	}

	authDir := filepath.Join(home, "instances", input.Name, "auth")
	logsDir = filepath.Join(authDir, "logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(authDir, "codex.json"), []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, logsDir
}
