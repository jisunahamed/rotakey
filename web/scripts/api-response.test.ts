import assert from "node:assert/strict";
import { test } from "node:test";
import { api } from "../src/api.ts";

test("accepted background jobs allow an empty success response", async () => {
  const previous = globalThis.fetch;
  try {
    globalThis.fetch = async () => new Response(null, { status: 202 });
    assert.equal(await api("/api/admin/drive-backup/run", { method: "POST", json: { action: "upload" } }), undefined);
    globalThis.fetch = async () => Response.json({ status: "accepted" }, { status: 202 });
    assert.deepEqual(await api("/api/admin/drive-backup/run", { method: "POST" }), { status: "accepted" });
    globalThis.fetch = async () => new Response(null, { status: 204 });
    assert.equal(await api("/api/admin/drive-backup", { method: "PUT" }), undefined);
    globalThis.fetch = async () => new Response("{broken", { status: 200 });
    await assert.rejects(api("/api/admin/drive-backup"), SyntaxError);
    globalThis.fetch = async () => Response.json({ error: { message: "Backup is running" } }, { status: 409 });
    await assert.rejects(api("/api/admin/drive-backup/run"), /Backup is running/);
  } finally {
    globalThis.fetch = previous;
  }
});
