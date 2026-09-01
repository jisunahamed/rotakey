/** Sending one turn to the gateway and reading what comes back.
 *
 *  This is the only place in the console that talks to `/api/admin/playground/run`,
 *  and it is deliberately not a React hook: it takes a conversation and a signal
 *  and returns what happened, so the page owns the state and this file owns the
 *  wire.
 *
 *  The request goes through the real dispatch path — the same key rotation, the
 *  same limits, the same failover as an application's request. That is the point
 *  of the page, and it is why the evidence below is worth collecting: it is the
 *  only place an operator can see a routing decision next to the reply it
 *  produced.
 */

import { api, apiStream } from "../../api";
import { errorMessage } from "../../lib/format";
import { readEventStream, type PlaygroundProtocol } from "../../lib/stream";
import { routingStageLabel } from "../../lib/copy";
import type { RequestLog } from "../../types";
import type { Conversation, Evidence, Turn } from "./session";

export type RunOutcome = {
  text: string;
  error: string;
  errorCode: string;
  stopped: boolean;
  evidence: Evidence;
};

function emptyEvidence(): Evidence {
  return {
    requestID: "",
    protocol: "",
    elapsedMS: 0,
    inputTokens: null,
    outputTokens: null,
    streamed: false,
    removed: [],
    replaced: [],
    switched: "",
    servedBy: "",
    providerName: "",
    upstreamProtocol: "",
    statusCode: 0,
    logFound: false
  };
}

function protocolFrom(header: string | null): PlaygroundProtocol {
  return header === "responses" || header === "messages" ? header : "chat";
}

function listFrom(header: string | null): string[] {
  return (header ?? "")
    .split(",")
    .map((value) => value.trim())
    .filter((value) => value !== "");
}

/** The gateway writes replacements as `from=to,from=to`. */
function pairsFrom(header: string | null): Array<[string, string]> {
  return listFrom(header).map((entry) => {
    const split = entry.indexOf("=");
    return split < 0 ? [entry, ""] : [entry.slice(0, split), entry.slice(split + 1)];
  });
}

function textIn(value: unknown): string {
  if (typeof value === "string") return value;
  // Both OpenAI shapes allow content to be a list of typed parts, and the
  // Anthropic shape always uses one.
  if (!Array.isArray(value)) return "";
  return value
    .map((part) => (part as { text?: unknown })?.text)
    .filter((part): part is string => typeof part === "string")
    .join("");
}

/** Pulls the reply out of a buffered response, in whichever of the three shapes
 *  it came back in. Returns an empty string when there is no text in it, which
 *  the transcript states rather than papering over with a JSON dump — printing
 *  `JSON.stringify` of a response as if the model had written it is what this
 *  page used to do. */
function replyText(payload: Record<string, unknown>): string {
  if (typeof payload.output_text === "string" && payload.output_text !== "") return payload.output_text;

  const choices = Array.isArray(payload.choices) ? payload.choices : [];
  const message = (choices[0] as { message?: { content?: unknown } } | undefined)?.message;
  const chat = textIn(message?.content);
  if (chat !== "") return chat;

  const anthropic = textIn(payload.content);
  if (anthropic !== "") return anthropic;

  const output = Array.isArray(payload.output) ? payload.output : [];
  return output.map((item) => textIn((item as { content?: unknown })?.content)).join("");
}

function usageIn(payload: Record<string, unknown>) {
  const usage = payload.usage as Record<string, unknown> | undefined;
  const read = (...names: string[]) => {
    for (const name of names) {
      const value = usage?.[name];
      if (typeof value === "number" && Number.isFinite(value)) return value;
    }
    return null;
  };
  return { input: read("prompt_tokens", "input_tokens"), output: read("completion_tokens", "output_tokens") };
}

/** The gateway's own failure, when it arrives inside a 200. */
function errorIn(payload: Record<string, unknown>) {
  const error = payload.error as { message?: unknown; code?: unknown; type?: unknown } | undefined;
  if (!error || typeof error !== "object") return null;
  return {
    message: typeof error.message === "string" ? error.message : "The reply failed after it started.",
    code:
      typeof error.code === "string" ? error.code : typeof error.type === "string" ? error.type : "request_failed"
  };
}

