/**
 * Reading a streamed reply.
 *
 * Rotakey speaks three wire shapes and the playground has to render all three
 * as one growing paragraph of text. Everything protocol-specific lives in the
 * three readers at the bottom of this file; everything above them is the same
 * for all three.
 *
 * Three rules here are not preferences. Breaking any one of them produces a
 * playground that appears to work until the day it hangs:
 *
 *  1. The stream ends when the reader says it ends, never at a sentinel. The
 *     Anthropic path finishes at `message_stop` and never sends `data: [DONE]`,
 *     so a parser waiting for the sentinel waits forever. Reader-done is also
 *     exactly the moment the gateway has finished writing its log row, so the
 *     same rule makes the evidence lookup safe.
 *  2. Unknown event names are ignored. Three of the nine protocol pairs are
 *     byte pumps that forward whatever the provider sent — pings, progress
 *     notices, vendor extensions — and a reader that treats an unrecognised
 *     event as a fault will break on providers it has never seen.
 *  3. An error after a 200 arrives inside the stream, not as a status code.
 *     Every reader looks for it, because the alternative is a reply that stops
 *     mid-sentence with nothing on screen to say why.
 */

export type PlaygroundProtocol = "chat" | "responses" | "messages";

export type ReplyDelta =
  | { kind: "text"; text: string }
  | { kind: "usage"; input: number | null; output: number | null }
  | { kind: "error"; message: string; code: string };

type StreamFrame = { event: string; data: string };

/**
 * Reads one server-sent event stream to its end, calling back once per piece of
 * the reply. Resolves when the response body is finished. Cancelling the
 * request's AbortSignal makes the underlying read throw; the caller owns that.
 */
export async function readEventStream(
  response: Response,
  protocol: PlaygroundProtocol,
  onDelta: (delta: ReplyDelta) => void
): Promise<void> {
  const body = response.body;
  if (!body) {
    onDelta({
      kind: "error",
      code: "stream_unavailable",
      message: "The reply arrived without a readable body."
    });
    return;
  }
  const read = readerFor(protocol);
  const decoder = new TextDecoder();
  const reader = body.getReader();
  let buffer = "";
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        // Whatever is left in the buffer is an unterminated frame. A provider
        // that closes cleanly does not leave one, so this only runs after a
        // truncated stream, and a partial frame is not worth guessing at.
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      let boundary = nextBoundary(buffer);
      while (boundary) {
        const frame = buffer.slice(0, boundary.index);
        buffer = buffer.slice(boundary.index + boundary.length);
        for (const delta of read(parseFrame(frame))) {
          onDelta(delta);
        }
        boundary = nextBoundary(buffer);
      }
    }
  } finally {
    reader.releaseLock();
  }
}

/** Finds the blank line that ends a frame, in either line ending. */
function nextBoundary(buffer: string): { index: number; length: number } | null {
  const unix = buffer.indexOf("\n\n");
  const windows = buffer.indexOf("\r\n\r\n");
  if (unix < 0 && windows < 0) return null;
  if (windows >= 0 && (unix < 0 || windows < unix)) {
    return { index: windows, length: 4 };
  }
  return { index: unix, length: 2 };
}

