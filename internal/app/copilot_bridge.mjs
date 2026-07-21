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
          "Use only the custom tools supplied by Claude Code. When a tool is needed, call it and stop.",
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
  } finally {
    await session?.disconnect().catch(() => {});
  }

  if (disconnected) return;
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
        `ASSISTANT TOOL CALL ${call?.function?.name || "unknown"}:\n${call?.function?.arguments || "{}"}`,
      );
    }
    if (message?.role === "tool") {
      const name = message.name || toolNames.get(message.tool_call_id) || "unknown";
      transcript.push(`CLAUDE CODE TOOL RESULT ${name}:\n${content}`);
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
