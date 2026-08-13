package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCopilotBridgeContinuesAfterTranscriptSpill(t *testing.T) {
	port := startCopilotSpillFixture(
		t,
		"Bash was interrupted • Interrupted by user\n\nUSER:\nActually, continue the task.\n---NEXT---\n",
	)
	completion := sendCopilotSpillRequest(t, port)
	if completion.FinishReason != "tool_calls" || completion.ToolName != "Bash" {
		t.Fatalf("completion = %#v, want a Bash tool call after continuation", completion)
	}
}

func TestCopilotBridgePromotesNeutralTranscriptToolCall(t *testing.T) {
	port := startCopilotSpillFixture(
		t,
		"I will test one.\n\nTOOL HISTORY Bash:\n{\"command\":\"printf test\"}",
	)
	completion := sendCopilotSpillRequest(t, port)
	if completion.FinishReason != "tool_calls" || completion.ToolName != "Bash" {
		t.Fatalf("completion = %#v, want a promoted Bash tool call", completion)
	}
}

type copilotSpillCompletion struct {
	FinishReason string
	ToolName     string
}

func sendCopilotSpillRequest(t *testing.T, port int) copilotSpillCompletion {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port),
		bytes.NewReader([]byte(`{
			"model": "gpt-5.6-luna",
			"messages": [{"role": "user", "content": "test"}],
			"tools": [{
				"type": "function",
				"function": {
					"name": "Bash",
					"parameters": {"type": "object", "properties": {}}
				}
			}]
		}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fixture-key")
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %s", response.Status)
	}

	var body struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Choices) != 1 {
		t.Fatalf("choices = %#v, want one choice", body.Choices)
	}
	result := copilotSpillCompletion{FinishReason: body.Choices[0].FinishReason}
	if len(body.Choices[0].Message.ToolCalls) == 1 {
		result.ToolName = body.Choices[0].Message.ToolCalls[0].Function.Name
	}
	return result
}

func startCopilotSpillFixture(t *testing.T, content string) int {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node.js is required for the Copilot bridge integration test")
	}
	root := t.TempDir()
	packageDir := filepath.Join(root, "node_modules", "@github", "copilot-sdk")
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "package.json"), []byte(`{"name":"@github/copilot-sdk","type":"module","main":"index.mjs"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeSDK := `
class FakeSession {
  handler;
  sendCount = 0;
  on(handler) { this.handler = handler; }
  async send() {
    const continuation = this.sendCount++ > 0;
    if (continuation && process.env.FAKE_EXTERNAL_TOOL_AFTER_FIRST === "1") {
      this.handler({
        type: "external_tool.requested",
        data: {
          toolName: "Bash",
          toolCallId: "fixture-call",
          arguments: { command: "printf test" },
        },
      });
    } else {
      this.handler({
        type: "assistant.message",
        data: {
          content: process.env.FAKE_ASSISTANT_CONTENT || "ok",
          model: "gpt-5.6-luna",
        },
      });
    }
    this.handler({ type: "session.idle" });
  }
  async abort() {}
  async disconnect() {}
}
export class CopilotClient {
  constructor() {}
  async start() {}
  async stop() {}
  async getAuthStatus() { return { isAuthenticated: true }; }
  async listModels() {
    return [{
      id: "gpt-5.6-luna",
      capabilities: { supports: { reasoningEffort: false } },
    }];
  }
  async createSession() { return new FakeSession(); }
}
`
	if err := os.WriteFile(filepath.Join(packageDir, "index.mjs"), []byte(fakeSDK), 0o600); err != nil {
		t.Fatal(err)
	}
	bridgePath := filepath.Join(root, "copilot_bridge.mjs")
	if err := os.WriteFile(bridgePath, copilotBridgeSource, 0o600); err != nil {
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
		"FAKE_ASSISTANT_CONTENT="+content,
	)
	if strings.Contains(content, "\n---NEXT---\n") {
		cmd.Env = append(cmd.Env, "FAKE_EXTERNAL_TOOL_AFTER_FIRST=1")
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
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
				return port
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("bridge did not become ready")
	return 0
}
