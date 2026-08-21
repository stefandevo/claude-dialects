package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Distinct per stream, so a substring match proves which stream a chunk came
// from rather than only that some text arrived.
const (
	fixtureReasoning = "REASONING-ALPHA reached for the file\n"
	fixtureSummary   = "SUMMARY-BRAVO checked the config"
	fixtureAnswer    = "ANSWER-CHARLIE the port is 8080"
)

// The bug this guards is a wire-format bug: reasoning left the bridge in the
// field that carries the answer, so nothing downstream could tell them apart.
// Every other Cursor bridge test asserts on the embedded source, which cannot
// see what the SSE body actually looks like — and the source assertions alone
// would still pass if the new writer emitted the wrong field.
func TestCursorBridgeSendsReasoningAndAnswerOnSeparateSSEFields(t *testing.T) {
	port := startCursorReasoningBridge(t)

	body := map[string]any{
		"model":  "auto",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "which port?"},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port),
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fixture-key")
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("bridge answered %d: %s", response.StatusCode, raw)
	}

	var content, reasoning strings.Builder
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err = json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("unparsable SSE chunk %q: %v", data, err)
		}
		if chunk.Error != nil {
			t.Fatalf("bridge streamed an error: %s", chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			content.WriteString(choice.Delta.Content)
			reasoning.WriteString(choice.Delta.ReasoningContent)
		}
	}
	if err = scanner.Err(); err != nil {
		t.Fatal(err)
	}

	streamedContent := content.String()
	streamedReasoning := reasoning.String()

	// The answer still travels as delta.content, unchanged.
	if !strings.Contains(streamedContent, fixtureAnswer) {
		t.Fatalf("delta.content did not carry the answer: %q", streamedContent)
	}
	// Thinking and summaries travel as delta.reasoning_content, which
	// CLIProxyAPI turns into Anthropic thinking blocks. Losing them here would
	// mean a thinking-heavy turn goes silent, so assert they are still sent.
	for _, expected := range []string{strings.TrimSpace(fixtureReasoning), fixtureSummary} {
		if !strings.Contains(streamedReasoning, expected) {
			t.Fatalf("delta.reasoning_content did not carry %q: %q", expected, streamedReasoning)
		}
	}
	// The regression itself: nothing from the reasoning stream may appear on the
	// channel Claude Code records as assistant content.
	for _, leaked := range []string{"REASONING-ALPHA", "SUMMARY-BRAVO"} {
		if strings.Contains(streamedContent, leaked) {
			t.Fatalf("delta.content leaked reasoning %q: %q", leaked, streamedContent)
		}
	}
	if strings.Contains(streamedReasoning, "ANSWER-CHARLIE") {
		t.Fatalf("delta.reasoning_content leaked the answer: %q", streamedReasoning)
	}
}

// startCursorReasoningBridge runs the embedded bridge under node against a fake
// @cursor/sdk and returns its port. There is no other live Cursor bridge
// fixture; the Copilot one (startCopilotRecoveryFixture) is the pattern.
func startCursorReasoningBridge(t *testing.T) int {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	root := t.TempDir()
	packageDir := filepath.Join(root, "node_modules", "@cursor", "sdk")
	if err = os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(root, "package.json"):       `{"type":"module"}`,
		filepath.Join(packageDir, "package.json"): `{"name":"@cursor/sdk","type":"module","main":"index.mjs"}`,
		filepath.Join(packageDir, "index.mjs"):    fakeCursorReasoningSDK,
	} {
		if err = os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bridgePath := filepath.Join(root, "cursor_bridge.mjs")
	if err = os.WriteFile(bridgePath, cursorBridgeSource, 0o600); err != nil {
		t.Fatal(err)
	}
	bridgeOutput := filepath.Join(root, "bridge.log")
	output, err := os.OpenFile(bridgeOutput, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	port := availablePortRange(t, 1)
	cmd := exec.Command(nodePath, bridgePath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--workspace", filepath.Join(root, "workspace"),
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"CURSOR_DIALECT_BRIDGE_KEY=fixture-key",
		"CURSOR_API_KEY=fixture-api-key",
		"FAKE_REASONING="+fixtureReasoning,
		"FAKE_SUMMARY="+fixtureSummary,
		"FAKE_ANSWER="+fixtureAnswer,
	)
	cmd.Stdout, cmd.Stderr = output, output
	if err = cmd.Start(); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-waitDone
		}
		_ = output.Close()
	})

	client := &http.Client{Timeout: 500 * time.Millisecond}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		request, requestErr := http.NewRequest(
			http.MethodGet,
			fmt.Sprintf("http://127.0.0.1:%d/health", port),
			nil,
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer fixture-key")
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return port
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	content, _ := os.ReadFile(bridgeOutput)
	t.Fatalf("bridge did not become ready:\n%s", content)
	return 0
}

// Stubs exactly the three symbols cursor_bridge.mjs imports. The run reports no
// assistant content of its own, so the reply is whatever onDelta streamed —
// which is the point: this fixture exercises the streaming delta path.
const fakeCursorReasoningSDK = `
const reasoning = process.env.FAKE_REASONING || "";
const summary = process.env.FAKE_SUMMARY || "";
const answer = process.env.FAKE_ANSWER || "";

class FakeRun {
  constructor() { this.status = "running"; }
  async *stream() {}
  async cancel() { this.status = "cancelled"; }
  async wait() {
    this.status = "completed";
    return { status: "completed", result: "", usage: { inputTokens: 11, outputTokens: 22 } };
  }
}

class FakeAgent {
  async send(prompt, options) {
    const onDelta = options?.onDelta;
    if (onDelta) {
      onDelta({ update: { type: "thinking-delta", text: reasoning } });
      onDelta({ update: { type: "summary-started" } });
      onDelta({ update: { type: "summary", summary } });
      onDelta({ update: { type: "text-delta", text: answer } });
    }
    return new FakeRun();
  }
  close() {}
}

export const Agent = { async create() { return new FakeAgent(); } };
export const Cursor = { models: { list: async () => [] } };
export class JsonlLocalAgentStore {
  constructor(directory) { this.directory = directory; }
}
`
