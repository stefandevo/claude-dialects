package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The bridges flatten the conversation into prose because neither SDK accepts a
// structured message array on the send path, and they label past tool calls and
// results so the model can tell them apart. A reply that copies those labels is
// passed through untouched and rendered as if it were real output, so each
// bridge carries an identical filter that strips them back out.
const (
	markerFilterStart = "// --- transcript marker filter "
	markerFilterEnd   = "// --- end transcript marker filter"
)

// Each bridge is installed as one standalone script, so the filter cannot live
// in a shared module: it is duplicated instead. Two copies of tricky string
// logic drift, and a bridge whose copy drifted would still pass every test
// written against the other one.
func TestBridgeMarkerFiltersAreIdentical(t *testing.T) {
	copilot := extractMarkerFilter(t, "copilot", copilotBridgeSource)
	cursor := extractMarkerFilter(t, "cursor", cursorBridgeSource)
	if copilot != cursor {
		t.Fatal("the Copilot and Cursor marker filters have drifted apart")
	}
	for _, expected := range []string{
		`const TOOL_CALL_MARKER = "ASSISTANT TOOL CALL";`,
		`const TOOL_RESULT_MARKER = "CLAUDE CODE TOOL RESULT";`,
		"function createMarkerFilter(onSuppress) {",
		"function stripTranscriptMarkers(text, onSuppress) {",
	} {
		if !strings.Contains(copilot, expected) {
			t.Fatalf("the marker filter does not contain %q", expected)
		}
	}
}

// The transcript builder and the filter must agree on the exact label text, or
// the backstop stops matching what the prompt actually emits. One definition of
// each marker per bridge is what keeps them from drifting, so the literal must
// appear nowhere else in the file.
func TestBridgeMarkersHaveASingleDefinition(t *testing.T) {
	for _, bridge := range []struct {
		name   string
		source []byte
	}{
		{"copilot", copilotBridgeSource},
		{"cursor", cursorBridgeSource},
	} {
		text := string(bridge.source)
		for _, marker := range []string{"ASSISTANT TOOL CALL", "CLAUDE CODE TOOL RESULT"} {
			if count := strings.Count(text, marker); count != 1 {
				t.Fatalf("%s bridge spells %q %d times, want 1 (the constant)", bridge.name, marker, count)
			}
		}
		if !strings.Contains(text, "${TOOL_CALL_MARKER} ") || !strings.Contains(text, "${TOOL_RESULT_MARKER} ") {
			t.Fatalf("%s bridge no longer builds its transcript from the marker constants", bridge.name)
		}
		// Telling the model not to imitate the framing is the half of the fix
		// that keeps the backstop from ever having to fire.
		if !strings.Contains(text, "are transcript framing added by Claude Code") {
			t.Fatalf("%s bridge preamble does not warn against reproducing the markers", bridge.name)
		}
	}
}

// Copilot buffers a whole turn into one assistant.message event, so filtering
// the completed text covers the SSE and the JSON path at once. Cursor streams,
// so its answer deltas go through the filter on the way to the wire, its
// reasoning goes through a second one, and the non-streaming paths are filtered
// whole.
func TestBridgesApplyTheMarkerFilter(t *testing.T) {
	copilot := string(copilotBridgeSource)
	if !strings.Contains(copilot, "text = stripTranscriptMarkers(text, () => {") {
		t.Fatal("Copilot bridge does not filter the assistant text before building its response")
	}
	if strings.Index(copilot, "stripTranscriptMarkers(text") > strings.Index(copilot, "if (body.stream) {") {
		t.Fatal("Copilot bridge filters after the streaming branch, so streamed text bypasses it")
	}

	cursor := string(cursorBridgeSource)
	for _, expected := range []string{
		"const outputFilter = createMarkerFilter(() => logMarkerSuppression(\"assistant text\"));",
		"const reasoningFilter = createMarkerFilter(() => logMarkerSuppression(\"reasoning output\"));",
		"const visible = outputFilter.push(chunk);",
		"streamContent(reasoningFilter.push(update.text));",
		"const tail = outputFilter.flush();",
		"text = stripTranscriptMarkers(text, () => logMarkerSuppression(\"assistant text\"));",
		"text = stripTranscriptMarkers(result.result, () => logMarkerSuppression(\"assistant text\"));",
	} {
		if !strings.Contains(cursor, expected) {
			t.Fatalf("Cursor bridge does not contain %q", expected)
		}
	}
	// Dropping text with no trace makes the next report of this impossible to
	// diagnose, so a suppression is always recorded.
	if !strings.Contains(cursor, `cursor bridge: suppressed ${stream} imitating the transcript markers`) {
		t.Fatal("Cursor bridge suppresses assistant text without logging it")
	}
	if !strings.Contains(copilot, "copilot bridge: suppressed assistant text imitating the transcript markers") {
		t.Fatal("Copilot bridge suppresses assistant text without logging it")
	}
	// Tool dispatch is unaffected by the bug and must stay that way.
	if !strings.Contains(cursor, "capture(tool.name, args, context?.toolCallId);") {
		t.Fatal("Cursor bridge no longer captures forwarded tool calls unchanged")
	}
	if !strings.Contains(copilot, `} else if (event.type === "external_tool.requested") {`) {
		t.Fatal("Copilot bridge no longer captures forwarded tool calls unchanged")
	}
}

