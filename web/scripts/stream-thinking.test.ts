import assert from "node:assert/strict";
import { test } from "node:test";
import { readEventStream } from "../src/lib/stream.ts";

for (const [protocol, payload] of [
  ["chat", { choices: [{ delta: { reasoning_content: "reason" } }] }],
  ["chat", { choices: [{ delta: { reasoning: "reason" } }] }],
  ["responses", { type: "response.reasoning_summary_text.delta", delta: "reason" }],
  ["responses", { type: "response.reasoning_text.delta", delta: "reason" }],
  ["messages", { type: "content_block_delta", delta: { type: "thinking_delta", thinking: "reason" } }],
] as const) {
  test(`${protocol} thinking arrives before the stream closes: ${JSON.stringify(payload)}`, async () => {
    let control!: ReadableStreamDefaultController<Uint8Array>;
    const body = new ReadableStream<Uint8Array>({ start(c) { control = c; } });
    let seen!: () => void;
    const arrived = new Promise<void>((resolve) => { seen = resolve; });
    const values: unknown[] = [];
    const reading = readEventStream(new Response(body), protocol, (delta) => { values.push(delta); seen(); });
    const encoded = new TextEncoder().encode(`data: ${JSON.stringify(payload)}\r\n\r\n`);
    control.enqueue(encoded.slice(0, 7)); control.enqueue(encoded.slice(7));
    await arrived;
    assert.deepEqual(values, [{ kind: "thinking", text: "reason" }]);
    control.close(); await reading;
  });
}
