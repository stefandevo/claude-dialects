#!/usr/bin/env node

import { CopilotClient } from "@github/copilot-sdk";
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import path from "node:path";

const options = parseArgs(process.argv.slice(2));
const host = options.host || "127.0.0.1";
const port = Number(options.port || 0);
const bridgeKey = process.env.COPILOT_DIALECT_BRIDGE_KEY || "";
const stateDir = path.resolve(options.state || process.env.COPILOT_DIALECT_HOME || process.cwd());
let modelCatalogCache;
let modelCatalogExpiresAt = 0;

// Module-private marker for errors this bridge creates for the client. A Symbol
// can never be forged by a third-party SDK/HTTP error (unlike the public
// `expose` convention), so only our own readJSON errors expose their message.
const CLIENT_SAFE = Symbol("clientSafe");

if (!Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error("--port must be a valid TCP port");
}
if (!bridgeKey) {
  throw new Error("COPILOT_DIALECT_BRIDGE_KEY is required");
}
fs.mkdirSync(stateDir, { recursive: true, mode: 0o700 });

const client = new CopilotClient({
  mode: "empty",
  baseDirectory: stateDir,
  workingDirectory: stateDir,
  logLevel: "error",
  useLoggedInUser: true,
});
await client.start();

const server = http.createServer(async (request, response) => {
  try {
    const url = new URL(request.url || "/", `http://${request.headers.host || host}`);
    if (url.pathname === "/health" && request.method === "GET") {
      if (!authorized(request)) return unauthorized(response);
      const auth = await client.getAuthStatus().catch(() => ({ isAuthenticated: false }));
      return json(response, 200, {
        ok: true,
        provider: "copilot",
        sdk: "official",
        authenticated: Boolean(auth?.isAuthenticated),
      });
    }
    if (url.pathname === "/v1/models" && request.method === "GET") {
      if (!authorized(request)) return unauthorized(response);
      const items = await listCopilotModels();
      return json(response, 200, {
        object: "list",
        data: items.map((item) => ({
          id: item.id,
          object: "model",
          created: 0,
          owned_by: "github-copilot",
          name: item.name || item.id,
          capabilities: item.capabilities,
          supported_reasoning_efforts: item.supportedReasoningEfforts,
        })),
      });
    }
    if (url.pathname === "/v1/chat/completions" && request.method === "POST") {
      if (!authorized(request)) return unauthorized(response);
      const body = await readJSON(request);
      return await chatCompletion(request, response, body);
    }
    return json(response, 404, {
      error: { message: "Not found", type: "invalid_request_error", code: "not_found" },
    });
  } catch (error) {
    if (!response.headersSent) {
      // Only errors this bridge creates for the client (400/413 from readJSON)
      // carry the CLIENT_SAFE marker and may return their message. Everything
      // else — including SDK/API failures that happen to carry a numeric status
      // or a public `expose` flag — is unexpected: log it server-side and return
      // a generic message so internal detail (SDK internals, stack traces)
      // never reaches the client.
      const expose = error?.[CLIENT_SAFE] === true;
      if (!expose) {
        process.stderr.write(
          `copilot bridge error: ${
            error instanceof Error ? error.stack || error.message : String(error)
          }\n`,
        );
      }
      return json(response, expose ? error.status : 500, {
        error: {
          message: expose
            ? (error instanceof Error ? error.message : String(error))
            : "Internal server error",
          type: "api_error",
          code: error?.code || "copilot_bridge_error",
        },
      });
    }
    response.end();
  }
});

server.listen(port, host, () => {
  process.stdout.write(`GitHub Copilot SDK bridge listening on http://${host}:${port}\n`);
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    server.close(async () => {
      await client.stop().catch(() => {});
      process.exit(0);
    });
  });
}

