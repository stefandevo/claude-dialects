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
		`runStateDir = path.join(workspace, ".cursor-dialect-state", ` + "`run-${crypto.randomUUID()}`" + `)`,
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
		`for (const pending of [...inFlight]) pending.abandon()`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("embedded Cursor bridge does not contain %q", expected)
		}
	}
}

// Cancelling an abandoned request only works once the run exists, and
// selectModel, Agent.create, and agent.send are each an await during which there
// is nothing to cancel. Without a flag checked in every one of those windows, a
// request whose client is already gone goes on to start a generation that is
// billed and whose response is discarded.
func TestEmbeddedCursorBridgeStopsAbandonedRequestsBeforeBilling(t *testing.T) {
	text := string(cursorBridgeSource)
	for _, expected := range []string{
		`if (pending.aborted) return;`,
		`if (pending.aborted) {`,
		`pending.onAbort = () => {`,
		`activeRun?.cancel().catch(() => {});`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("embedded Cursor bridge does not contain %q", expected)
		}
	}
	selection := strings.Index(text, "await selectModel(")
	create := strings.Index(text, "agent = await Agent.create(")
	send := strings.Index(text, "activeRun = await agent.send(")
	stream := strings.Index(text, "for await (const event of activeRun.stream())")
	if selection < 0 || create < 0 || send < 0 || stream < 0 {
		t.Fatal("embedded Cursor bridge no longer has the expected run sequence")
	}
	// An abort check in each window: after the catalog lookup, after the agent is
	// created, and after the send that starts billing.
	if !strings.Contains(text[selection:create], "if (pending.aborted) return;") {
		t.Fatal("embedded Cursor bridge builds a run without rechecking abandonment after selectModel")
	}
	if !strings.Contains(text[create:send], "if (pending.aborted) return;") {
		t.Fatal("embedded Cursor bridge starts a run without rechecking abandonment after Agent.create")
	}
	if !strings.Contains(text[send:stream], "if (pending.aborted) {") {
		t.Fatal("embedded Cursor bridge streams a run without rechecking abandonment after agent.send")
	}
}

// A cancelled run normally settles, so the request's finally closes its agent
// and removes its store. A run whose transport died may never settle, leaving
// that finally unreached — so the release has to be reachable from a timer too,
// or every fault permanently costs the bridge a live agent and a directory.
//
// Both callers can run, in either order. Releasing behind a single "this request
// is done" flag breaks that: a timer firing while Agent.create is still pending
// spends the flag on an agent that does not exist, and the one created a moment
// later is never closed. Each resource therefore carries its own condition.
func TestEmbeddedCursorBridgeReleasesRunsThatNeverSettle(t *testing.T) {
	text := string(cursorBridgeSource)
	for _, expected := range []string{
		`const abandonedRunGraceMs =`,
		`setTimeout(releaseRun, abandonedRunGraceMs).unref()`,
		`if (agent && !agentClosed) {`,
		`agentClosed = true;`,
		"} finally {\n    stopHeartbeat();",
		`keepTurnAlive()`,
		`closeTurnSession(turnSession)`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("embedded Cursor bridge does not contain %q", expected)
		}
	}
	release := strings.Index(text, "const releaseRun = ()")
	if release < 0 {
		t.Fatal("embedded Cursor bridge no longer defines releaseRun")
	}
	body := text[release:]
	if end := strings.Index(body, "\n  };"); end > 0 {
		body = body[:end]
	}
	// The agent has to stop before its directory goes, or the removal lands
	// underneath an SDK that is still writing.
	closeAgent := strings.Index(body, "agent.close()")
	discard := strings.Index(body, "discardRunState(runStateDir)")
	if closeAgent < 0 || discard < 0 || closeAgent > discard {
		t.Fatal("embedded Cursor bridge discards the run store before closing its agent")
	}
	// The discard must not sit behind the agent's own guard, or a store the SDK
	// recreated after an early release would survive for the life of the bridge.
	guardEnd := strings.Index(body, "}")
	if guardEnd < 0 || discard < guardEnd {
		t.Fatal("embedded Cursor bridge discards the run store only when it closes an agent")
	}
	// A bare early return would restore exactly the spent-flag behaviour above.
	if strings.Contains(body, "return;") {
		t.Fatal("embedded Cursor bridge short-circuits releaseRun, so a later-created agent is never closed")
	}
}

