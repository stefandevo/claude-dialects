package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// idleRuntimePorts declares that no dialect runtime is listening. Tests that
// exercise an unreadable PID record reach the port liveness check, and without
// this they would depend on whatever happens to be bound on the machine running
// them — a real cc-dialect proxy on the same port makes them fail.
func idleRuntimePorts(t *testing.T) {
	t.Helper()
	original := portBusy
	portBusy = func(int) bool { return false }
	t.Cleanup(func() { portBusy = original })
}

func symlinkedInstance(t *testing.T, name string) (home, target string) {
	t.Helper()
	idleRuntimePorts(t)
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

// The proxy and its managed bridge must be stopped in one directory. The bridge
// stop used to re-open the dialect by name, so an entry replaced between the two
// left the original bridge running behind a successful stop: the replacement's
// missing PID read as "already stopped".
func TestStopReadsTheBridgePIDThroughThePinnedDirectory(t *testing.T) {
	idleRuntimePorts(t)
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	instances := filepath.Join(home, "instances")
	pinned := filepath.Join(instances, "cc-bridge")
	if err := os.MkdirAll(pinned, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pinned, "cursor-bridge.pid"), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	instance, err := openInstanceFS("cc-bridge")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if !instance.Pin() {
		t.Fatal("Pin refused a real directory")
	}

	// The name now resolves to a different directory than the pinned one, which
	// is what a rename racing the stop looks like from here.
	moved := filepath.Join(instances, "cc-moved")
	if err = os.Rename(pinned, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(pinned, 0o700); err != nil {
		t.Fatal(err)
	}

	base := availablePortRange(t, 2)
	dialect := Dialect{Model: "model", Port: base, Bridge: "cursor", BridgePort: base + 1, APIKey: "key"}
	if err = stopProxyDialect(instance, dialect); err != nil {
		t.Fatalf("stopProxyDialect failed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(moved, "cursor-bridge.pid")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("bridge PID was not cleared through the pinned directory: %v", statErr)
	}
}
