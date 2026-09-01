/** A stand-in gateway, for looking at the console on a workstation.
 *
 *  Enabled only by `npm run dev:mock`, which is `vite --mode mock`; the plugin
 *  is registered on that mode alone, so `vite build` never sees it. Nothing here
 *  is imported by `src/`, so none of it can reach `dist/`; under plain `npm run
 *  dev` the server proxies `/api` to a real gateway exactly as it always has.
 *
 *  It exists for one reason. The console's only safety net is `tsc`, which
 *  proves nothing about layout, focus order, drawer traps or a streamed reply
 *  arriving one token at a time — and without a database and a Redis there was
 *  no way to see any page but the login card. Every layout bug this overhaul is
 *  fixing was invisible to every gate the project had.
 *
 *  ## Driving it from the playground
 *
 *  The reply is chosen by what the last user message contains, so every state
 *  the transcript can draw is reachable by typing:
 *
 *  | Type this | What comes back |
 *  |---|---|
 *  | anything | a markdown document — headings, lists, two fenced blocks, a quote |
 *  | `long`   | forty paragraphs, for scroll and auto-follow behaviour |
 *  | `slow`   | the same reply at one chunk every 140 ms, for Stop |
 *  | `fail`   | text, then a mid-stream error frame inside a 200 |
 *  | `switch` | evidence saying the request was re-sent to /responses |
 *  | `strip`  | evidence naming dropped and renamed parameters |
 *  | `empty`  | a reply with no text in it at all |
 *  | `reject` | an HTTP 429 before the first byte, as a plain JSON error |
 */

import type { Plugin } from "vite";
import { bodies, logs, overview, providers, settings, version } from "./fixtures";
import type { LearnedFact, Overview, RequestLog } from "../src/types";

/** Node's own `IncomingMessage` and `ServerResponse`, named structurally.
 *
 *  `@types/node` is not a dependency of this project and adding one for a dev
 *  fixture would be the wrong trade — so the two objects are described by the
 *  handful of members used below. Everything here is exact: a typo in a method
 *  name still fails the typecheck. */
type Req = {
  url?: string;
  method?: string;
  on(event: string, listener: (chunk: never) => void): void;
};

type Res = {
  statusCode: number;
  setHeader(name: string, value: string): void;
  write(chunk: string): void;
  end(chunk?: string): void;
};

const CSRF = "dev-csrf-token";

/** Rows minted by the playground during this dev session, so the evidence strip
 *  under a reply can find the request it is describing. Bounded because a long
 *  afternoon of prompting should not grow without end. */
const minted: RequestLog[] = [];
let requestCounter = 0;

function json(res: Res, status: number, payload: unknown) {
  const body = JSON.stringify(payload);
  res.statusCode = status;
  res.setHeader("Content-Type", "application/json");
  res.end(body);
}

function fail(res: Res, status: number, code: string, message: string) {
  json(res, status, { error: { code, message } });
}

function readBody(req: Req): Promise<Record<string, unknown>> {
  return new Promise((resolve) => {
    let raw = "";
    req.on("data", (chunk: unknown) => {
      raw += String(chunk);
    });
    req.on("end", () => {
      try {
        const parsed = JSON.parse(raw || "{}");
        resolve(parsed && typeof parsed === "object" ? parsed : {});
      } catch {
        resolve({});
      }
    });
  });
}

/** The gateway is slower than a function call, and a console that only ever sees
 *  an instant answer never shows its own loading states. */
function slowly<T>(value: T, ms = 260): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), ms));
}

/* ── The reply ─────────────────────────────────────────────────────────────── */

const DOC = `Here is what **Rotakey** does with a request, in the order it does it.

## The short version

1. It matches \`model\` against a route's public alias.
2. It picks a key from that route's pool, skipping any in cooldown.
3. It translates the body when the upstream speaks a different shape.

> A key in cooldown is *skipped*, not failed. The difference matters: a skipped
> key is still counted in the pool, so the readiness figure does not drop.

### Sending one

\`\`\`bash
curl https://gateway.rotakey.dev/v1/chat/completions \\
  -H "Authorization: Bearer $ROTAKEY_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"azure/gpt-5.6-sol","messages":[{"role":"user","content":"hi"}]}'
\`\`\`

The same thing through the OpenAI SDK:

\`\`\`ts
const reply = await client.chat.completions.create({
  model: "azure/gpt-5.6-sol",
  messages: [{ role: "user", content: "hi" }],
  max_tokens: 1024
});
\`\`\`

What changes on the way out:

- \`messages\` becomes \`input\` on the Responses path
- \`max_tokens\` becomes \`max_output_tokens\`
- \`system\` is lifted out of the array for an Anthropic upstream
- everything else is passed through untouched

Read the [operator guide](https://github.com/jisunahamed/rotakey) for the rest.`;

