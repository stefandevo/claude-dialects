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

// Module-private marker for errors this bridge creates for the client. A Symbol
// can never be forged by a third-party SDK/HTTP error (unlike the public
// `expose` convention), so only our own readJSON errors expose their message.
const CLIENT_SAFE = Symbol("clientSafe");

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
  // Tracked before the first await of any route. Every route reaches the SDK
  // eventually — /v1/models and the chat completion's model selection both call
  // the catalog — and a client that goes away during one of those awaits fires
  // 'close' once, immediately: a listener attached afterwards never hears it.
  const pending = trackRequest(response);
  try {
    const url = new URL(request.url || "/", `http://${request.headers.host || host}`);
    if (url.pathname === "/health" && request.method === "GET") {
      if (!authorized(request)) return unauthorized(response);
      return json(response, 200, { ok: true, provider: "cursor", sdk: "official" });
    }
    if (url.pathname === "/v1/models" && request.method === "GET") {
      if (!authorized(request)) return unauthorized(response);
      const items = await listCursorModels();
      if (pending.aborted) return;
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
      return await chatCompletion(request, response, body, pending);
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
        process.stderr.write(`cursor bridge error: ${describeError(error)}\n`);
      }
      return json(response, expose ? error.status : 500, {
        error: {
          message: expose
            ? (error instanceof Error ? error.message : String(error))
            : "Internal server error",
          type: "api_error",
          code: error?.code || "cursor_bridge_error",
        },
      });
    }
    response.end();
  } finally {
    // The single owner of the entry's lifetime, so no route can leak one by
    // throwing between registration and its own cleanup.
    inFlight.delete(pending);
  }
});

server.listen(port, host, () => {
  process.stdout.write(`Cursor SDK bridge listening on http://${host}:${port}\n`);
});

for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => {
    for (const session of activeTurnSessions.values()) closeTurnSession(session);
    activeTurnSessions.clear();
    server.close(() => process.exit(0));
  });
}

// Requests currently being served, so a fault raised outside any of their await
// chains can end them instead of leaving them hanging.
const inFlight = new Set();

// How long an abandoned run may take to settle after being cancelled before its
// agent and store are released regardless. A run whose transport died may never
// settle, and until it does the request stays suspended and its own cleanup is
// never reached — so without this a bridge that keeps serving accumulates one
// live agent and one store directory for every request a fault abandons.
const abandonedRunGraceMs = 30_000;
const streamHeartbeatMs = 15_000;
const turnSessionIdleMs = 5 * 60 * 1000;
const turnSessionMaxEntries = 8;

// Agents kept alive across tool-call steps within one Claude Code turn. Each
// entry has a unique sessionId; lookup matches on prefix hash plus the parked
// pendingToolCall so concurrent turns with the same prefix cannot cross-wire.
// Idle entries are evicted on a per-session timer and on later traffic.
const activeTurnSessions = new Map();

function logBridgeTiming(message) {
  process.stderr.write(`cursor bridge timing: ${message}\n`);
}

function startSSEHeartbeat(response) {
  const timer = setInterval(() => {
    if (!response.writableEnded) response.write(": keepalive\n\n");
  }, streamHeartbeatMs);
  timer.unref();
  return () => clearInterval(timer);
}

function beginStreamingResponse(response, { id, created, model }) {
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
}

function writeStreamContentDelta(response, { id, created, model, content }) {
  if (!content) return;
  writeSSE(response, {
    id,
    object: "chat.completion.chunk",
    created,
    model,
    choices: [{ index: 0, delta: { content }, finish_reason: null }],
  });
}

function forwardedToolFromDeltaUpdate(update, forwardedTools) {
  if (update.type !== "tool-call-started" && update.type !== "partial-tool-call") return undefined;
  const toolCall = update.toolCall;
  if (!toolCall || toolCall.type !== "mcp") return undefined;
  const name = toolCall.args?.toolName;
  if (typeof name !== "string") return undefined;
  return findForwardedTool(forwardedTools, name);
}

function noteFirstDelta(timing, runStart) {
  if (timing.firstDelta) return;
  timing.firstDelta = Date.now();
  logBridgeTiming(`first delta ${timing.firstDelta - runStart}ms after run start`);
}