// A close event fires once, immediately. A listener attached after the handler's
// first await never hears it, so a client that disconnects during the catalog
// lookup would leave the request believing it still has a reader. Tracking has
// to be installed before any route awaits anything.
// Streaming requests must emit SSE headers and the role chunk before agent.send
// settles, and must subscribe to onDelta for incremental text. Buffering until
// the run finishes is what made Cursor dialects appear frozen.
func TestEmbeddedCursorBridgeStreamsIncrementally(t *testing.T) {
	text := string(cursorBridgeSource)
	send := strings.Index(text, "activeRun = await agent.send(")
	streamHead := strings.Index(text, `response.writeHead(200, {`)
	keepalive := strings.Index(text, ": keepalive")
	timing := strings.Index(text, "cursor bridge timing:")
	if send < 0 || streamHead < 0 || keepalive < 0 || timing < 0 {
		t.Fatal("embedded Cursor bridge is missing incremental streaming hooks")
	}
	if streamHead > send {
		t.Fatal("embedded Cursor bridge writes SSE headers after agent.send instead of before it")
	}
	sendEnd := strings.Index(text[send:], "});")
	if sendEnd < 0 {
		t.Fatal("embedded Cursor bridge no longer has the expected agent.send call")
	}
	sendBlock := text[send : send+sendEnd]
	if !strings.Contains(sendBlock, "onDelta:") {
		t.Fatal("embedded Cursor bridge does not pass onDelta to agent.send")
	}
	// Non-streaming responses stay buffered until the run settles.
	if !strings.Contains(text, "const isStreaming = Boolean(body.stream)") {
		t.Fatal("embedded Cursor bridge does not derive isStreaming from body.stream")
	}
	// Partial tool-call deltas are timing-only; capture happens on complete stream events.
	toolDelta := strings.Index(text, `case "tool-call-started":`)
	toolDeltaEnd := strings.Index(text[toolDelta:], "default:")
	if toolDelta < 0 || toolDeltaEnd < 0 {
		t.Fatal("embedded Cursor bridge is missing tool-call delta handling")
	}
	if strings.Contains(text[toolDelta:toolDelta+toolDeltaEnd], "capture(") {
		t.Fatal("embedded Cursor bridge captures tool calls from partial streaming deltas")
	}
}

// One Claude Code turn may require several HTTP requests (one per tool call).
// The bridge keeps the Cursor agent alive across those steps when the incoming
// transcript extends the cached prefix by assistant(tool_calls) + tool result
// and the tool call id/name matches the parked pendingToolCall.
func TestEmbeddedCursorBridgeReusesAgentsAcrossToolCalls(t *testing.T) {
	text := string(cursorBridgeSource)
	for _, expected := range []string{
		`const activeTurnSessions = new Map()`,
		`function findContinuationSession(`,
		`function registerTurnSession(`,
		`function buildContinuationPrompt(`,
		`function continuationMatchesPendingTool(`,
		`function scheduleSessionIdleTimer(`,
		`function evictExpiredIdleSessions(`,
		`function enforceTurnSessionCapacity(`,
		`function failStreamingResponse(`,
		`function toolCallName(`,
		`(body.messages || []).slice(turnSession.messageCount)`,
		`pendingToolCall:`,
		`sessionId: crypto.randomUUID()`,
		`keepTurnAlive()`,
		`turn continue messages=`,
		`isToolStepContinuation`,
		`const turnSessionIdleMs =`,
		`activeTurnSessions.size >= turnSessionMaxEntries`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("embedded Cursor bridge does not contain %q", expected)
		}
	}
	create := strings.Index(text, "if (!agent) {")
	send := strings.Index(text, "activeRun = await agent.send(")
	if create < 0 || send < 0 || create > send {
		t.Fatal("embedded Cursor bridge should create agents only when no turn session is reused")
	}
	// Capacity enforcement runs only when registering a new session, not during lookup.
	findContinuation := strings.Index(text, "function findContinuationSession(")
	registerTurn := strings.Index(text, "function registerTurnSession(")
	if findContinuation < 0 || registerTurn < 0 {
		t.Fatal("embedded Cursor bridge is missing turn-session helpers")
	}
	findBlock := text[findContinuation:registerTurn]
	if strings.Contains(findBlock, "enforceTurnSessionCapacity") {
		t.Fatal("embedded Cursor bridge evicts by capacity during continuation lookup")
	}
	// When every cached session is inUse, refuse to grow the map past the limit.
	if !strings.Contains(text, `if (!registerTurnSession(session)) {`) {
		t.Fatal("embedded Cursor bridge does not handle turn-session cache overflow")
	}
	registerBlock := text[registerTurn:]
	registerEnd := strings.Index(registerBlock, "\n}\n\n// trackRequest")
	if registerEnd < 0 {
		t.Fatal("embedded Cursor bridge is missing registerTurnSession body")
	}
	if !strings.Contains(registerBlock[:registerEnd], "return false") {
		t.Fatal("embedded Cursor bridge registerTurnSession does not refuse insertion at capacity")
	}
}