const LONG = Array.from(
  { length: 40 },
  (_, index) =>
    `**Paragraph ${index + 1}.** Rotation is per request, not per session: the key that served the ` +
    `last request has no claim on the next one. That is what makes a pool of five keys behave like ` +
    `one key with five times the headroom, and it is also why a single bad key shows up as an ` +
    `error rate rather than an outage.`
).join("\n\n");

function replyFor(prompt: string, turnNumber: number): string {
  const asked = prompt.toLowerCase();
  if (asked.includes("empty")) return "";
  if (asked.includes("long")) return `**Turn ${turnNumber}.**\n\n${LONG}`;
  return `**Turn ${turnNumber}.** ${DOC}`;
}

/** Cut into small pieces so the transcript is exercised the way a real reply
 *  exercises it: an unterminated fence for several frames, a heading arriving
 *  one character at a time, and a list that grows an item at a time. */
function chunk(text: string): string[] {
  const pieces: string[] = [];
  for (let index = 0; index < text.length; ) {
    const size = 3 + ((index * 7) % 9);
    pieces.push(text.slice(index, index + size));
    index += size;
  }
  return pieces;
}

function frame(payload: unknown, event = ""): string {
  const head = event === "" ? "" : `event: ${event}\n`;
  return `${head}data: ${JSON.stringify(payload)}\n\n`;
}

type Protocol = "chat" | "responses" | "messages";

function textFrame(protocol: Protocol, piece: string): string {
  if (protocol === "responses") {
    return frame({ type: "response.output_text.delta", delta: piece }, "response.output_text.delta");
  }
  if (protocol === "messages") {
    return frame({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: piece } }, "content_block_delta");
  }
  return frame({
    id: "chatcmpl-dev",
    object: "chat.completion.chunk",
    choices: [{ index: 0, delta: { content: piece }, finish_reason: null }]
  });
}

function openingFrames(protocol: Protocol, model: string, inputTokens: number): string[] {
  if (protocol === "messages") {
    return [
      frame(
        {
          type: "message_start",
          message: { id: "msg_dev", role: "assistant", model, usage: { input_tokens: inputTokens, output_tokens: 0 } }
        },
        "message_start"
      ),
      frame({ type: "content_block_start", index: 0, content_block: { type: "text", text: "" } }, "content_block_start")
    ];
  }
  if (protocol === "responses") {
    return [frame({ type: "response.created", response: { id: "resp_dev", model } }, "response.created")];
  }
  return [];
}

function closingFrames(protocol: Protocol, model: string, inputTokens: number, outputTokens: number): string[] {
  if (protocol === "messages") {
    return [
      frame({ type: "content_block_stop", index: 0 }, "content_block_stop"),
      frame(
        { type: "message_delta", delta: { stop_reason: "end_turn" }, usage: { output_tokens: outputTokens } },
        "message_delta"
      ),
      frame({ type: "message_stop" }, "message_stop")
    ];
  }
  if (protocol === "responses") {
    return [
      frame(
        {
          type: "response.completed",
          response: { id: "resp_dev", model, usage: { input_tokens: inputTokens, output_tokens: outputTokens } }
        },
        "response.completed"
      )
    ];
  }
  return [
    frame({
      id: "chatcmpl-dev",
      object: "chat.completion.chunk",
      choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
      usage: { prompt_tokens: inputTokens, completion_tokens: outputTokens, total_tokens: inputTokens + outputTokens }
    }),
    "data: [DONE]\n\n"
  ];
}

