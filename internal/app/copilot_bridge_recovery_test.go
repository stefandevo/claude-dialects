package app

import (
	"bytes"
	"context"
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

func TestCopilotBridgeContinuesTranscriptSpillInFreshSession(t *testing.T) {
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
		scenario: "spill-once",
		content:  "Bash was interrupted • Interrupted by user\n\nUSER:\nActually, continue the task.\n",
	})
	completion := fixture.completion(t)
	if completion.FinishReason != "tool_calls" || completion.ToolName != "Bash" {
		t.Fatalf("completion = %#v, want a Bash tool call after continuation", completion)
	}
	fixture.requireLog(t,
		"create:0", "send:0", "unsubscribe:0", "disconnect:0",
		"create:1", "send:1", "abort:1", "unsubscribe:1", "disconnect:1",
	)
}

func TestCopilotBridgeContinuesAfterMarkerImitation(t *testing.T) {
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
		scenario: "spill-once",
		content:  "I will inspect that.\nTOOL HISTORY Bash:\n{\"command\":\"printf test\"}\n",
	})
	completion := fixture.completion(t)
	if completion.FinishReason != "tool_calls" || completion.ToolName != "Bash" {
		t.Fatalf("completion = %#v, want a Bash tool call after marker spill", completion)
	}
	fixture.requireLog(t,
		"create:0", "send:0", "unsubscribe:0", "disconnect:0",
		"create:1", "send:1", "abort:1", "unsubscribe:1", "disconnect:1",
	)
}

func TestCopilotBridgeRetriesTurnStartWithNoWork(t *testing.T) {
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
		scenario: "timeout-once",
		timeout:  40 * time.Millisecond,
	})
	completion := fixture.completion(t)
	if completion.FinishReason != "tool_calls" || completion.ToolName != "Bash" {
		t.Fatalf("completion = %#v, want a Bash tool call after timeout retry", completion)
	}
	fixture.requireLog(t,
		"create:0", "send:0", "abort:0", "unsubscribe:0", "disconnect:0",
		"create:1", "send:1", "abort:1", "unsubscribe:1", "disconnect:1",
	)
}

func TestCopilotBridgeStopsAfterSecondEmptyTimeout(t *testing.T) {
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
		scenario: "timeout-always",
		timeout:  40 * time.Millisecond,
	})
	started := time.Now()
	status, code, _ := fixture.request(t, context.Background())
	if status != http.StatusInternalServerError || code != "copilot_timeout" {
		t.Fatalf("status/code = %d/%q, want 500/copilot_timeout", status, code)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("exhausted retry took %s, want a prompt bounded failure", elapsed)
	}
	fixture.requireLog(t,
		"create:0", "send:0", "abort:0", "unsubscribe:0", "disconnect:0",
		"create:1", "send:1", "abort:1", "unsubscribe:1", "disconnect:1",
	)
}

func TestCopilotBridgeDoesNotRetryTimeoutAfterPartialText(t *testing.T) {
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
		scenario: "partial-timeout",
		timeout:  40 * time.Millisecond,
	})
	status, code, _ := fixture.request(t, context.Background())
	if status != http.StatusInternalServerError || code != "copilot_timeout" {
		t.Fatalf("status/code = %d/%q, want 500/copilot_timeout", status, code)
	}
	fixture.requireLog(t,
		"create:0", "send:0", "abort:0", "unsubscribe:0", "disconnect:0",
	)
}

func TestCopilotBridgeDoesNotRetrySessionError(t *testing.T) {
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{scenario: "session-error"})
	status, code, _ := fixture.request(t, context.Background())
	if status != http.StatusInternalServerError || code != "copilot_session_error" {
		t.Fatalf("status/code = %d/%q, want 500/copilot_session_error", status, code)
	}
	fixture.requireLog(t, "create:0", "send:0", "unsubscribe:0", "disconnect:0")
}

func TestCopilotBridgeDoesNotRetryFencedTranscriptExamples(t *testing.T) {
	content := "The flattened format is:\n```text\nUSER:\nhello\nASSISTANT:\n<invoke>\n<parameter>\n```\nThe names <invoke> and <parameter> are only documentation."
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
		scenario: "answer",
		content:  content,
	})
	completion := fixture.completion(t)
	if completion.FinishReason != "stop" || completion.Text != content {
		t.Fatalf("completion = %#v, want the fenced example unchanged", completion)
	}
	fixture.requireLog(t, "create:0", "send:0", "unsubscribe:0", "disconnect:0")
}