// Streaming failures after SSE headers must not look like successful completions.
func TestEmbeddedCursorBridgeSignalsStreamingFailures(t *testing.T) {
	text := string(cursorBridgeSource)
	failStart := strings.Index(text, "function failStreamingResponse(")
	if failStart < 0 {
		t.Fatal("embedded Cursor bridge is missing failStreamingResponse")
	}
	failEnd := strings.Index(text[failStart:], "\n}\n\nfunction forwardedToolFromDeltaUpdate")
	if failEnd < 0 {
		t.Fatal("embedded Cursor bridge failStreamingResponse has unexpected shape")
	}
	failBlock := text[failStart : failStart+failEnd]
	if !strings.Contains(failBlock, "error: {") {
		t.Fatal("embedded Cursor bridge does not emit protocol-level SSE errors")
	}
	if strings.Contains(failBlock, `finish_reason: "stop"`) || strings.Contains(failBlock, "data: [DONE]") {
		t.Fatal("embedded Cursor bridge encodes streaming failures as successful completions")
	}
}

func TestEmbeddedCursorBridgeTracksRequestsBeforeTheFirstAwait(t *testing.T) {
	text := string(cursorBridgeSource)
	handler := strings.Index(text, "http.createServer(")
	track := strings.Index(text, "const pending = trackRequest(response)")
	if handler < 0 || track < 0 {
		t.Fatal("embedded Cursor bridge no longer registers requests in its server handler")
	}
	if track < handler {
		t.Fatal("embedded Cursor bridge registers requests outside the server handler")
	}
	// Every await a route can reach must come after registration.
	for _, await := range []string{
		"await listCursorModels()",
		"await readJSON(request)",
		"await chatCompletion(",
	} {
		index := strings.Index(text[handler:], await)
		if index < 0 {
			t.Fatalf("embedded Cursor bridge server handler no longer contains %q", await)
		}
		if handler+index < track {
			t.Fatalf("embedded Cursor bridge awaits %q before the request is tracked", await)
		}
	}
	// The handler owns the entry's lifetime, so no route can leak one by throwing.
	// The release therefore has to sit past the handler's own catch, in a finally.
	catchStart := strings.Index(text[handler:], "} catch (error) {")
	listen := strings.Index(text, "server.listen(")
	if catchStart < 0 || listen < 0 {
		t.Fatal("embedded Cursor bridge no longer has the expected server handler shape")
	}
	tail := text[handler+catchStart : listen]
	if !strings.Contains(tail, "} finally {") || !strings.Contains(tail, "inFlight.delete(pending)") {
		t.Fatal("embedded Cursor bridge does not release tracked requests in the server handler's finally")
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