function bufferedReply(protocol: Protocol, model: string, text: string, inputTokens: number, outputTokens: number) {
  if (protocol === "messages") {
    return {
      id: "msg_dev",
      type: "message",
      role: "assistant",
      model,
      content: text === "" ? [] : [{ type: "text", text }],
      stop_reason: "end_turn",
      usage: { input_tokens: inputTokens, output_tokens: outputTokens }
    };
  }
  if (protocol === "responses") {
    return {
      id: "resp_dev",
      object: "response",
      model,
      output: text === "" ? [] : [{ type: "message", role: "assistant", content: [{ type: "output_text", text }] }],
      output_text: text,
      usage: { input_tokens: inputTokens, output_tokens: outputTokens }
    };
  }
  return {
    id: "chatcmpl-dev",
    object: "chat.completion",
    model,
    choices: [{ index: 0, message: { role: "assistant", content: text }, finish_reason: "stop" }],
    usage: { prompt_tokens: inputTokens, completion_tokens: outputTokens, total_tokens: inputTokens + outputTokens }
  };
}

async function playgroundRun(req: Req, res: Res) {
  const body = await readBody(req);
  const model = String(body.model ?? "");
  const protocol = (["chat", "responses", "messages"].includes(String(body.protocol))
    ? String(body.protocol)
    : "chat") as Protocol;
  const streaming = body.stream !== false;
  const messages = Array.isArray(body.messages) ? (body.messages as Array<{ role?: string; content?: string }>) : [];
  const lastUser = [...messages].reverse().find((turn) => turn.role === "user")?.content ?? String(body.prompt ?? "");
  const asked = lastUser.toLowerCase();
  const turnNumber = messages.filter((turn) => turn.role === "user").length || 1;

  if (asked.includes("reject")) {
    await slowly(null, 400);
    fail(res, 429, "rate_limit_exceeded", "No key could take this request. All 3 keys were over a rate limit — the first frees up in 24s.");
    return;
  }

  const target = providers
    .flatMap((provider) => provider.models.map((route) => ({ provider, route })))
    .find(({ route }) => route.public_alias === model);
  const requestID = `req_dev${String(++requestCounter).padStart(4, "0")}`;
  const text = replyFor(asked, turnNumber);
  const inputTokens = Math.max(1, Math.round(messages.reduce((sum, turn) => sum + (turn.content?.length ?? 0), 0) / 4));
  const outputTokens = Math.max(1, Math.round(text.length / 4));
  const startedAt = Date.now();

  res.setHeader("X-Rotakey-Request-Id", requestID);
  res.setHeader("X-Rotakey-Playground-Protocol", protocol);
  if (asked.includes("switch")) res.setHeader("X-Rotakey-Switched-Endpoint", "responses");
  if (asked.includes("strip")) {
    res.setHeader("X-Rotakey-Removed-Parameters", "stream_options, service_tier");
    res.setHeader("X-Rotakey-Replaced-Parameters", "max_tokens=max_output_tokens");
  }

  const failing = asked.includes("fail");
  const record = (interrupted: boolean) => {
    minted.unshift({
      id: `log_dev_${requestCounter}`,
      request_id: requestID,
      model_alias: model,
      provider_name: target?.provider.name ?? "Azure Foundry",
      credential_label: target?.provider.credentials.find((c) => c.status === "healthy")?.label ?? "eastus · primary",
      endpoint: protocol === "messages" ? "/v1/messages" : protocol === "responses" ? "/v1/responses" : "/v1/chat/completions",
      public_protocol: protocol === "messages" ? "anthropic" : "openai",
      upstream_protocol: target?.provider.api_format ?? "openai",
      upstream_request_id: `upstream_${requestID}`,
      attempts: [],
      routing_decisions: [],
      status_code: 200,
      latency_ms: Date.now() - startedAt,
      input_tokens: inputTokens,
      output_tokens: interrupted ? 0 : outputTokens,
      error_code: interrupted ? "stream_interrupted" : undefined,
      error_message: interrupted ? "The reply stopped after it started." : undefined,
      body_captured: target?.route.capture_bodies ?? false,
      body_truncated: false,
      created_at: new Date(startedAt).toISOString()
    });
    minted.splice(40);
  };

  if (!streaming) {
    await slowly(null, 700);
    record(failing);
    const payload = bufferedReply(protocol, model, text, inputTokens, outputTokens) as Record<string, unknown>;
    if (failing) {
      payload.error = { code: "upstream_error", message: "The provider returned a 500 halfway through." };
    }
    json(res, 200, payload);
    return;
  }

  res.statusCode = 200;
  res.setHeader("Content-Type", "text/event-stream");
  res.setHeader("Cache-Control", "no-cache");
  res.setHeader("Connection", "keep-alive");
  res.setHeader("X-Accel-Buffering", "no");

  const pieces = chunk(text);
  const pace = asked.includes("slow") ? 140 : 14;
  const queue = [
    ...openingFrames(protocol, model, inputTokens),
    ...pieces.map((piece) => textFrame(protocol, piece))
  ];
  if (failing) {
    // A failure after a 200 arrives inside the stream, which is the whole reason
    // the reader has to look for one on every frame.
    queue.splice(
      Math.floor(queue.length / 3),
      queue.length,
      frame({ error: { code: "upstream_error", message: "The provider dropped the connection 1.2s in." } })
    );
  } else {
    queue.push(...closingFrames(protocol, model, inputTokens, outputTokens));
  }

  let index = 0;
  let closed = false;
  req.on("close", () => {
    closed = true;
  });
  const tick = () => {
    if (closed) return;
    if (index >= queue.length) {
      record(failing);
      res.end();
      return;
    }
    res.write(queue[index]);
    index += 1;
    setTimeout(tick, index === 1 ? 320 : pace);
  };
  setTimeout(tick, 240);
}