async function chatCompletion(request, response, body) {
  const requestedModel = typeof body.model === "string" && body.model ? body.model : "auto";
  const modelSelection = await selectModel(requestedModel, body);
  const toolDefinitions = normalizeTools(body.tools);
  const prompt = buildPrompt(body.messages, toolDefinitions);
  let session;
  let text = "";
  let capturedToolCall;
  let disconnected = false;

  response.once("close", () => {
    if (!response.writableEnded) {
      disconnected = true;
      session?.abort().catch(() => {});
    }
  });

  try {
    session = await client.createSession({
      clientName: "claude-dialects",
      model: modelSelection.model,
      ...(modelSelection.reasoningEffort
        ? { reasoningEffort: modelSelection.reasoningEffort }
        : {}),
      tools: toolDefinitions.map((tool) => ({
        name: tool.name,
        description: tool.description,
        parameters: tool.parameters,
        skipPermission: true,
        defer: "never",
      })),
      availableTools: ["custom:*"],
      enableConfigDiscovery: false,
      infiniteSessions: { enabled: false },
      memory: { enabled: false },
      systemMessage: {
        mode: "replace",
        content: [
          "You are the model running inside the Claude Code harness.",
          "Claude Code owns all filesystem, terminal, web, MCP, and tool execution.",
          "Use only the custom tools supplied by Claude Code. When a tool is needed, call it and stop that turn.",
          "Keep going: do not stop after narrating a step or describing what you will do. Only stop once the task is fully done or you need user input; when work remains, call the next appropriate tool instead of ending with a plan.",
          "Transcript history entries are context only. Never reproduce them as your response; call the tool instead.",
          `Lines beginning with ${TOOL_CALL_MARKER} or ${TOOL_RESULT_MARKER} are transcript framing added by Claude Code to show you the conversation so far. Never write such a line yourself: call the tool instead.`,
        ].join("\n"),
      },
    });

    const turn = waitForTurn(session, toolDefinitions, (value) => {
      text = value;
    }, (value) => {
      capturedToolCall = value;
    });
    await session.send({ prompt });
    await turn;
    if (!capturedToolCall && transcriptSpill(text)) {
      text = "";
      const continuation = waitForTurn(session, toolDefinitions, (value) => {
        text = value;
      }, (value) => {
        capturedToolCall = value;
      });
      await session.send({
        prompt: "Continue the task. Do not quote or reproduce transcript history. If work remains, call the next appropriate tool now; otherwise provide the final answer.",
      });
      await continuation;
    }
  } finally {
    await session?.disconnect().catch(() => {});
  }

  if (disconnected) return;
  const transcriptTool = promoteTranscriptToolCall(text, toolDefinitions);
  if (!capturedToolCall && transcriptTool) {
    capturedToolCall = transcriptTool.call;
    text = transcriptTool.text;
  }
  // The whole reply arrives in one assistant.message event, so both the SSE and
  // the JSON path can be covered by filtering the buffered text once, here,
  // before anything is written to the wire.
  text = stripTranscriptMarkers(text, () => {
    process.stderr.write(
      "copilot bridge: suppressed assistant text imitating the transcript markers\n",
    );
  });
  const id = `chatcmpl_${crypto.randomUUID().replaceAll("-", "")}`;
  const created = Math.floor(Date.now() / 1000);
  const usage = estimatedUsage(prompt, text, capturedToolCall);

  if (body.stream) {
    response.writeHead(200, {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
      connection: "keep-alive",
    });
    writeSSE(response, {
      id,
      object: "chat.completion.chunk",
      created,
      model: requestedModel,
      choices: [{ index: 0, delta: { role: "assistant" }, finish_reason: null }],
    });
    if (text) {
      writeSSE(response, {
        id,
        object: "chat.completion.chunk",
        created,
        model: requestedModel,
        choices: [{ index: 0, delta: { content: text }, finish_reason: null }],
      });
    }
    if (capturedToolCall) {
      writeSSE(response, {
        id,
        object: "chat.completion.chunk",
        created,
        model: requestedModel,
        choices: [{
          index: 0,
          delta: {
            tool_calls: [{
              index: 0,
              id: capturedToolCall.id,
              type: "function",
              function: {
                name: capturedToolCall.name,
                arguments: JSON.stringify(capturedToolCall.arguments),
              },
            }],
          },
          finish_reason: null,
        }],
      });
    }
    writeSSE(response, {
      id,
      object: "chat.completion.chunk",
      created,
      model: requestedModel,
      choices: [{
        index: 0,
        delta: {},
        finish_reason: capturedToolCall ? "tool_calls" : "stop",
      }],
      usage,
    });
    response.write("data: [DONE]\n\n");
    return response.end();
  }

  const message = { role: "assistant", content: text || null };
  if (capturedToolCall) {
    message.tool_calls = [{
      id: capturedToolCall.id,
      type: "function",
      function: {
        name: capturedToolCall.name,
        arguments: JSON.stringify(capturedToolCall.arguments),
      },
    }];
  }
  return json(response, 200, {
    id,
    object: "chat.completion",
    created,
    model: requestedModel,
    choices: [{
      index: 0,
      message,
      finish_reason: capturedToolCall ? "tool_calls" : "stop",
    }],
    usage,
  });
}

