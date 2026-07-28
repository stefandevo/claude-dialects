package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The SDK's local agent store is append-only and is read back whole, so a
// directory shared by every request a dialect has ever served grows until a
// parse of it exhausts the bridge's heap. Each launch starts from an empty
// store, which is also what repairs an instance whose store already grew past
// what the bridge can parse.
func TestResetCursorAgentStoreClearsAccumulatedState(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	workspace, logFile, err := prepareCursorBridgeFiles(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err = logFile.Close(); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(workspace, ".cursor-dialect-state")
	if err = os.MkdirAll(filepath.Join(store, "run-abandoned"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"checkpoints.ndjson",
		"run_events.ndjson",
		".checkpoints.ndjson.9f2c.tmp",
		filepath.Join("run-abandoned", "run_events.ndjson"),
	} {
		if err = os.WriteFile(filepath.Join(store, rel), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err = resetCursorAgentStore(instance); err != nil {
		t.Fatalf("reset the Cursor agent store: %v", err)
	}

	if _, statErr := os.Stat(store); !os.IsNotExist(statErr) {
		t.Fatalf("agent store survived the reset: %v", statErr)
	}
	if info, statErr := os.Stat(workspace); statErr != nil || !info.IsDir() {
		t.Fatalf("reset removed the workspace itself: %v", statErr)
	}
	logPath, _ := instance.Abs("cursor-bridge.log")
	if _, statErr := os.Stat(logPath); statErr != nil {
		t.Fatalf("reset removed the bridge log: %v", statErr)
	}
	// A store that is already absent is the normal case on a first launch.
	if err = resetCursorAgentStore(instance); err != nil {
		t.Fatalf("reset an absent Cursor agent store: %v", err)
	}
}

// The reset is a recursive removal, so it must refuse a symlinked instance for
// the same reason every other instance write does: following the link would
// delete a tree outside the instances root.
func TestResetCursorAgentStoreRejectsSymlinkedInstance(t *testing.T) {
	_, target := symlinkedInstance(t, "cc-test")
	store := filepath.Join(target, "cursor-workspace", ".cursor-dialect-state")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(store, "checkpoints.ndjson")
	if err := os.WriteFile(planted, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	if err = resetCursorAgentStore(instance); err == nil {
		t.Fatal("resetting the agent store should reject a symlinked instance")
	}
	if _, statErr := os.Stat(planted); statErr != nil {
		t.Fatalf("reset deleted through the escaping symlink: %v", statErr)
	}
}

// The recursion unlinks a symlink it discovers inside the tree rather than
// following it, but the tree's own root arrives as a name — and a name resolves
// through a link whose target stays inside the dialect. A store directory
// replaced by a link to a sibling must therefore leave that sibling's contents
// alone; deleting them would erase credentials or history on the next launch.
func TestResetCursorAgentStoreRefusesSymlinkedStoreRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIALECT_HOME", home)
	dialect := filepath.Join(home, "instances", "cc-test")
	if err := os.MkdirAll(filepath.Join(dialect, "cursor-workspace"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dialect, "auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(dialect, "auth", "codex.json")
	if err := os.WriteFile(credential, []byte(`{"type":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dialect, "cursor-workspace", ".cursor-dialect-state")
	if err := os.Symlink("../auth", link); err != nil {
		t.Fatal(err)
	}
	instance, err := openInstanceFS("cc-test")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()

	if err = resetCursorAgentStore(instance); err != nil {
		t.Fatalf("reset a symlinked store root: %v", err)
	}

	if _, statErr := os.Stat(credential); statErr != nil {
		t.Fatalf("reset deleted through the symlinked store root: %v", statErr)
	}
	if _, statErr := os.Lstat(link); !os.IsNotExist(statErr) {
		t.Fatalf("the store symlink itself survived the reset: %v", statErr)
	}
}

// Node sizes its default old-space limit from the machine's memory, so the
// ceiling a bridge dies at otherwise depends on the host it runs on. Pinning it
// makes a large-but-legitimate parse behave the same everywhere.
func TestCursorBridgeNodeArgsPinTheHeapLimit(t *testing.T) {
	args := cursorBridgeNodeArgs("/runtime/cursor_bridge.mjs", 43175, "/instances/cc-cursor/cursor-workspace")
	want := []string{
		"--max-old-space-size=4096",
		"/runtime/cursor_bridge.mjs",
		"--host", "127.0.0.1",
		"--port", "43175",
		"--workspace", "/instances/cc-cursor/cursor-workspace",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("Cursor bridge node args = %v, want %v", args, want)
	}
	if index := slices.Index(args, "/runtime/cursor_bridge.mjs"); index == 0 {
		t.Fatal("V8 options must precede the script path or node treats them as script arguments")
	}
}

func TestEmbeddedCursorBridgeScopesAgentStorePerRun(t *testing.T) {
	text := string(cursorBridgeSource)
	for _, expected := range []string{
		`const runStateDir = path.join(workspace, ".cursor-dialect-state", ` + "`run-${crypto.randomUUID()}`" + `)`,
		`new JsonlLocalAgentStore(runStateDir)`,
		`discardRunState(runStateDir)`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("embedded Cursor bridge does not contain %q", expected)
		}
	}
	if strings.Contains(text, `new JsonlLocalAgentStore(path.join(workspace, ".cursor-dialect-state"))`) {
		t.Fatal("embedded Cursor bridge still shares one unbounded agent store across every request")
	}
}

func TestEmbeddedCursorBridgeSurvivesUncaughtFaults(t *testing.T) {
	text := string(cursorBridgeSource)
	for _, expected := range []string{
		`"uncaughtException"`,
		`"unhandledRejection"`,
		`const inFlight = new Set()`,
		`inFlight.add(pending)`,
		`inFlight.delete(pending)`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("embedded Cursor bridge does not contain %q", expected)
		}
	}
}

// Cancelling an abandoned request only works once the run exists. Agent.create
// and agent.send are each an await during which nothing can be cancelled, so
// without a checked abort flag a request failed by a fault handler would go on
// to start a generation that is billed and whose response is discarded.
func TestEmbeddedCursorBridgeStopsAbandonedRequestsBeforeBilling(t *testing.T) {
	text := string(cursorBridgeSource)
	for _, expected := range []string{
		`let aborted = false`,
		`aborted = true`,
		`if (aborted) return;`,
		`if (aborted) {`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("embedded Cursor bridge does not contain %q", expected)
		}
	}
	create := strings.Index(text, "agent = await Agent.create(")
	send := strings.Index(text, "activeRun = await agent.send(")
	stream := strings.Index(text, "for await (const event of activeRun.stream())")
	if create < 0 || send < 0 || stream < 0 {
		t.Fatal("embedded Cursor bridge no longer has the expected run sequence")
	}
	// One abort check between agent creation and the send that starts billing,
	// and one between that send and the stream it feeds.
	if !strings.Contains(text[create:send], "if (aborted) return;") {
		t.Fatal("embedded Cursor bridge starts a run without rechecking the abort flag after Agent.create")
	}
	if !strings.Contains(text[send:stream], "if (aborted) {") {
		t.Fatal("embedded Cursor bridge streams a run without rechecking the abort flag after agent.send")
	}
}

// A bridge that died mid-session leaves its PID record behind, so the port stops
// answering while the record still asserts ownership. Reporting that as merely
// "stopped" is what left the proxy forwarding 500s into a dead port with nothing
// pointing at the bridge log.
func TestRuntimeStatusReportsCrashedBridge(t *testing.T) {
	t.Setenv("DIALECT_HOME", t.TempDir())
	cfg := defaultConfig()
	dialect := presets["cursor-composer"]
	dialect.Port = 43180
	dialect.BridgePort = 43181
	dialect.APIKey = "private"
	cfg.Dialects["cc-cursor"] = dialect
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	instance, err := openInstanceFS("cc-cursor")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	if err = instance.WritePID("cursor-bridge.pid", exitedPID(t)); err != nil {
		t.Fatal(err)
	}
	service := newAppService()
	service.proxyProbe = func(Dialect) bool { return true }
	service.bridgeProbe = func(Dialect) bool { return false }

	status, err := service.DialectStatus("cc-cursor")
	if err != nil {
		t.Fatal(err)
	}
	if status.Bridge == nil || status.Bridge.State != RuntimeCrashed {
		t.Fatalf("crashed bridge reported as %#v", status.Bridge)
	}
	if status.State != RuntimeDegraded {
		t.Fatalf("dialect with a crashed bridge reported %s", status.State)
	}

	// No PID record at all is an ordinary stop, not a crash.
	if err = instance.RemoveIfExists("cursor-bridge.pid"); err != nil {
		t.Fatal(err)
	}
	status, err = service.DialectStatus("cc-cursor")
	if err != nil {
		t.Fatal(err)
	}
	if status.Bridge == nil || status.Bridge.State != RuntimeStopped {
		t.Fatalf("stopped bridge reported as %#v", status.Bridge)
	}
}

// exitedPID returns the PID of a process that has run to completion and been
// reaped, which is the closest a test can get to "recorded, but gone".
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if processAlive(pid) {
		t.Skip("the reaped PID was reused before the assertion could run")
	}
	return pid
}