function noteFirstToolCall(timing, runStart) {
  if (timing.firstToolCall) return;
  timing.firstToolCall = Date.now();
  logBridgeTiming(`first tool call ${timing.firstToolCall - runStart}ms after run start`);
}

function normalizeMessagesForSession(messages) {
  return (messages || []).map((message) => ({
    role: message?.role || "user",
    content: contentText(message?.content),
    tool_calls: (message?.tool_calls || []).map((call) => ({
      id: call?.id || "",
      name: call?.function?.name || "",
      arguments: call?.function?.arguments || "{}",
    })),
    tool_call_id: message?.tool_call_id || "",
    name: message?.name || "",
  }));
}

function turnSessionKey(messages, model, toolNames) {
  return crypto.createHash("sha256").update(JSON.stringify({
    model,
    toolNames,
    messages,
  })).digest("hex");
}

function isToolStepContinuation(extension) {
  if (extension.length !== 2) return false;
  const [assistant, tool] = extension;
  return assistant.role === "assistant"
    && Array.isArray(assistant.tool_calls)
    && assistant.tool_calls.length > 0
    && tool.role === "tool";
}

function clearSessionIdleTimer(session) {
  if (!session?.idleTimer) return;
  clearTimeout(session.idleTimer);
  session.idleTimer = undefined;
}

function scheduleSessionIdleTimer(session) {
  clearSessionIdleTimer(session);
  session.idleTimer = setTimeout(() => {
    session.idleTimer = undefined;
    if (session.inUse) return;
    if (Date.now() - session.lastUsedAt < turnSessionIdleMs) {
      scheduleSessionIdleTimer(session);
      return;
    }
    closeTurnSession(session);
    activeTurnSessions.delete(session.sessionId);
  }, turnSessionIdleMs).unref();
}

function evictIdleTurnSessions(now = Date.now()) {
  for (const [sessionId, session] of activeTurnSessions) {
    if (session.inUse) continue;
    if (now - session.lastUsedAt > turnSessionIdleMs) {
      closeTurnSession(session);
      activeTurnSessions.delete(sessionId);
    }
  }
  while (activeTurnSessions.size >= turnSessionMaxEntries) {
    let oldestId;
    let oldestUsedAt = Infinity;
    for (const [sessionId, session] of activeTurnSessions) {
      if (session.inUse) continue;
      if (session.lastUsedAt < oldestUsedAt) {
        oldestUsedAt = session.lastUsedAt;
        oldestId = sessionId;
      }
    }
    if (!oldestId) break;
    closeTurnSession(activeTurnSessions.get(oldestId));
    activeTurnSessions.delete(oldestId);
  }
}

function closeTurnSession(session) {
  if (!session || session.agentClosed) return;
  clearSessionIdleTimer(session);
  session.agentClosed = true;
  session.agent?.close();
  discardRunState(session.runStateDir);
}

function continuationMatchesPendingTool(extension, pendingToolCall) {
  if (!pendingToolCall) return false;
  const [assistant, tool] = extension;
  const call = assistant.tool_calls?.[0];
  if (!call) return false;
  if ((call.id || "") !== pendingToolCall.id) return false;
  if ((call?.function?.name || call?.name || "") !== pendingToolCall.name) return false;
  return (tool.tool_call_id || "") === pendingToolCall.id;
}

function findContinuationSession(messages, model, toolNames) {
  evictIdleTurnSessions();
  for (const session of activeTurnSessions.values()) {
    if (session.inUse || session.model !== model) continue;
    if (!session.pendingToolCall) continue;
    if (messages.length <= session.messageCount) continue;
    const prefix = messages.slice(0, session.messageCount);
    if (turnSessionKey(prefix, model, toolNames) !== session.prefixHash) continue;
    const extension = messages.slice(session.messageCount);
    if (!isToolStepContinuation(extension)) continue;
    if (!continuationMatchesPendingTool(extension, session.pendingToolCall)) continue;
    return { session, extension };
  }
  return undefined;
}

function registerTurnSession(entry) {
  evictIdleTurnSessions();
  activeTurnSessions.set(entry.sessionId, entry);
  scheduleSessionIdleTimer(entry);
}