/* ── Routing ───────────────────────────────────────────────────────────────── */

function findLog(id: string): RequestLog | undefined {
  return [...minted, ...logs].find((row) => row.id === id || row.request_id === id);
}

/** Routes whose learned state has been dropped this session, so that "Forget it"
 *  visibly changes the panel instead of leaving the same three sentences under a
 *  success toast. Redis holds this on a real install; nothing here does. */
const forgotten = new Set<string>();

/** Expiries are stamped when the fact is read rather than fixed in the fixture,
 *  because the panel's whole claim is that these are facts with a clock on them —
 *  a stamp from whenever the file was written would render as "about to forget"
 *  on every load. */
function learnedFacts(id: string): LearnedFact[] {
  if (id !== "mdl_azure_sol") return [];
  const hours = (count: number) => new Date(Date.now() + count * 3600_000).toISOString();
  return [
    { kind: "prefer_responses", expires_at: hours(21) },
    { kind: "rename_parameters", endpoint: "responses", renames: [["max_tokens", "max_output_tokens"]], expires_at: hours(21) },
    { kind: "strip_parameters", endpoint: "responses", parameters: ["stream_options"], expires_at: hours(9) },
    // The Codex field. It is the longest sentence the panel can produce, so it is
    // the one worth having on screen while the layout is being judged.
    { kind: "strip_item_fields", parameters: ["input[].internal_chat_message_metadata_passthrough"], expires_at: hours(23) }
  ];
}