// The filter is pure string logic with a streaming case that has to withhold
// output until a match can be ruled out, which structural assertions cannot
// check. The block is self-contained by construction, so it runs under node on
// its own with a harness appended.
func TestBridgeMarkerFilterBehaviour(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	for _, bridge := range []struct {
		name   string
		source []byte
	}{
		{"copilot", copilotBridgeSource},
		{"cursor", cursorBridgeSource},
	} {
		t.Run(bridge.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "marker_filter_test.mjs")
			block := extractMarkerFilter(t, bridge.name, bridge.source)
			if err := os.WriteFile(script, []byte(block+markerFilterHarness), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(node, script).CombinedOutput()
			if err != nil {
				t.Fatalf("%s bridge marker filter: %v\n%s", bridge.name, err, output)
			}
		})
	}
}

func extractMarkerFilter(t *testing.T, name string, source []byte) string {
	t.Helper()
	text := string(source)
	start := strings.Index(text, markerFilterStart)
	end := strings.Index(text, markerFilterEnd)
	if start < 0 || end <= start {
		t.Fatalf("%s bridge no longer delimits its marker filter with %q/%q", name, markerFilterStart, markerFilterEnd)
	}
	return text[start:end]
}

// Written without template literals so it can live in a Go raw string.
const markerFilterHarness = `
const failures = [];
const check = (name, actual, expected) => {
  if (actual !== expected) {
    failures.push(name + ": got " + JSON.stringify(actual) + ", want " + JSON.stringify(expected));
  }
};
const strip = (text) => stripTranscriptMarkers(text);

const prose = "Here is the answer.\nIt spans two lines.\n";
check("prose survives", strip(prose), prose);
check("empty input", strip(""), "");

const imitatedCall = "Reading the file.\nASSISTANT TOOL CALL Read:\n{\"path\":\"a\"}\ntrailing\n";
check("imitated tool call", strip(imitatedCall), "Reading the file.\n");
const imitatedResult = "Done.\nCLAUDE CODE TOOL RESULT Bash:\nok\n";
check("imitated tool result", strip(imitatedResult), "Done.\n");
const leadingMarker = "ASSISTANT TOOL CALL Bash:\nls\n";
check("marker opens the reply", strip(leadingMarker), "");
check("marker without a closing newline", strip("Sure.\nASSISTANT TOOL CALL Bash:"), "Sure.\n");
check("marker padded with spaces", strip("Sure.\nASSISTANT TOOL CALL Bash:  \nx"), "Sure.\n");

// Someone pointing a dialect at this repository gets replies that legitimately
// discuss the labels. Anchoring at a line start and requiring the full
// "MARKER <tool>:" shape is what keeps those answers whole.
const discussion = "The ASSISTANT TOOL CALL Bash: label is framing.\n" +
  "ASSISTANT TOOL CALL lines are added by the harness.\n" +
  "CLAUDE CODE TOOL RESULT marks a result, but this line has no colon\n" +
  "  ASSISTANT TOOL CALL Bash: is indented, so it does not open a line\n" +
  "CLAUDE CODE TOOL RESULT Bash: marks a returned result, like this:\n" +
  "ASSISTANT TOOL CALL Bash: is how a call is framed, for example:\n";
check("discussing the labels", strip(discussion), discussion);
check("marker needs a tool name", strip("ASSISTANT TOOL CALL :\n"), "ASSISTANT TOOL CALL :\n");
// The name has the shape of an identifier, which is what separates a marker
// from a sentence that opens with a label and happens to end in a colon.
check(
  "namespaced tool name",
  strip("ok\nASSISTANT TOOL CALL mcp__devflow__devflow_start:\n{}\n"),
  "ok\n",
);
check("CRLF marker", strip("Sure.\r\nASSISTANT TOOL CALL Bash:\r\nargs\r\n"), "Sure.\r\n");

// An exact example is indistinguishable from an imitation by shape, so a reply
// explaining the format is only recoverable by where it puts that example.
// (Built from a char code because the Go raw string holding this cannot carry a
// backtick.)
const tick = String.fromCharCode(96);
const fence = tick.repeat(3);
const explanation = "The bridge frames a call like this:\n" +
  fence + "\nASSISTANT TOOL CALL Read:\n{\"path\":\"a\"}\n" + fence + "\n" +
  "and the result like this:\n" +
  "~~~\nCLAUDE CODE TOOL RESULT Read:\nfile contents\n~~~\n" +
  "That framing is added by the harness.\n";
check("fenced examples survive", strip(explanation), explanation);
// A closed fence must not leave the rest of the turn unguarded.
check(
  "marker after a closed fence",
  strip(fence + "\ncode\n" + fence + "\nASSISTANT TOOL CALL Read:\nx\n"),
  fence + "\ncode\n" + fence + "\n",
);
// The turn can also end on the example, inside a fence that never closed,
// which is the flush path rather than the line-completion path.
const openFence = fence + "\nASSISTANT TOOL CALL Read:";
check("turn ends inside a fence", strip(openFence), openFence);

// A fence closes only on its own character at its own length or longer. A reply
// documenting a fenced example wraps it in a longer fence, so the inner fence
// must not end the outer one and expose the example it contains.
const nested = "To document the framing, write:\n" +
  fence + tick + "\n" + fence + "\nASSISTANT TOOL CALL Read:\n{}\n" + fence + "\n" +
  fence + tick + "\nBack to prose.\n";
check("fence nested in a longer fence", strip(nested), nested);
// A tilde run does not close a backtick fence.
const mismatched = fence + "\n~~~\nCLAUDE CODE TOOL RESULT Read:\nout\n~~~\n" + fence + "\n";
check("mismatched fence delimiters", strip(mismatched), mismatched);
// A longer closing run is still a close, so what follows is guarded again.
check(
  "longer closing run still closes",
  strip(fence + "\ncode\n" + fence + tick + "\nASSISTANT TOOL CALL Read:\nx\n"),
  fence + "\ncode\n" + fence + tick + "\n",
);
// A close carries nothing but whitespace, so a same-delimiter line with an info
// string is content inside the block rather than its end.
const infoString = fence + "\n" + fence + "js\nASSISTANT TOOL CALL Read:\n{}\n" +
  fence + "\nDone.\n";
check("info string does not close a fence", strip(infoString), infoString);
check(
  "trailing whitespace still closes",
  strip(fence + "\ncode\n" + fence + "  \nASSISTANT TOOL CALL Read:\nx\n"),
  fence + "\ncode\n" + fence + "  \n",
);
// A fence opener tolerates up to three spaces; a fourth makes it indented code,
// so it must not put the filter into a fence state a marker could hide behind.
const indentedFence = "   " + fence + "\nASSISTANT TOOL CALL Read:\n{}\n   " + fence + "\n";
check("three-space indent still opens a fence", strip(indentedFence), indentedFence);
const pseudoFence = "    " + fence + "\nindented code\nASSISTANT TOOL CALL Read:\nx\n";
check("four-space indent is not a fence", strip(pseudoFence), "    " + fence + "\nindented code\n");

// Streaming must reach the same answer as the buffered path no matter where the
// chunk boundaries fall, including inside a marker.
const cases = [prose, discussion, imitatedCall, imitatedResult, leadingMarker, explanation,
  nested, mismatched, infoString, indentedFence, pseudoFence, "no newline in this reply"];
for (const source of cases) {
  const want = strip(source);
  for (let split = 0; split <= source.length; split += 1) {
    const filter = createMarkerFilter();
    let got = filter.push(source.slice(0, split));
    got += filter.push(source.slice(split));
    got += filter.flush();
    check("split " + split + " of " + JSON.stringify(source), got, want);
  }
  const filter = createMarkerFilter();
  let got = "";
  for (const character of source) got += filter.push(character);
  got += filter.flush();
  check("one character at a time: " + JSON.stringify(source), got, want);
}

let suppressions = 0;
const latching = createMarkerFilter(() => { suppressions += 1; });
check("text before the marker", latching.push("ok\n"), "ok\n");
check("the marker itself", latching.push("ASSISTANT TOOL CALL Bash:\n"), "");
check("the rest of the turn", latching.push("ASSISTANT TOOL CALL Read:\nmore\n"), "");
check("the final flush", latching.flush(), "");
check("suppression latches", latching.suppressed, true);
check("suppression is reported once", suppressions, 1);

// Line-boundary buffering, not whole-line buffering: a line whose opening
// diverges from both labels streams on at token granularity.
const granular = createMarkerFilter();
check("first token released", granular.push("Hello"), "Hello");
check("second token released", granular.push(" world"), " world");
const ambiguous = createMarkerFilter();
check("ambiguous opening withheld", ambiguous.push("A"), "");
check("ambiguous opening released", ambiguous.push("nswer:"), "Answer:");

// A completed label is not permanently ambiguous: the line is withheld only
// while what follows could still be a tool name, so an explanation opening with
// the label streams on instead of waiting for its newline.
const diverged = createMarkerFilter();
check("viable name withheld", diverged.push("ASSISTANT TOOL CALL Ba"), "");
check(
  "released once the name diverges",
  diverged.push("sh lines are framing"),
  "ASSISTANT TOOL CALL Bash lines are framing",
);
const viable = createMarkerFilter();
check("name still viable", viable.push("ASSISTANT TOOL CALL Bash"), "");
check("colon still viable", viable.push(":"), "");
check("marker completes on the newline", viable.push("\n"), "");
check("and suppresses", viable.suppressed, true);

// Inside a fence nothing is suppressed, so nothing is withheld either.
const fenced = createMarkerFilter();
check("fence opener released", fenced.push(fence + "\n"), fence + "\n");
check(
  "marker text streams inside a fence",
  fenced.push("ASSISTANT TOOL CALL Ba"),
  "ASSISTANT TOOL CALL Ba",
);

if (failures.length > 0) {
  console.error(failures.join("\n"));
  process.exit(1);
}
`