function waitForTurn(session, tools, setText, setToolCall) {
  return new Promise((resolve, reject) => {
    let settled = false;
    const finish = (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      error ? reject(error) : resolve();
    };
    const timeout = setTimeout(() => {
      session.abort().catch(() => {});
      const error = new Error("GitHub Copilot SDK request timed out");
      error.code = "copilot_timeout";
      finish(error);
    }, 10 * 60 * 1000);

    session.on((event) => {
      if (event.type === "assistant.message" && typeof event.data?.content === "string") {
        setText(event.data.content);
      } else if (event.type === "external_tool.requested") {
        const tool = findTool(tools, event.data?.toolName);
        if (!tool) return;
        setToolCall({
          id: event.data.toolCallId || `call_${crypto.randomUUID().replaceAll("-", "")}`,
          name: tool.name,
          arguments: event.data.arguments && typeof event.data.arguments === "object"
            ? event.data.arguments
            : {},
        });
        session.abort().catch(() => {}).finally(() => finish());
      } else if (event.type === "session.idle") {
        finish();
      } else if (event.type === "session.error") {
        const error = new Error(event.data?.message || "GitHub Copilot SDK session failed");
        error.code = "copilot_session_error";
        finish(error);
      }
    });
  });
}

async function listCopilotModels() {
  if (modelCatalogCache && Date.now() < modelCatalogExpiresAt) return modelCatalogCache;
  const items = await client.listModels();
  modelCatalogCache = items.filter((item) => item?.id && item.policy?.state !== "disabled");
  modelCatalogExpiresAt = Date.now() + 5 * 60 * 1000;
  return modelCatalogCache;
}

async function selectModel(model, body) {
  if (model === "auto") return { model: "auto" };
  const catalog = await listCopilotModels().catch(() => []);
  const definition = catalog.find((item) => item.id === model);
  if (!definition) return { model };
  const effort = requestedEffort(body);
  if (!effort || !definition.capabilities?.supports?.reasoningEffort) return { model };
  const supported = definition.supportedReasoningEfforts || [];
  for (const candidate of effortCandidates(effort)) {
    if (supported.includes(candidate)) return { model, reasoningEffort: candidate };
  }
  return { model };
}

function requestedEffort(body) {
  const explicit = body?.reasoning_effort || body?.reasoning?.effort || body?.effort;
  if (typeof explicit === "string" && explicit) return normalizeEffort(explicit);
  const budget = body?.thinking?.budget_tokens;
  if (typeof budget === "number" && budget > 0) {
    if (budget >= 32_000) return "xhigh";
    if (budget >= 16_000) return "high";
    if (budget >= 8_000) return "medium";
    return "low";
  }
  return "";
}

function effortCandidates(effort) {
  switch (normalizeEffort(effort)) {
    case "max": return ["xhigh", "high"];
    case "xhigh": return ["xhigh", "high"];
    case "medium": return ["medium"];
    case "low": return ["low"];
    default: return [normalizeEffort(effort)];
  }
}

function normalizeEffort(value) {
  return String(value || "").toLowerCase().replaceAll(/[^a-z0-9]+/g, "");
}

function buildPrompt(messages, tools) {
  const toolNames = new Map();
  for (const message of messages || []) {
    for (const call of message?.tool_calls || []) {
      if (call?.id && call?.function?.name) toolNames.set(call.id, call.function.name);
    }
  }
  const transcript = [];
  for (const message of messages || []) {
    const role = String(message?.role || "user").toUpperCase();
    const content = contentText(message?.content);
    if (content && message?.role !== "tool") transcript.push(`${role}:\n${content}`);
    for (const call of message?.tool_calls || []) {
      transcript.push(
        `${TOOL_CALL_MARKER} ${call?.function?.name || "unknown"}:\n${call?.function?.arguments || "{}"}`,
      );
    }
    if (message?.role === "tool") {
      const name = message.name || toolNames.get(message.tool_call_id) || "unknown";
      transcript.push(`${TOOL_RESULT_MARKER} ${name}:\n${content}`);
    }
  }
  const names = tools.map((tool) => tool.name).join(", ");
  return [
    "Continue the following Claude Code conversation.",
    names ? `Available Claude Code tools: ${names}` : "No Claude Code tools are available for this request.",
    "",
    transcript.join("\n\n"),
  ].join("\n");
}

