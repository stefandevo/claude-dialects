#!/usr/bin/env node

import { Agent, Cursor, JsonlLocalAgentStore } from "@cursor/sdk";
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import path from "node:path";

const options = parseArgs(process.argv.slice(2));
const host = options.host || "127.0.0.1";
const port = Number(options.port || 0);
const bridgeKey = process.env.CURSOR_DIALECT_BRIDGE_KEY || "";
const cursorAPIKey = process.env.CURSOR_API_KEY || "";
const workspace = path.resolve(options.workspace || process.cwd());
let modelCatalogCache;
let modelCatalogExpiresAt = 0;

if (!Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error("--port must be a valid TCP port");
}
if (!bridgeKey) {
  throw new Error("CURSOR_DIALECT_BRIDGE_KEY is required");
}
if (!cursorAPIKey) {
  throw new Error("CURSOR_API_KEY is required");
}
fs.mkdirSync(workspace, { recursive: true, mode: 0o700 });

const server = http.createServer(async (request, response) => {
  try {
    const url = new URL(request.url || "/", `http://${request.headers.host || host}`);
    if (url.pathname === "/health" && request.method === "GET") {
      if (!authorized(request)) return unauthorized(response);
      return json(response, 200, { ok: true, provider: "cursor", sdk: "official" });
    }
    if (url.pathname === "/v1/models" && request.method === "GET") {
      if (!authorized(request)) return unauthorized(response);
      const items = await listCursorModels();
      return json(response, 200, {
        object: "list",
        data: items.map((item) => ({
          id: item.id,
          object: "model",
          created: 0,
          owned_by: "cursor",
          name: item.displayName || item.id,
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
      return json(response, error?.status || 500, {
        error: {
          message: error instanceof Error ? error.message : String(error),
          type: "api_error",
          code: error?.code || "cursor_bridge_error",
        },
      });
    }
    response.end();
  }
});

server.listen(port, host, () => {
  process.stdout.write(`Cursor SDK bridge listening on http://${host}:${port}\n`);
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => server.close(() => process.exit(0)));
}

async function chatCompletion(request, response, body) {
  const model = typeof body.model === "string" && body.model ? body.model : "auto";
  const modelSelection = await selectModel(model, body);
  const toolDefinitions = normalizeTools(body.tools);
  const forwardedTools = aliasTools(toolDefinitions);
  const prompt = buildPrompt(body.messages, forwardedTools);
  const customTools = {};
  const store = new JsonlLocalAgentStore(path.join(workspace, ".cursor-dialect-state"));
  let capturedToolCall;
  let activeRun;

  const capture = (name, args, id) => {
    if (capturedToolCall) return;
    capturedToolCall = {
      id: id || `call_${crypto.randomUUID().replaceAll("-", "")}`,
      name,
      arguments: args && typeof args === "object" ? args : {},
    };
    if (activeRun) {
      queueMicrotask(() => activeRun.cancel().catch(() => {}));
    }
  };

  for (const tool of forwardedTools) {
    customTools[tool.alias] = {
      description: tool.description,
      inputSchema: tool.parameters,
      execute: async (args, context) => {
        capture(tool.name, args, context?.toolCallId);
        return "Tool call forwarded to Claude Code for execution.";
      },
    };
  }

  const agent = await Agent.create({
    apiKey: cursorAPIKey,
    model: modelSelection,
    name: "Claude Dialects Cursor bridge",
    mode: "agent",
    local: {
      cwd: workspace,
      settingSources: [],
      // Cursor exposes SDK custom tools through its synthetic
      // custom-user-tools MCP server. A sandboxed headless local run cannot
      // request the interactive approval those calls require, so it blocks
      // every tool before our callback can return it to Claude Code. Claude
      // Code remains the permission and execution boundary; this bridge only
      // captures the requested tool call.
      sandboxOptions: { enabled: false },
      autoReview: false,
      store,
      customTools,
    },
  });

  let text = "";
  let usage;
  let disconnected = false;
  response.once("close", () => {
    if (!response.writableEnded) {
      disconnected = true;
      activeRun?.cancel().catch(() => {});
    }
  });

  try {
    activeRun = await agent.send(prompt, {
      model: modelSelection,
      mode: "agent",
      local: { customTools },
    });

    for await (const event of activeRun.stream()) {
      if (event.type === "assistant") {
        for (const block of event.message?.content || []) {
          if (block?.type === "text" && typeof block.text === "string") {
            text += block.text;
          } else if (block?.type === "tool_use") {
            const tool = findForwardedTool(forwardedTools, block.name);
            if (tool) capture(tool.name, block.input, block.id);
          }
        }
      } else if (event.type === "tool_call" && event.status === "running") {
        const tool = findForwardedTool(forwardedTools, event.name);
        if (tool) capture(tool.name, event.args, event.call_id);
      } else if (event.type === "usage") {
        usage = event.usage;
      }
      if (capturedToolCall) break;
    }

    if (capturedToolCall && activeRun.status === "running") {
      await activeRun.cancel().catch(() => {});
    }
    if (!capturedToolCall && activeRun.status === "running") {
      const result = await activeRun.wait();
      if (result.status === "error") {
        const error = new Error(result.error?.message || "Cursor SDK run failed");
        error.code = result.error?.code || "cursor_sdk_error";
        throw error;
      }
      if (!text && typeof result.result === "string") text = result.result;
      usage ||= result.usage;
    }
  } finally {
    agent.close();
  }

  if (disconnected) return;
  const id = `chatcmpl_${crypto.randomUUID().replaceAll("-", "")}`;
  const created = Math.floor(Date.now() / 1000);
  const normalizedUsage = openAIUsage(usage, prompt, text, capturedToolCall);
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
      model,
      choices: [{ index: 0, delta: { role: "assistant" }, finish_reason: null }],
    });
    if (text) {
      writeSSE(response, {
        id,
        object: "chat.completion.chunk",
        created,
        model,
        choices: [{ index: 0, delta: { content: text }, finish_reason: null }],
      });
    }
    if (capturedToolCall) {
      writeSSE(response, {
        id,
        object: "chat.completion.chunk",
        created,
        model,
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
      model,
      choices: [{
        index: 0,
        delta: {},
        finish_reason: capturedToolCall ? "tool_calls" : "stop",
      }],
      usage: normalizedUsage,
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
    model,
    choices: [{
      index: 0,
      message,
      finish_reason: capturedToolCall ? "tool_calls" : "stop",
    }],
    usage: normalizedUsage,
  });
}

async function listCursorModels() {
  if (modelCatalogCache && Date.now() < modelCatalogExpiresAt) return modelCatalogCache;
  const response = await Cursor.models.list({ apiKey: cursorAPIKey });
  modelCatalogCache = Array.isArray(response)
    ? response
    : response?.items || response?.models || [];
  modelCatalogExpiresAt = Date.now() + 5 * 60 * 1000;
  return modelCatalogCache;
}

async function selectModel(model, body) {
  const requested = cursorModelAlias(model);
  const effort = requestedEffort(body);
  const catalog = await listCursorModels();
  const definition = catalog.find((item) =>
    item?.id === requested.id || Array.isArray(item?.aliases) && item.aliases.includes(requested.id)
  );
  if (!definition) return { id: requested.id, ...(requested.params ? { params: requested.params } : {}) };

  if (requested.params) {
    const matchingVariant = (definition.variants || []).find((variant) =>
      requested.params.every((wanted) =>
        (variant.params || []).some((parameter) =>
          parameter.id === wanted.id && parameter.value === wanted.value
        )
      )
    );
    return {
      id: definition.id,
      params: matchingVariant?.params || requested.params,
    };
  }
  if (effort) {
    const candidates = effortCandidates(effort);
    for (const variant of definition.variants || []) {
      const label = normalizeEffort(variant.displayName);
      if (candidates.includes(label) && Array.isArray(variant.params)) {
        return { id: definition.id, params: variant.params };
      }
    }
    for (const parameter of definition.parameters || []) {
      const parameterName = normalizeEffort(`${parameter.id} ${parameter.displayName || ""}`);
      if (!/(effort|reason|think)/.test(parameterName)) continue;
      for (const candidate of candidates) {
        const value = (parameter.values || []).find((entry) =>
          normalizeEffort(`${entry.value} ${entry.displayName || ""}`) === candidate
          || normalizeEffort(entry.value) === candidate
        );
        if (value) {
          return { id: definition.id, params: [{ id: parameter.id, value: value.value }] };
        }
      }
    }
  }
  const defaultVariant = (definition.variants || []).find((variant) => variant.isDefault)
    || definition.variants?.[0];
  if (Array.isArray(defaultVariant?.params)) {
    return { id: definition.id, params: defaultVariant.params };
  }
  return { id: definition.id };
}

function cursorModelAlias(model) {
  switch (model) {
    case "composer-2.5-fast":
      return { id: "composer-2.5", params: [{ id: "fast", value: "true" }] };
    case "composer-2.5-standard":
      return { id: "composer-2.5", params: [{ id: "fast", value: "false" }] };
    default:
      return { id: model };
  }
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
    case "xhigh": return ["xhigh", "max", "high"];
    case "max": return ["max", "xhigh", "high"];
    case "medium": return ["medium", "med", "default"];
    case "low": return ["low", "minimal"];
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
  const names = tools.map((tool) => `${tool.alias} → ${tool.name}`).join(", ");
  return [
    "You are the model running inside the Claude Code harness.",
    "Claude Code owns all filesystem, terminal, web, MCP, and other tool execution.",
    "Use only the custom tools whose names begin with cc_tool_. Never call Cursor's built-in shell, read, write, edit, search, browser, or other workspace tools.",
    names ? `Custom tool aliases mapped to Claude Code tools: ${names}` : "No Claude Code tools are available for this request.",
    "Call the exact cc_tool_ alias with the arguments required by its custom-tool schema, then stop so Claude Code can execute it.",
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

function aliasTools(tools) {
  return tools.map((tool, index) => {
    const stem = tool.name.replaceAll(/[^a-zA-Z0-9_-]+/g, "_").slice(0, 44) || "tool";
    return { ...tool, alias: `cc_tool_${index}_${stem}` };
  });
}

function findForwardedTool(tools, name) {
  if (typeof name !== "string") return undefined;
  const lower = name.toLowerCase();
  return tools.find((tool) => tool.alias.toLowerCase() === lower);
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

function openAIUsage(usage, prompt, text, toolCall) {
  const input = numberValue(usage?.inputTokens, usage?.input_tokens)
    || Math.max(1, Math.round(prompt.length / 4));
  const serializedTool = toolCall ? JSON.stringify(toolCall.arguments) : "";
  const output = numberValue(usage?.outputTokens, usage?.output_tokens)
    || Math.max(1, Math.round((text.length + serializedTool.length) / 4));
  return { prompt_tokens: input, completion_tokens: output, total_tokens: input + output };
}

function numberValue(...values) {
  return values.find((value) => typeof value === "number" && Number.isFinite(value));
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
    const key = item.slice(2);
    result[key] = args[index + 1];
    index += 1;
  }
  return result;
}
