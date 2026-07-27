package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func symlinkedInstance(t *testing.T, name string) (home, target string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	target = t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, "instances", name)); err != nil {
		t.Fatal(err)
	}
	return home, target
}

func TestHasProviderCredentialsRejectsSymlinkedAuthDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	instance := filepath.Join(home, "instances", "cc-test")
	if err := os.MkdirAll(instance, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "codex.json"), []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(instance, "auth")); err != nil {
		t.Fatal(err)
	}
	if hasProviderCredentials("cc-test", "codex") {
		t.Fatal("credentials reached through an escaping auth symlink should not be accepted")
	}
}

func TestProxyPIDRejectsSymlinkedInstance(t *testing.T) {
	_, target := symlinkedInstance(t, "cc-test")
	if err := os.WriteFile(filepath.Join(target, "proxy.pid"), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := proxyPID("cc-test"); got != 0 {
		t.Fatalf("proxyPID read %d through an escaping instance symlink", got)
	}
}

func TestTailLogRejectsSymlinkedInstance(t *testing.T) {
	_, target := symlinkedInstance(t, "cc-test")
	const secret = "external-log-content"
	if err := os.WriteFile(filepath.Join(target, "proxy.log"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	var tailErr error
	output := captureStdout(t, func() error {
		tailErr = tailLog("cc-test")
		return nil
	})
	if tailErr == nil {
		t.Fatal("tailLog should reject a symlinked instance")
	}
	if strings.Contains(output, secret) {
		t.Fatalf("tailLog exposed external file contents: %q", output)
	}
}