function parseFrame(frame: string): StreamFrame {
  let event = "";
  const data: string[] = [];
  for (const rawLine of frame.split("\n")) {
    const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;
    // A line beginning with a colon is a comment. Providers use them as
    // keep-alives, and one arrives every few seconds on a slow reply.
    if (line === "" || line.startsWith(":")) continue;
    const separator = line.indexOf(":");
    const field = separator < 0 ? line : line.slice(0, separator);
    let value = separator < 0 ? "" : line.slice(separator + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") event = value;
    else if (field === "data") data.push(value);
  }
  return { event, data: data.join("\n") };
}

function readerFor(protocol: PlaygroundProtocol) {
  if (protocol === "responses") return readResponsesFrame;
  if (protocol === "messages") return readAnthropicFrame;
  return readChatFrame;
}

function payloadOf(frame: StreamFrame): Record<string, unknown> | null {
  if (frame.data === "" || frame.data === "[DONE]") return null;
  try {
    const parsed = JSON.parse(frame.data);
    return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

/**
 * The gateway's own mid-stream failure is written as a bare `{"error": …}`
 * frame on both OpenAI-shaped paths and on the Anthropic path, so all three
 * readers check for it before anything else.
 */
function errorIn(value: unknown): ReplyDelta | null {
  const error = (value as { error?: unknown } | null)?.error;
  if (!error || typeof error !== "object") return null;
  const detail = error as { message?: unknown; code?: unknown; type?: unknown };
  return {
    kind: "error",
    message: typeof detail.message === "string" ? detail.message : "The reply failed after it started.",
    code:
      typeof detail.code === "string"
        ? detail.code
        : typeof detail.type === "string"
          ? detail.type
          : "stream_failed"
  };
}

function count(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function readChatFrame(frame: StreamFrame): ReplyDelta[] {
  const payload = payloadOf(frame);
  if (!payload) return [];
  const failure = errorIn(payload);
  if (failure) return [failure];
  const deltas: ReplyDelta[] = [];
  const choices = payload.choices;
  if (Array.isArray(choices)) {
    for (const choice of choices) {
      const text = (choice as { delta?: { content?: unknown } })?.delta?.content;
      if (typeof text === "string" && text !== "") {
        deltas.push({ kind: "text", text });
      }
    }
  }
  // Present only when the request asked for include_usage, and then only on the
  // final frame. Without it the console has no measured counts and says so.
  const usage = payload.usage as { prompt_tokens?: unknown; completion_tokens?: unknown } | undefined;
  if (usage) {
    deltas.push({ kind: "usage", input: count(usage.prompt_tokens), output: count(usage.completion_tokens) });
  }
  return deltas;
}

function readResponsesFrame(frame: StreamFrame): ReplyDelta[] {
  const payload = payloadOf(frame);
  if (!payload) return [];
  const failure = errorIn(payload);
  if (failure) return [failure];
  const type = typeof payload.type === "string" ? payload.type : frame.event;
  if (type === "response.output_text.delta") {
    const text = payload.delta;
    return typeof text === "string" && text !== "" ? [{ kind: "text", text }] : [];
  }
  if (type === "response.completed" || type === "response.incomplete" || type === "response.failed") {
    const response = payload.response as
      | { usage?: { input_tokens?: unknown; output_tokens?: unknown }; error?: unknown }
      | undefined;
    const deltas: ReplyDelta[] = [];
    const inner = errorIn(response);
    if (inner) deltas.push(inner);
    if (response?.usage) {
      deltas.push({
        kind: "usage",
        input: count(response.usage.input_tokens),
        output: count(response.usage.output_tokens)
      });
    }
    return deltas;
  }
  return [];
}

function readAnthropicFrame(frame: StreamFrame): ReplyDelta[] {
  const payload = payloadOf(frame);
  if (!payload) return [];
  const failure = errorIn(payload);
  if (failure) return [failure];
  const type = typeof payload.type === "string" ? payload.type : frame.event;
  if (type === "content_block_delta") {
    const delta = payload.delta as { type?: unknown; text?: unknown } | undefined;
    const text = delta?.text;
    return typeof text === "string" && text !== "" ? [{ kind: "text", text }] : [];
  }
  // Anthropic splits its usage across the first and last events: the input
  // count arrives before any text does, the output count only at the end.
  if (type === "message_start") {
    const usage = (payload.message as { usage?: { input_tokens?: unknown } } | undefined)?.usage;
    return usage ? [{ kind: "usage", input: count(usage.input_tokens), output: null }] : [];
  }
  if (type === "message_delta") {
    const usage = payload.usage as { input_tokens?: unknown; output_tokens?: unknown } | undefined;
    return usage ? [{ kind: "usage", input: count(usage.input_tokens), output: count(usage.output_tokens) }] : [];
  }
  return [];
}
