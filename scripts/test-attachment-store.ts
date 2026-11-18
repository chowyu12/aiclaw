import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile, lstat, rm, symlink, unlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { AttachmentStore } from "../apps/desktop/src/main/attachment-store.ts";
import { parseDocumentAttachment } from "../apps/desktop/src/main/document-parser.ts";
import { inlineDocument, attachmentDisplayText } from "../apps/desktop/src/renderer/attachments.ts";

async function setup(run: (store: AttachmentStore, root: string) => Promise<void>) {
  const root = await mkdtemp(join(tmpdir(), "aiclaw-attachment-store-"));
  try { await run(new AttachmentStore(root, parseDocumentAttachment), root); }
  finally { await rm(root, { recursive: true, force: true }); }
}
test("original uploads are private, immutable, retained beyond preview limits and bound only by valid claims", async () => setup(async (store, root) => {
  const data = Buffer.from("中".repeat(60000) + "THE_END");
  const file = await store.stage({ name: "../report.txt", data, sessionId: "s" });
  assert.equal(file.name, "report.txt");
  assert.deepEqual(await readFile(join(root, file.id, "original")), data);
  if (process.platform !== "win32") {
    assert.equal((await lstat(join(root, file.id))).mode & 0o777, 0o700);
    assert.equal((await lstat(join(root, file.id, "original"))).mode & 0o777, 0o600);
  }
  assert.deepEqual(await store.list("s"), []);
  await assert.rejects(store.read("s", file.id), /belong/);
  await assert.rejects(store.bind("other", [file]), /belong/);
  await assert.rejects(store.bind("s", [{ id: file.id, claimToken: "forged" }]), /belong/);
  await store.bind("s", [file]);
  await store.bind("s", [file]); // Retry is idempotent.
  assert.equal((await store.list("s")).length, 1);
  assert.deepEqual(await store.original("s", file.id), { name: "report.txt", bytes: data });
  assert.deepEqual(await store.list("other"), []);
  await assert.rejects(store.original("other", file.id), /belong/);
  assert.ok(!JSON.stringify(await store.list("s")).includes(file.claimToken));
  const prompt = inlineDocument({ kind: "document", name: file.name, file });
  assert.ok(prompt.includes(`[attachment:${file.id}]`));
  assert.ok(!prompt.includes(file.claimToken));
  assert.ok(!prompt.includes("THE_END"));
  assert.ok(!attachmentDisplayText(prompt).includes(file.id));
  assert.ok(attachmentDisplayText(prompt).includes(file.name));
  assert.equal(attachmentDisplayText("untouched [attachment:example]"), "untouched [attachment:example]");
  let offset = 0, text = "";
  do {
    const result = await store.read("s", file.id, { mode: "read", offset, limit: 32768 });
    assert.ok(!result.text.includes("\uFFFD"));
    text += result.text;
    offset = result.nextOffset ?? 0;
  } while (offset);
  assert.equal(text, data.toString());
  await assert.rejects(store.read("other", file.id), /belong/);
  await assert.rejects(store.read("s", "../../etc/passwd"), /Invalid attachment ID/);
}));
test("attachment references and cached reads survive restart, fork and parent deletion", async () => setup(async (store, root) => {
  const file = await store.stage({ name: "a.txt", data: Buffer.from("original text"), sessionId: "" });
  await store.bind("new-session", [file]);
  const query = { mode: "read" as const, offset: 0, limit: 8 };
  const first = await store.read("new-session", file.id, query);
  const restored = new AttachmentStore(root, async () => { throw Error("cached result expected"); });
  assert.deepEqual(await restored.read("new-session", file.id, query), first);
  await restored.fork("new-session", "fork");
  await restored.removeSession("new-session");
  assert.equal((await restored.list("fork"))[0]?.id, file.id);
  assert.deepEqual(await restored.read("fork", file.id, query), first);
  await assert.rejects(restored.read("new-session", file.id), /belong/);
  assert.equal((await readFile(join(root, file.id, "original"))).toString(), "original text");
}));
test("invalid multi-file claims bind nothing, and drafts cannot claim another session's upload", async () => setup(async (store) => {
  const a = await store.stage({ name: "a.txt", data: Buffer.from("a"), sessionId: "s" });
  const b = await store.stage({ name: "b.txt", data: Buffer.from("b"), sessionId: "other" });
  await assert.rejects(store.bind("s", [a, b]), /belong/);
  assert.deepEqual(await store.list("s"), []);
  await store.bind("other", [b]);
  await assert.rejects(store.bind("s", [b]), /belong/);
}));
test("cache queries are isolated and original tampering is detected even on a cache hit", async () => setup(async (store, root) => {
  const file = await store.stage({ name: "a.txt", data: Buffer.from("0123456789"), sessionId: "s" });
  await store.bind("s", [file]);
  const query = { mode: "read" as const, limit: 4 };
  const results = await Promise.all([store.read("s", file.id, query), store.read("s", file.id, query)]);
  assert.equal(results[0]?.text, "0123");
  assert.deepEqual(results[0], results[1]);
  assert.equal((await store.read("s", file.id, { ...query, offset: 4 })).text, "4567");
  assert.equal((await store.read("s", file.id, { mode: "index" })).sections?.length, 1);
  await writeFile(join(root, file.id, "original"), "tampered!!");
  await assert.rejects(store.read("s", file.id, query), /changed or is damaged/);
  await assert.rejects(store.original("s", file.id), /changed or is damaged/);
}));
test("linked originals and linked attachment directories are refused", { skip: process.platform === "win32" }, async () => setup(async (store, root) => {
  const file = await store.stage({ name: "a.txt", data: Buffer.from("file"), sessionId: "s" });
  await store.bind("s", [file]);
  const original = join(root, file.id, "original");
  const target = join(root, "outside.txt");
  await writeFile(target, "file");
  await unlink(original);
  await symlink(target, original);
  await assert.rejects(store.read("s", file.id), /regular file/);
  const linked = join(root, "linked");
  await symlink(join(root, file.id), linked);
  const redirected = new AttachmentStore(linked, parseDocumentAttachment);
  await assert.rejects(redirected.stage({ name: "x.txt", data: Buffer.from("x"), sessionId: "s" }), /not linked/);
}));