func TestCopilotBridgeDoesNotRetryUnfencedTranscriptExamples(t *testing.T) {
	for name, content := range map[string]string{
		"role headings": "An unfenced conversation example follows:\nUSER:\nhello\nASSISTANT:\nhi",
		"tool markup":   "An unfenced tool example follows:\n<invoke name=\"Read\">\n<parameter name=\"path\">README.md</parameter>\n</invoke>",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
				scenario: "answer",
				content:  content,
			})
			completion := fixture.completion(t)
			if completion.FinishReason != "stop" || completion.Text != content {
				t.Fatalf("completion = %#v, want the unfenced example unchanged", completion)
			}
			fixture.requireLog(t, "create:0", "send:0", "unsubscribe:0", "disconnect:0")
		})
	}
}

func TestCopilotBridgeContinuesCombinedTranscriptSpill(t *testing.T) {
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
		scenario: "spill-once",
		content:  "Continuing.\n<invoke name=\"Read\">\n<parameter name=\"path\">README.md</parameter>\n</invoke>\n\nUSER:\nPlease continue.",
	})
	completion := fixture.completion(t)
	if completion.FinishReason != "tool_calls" || completion.ToolName != "Bash" {
		t.Fatalf("completion = %#v, want a Bash tool call after combined transcript spill", completion)
	}
	fixture.requireLog(t,
		"create:0", "send:0", "unsubscribe:0", "disconnect:0",
		"create:1", "send:1", "abort:1", "unsubscribe:1", "disconnect:1",
	)
}

func TestCopilotBridgeAccumulatesAssistantMessageChunks(t *testing.T) {
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
		scenario: "answer",
		content:  "first chunk\n---CHUNK---\n and second chunk",
	})
	completion := fixture.completion(t)
	if completion.FinishReason != "stop" || completion.Text != "first chunk and second chunk" {
		t.Fatalf("completion = %#v, want both assistant.message chunks", completion)
	}
	fixture.requireLog(t, "create:0", "send:0", "unsubscribe:0", "disconnect:0")
}