// trackRequest registers a request the moment it arrives and reports, through
// `aborted`, whether it can still be answered. Handlers check that flag after
// every await that precedes irreversible work, and attach any cancellable work
// they own to `onAbort` — cancelling an SDK run needs the run to exist, so
// before that the flag is the only thing that can stop it from starting.
//
// Removal from the set is the server handler's job, not this function's: an
// entry has to survive every path a route can take, and only the handler's
// finally sees them all.
function trackRequest(response) {
  const pending = {
    aborted: false,
    onAbort: undefined,
    abandon: () => {
      // Deregister first: an abandoned request may never settle, so an entry
      // left here would be abandoned again by every later fault.
      inFlight.delete(pending);
      pending.aborted = true;
      pending.onAbort?.();
      failPending(response);
    },
  };
  response.once("close", () => {
    if (response.writableEnded) return;
    // The client is gone, so there is nothing to fail — only work to stop.
    inFlight.delete(pending);
    pending.aborted = true;
    pending.onAbort?.();
  });
  inFlight.add(pending);
  return pending;
}

// A fault the request handler's try/catch cannot see — an 'error' event on a
// socket the SDK owns, a rejection with nothing awaiting it — reaches Node's
// default handler and terminates the process. That takes down the listener and
// every concurrent request with it, and the proxy in front keeps forwarding into
// the dead port, so the dialect fails for the rest of the session with nothing
// pointing at this log.
//
// Such a fault cannot be attributed to the request that provoked it, and a run
// whose transport just died may never settle, so every request in flight is
// failed rather than left pending: a 500 is retried, a response that never
// arrives is waited on forever. The listener stays up, so the retry lands.
for (const fault of ["uncaughtException", "unhandledRejection"]) {
  process.on(fault, (reason) => {
    process.stderr.write(`cursor bridge ${fault}: ${describeError(reason)}\n`);
    for (const pending of [...inFlight]) pending.abandon();
  });
}

