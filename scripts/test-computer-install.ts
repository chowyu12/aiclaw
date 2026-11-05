import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, stat, symlink, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { COMPUTER_APP_NAME, installComputerBundle } from "../apps/desktop/src/main/computer-install.ts";

async function fixture() {
  const root = await mkdtemp(join(tmpdir(), "aiclaw-helper-install-"));
  const source = join(root, "bundled", COMPUTER_APP_NAME), directory = join(root, "user-computer-use");
  await mkdir(join(source, "Contents/MacOS"), { recursive: true });
  await mkdir(join(source, "Contents/_CodeSignature"), { recursive: true });
  await writeFile(join(source, "Contents/MacOS/AIClawComputerUse"), "native-one");
  await writeFile(join(source, "Contents/_CodeSignature/CodeResources"), "sealed-resources");
  return { root, source, directory, binary: (bundle: string) => join(bundle, "Contents/MacOS/AIClawComputerUse"), async close() { await rm(root, { recursive: true, force: true }); } };
}

test("stable installation is idempotent and replaces a complete verified generation", { skip: !process.getuid }, async () => {
  const h = await fixture(); const verified: string[] = [];
  const verify = async (path: string) => { verified.push(path); };
  try {
    const target = await installComputerBundle(h.source, h.directory, verify);
    assert.equal(target, join(h.directory, COMPUTER_APP_NAME));
    const first = await stat(target);
    await installComputerBundle(h.source, h.directory, verify);
    assert.equal((await stat(target)).ino, first.ino, "an identical app must not be rewritten or re-signed");
    await writeFile(h.binary(h.source), "native-two");
    await installComputerBundle(h.source, h.directory, verify);
    assert.equal(await readFile(h.binary(target), "utf8"), "native-two");
    assert.notEqual((await stat(target)).ino, first.ino);
    assert.ok(verified.some(path => path.includes(".install-")), "verify the staged candidate before replacement");
  } finally { await h.close(); }
});

test("resource-only updates synchronize even when the executable is unchanged", { skip: !process.getuid }, async () => {
  const h = await fixture();
  try {
    const target = await installComputerBundle(h.source, h.directory, async () => {});
    await writeFile(join(h.source, "Contents/_CodeSignature/CodeResources"), "new-version-resources");
    await installComputerBundle(h.source, h.directory, async () => {});
    assert.equal(await readFile(h.binary(target), "utf8"), "native-one");
    assert.equal(await readFile(join(target, "Contents/_CodeSignature/CodeResources"), "utf8"), "new-version-resources");
  } finally { await h.close(); }
});

test("invalid updates leave the installed app intact and release the install lock", { skip: !process.getuid }, async () => {
  const h = await fixture();
  try {
    const target = await installComputerBundle(h.source, h.directory, async () => {});
    await writeFile(h.binary(h.source), "native-two");
    await assert.rejects(installComputerBundle(h.source, h.directory, async path => { if (path.includes(".install-")) throw Error("signature invalid"); }), /signature invalid/);
    assert.equal(await readFile(h.binary(target), "utf8"), "native-one");
    await installComputerBundle(h.source, h.directory, async () => {});
    assert.equal(await readFile(h.binary(target), "utf8"), "native-two");
  } finally { await h.close(); }
});

test("concurrent hosts share the stable install and linked targets are refused", { skip: !process.getuid }, async () => {
  const h = await fixture();
  try {
    const installed = await Promise.all([installComputerBundle(h.source, h.directory, async () => {}), installComputerBundle(h.source, h.directory, async () => {})]);
    assert.equal(installed[0], installed[1]);
    await rm(installed[0]!, { recursive: true });
    await symlink(h.source, installed[0]!);
    await assert.rejects(installComputerBundle(h.source, h.directory, async () => {}), /linked Computer Use/);
    const linked = join(h.root, "linked-parent"); await symlink(h.directory, linked);
    await assert.rejects(installComputerBundle(h.source, linked, async () => {}), /owned directory/);
  } finally { await h.close(); }
});