// --- transcript marker filter -----------------------------------------------
// Each bridge is installed as one standalone script in its own runtime
// directory, so there is nothing to import from: this block is duplicated
// byte-for-byte in the other bridge, and a Go test extracts it from both to
// check they have not drifted. Edit both copies together.
//
// Neither SDK accepts a structured message array on the send path, so prior
// turns can only be conveyed as prose. These two labels are how the flattened
// transcript marks where a past tool call and its result sit — framing, not
// content. With enough of it in view the model sometimes copies the style into
// its own reply; the prompt tells it not to, and this filter is the backstop
// for when it does anyway.
const TOOL_CALL_MARKER = "TOOL HISTORY";
const TOOL_RESULT_MARKER = "RESULT HISTORY";
const LEGACY_TOOL_CALL_MARKER = "ASSISTANT TOOL CALL";
const LEGACY_TOOL_RESULT_MARKER = "CLAUDE CODE TOOL RESULT";
const TOOL_CALL_MARKERS = [TOOL_CALL_MARKER, LEGACY_TOOL_CALL_MARKER];
const TOOL_RESULT_MARKERS = [TOOL_RESULT_MARKER, LEGACY_TOOL_RESULT_MARKER];
const MARKER_LABELS = [
  ...TOOL_CALL_MARKERS,
  ...TOOL_RESULT_MARKERS,
].flatMap((label) => [`${label} `]);
// An imitation is only recognisable as a whole line: the label opens the line,
// one tool name follows, and a colon closes it. Matching the entire line rather
// than a bare substring is what keeps a reply that merely discusses a label —
// the case when someone points a dialect at this repository — intact, and
// holding the name to the shape of an identifier is what stops a sentence that
// happens to open with a label and end in a colon from matching. A name that
// somehow fell outside this shape would only mean the backstop misses one
// imitation, which is the right way for it to fail. Both markers are letters
// and spaces only, so interpolating them is safe here.
const MARKER_LINE = new RegExp(
  `^(?:${[...TOOL_CALL_MARKERS, ...TOOL_RESULT_MARKERS].join("|")}) [A-Za-z0-9_.-]{1,64}:[ \\t\\r]*$`,
);

