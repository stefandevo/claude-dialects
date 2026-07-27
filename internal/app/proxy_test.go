package app

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

// servePort starts a loopback listener answering every request with 200, which
// is what both proxyHealthy and the bridge health probes look for. before runs
// inside the handler — that is, inside the health call — so a test can move the
// dialect directory in the exact window between the pin and the probe's result.
func servePort(t *testing.T, before func()) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if before != nil {
			before()
		}
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.Addr().(*net.TCPAddr).Port
}

// The already-healthy fast path must make the same pin check the spawn path
// does. Without it a name replaced since the pin reports success while the
// running proxy and its PID stay in the old directory, where a later stop by
// name will not look.
func TestStartProxyRejectsAHealthyProxyWhoseDirectoryMoved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	instances := filepath.Join(home, "instances")
	pinned := filepath.Join(instances, "cc-move")
	if err := os.MkdirAll(pinned, 0o700); err != nil {
		t.Fatal(err)
	}
	// The ownership record the fast path requires, so this test reaches the pin
	// check rather than stopping at the record that is missing.
	if err := os.WriteFile(filepath.Join(pinned, "proxy.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved := false
	port := servePort(t, func() {
		if moved {
			return
		}
		moved = true
		if err := os.Rename(pinned, filepath.Join(instances, "cc-moved")); err != nil {
			t.Error(err)
			return
		}
		if err := os.MkdirAll(pinned, 0o700); err != nil {
			t.Error(err)
		}
	})
	err := startProxy("cc-move", Dialect{Model: "model", Port: port, APIKey: "key"})
	if err == nil {
		t.Fatal("startProxy accepted a healthy proxy after its directory moved")
	}
	if !strings.Contains(err.Error(), "changed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Health is answered by the port, which cannot say which directory the process
// belongs to. A proxy left over from a directory replaced before startup answers
// with the same configured key, so without an ownership record under the pinned
// root startup would succeed with no proxy.pid there at all — and a later stop
// would read that absence as "already stopped".
func TestStartProxyRefusesAHealthyProxyItHasNoRecordOf(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "instances", "cc-orphan"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := startProxy("cc-orphan", Dialect{Model: "model", Port: servePort(t, nil), APIKey: "key"})
	if err == nil {
		t.Fatal("startProxy adopted a healthy proxy with no ownership record")
	}
	if !strings.Contains(err.Error(), "no record of owning it") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A caller that unwinds its own failed startup has to undo what it started and
// nothing else, so startManagedBridge returns a teardown closure only for a
// bridge this call launched — and refuses to adopt one it cannot prove it owns.
func TestStartManagedBridgeReturnsTeardownOnlyForItsOwnLaunch(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	instance, err := openInstanceFS("cc-bridge")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	serving := Dialect{Bridge: "cursor", BridgePort: servePort(t, nil), APIKey: "key"}

	// Serving on the port, but nothing under the pinned root claims it.
	if _, startErr := startManagedBridge(instance, serving); startErr == nil {
		t.Fatal("a healthy bridge with no ownership record was adopted")
	}

	// With a live record, the same bridge is this dialect's to keep — and not
	// this call's to tear down.
	if writeErr := instance.WritePID("cursor-bridge.pid", os.Getpid()); writeErr != nil {
		t.Fatal(writeErr)
	}
	unwind, startErr := startManagedBridge(instance, serving)
	if startErr != nil {
		t.Fatalf("an already-serving bridge with a live record should be adopted: %v", startErr)
	}
	if unwind != nil {
		t.Fatal("a bridge that was already serving was claimed as this call's launch")
	}

	// Nothing is listening, so this call goes on to launch. The launch fails, and
	// a failed launch cleans up after itself rather than handing back a teardown.
	t.Setenv("CURSOR_API_KEY", "")
	unwind, startErr = startManagedBridge(instance, Dialect{
		Bridge: "cursor", BridgePort: availablePortRange(t, 1), APIKey: "key",
	})
	if startErr == nil {
		t.Fatal("the launch should fail without CURSOR_API_KEY")
	}
	if unwind != nil {
		t.Fatal("a failed launch handed back a teardown closure")
	}
}