func TestCopilotBridgeDoesNotRetryAfterClientDisconnect(t *testing.T) {
	fixture := startCopilotRecoveryFixture(t, copilotRecoveryFixture{
		scenario: "timeout-always",
		timeout:  60 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		request, err := fixture.newRequest(ctx)
		if err != nil {
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	fixture.waitForLog(t, "send:0")
	cancel()
	<-done
	time.Sleep(3 * fixture.timeout)
	log := fixture.log(t)
	if strings.Count(log, "create:") != 1 || strings.Count(log, "send:") != 1 {
		t.Fatalf("bridge retried an abandoned request:\n%s", log)
	}
}

type copilotRecoveryCompletion struct {
	FinishReason string
	ToolName     string
	Text         string
}

type copilotRecoveryFixture struct {
	scenario string
	content  string
	timeout  time.Duration
}

type runningCopilotRecoveryFixture struct {
	port    int
	logPath string
	timeout time.Duration
}

func (fixture *runningCopilotRecoveryFixture) completion(t *testing.T) copilotRecoveryCompletion {
	t.Helper()
	status, code, body := fixture.request(t, context.Background())
	if status != http.StatusOK {
		t.Fatalf("status/code = %d/%q, want 200; body = %s", status, code, body)
	}
	var response struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 {
		t.Fatalf("choices = %#v, want one choice", response.Choices)
	}
	result := copilotRecoveryCompletion{
		FinishReason: response.Choices[0].FinishReason,
		Text:         response.Choices[0].Message.Content,
	}
	if len(response.Choices[0].Message.ToolCalls) == 1 {
		result.ToolName = response.Choices[0].Message.ToolCalls[0].Function.Name
	}
	return result
}

func (fixture *runningCopilotRecoveryFixture) request(t *testing.T, ctx context.Context) (int, string, []byte) {
	t.Helper()
	request, err := fixture.newRequest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &envelope)
	return response.StatusCode, envelope.Error.Code, body
}

func (fixture *runningCopilotRecoveryFixture) newRequest(ctx context.Context) (*http.Request, error) {
	body := []byte(`{
		"model": "gpt-fixture",
		"messages": [{"role": "user", "content": "test"}],
		"tools": [{
			"type": "function",
			"function": {
				"name": "Bash",
				"parameters": {"type": "object", "properties": {}}
			}
		}]
	}`)
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", fixture.port),
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer fixture-key")
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func (fixture *runningCopilotRecoveryFixture) requireLog(t *testing.T, entries ...string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		log := fixture.log(t)
		lines := strings.Fields(log)
		if len(lines) >= len(entries) {
			if got := strings.Join(lines, " "); got != strings.Join(entries, " ") {
				t.Fatalf("fixture log:\n%s\nwant:\n%s", log, strings.Join(entries, "\n"))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture log did not reach %d entries:\n%s", len(entries), log)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (fixture *runningCopilotRecoveryFixture) waitForLog(t *testing.T, entry string) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if strings.Contains(fixture.log(t), entry) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fixture log never contained %q:\n%s", entry, fixture.log(t))
}

func (fixture *runningCopilotRecoveryFixture) log(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(fixture.logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(content)
}

func startCopilotRecoveryFixture(t *testing.T, config copilotRecoveryFixture) *runningCopilotRecoveryFixture {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if config.timeout <= 0 {
		config.timeout = 100 * time.Millisecond
	}
	root := t.TempDir()
	packageDir := filepath.Join(root, "node_modules", "@github", "copilot-sdk")
	if err = os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(root, "package.json"):       `{"type":"module"}`,
		filepath.Join(packageDir, "package.json"): `{"name":"@github/copilot-sdk","type":"module","main":"index.mjs"}`,
		filepath.Join(packageDir, "index.mjs"):    fakeCopilotRecoverySDK,
	} {
		if err = os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bridgePath := filepath.Join(root, "copilot_bridge.mjs")
	if err = os.WriteFile(bridgePath, copilotBridgeSource, 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "fixture.log")
	bridgeOutput := filepath.Join(root, "bridge.log")
	output, err := os.OpenFile(bridgeOutput, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	port := availablePortRange(t, 1)
	cmd := exec.Command(nodePath, bridgePath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--state", filepath.Join(root, "state"),
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"COPILOT_DIALECT_BRIDGE_KEY=fixture-key",
		"COPILOT_BRIDGE_TIMEOUT_MS="+strconv.FormatInt(config.timeout.Milliseconds(), 10),
		"FAKE_ASSISTANT_CONTENT="+config.content,
		"FAKE_LOG="+logPath,
		"FAKE_SCENARIO="+config.scenario,
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
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
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
				return &runningCopilotRecoveryFixture{port: port, logPath: logPath, timeout: config.timeout}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	content, _ := os.ReadFile(bridgeOutput)
	t.Fatalf("bridge did not become ready:\n%s", content)
	return nil
}

const fakeCopilotRecoverySDK = `
import fs from "node:fs";
const scenario = process.env.FAKE_SCENARIO || "answer";
const content = process.env.FAKE_ASSISTANT_CONTENT || "ok";
const writeLog = (entry) => fs.appendFileSync(process.env.FAKE_LOG, entry + "\n");

class FakeSession {
  constructor(index) {
    this.index = index;
    this.handlers = new Set();
  }
  on(handler) {
    this.handlers.add(handler);
    return () => {
      if (this.handlers.delete(handler)) writeLog("unsubscribe:" + this.index);
    };
  }
  emit(event) {
    for (const handler of this.handlers) handler(event);
  }
  async send() {
    writeLog("send:" + this.index);
    if (
      scenario === "timeout-always"
      || scenario === "partial-timeout"
      || (scenario === "timeout-once" && this.index === 0)
    ) {
      this.emit({ type: "assistant.turn_start", data: { turnId: String(this.index) } });
      if (scenario === "partial-timeout") {
        this.emit({ type: "assistant.message", data: { content: "partial output" } });
      }
      return;
    }
    if (scenario === "session-error") {
      this.emit({ type: "session.error", data: { message: "fixture failure" } });
      return;
    }
    if (scenario === "spill-once" && this.index === 0) {
      this.emit({ type: "assistant.message", data: { content } });
      this.emit({ type: "assistant.turn_end", data: { turnId: String(this.index) } });
      this.emit({ type: "session.idle", data: {} });
      return;
    }
    if (scenario === "answer") {
      const chunks = content.split("\n---CHUNK---\n");
      chunks.forEach((chunk, chunkIndex) => this.emit({
        type: "assistant.message",
        data: { content: chunk, chunkIndex, chunkCount: chunks.length },
      }));
      this.emit({ type: "session.idle", data: {} });
      return;
    }
    this.emit({
      type: "external_tool.requested",
      data: {
        requestId: "fixture-request",
        sessionId: "fixture-session-" + this.index,
        toolName: "Bash",
        toolCallId: "fixture-call",
        arguments: { command: "printf test" },
      },
    });
  }
  async abort() { writeLog("abort:" + this.index); }
  async disconnect() { writeLog("disconnect:" + this.index); }
}

export class CopilotClient {
  constructor() { this.sessionCount = 0; }
  async start() {}
  async stop() {}
  async getAuthStatus() { return { isAuthenticated: true }; }
  async listModels() {
    return [{
      id: "gpt-fixture",
      capabilities: { supports: { reasoningEffort: false } },
    }];
  }
  async createSession() {
    const index = this.sessionCount++;
    writeLog("create:" + index);
    return new FakeSession(index);
  }
}
`