// An exact example of the framing is indistinguishable from an imitation of it
// by shape alone, and a reply explaining the transcript format puts that
// example in a fenced block. What was actually reported is unfenced framing, so
// a marker inside a fence is left alone: an explanation survives whole, and the
// backstop still covers the case it exists for.
//
// The delimiter run and what follows it are both captured because a fence
// closes only on its own character, at its own length or longer, with nothing
// after it but whitespace. A reply that documents a fenced example wraps it in
// a longer fence and may carry info strings inside; treating either as a close
// would expose exactly the example the fence was meant to protect.
// Indentation stops at three spaces: a fourth makes the line indented code
// rather than a fence, and reading one as a fence would leave the filter
// believing it is inside a block Markdown never opened — with every marker
// after it exempt from suppression.
const FENCE_LINE = /^ {0,3}(```+|~~~+)(.*)$/;

// What follows a complete label while the line could still become a marker: a
// tool name still being typed, optionally already closed by its colon. Anything
// else — a space, a second word, an over-long name — settles the line as
// ordinary prose, which is what lets it stream on rather than waiting for its
// newline.
const MARKER_NAME_PREFIX = /^[A-Za-z0-9_.-]{0,64}(?::[ \t\r]*)?$/;

// True while `line` could still grow into a marker line: either it is a partial
// label, or the label is complete and what follows it remains a viable name.
function couldStartMarker(line) {
  return MARKER_LABELS.some((label) => {
    if (label.startsWith(line)) return true;
    return line.startsWith(label) && MARKER_NAME_PREFIX.test(line.slice(label.length));
  });
}

// Line-boundary buffering rather than whole-line buffering: a marker can only
// begin at a line start, so as soon as a line's opening characters diverge from
// both labels the rest of that line streams through a character at a time. Only
// a line that still looks like a marker is withheld, which keeps ordinary prose
// at the token granularity the streaming path was built for.
function createMarkerFilter(onSuppress) {
  let lineText = "";
  let held = "";
  let lineSafe = false;
  // The delimiter run of the open fence, or "" when outside one.
  let fence = "";
  let suppressed = false;

  const suppress = () => {
    suppressed = true;
    held = "";
    lineText = "";
    onSuppress?.();
  };

  return {
    get suppressed() {
      return suppressed;
    },
    push(chunk) {
      if (suppressed || !chunk) return "";
      let out = "";
      let rest = chunk;
      while (rest) {
        const index = rest.indexOf("\n");
        if (index === -1) {
          lineText += rest;
          held += rest;
          // Inside a fence nothing is suppressed, so withholding a line that
          // merely looks like a marker would cost latency for no benefit.
          if (!lineSafe && !fence && couldStartMarker(lineText)) break;
          lineSafe = true;
          out += held;
          held = "";
          break;
        }
        lineText += rest.slice(0, index);
        held += rest.slice(0, index + 1);
        rest = rest.slice(index + 1);
        const fenceMatch = FENCE_LINE.exec(lineText);
        if (fenceMatch) {
          const [, delimiter, trailing] = fenceMatch;
          if (!fence) {
            // A backtick fence carrying a backtick in its info string is not an
            // opener at all. Taking one for a fence would leave every marker
            // after it exempt from suppression. Tilde fences have no such rule.
            if (delimiter[0] !== "`" || !trailing.includes("`")) fence = delimiter;
          } else if (
            delimiter[0] === fence[0]
            && delimiter.length >= fence.length
            && trailing.trim() === ""
          ) {
            fence = "";
          }
          // Anything else is content inside the open fence, not its close.
        } else if (!fence && !lineSafe && MARKER_LINE.test(lineText)) {
          // The turn has started imitating the transcript. Everything after the
          // marker belongs to the imitation too, so drop the rest of the turn.
          suppress();
          return out;
        }
        out += held;
        held = "";
        lineText = "";
        lineSafe = false;
      }
      return out;
    },
    // Releases a withheld trailing line that turned out to be ordinary text, or
    // suppresses it when the turn ended on a marker with no closing newline.
    flush() {
      if (suppressed) return "";
      if (!fence && !lineSafe && MARKER_LINE.test(lineText)) {
        suppress();
        return "";
      }
      const out = held;
      held = "";
      lineText = "";
      lineSafe = false;
      return out;
    },
  };
}

function stripTranscriptMarkers(text, onSuppress) {
  if (!text) return text;
  const filter = createMarkerFilter(onSuppress);
  return filter.push(text) + filter.flush();
}

function promoteTranscriptToolCall(text, tools) {
  const lines = String(text || "").split(/\r?\n/);
  let fence = "";
  let offset = 0;
  for (const line of lines) {
    const newlineLength = offset + line.length >= text.length
      ? 0
      : text[offset + line.length] === "\r" && text[offset + line.length + 1] === "\n"
        ? 2
        : 1;
    const fenceMatch = FENCE_LINE.exec(line);
    if (fenceMatch) {
      const [, delimiter, trailing] = fenceMatch;
      if (!fence) {
        fence = delimiter;
      } else if (
        delimiter[0] === fence[0]
        && delimiter.length >= fence.length
        && trailing.trim() === ""
      ) {
        fence = "";
      }
    } else if (!fence) {
      const marker = new RegExp(
        `^(?:${TOOL_CALL_MARKERS.join("|")}) ([A-Za-z0-9_.-]{1,64}):[ \\t]*$`,
      ).exec(line);
      const tool = marker && findTool(tools, marker[1]);
      if (tool) {
        const rawStart = offset + line.length + newlineLength;
        const raw = text.slice(rawStart).trimStart();
        const jsonEnd = balancedJSONEnd(raw);
        if (jsonEnd <= 0) return undefined;
        try {
          const args = JSON.parse(raw.slice(0, jsonEnd));
          if (!args || typeof args !== "object" || Array.isArray(args)) return undefined;
          return {
            call: {
              id: `call_${crypto.randomUUID().replaceAll("-", "")}`,
              name: tool.name,
              arguments: args,
            },
            text: text.slice(0, offset).trimEnd(),
          };
        } catch {
          return undefined;
        }
      }
    }
    offset += line.length + newlineLength;
  }
  return undefined;
}

function balancedJSONEnd(value) {
  if (!value.startsWith("{")) return 0;
  let depth = 0;
  let inString = false;
  let escaped = false;
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (inString) {
      if (escaped) escaped = false;
      else if (character === "\\") escaped = true;
      else if (character === "\"") inString = false;
      continue;
    }
    if (character === "\"") inString = true;
    else if (character === "{") depth += 1;
    else if (character === "}") {
      depth -= 1;
      if (depth === 0) return index + 1;
    }
  }
  return 0;
}

function transcriptSpill(text) {
  return new RegExp(
    `(?:^|\\n)(?:USER|ASSISTANT|${TOOL_CALL_MARKER}|${TOOL_RESULT_MARKER}):|<invoke\\b|<parameter\\b|<\\/invoke>|Bash was interrupted|Interrupted by user`,
    "i",
  ).test(String(text || ""));
}
// --- end transcript marker filter -------------------------------------------

function normalizeTools(rawTools) {
  const result = [];
  for (const entry of rawTools || []) {
    const fn = entry?.type === "function" ? entry.function : entry;
    if (!fn || typeof fn.name !== "string" || !fn.name) continue;
    result.push({
      name: fn.name,
      description: typeof fn.description === "string" ? fn.description : "",
      parameters: fn.parameters && typeof fn.parameters === "object"
        ? fn.parameters
        : { type: "object", properties: {} },
    });
  }
  return result;
}

function findTool(tools, name) {
  if (typeof name !== "string") return undefined;
  const lower = name.toLowerCase();
  return tools.find((tool) => tool.name.toLowerCase() === lower);
}

function contentText(content) {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content.map((part) => {
    if (typeof part === "string") return part;
    if (part?.type === "text" || part?.type === "input_text" || part?.type === "output_text") {
      return typeof part.text === "string" ? part.text : "";
    }
    if (part?.type === "image_url") return "[Image supplied by Claude Code]";
    return "";
  }).filter(Boolean).join("\n");
}

function estimatedUsage(prompt, text, toolCall) {
  const input = Math.max(1, Math.round(prompt.length / 4));
  const serializedTool = toolCall ? JSON.stringify(toolCall.arguments) : "";
  const output = Math.max(1, Math.round((text.length + serializedTool.length) / 4));
  return { prompt_tokens: input, completion_tokens: output, total_tokens: input + output };
}

function authorized(request) {
  return request.headers.authorization === `Bearer ${bridgeKey}`;
}

function unauthorized(response) {
  return json(response, 401, {
    error: { message: "Invalid bridge key", type: "authentication_error", code: "unauthorized" },
  });
}

function readJSON(request) {
  return new Promise((resolve, reject) => {
    let raw = "";
    request.setEncoding("utf8");
    request.on("data", (chunk) => {
      raw += chunk;
      if (raw.length > 16 * 1024 * 1024) {
        const error = new Error("Request body is too large");
        error.status = 413;
        error[CLIENT_SAFE] = true;
        reject(error);
        request.destroy();
      }
    });
    request.on("end", () => {
      try {
        resolve(JSON.parse(raw || "{}"));
      } catch {
        const error = new Error("Invalid JSON request");
        error.status = 400;
        error[CLIENT_SAFE] = true;
        reject(error);
      }
    });
    request.on("error", reject);
  });
}

function json(response, status, value) {
  const body = JSON.stringify(value);
  response.writeHead(status, {
    "content-type": "application/json",
    "content-length": Buffer.byteLength(body),
  });
  response.end(body);
}

function writeSSE(response, value) {
  response.write(`data: ${JSON.stringify(value)}\n\n`);
}

function parseArgs(args) {
  const result = {};
  for (let index = 0; index < args.length; index += 1) {
    const item = args[index];
    if (!item.startsWith("--")) continue;
    result[item.slice(2)] = args[index + 1];
    index += 1;
  }
  return result;
}