async function handle(req: Req, res: Res, url: URL): Promise<boolean> {
  const path = url.pathname;
  const method = (req.method ?? "GET").toUpperCase();

  if (path === "/api/setup/status") return json(res, 200, { setup_required: false }), true;
  if (path === "/api/auth/session") return json(res, 200, { username: "owner", csrf_token: CSRF }), true;
  if (path === "/api/auth/login" && method === "POST") {
    await slowly(null, 500);
    return json(res, 200, { csrf_token: CSRF }), true;
  }
  if (path === "/api/auth/logout") return (res.statusCode = 204), res.end(), true;
  if (path === "/api/version") return json(res, 200, version), true;

  if (path === "/api/admin/playground/run" && method === "POST") {
    await playgroundRun(req, res);
    return true;
  }

  if (path === "/api/admin/providers" && method === "GET") {
    return json(res, 200, await slowly({ providers })), true;
  }
  if (path === "/api/admin/settings" && method === "GET") {
    return json(res, 200, await slowly(settings)), true;
  }
  if (path === "/api/admin/settings" && method === "PUT") {
    const body = await readBody(req);
    Object.assign(settings, body);
    return (
      json(res, 200, {
        routing_mode: settings.routing_mode,
        aliases_rewritten: 0,
        alias_conflicts: [],
        providers_retimed: body.apply_timeout_to_all_providers ? providers.length : 0
      }),
      true
    );
  }
  if (path === "/api/admin/overview" && method === "GET") {
    const range = (url.searchParams.get("range") ?? "1h") as Overview["range"];
    return json(res, 200, await slowly(overview(range))), true;
  }
  if (path === "/api/admin/access/rotate" && method === "POST") {
    // `gw_` and 43 characters after it, which is what `randomToken("gw_", 32)`
    // produces, so the Connect panel lays out against a key the length of a real
    // one. The earlier fixture here was `rk_live_…`, which is not a shape this
    // gateway has ever minted and is the shape of a Stripe restricted key —
    // enough for a secret scanner to stop the push, which is the correct
    // behaviour for that string and the reason it is gone.
    return json(res, 200, await slowly({ gateway_key: "gw_devONLYnotARealGatewayKey000000000000000000" }, 600)), true;
  }

  /* What the gateway taught itself, which lives in Redis on a real install and
     nowhere at all here. The fixture answers for one route — the one this panel
     was built for, whose learned preference sent a Chat body to /responses for a
     day while every screen stayed truthful about its own half — because a panel
     that is only ever empty cannot be looked at. */
  const learnedMatch = /^\/api\/admin\/models\/([^/]+)\/learned$/.exec(path);
  if (learnedMatch) {
    const id = decodeURIComponent(learnedMatch[1]);
    if (!providers.some((provider) => provider.models.some((model) => model.id === id))) {
      return fail(res, 404, "not_found", "There is no route with that id."), true;
    }
    if (method === "DELETE") {
      forgotten.add(id);
      return (res.statusCode = 204), res.end(), true;
    }
    return json(res, 200, await slowly({ facts: forgotten.has(id) ? [] : learnedFacts(id) }, 160)), true;
  }

  const bodyMatch = /^\/api\/admin\/logs\/([^/]+)\/bodies$/.exec(path);
  if (bodyMatch) {
    const row = findLog(decodeURIComponent(bodyMatch[1]));
    if (!row) return fail(res, 404, "not_found", "That request is no longer in the log."), true;
    const captured = bodies[row.id];
    return json(res, 200, await slowly(captured ?? { request: null, response: null })), true;
  }

  const oneMatch = /^\/api\/admin\/logs\/([^/]+)$/.exec(path);
  if (oneMatch && method === "GET") {
    const row = findLog(decodeURIComponent(oneMatch[1]));
    if (!row) return fail(res, 404, "not_found", "That request is no longer in the log."), true;
    return json(res, 200, { log: row }), true;
  }

  if (path === "/api/admin/logs" && method === "GET") {
    const query = (url.searchParams.get("q") ?? "").toLowerCase();
    const status = url.searchParams.get("status") ?? "";
    const rows = [...minted, ...logs].filter((row) => {
      if (query !== "") {
        const haystack = `${row.request_id} ${row.model_alias} ${row.provider_name} ${row.credential_label}`.toLowerCase();
        if (!haystack.includes(query)) return false;
      }
      if (status === "errors") return row.status_code >= 400 || (row.error_code ?? "") !== "";
      if (status !== "" && /^\d+$/.test(status)) return row.status_code === Number(status);
      return true;
    });
    return json(res, 200, await slowly({ logs: rows }, 180)), true;
  }

  // Anything else under /api/admin is a write this fixture has not been taught.
  // It answers with a sentence that says so rather than a silent 200, because a
  // fake success is how a fixture starts lying about what the console can do.
  if (path.startsWith("/api/admin") || path.startsWith("/api/")) {
    return (
      fail(
        res,
        501,
        "not_implemented",
        `The dev fixture has no answer for ${method} ${path}. It serves reads, the playground and settings; writes are exercised on a real gateway.`
      ),
      true
    );
  }
  return false;
}

export function devAPI(): Plugin {
  return {
    name: "rotakey-dev-api",
    // Registered from the hook body rather than from a returned function, so it
    // sits ahead of Vite's own middlewares instead of behind them.
    configureServer(server) {
      // Connect's own parameter types name Node's IncomingMessage, which has no
      // declaration here; the handler is written against the members it uses and
      // narrowed at the boundary.
      server.middlewares.use((incoming, outgoing, next) => {
        const req = incoming as unknown as Req;
        const res = outgoing as unknown as Res;
        const url = new URL(req.url ?? "/", "http://localhost");
        if (!url.pathname.startsWith("/api/")) {
          next();
          return;
        }
        void handle(req, res, url).then((handled) => {
          if (!handled) next();
        });
      });
      server.config.logger.info("  [36m➜[0m  [1mmock API:[0m  on — /api is served from dev-api/fixtures.ts");
    }
  };
}
