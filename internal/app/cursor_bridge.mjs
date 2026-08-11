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
    if (response.headersSent) {
      process.stderr.write(`cursor bridge error after headers sent: ${describeError(error)}\n`);
      if (!response.writableEnded) response.end();
      return;
    }
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

function failStreamingResponse(response, error) {
  process.stderr.write(`cursor bridge stream error: ${describeError(error)}\n`);
  if (response.writableEnded) return;
  try {
    response.write(`data: ${JSON.stringify({
      error: {
        message: error?.message || "Internal server error",
        type: "api_error",
        code: error?.code || "cursor_bridge_error",
      },
    })}\n\n`);
  } catch {
    // Best effort — the client still needs the connection closed.
  }
  response.end();
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

function evictExpiredIdleSessions(now = Date.now()) {
  for (const [sessionId, session] of activeTurnSessions) {
    if (session.inUse) continue;
    if (now - session.lastUsedAt > turnSessionIdleMs) {
      closeTurnSession(session);
      activeTurnSessions.delete(sessionId);
    }
  }
}

function enforceTurnSessionCapacity() {
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
  evictExpiredIdleSessions();
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
  evictExpiredIdleSessions();
  enforceTurnSessionCapacity();
  if (activeTurnSessions.size >= turnSessionMaxEntries) {
    return false;
  }
  activeTurnSessions.set(entry.sessionId, entry);
  scheduleSessionIdleTimer(entry);
  return true;
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
    prompt = buildContinuationPrompt(
      (body.messages || []).slice(turnSession.messageCount),
      forwardedTools,
    );
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
  // Whether the run produced any assistant text at all. `text` alone cannot
  // answer that while streaming: the filter may still be holding the only line
  // there was, or have suppressed it outright.
  let sawAssistantText = false;
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
  const logMarkerSuppression = (stream) => {
    process.stderr.write(`cursor bridge: suppressed ${stream} imitating the transcript markers\n`);
  };
  // Two filters rather than one. The answer and the reasoning are separate
  // logical streams that each carry their own line boundaries, and only the
  // answer feeds the billed `text` — sharing a filter would let a marker in the
  // reasoning suppress the answer, and would release withheld reasoning into
  // `text`. The reasoning is covered at all because it is displayed to the user
  // exactly like the answer is, so an imitation there is just as unreadable.
  const outputFilter = createMarkerFilter(() => logMarkerSuppression("assistant text"));
  const reasoningFilter = createMarkerFilter(() => logMarkerSuppression("reasoning output"));
  const streamAnswer = (chunk) => {
    sawAssistantText = true;
    const visible = outputFilter.push(chunk);
    if (!visible) return;
    text += visible;
    streamContent(visible);
  };
  const handleDelta = (update) => {
    if (pending.aborted || response.writableEnded) return;
    switch (update.type) {
      case "text-delta":
        if (typeof update.text === "string" && update.text) {
          noteFirstDelta(timing, timing.runStart);
          streamAnswer(update.text);
        }
        break;
      case "thinking-delta":
        // Visible progress only — do not merge reasoning into billed assistant text.
        if (typeof update.text === "string" && update.text) {
          noteFirstDelta(timing, timing.runStart);
          streamContent(reasoningFilter.push(update.text));
        }
        break;
      case "summary-started":
        noteFirstDelta(timing, timing.runStart);
        streamContent(reasoningFilter.push("…"));
        break;
      case "summary":
        if (typeof update.summary === "string" && update.summary) {
          noteFirstDelta(timing, timing.runStart);
          streamContent(reasoningFilter.push(`${update.summary}\n`));
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
    if (!registerTurnSession(session)) {
      closeTurnSession(session);
      turnSession = undefined;
      agent = undefined;
      agentClosed = true;
      return;
    }
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
      // A third path into `text`, taken when the run reported no assistant
      // content at all, so it needs the same filtering the other two get. It
      // must not fire on a streamed reply the filter is still holding or has
      // suppressed, or the later flush would append the same content twice.
      if (!text && !sawAssistantText && typeof result.result === "string") {
        text = stripTranscriptMarkers(result.result, () => logMarkerSuppression("assistant text"));
      }
      usage ||= result.usage;
    }
  } catch (error) {
    if (isStreaming && response.headersSent && !response.writableEnded) {
      failStreamingResponse(response, error);
      return;
    }
    throw error;
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
  if (isStreaming) {
    // Release whatever the filters were still holding when the turn ended: a
    // trailing partial line that never grew into a marker.
    streamContent(reasoningFilter.flush());
    const tail = outputFilter.flush();
    if (tail) {
      text += tail;
      streamContent(tail);
    }
  } else {
    text = stripTranscriptMarkers(text, () => logMarkerSuppression("assistant text"));
  }
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
    `Lines beginning with ${TOOL_CALL_MARKER} or ${TOOL_RESULT_MARKER} are transcript framing added by Claude Code to show you the conversation so far. Never write such a line yourself: call the tool instead.`,
  ].join("\n");
}

function toolCallName(call) {
  return call?.function?.name || call?.name || "unknown";
}

function toolCallArguments(call) {
  if (call?.function?.arguments !== undefined) return call.function.arguments;
  if (call?.arguments !== undefined) {
    return typeof call.arguments === "string" ? call.arguments : JSON.stringify(call.arguments);
  }
  return "{}";
}

function buildTranscript(messages, tools) {
  const toolNames = new Map();
  for (const message of messages || []) {
    for (const call of message?.tool_calls || []) {
      const name = toolCallName(call);
      if (call?.id && name !== "unknown") toolNames.set(call.id, name);
    }
  }
  const transcript = [];
  for (const message of messages || []) {
    const role = String(message?.role || "user").toUpperCase();
    const content = contentText(message?.content);
    if (content && message?.role !== "tool") transcript.push(`${role}:\n${content}`);
    for (const call of message?.tool_calls || []) {
      transcript.push(
        `${TOOL_CALL_MARKER} ${toolCallName(call)}:\n${toolCallArguments(call)}`,
      );
    }
    if (message?.role === "tool") {
      const name = message.name || toolNames.get(message.tool_call_id) || "unknown";
      transcript.push(`${TOOL_RESULT_MARKER} ${name}:\n${content}`);
    }
  }
  return transcript.join("\n\n");
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
const TOOL_CALL_MARKER = "ASSISTANT TOOL CALL";
const TOOL_RESULT_MARKER = "CLAUDE CODE TOOL RESULT";
const MARKER_LABELS = [`${TOOL_CALL_MARKER} `, `${TOOL_RESULT_MARKER} `];
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
  `^(?:${TOOL_CALL_MARKER}|${TOOL_RESULT_MARKER}) [A-Za-z0-9_.-]{1,64}:[ \\t\\r]*$`,
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
const FENCE_LINE = /^\s*(```+|~~~+)(.*)$/;

// True while `line` could still grow into a marker line: either it is a partial
// label, or the label is complete and the tool name is still arriving.
function couldStartMarker(line) {
  return MARKER_LABELS.some((label) => label.startsWith(line) || line.startsWith(label));
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
          if (!lineSafe && couldStartMarker(lineText)) break;
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
            fence = delimiter;
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