async function chatCompletion(request, response, body, pending) {
  const model = typeof body.model === "string" && body.model ? body.model : "auto";
  // The catalog lookup behind selectModel is an SDK call whenever the cache is
  // cold, so the request can already have been abandoned by the time it returns.
  const modelSelection = await selectModel(model, body);
  if (pending.aborted) return;
  const toolDefinitions = normalizeTools(body.tools);
  const forwardedTools = aliasTools(toolDefinitions);
  const normalizedMessages = normalizeMessagesForSession(body.messages);
  const toolNames = forwardedTools.map((tool) => tool.name).sort();
  const continuation = findContinuationSession(normalizedMessages, model, toolNames);
  let turnSession = continuation?.session;
  let prompt;
  if (turnSession) {
    clearSessionIdleTimer(turnSession);
    turnSession.inUse = true;
    turnSession.lastUsedAt = Date.now();
    prompt = buildContinuationPrompt(continuation.extension, forwardedTools);
    logBridgeTiming(`turn continue messages=${normalizedMessages.length} cached=${turnSession.messageCount}`);
  } else {
    prompt = buildPrompt(body.messages, forwardedTools);
  }
  const customTools = {};
  // One agent store per Claude Code turn, not one per HTTP request. Within a
  // turn the same agent and store are reused across tool-call steps; each new
  // turn gets a fresh run-{uuid} directory so concurrent turns stay disjoint
  // and nothing accumulates without bound.
  let runStateDir = turnSession?.runStateDir;
  if (!runStateDir) {
    runStateDir = path.join(workspace, ".cursor-dialect-state", `run-${crypto.randomUUID()}`);
    fs.mkdirSync(runStateDir, { recursive: true, mode: 0o700 });
  }
  const store = turnSession?.store || new JsonlLocalAgentStore(runStateDir);
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

  let text = "";
  let usage;
  let agent = turnSession?.agent;
  let agentClosed = turnSession?.agentClosed || false;
  const isStreaming = Boolean(body.stream);
  const id = `chatcmpl_${crypto.randomUUID().replaceAll("-", "")}`;
  const created = Math.floor(Date.now() / 1000);
  let stopHeartbeat = () => {};
  const timing = { runStart: 0, firstDelta: 0, firstToolCall: 0 };
  const streamMeta = () => ({ id, created, model });
  const streamContent = (content) => {
    if (!isStreaming || pending.aborted || response.writableEnded) return;
    writeStreamContentDelta(response, { ...streamMeta(), content });
  };
  const handleDelta = (update) => {
    if (pending.aborted || response.writableEnded) return;
    switch (update.type) {
      case "text-delta":
        if (typeof update.text === "string" && update.text) {
          noteFirstDelta(timing, timing.runStart);
          text += update.text;
          streamContent(update.text);
        }
        break;
      case "thinking-delta":
        // Visible progress only — do not merge reasoning into billed assistant text.
        if (typeof update.text === "string" && update.text) {
          noteFirstDelta(timing, timing.runStart);
          streamContent(update.text);
        }
        break;
      case "summary-started":
        noteFirstDelta(timing, timing.runStart);
        streamContent("…");
        break;
      case "summary":
        if (typeof update.summary === "string" && update.summary) {
          noteFirstDelta(timing, timing.runStart);
          streamContent(`${update.summary}\n`);
        }
        break;
      case "tool-call-started":
      case "partial-tool-call": {
        const tool = forwardedToolFromDeltaUpdate(update, forwardedTools);
        if (!tool) break;
        // Timing only — partial deltas carry incomplete args; capture from stream events.
        noteFirstToolCall(timing, timing.runStart);
        break;
      }
      default:
        break;
    }
  };
  // Runs from the request's own finally on every ordinary path, and from a timer
  // when the request was abandoned and its run never settled — in which case
  // that finally is never reached at all. Both can run, in either order, so each
  // resource is released on its own terms rather than behind one flag meaning
  // "this request is done": a timer that fires while Agent.create is still
  // pending would otherwise spend that flag on an agent which does not exist
  // yet, and the one created afterwards would never be closed.
  //
  // The agent is closed once, and only once there is one. The directory is
  // discarded every time — the SDK may have recreated it since an earlier
  // release, and removing an absent one costs nothing. Closing first is what
  // keeps the removal from landing underneath an SDK that is still writing.
  const releaseRun = () => {
    if (agent && !agentClosed) {
      agentClosed = true;
      agent.close();
    }
    discardRunState(runStateDir);
  };
  const keepTurnAlive = () => {
    const prefixMessages = normalizedMessages.slice(0, normalizedMessages.length);
    const prefixHash = turnSessionKey(prefixMessages, model, toolNames);
    const session = {
      sessionId: crypto.randomUUID(),
      prefixHash,
      messageCount: prefixMessages.length,
      model,
      modelSelection,
      toolNames,
      forwardedTools,
      pendingToolCall: capturedToolCall
        ? { id: capturedToolCall.id, name: capturedToolCall.name }
        : undefined,
      agent,
      store,
      runStateDir,
      lastUsedAt: Date.now(),
      inUse: false,
      agentClosed: false,
    };
    if (turnSession) {
      clearSessionIdleTimer(turnSession);
      activeTurnSessions.delete(turnSession.sessionId);
      turnSession.agent = undefined;
      turnSession.store = undefined;
      turnSession.runStateDir = undefined;
    }
    registerTurnSession(session);
    turnSession = session;
    agentClosed = false;
  };
  // The run is the cancellable work this request owns. Cancelling normally lets
  // it settle so the finally releases it; when the transport is what died it may
  // never settle, so release it anyway rather than leaving a live agent and its
  // store behind for the rest of the bridge's life.
  pending.onAbort = () => {
    stopHeartbeat();
    activeRun?.cancel().catch(() => {});
    if (turnSession) {
      closeTurnSession(turnSession);
      activeTurnSessions.delete(turnSession.sessionId);
      turnSession = undefined;
      agent = undefined;
      agentClosed = true;
    } else {
      // unref so a pending release never holds the process open at shutdown.
      setTimeout(releaseRun, abandonedRunGraceMs).unref();
    }
  };

  try {
    if (!agent) {
      agent = await Agent.create({
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
    }

    // Creating the agent is itself an await, so the request may have been
    // abandoned in the meantime. Starting the run now would bill a generation
    // whose response has already been sent.
    if (pending.aborted) return;

    if (isStreaming && !response.writableEnded) {
      beginStreamingResponse(response, streamMeta());
      stopHeartbeat = startSSEHeartbeat(response);
    }

    timing.runStart = Date.now();
    logBridgeTiming(`run start model=${model}`);

    activeRun = await agent.send(prompt, {
      model: modelSelection,
      mode: "agent",
      local: { customTools },
      onDelta: isStreaming ? ({ update }) => handleDelta(update) : undefined,
    });
    // Same window again: nothing could cancel this run while it was starting,
    // because until the assignment above there was no run to cancel.
    if (pending.aborted) {
      await activeRun.cancel().catch(() => {});
      return;
    }

    for await (const event of activeRun.stream()) {
      if (event.type === "assistant") {
        for (const block of event.message?.content || []) {
          if (block?.type === "text" && typeof block.text === "string") {
            if (!isStreaming) text += block.text;
          } else if (block?.type === "tool_use") {
            const tool = findForwardedTool(forwardedTools, block.name);
            if (tool) {
              noteFirstToolCall(timing, timing.runStart);
              capture(tool.name, block.input, block.id);
            }
          }
        }
      } else if (event.type === "tool_call" && event.status === "running") {
        const tool = findForwardedTool(forwardedTools, event.name);
        if (tool) {
          noteFirstToolCall(timing, timing.runStart);
          capture(tool.name, event.args, event.call_id);
        }
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
    stopHeartbeat();
    if (capturedToolCall && !pending.aborted && agent) {
      keepTurnAlive();
    } else {
      if (turnSession) {
        closeTurnSession(turnSession);
        activeTurnSessions.delete(turnSession.sessionId);
      } else {
        releaseRun();
      }
    }
    if (turnSession) {
      turnSession.lastUsedAt = Date.now();
      scheduleSessionIdleTimer(turnSession);
    }
    if (turnSession) turnSession.inUse = false;
  }

  // writableEnded as well as aborted: a fault handler may have already failed
  // this response, and the close event that follows arrives too late to be the
  // thing that tells us.
  if (pending.aborted || response.writableEnded) return;
  const runEnd = Date.now();
  const timingParts = [`run end ${runEnd - timing.runStart}ms`];
  if (timing.firstDelta) timingParts.push(`first-delta=${timing.firstDelta - timing.runStart}ms`);
  if (timing.firstToolCall) timingParts.push(`first-tool=${timing.firstToolCall - timing.runStart}ms`);
  logBridgeTiming(timingParts.join(" "));
  const normalizedUsage = openAIUsage(usage, prompt, text, capturedToolCall);
  if (isStreaming) {
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
  return [
    harnessInstructions(tools),
    "",
    buildTranscript(messages, tools),
  ].join("\n");
}

function buildContinuationPrompt(extension, tools) {
  return buildTranscript(extension, tools);
}

function harnessInstructions(tools) {
  const names = tools.map((tool) => `${tool.alias} → ${tool.name}`).join(", ");
  return [
    "You are the model running inside the Claude Code harness.",
    "Claude Code owns all filesystem, terminal, web, MCP, and other tool execution.",
    "Use only the custom tools whose names begin with cc_tool_. Never call Cursor's built-in shell, read, write, edit, search, browser, or other workspace tools.",
    names ? `Custom tool aliases mapped to Claude Code tools: ${names}` : "No Claude Code tools are available for this request.",
    "Call the exact cc_tool_ alias with the arguments required by its custom-tool schema, then stop so Claude Code can execute it.",
  ].join("\n");
}

function buildTranscript(messages, tools) {
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
  return transcript.join("\n\n");
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

function describeError(error) {
  return error instanceof Error ? error.stack || error.message : String(error);
}

// discardRunState deletes one request's agent store. A failure is logged and
// never fails the request: what is left behind is bounded by the runs a single
// bridge process serves, and the next launch clears the parent directory
// outright.
function discardRunState(directory) {
  try {
    fs.rmSync(directory, { recursive: true, force: true });
  } catch (error) {
    process.stderr.write(
      `cursor bridge could not discard run state ${directory}: ${describeError(error)}\n`,
    );
  }
}

// failPending ends a request the bridge can no longer complete. A response
// already streaming is closed rather than rewritten — its status is long since
// sent — which is enough for the client to stop waiting and retry.
function failPending(response) {
  if (response.writableEnded) return;
  if (response.headersSent) return response.end();
  return json(response, 500, {
    error: {
      message: "The Cursor bridge recovered from an internal fault; see cursor-bridge.log",
      type: "api_error",
      code: "cursor_bridge_fault",
    },
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