/** Which key served the request, and what it was translated into.
 *
 *  Read after the reply is complete, never during it. The log row is written
 *  before the handler returns and net/http only ends the chunked body at that
 *  point, so a reader that waited for the stream to finish is guaranteed to find
 *  it. A 404 here means the row aged out of retention, which is not an error —
 *  the reply above it is still true, and the strip simply says less. */
async function readEvidenceLog(requestID: string): Promise<Partial<Evidence>> {
  try {
    const { log } = await api<{ log: RequestLog }>(`/api/admin/logs/${encodeURIComponent(requestID)}`);
    return {
      servedBy: routingStageLabel(log),
      providerName: log.provider_name ?? "",
      upstreamProtocol: log.upstream_protocol ?? "",
      statusCode: log.status_code ?? 0,
      logFound: true
    };
  } catch {
    return { logFound: false };
  }
}

export async function runTurn({
  chat,
  turns,
  signal,
  onText
}: {
  chat: Conversation;
  /** The conversation as it should be sent: every turn up to and including the
   *  one being answered. */
  turns: Turn[];
  signal: AbortSignal;
  onText: (text: string) => void;
}): Promise<RunOutcome> {
  const evidence = emptyEvidence();
  // `decodeJSON` refuses unknown fields, so this object is exactly
  // `playgroundInput` and nothing else. `prompt` is never sent beside
  // `messages`: the server rejects the pair rather than guessing.
  const body: Record<string, unknown> = {
    model: chat.model,
    messages: turns.map((turn) => ({ role: turn.role, content: turn.text })),
    protocol: chat.settings.protocol,
    stream: chat.settings.stream,
    max_tokens: chat.settings.maxTokens,
    temperature: chat.settings.temperature
  };
  const system = chat.settings.system.trim();
  if (system !== "") body.system = system;

  const startedAt = performance.now();
  let response: Response;
  try {
    response = await apiStream("/api/admin/playground/run", body, signal);
  } catch (caught) {
    evidence.elapsedMS = performance.now() - startedAt;
    if (signal.aborted) {
      return { text: "", error: "", errorCode: "", stopped: true, evidence };
    }
    return {
      text: "",
      error: errorMessage(caught),
      errorCode: (caught as { code?: string })?.code ?? "request_failed",
      stopped: false,
      evidence
    };
  }

  evidence.requestID = response.headers.get("X-Rotakey-Request-Id") ?? "";
  evidence.protocol = protocolFrom(response.headers.get("X-Rotakey-Playground-Protocol"));
  evidence.removed = listFrom(response.headers.get("X-Rotakey-Removed-Parameters"));
  evidence.replaced = pairsFrom(response.headers.get("X-Rotakey-Replaced-Parameters"));
  evidence.switched = response.headers.get("X-Rotakey-Switched-Endpoint") ?? "";
  evidence.streamed = chat.settings.stream;

  let text = "";
  let error = "";
  let errorCode = "";
  let stopped = false;

  try {
    if (chat.settings.stream) {
      await readEventStream(response, evidence.protocol as PlaygroundProtocol, (delta) => {
        if (delta.kind === "text") {
          text += delta.text;
          onText(text);
          return;
        }
        if (delta.kind === "usage") {
          // Anthropic reports the input count first and the output count last,
          // so each half is kept as it arrives rather than overwritten by the
          // next frame's nulls.
          if (delta.input !== null) evidence.inputTokens = delta.input;
          if (delta.output !== null) evidence.outputTokens = delta.output;
          return;
        }
        error = delta.message;
        errorCode = delta.code;
      });
    } else {
      const payload = (await response.json()) as Record<string, unknown>;
      const failure = errorIn(payload);
      if (failure) {
        error = failure.message;
        errorCode = failure.code;
      }
      text = replyText(payload);
      if (text !== "") onText(text);
      const usage = usageIn(payload);
      evidence.inputTokens = usage.input;
      evidence.outputTokens = usage.output;
    }
  } catch (caught) {
    if (signal.aborted) {
      stopped = true;
    } else {
      error = errorMessage(caught);
      errorCode = "stream_interrupted";
    }
  }

  evidence.elapsedMS = performance.now() - startedAt;
  // A stopped request has no settled log row to read: the handler is still
  // unwinding a connection the browser closed underneath it.
  if (evidence.requestID !== "" && !stopped) {
    Object.assign(evidence, await readEvidenceLog(evidence.requestID));
  }
  return { text, error, errorCode, stopped, evidence };
}
