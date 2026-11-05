import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, stat, symlink, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { COMPUTER_APP_NAME, installComputerBundle } from "../apps/desktop/src/main/computer-install.ts";
import { installPortableComputer, portableSources } from "../apps/desktop/src/main/computer-portable-install.ts";
import { stageComputerRuntime } from "./stage-computer-runtime.mjs";
import { fileURLToPath } from "node:url";

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

test("portable packages stage only shipped service sources and the action manifest", async () => {
  const root = await mkdtemp(join(tmpdir(),"aiclaw-portable-stage-"));
  const repo = fileURLToPath(new URL("../",import.meta.url));
  try {
    for(const platform of ["linux","win32"] as const) {
      await stageComputerRuntime({repo,destination:root,platform,arch:process.arch});
      const installed=await installPortableComputer(join(root,"computer-use",platform),platform,root);
      for(const name of portableSources[platform]) assert.ok((await readFile(join(installed.directory,name))).length>0);
      assert.equal(JSON.parse(await readFile(join(installed.directory,"Tools.json"),"utf8")).length,18);
      await assert.rejects(readFile(join(installed.directory,"Fixture.cs")),/ENOENT/);
      await assert.rejects(readFile(join(installed.directory,"test_host.mjs")),/ENOENT/);
    }
  } finally {await rm(root,{recursive:true,force:true});}
});

test("portable generations synchronize atomically, preserve live old versions and reject corruption", async () => {
  const root=await mkdtemp(join(tmpdir(),"aiclaw-portable-install-")), source=join(root,"bundled");
  await mkdir(source);
  for(const name of [...portableSources.linux,"Tools.json"]) await writeFile(join(source,name),name);
  try {
    const [first,concurrent]=await Promise.all([installPortableComputer(source,"linux",root),installPortableComputer(source,"linux",root)]);
    assert.equal(first.directory,concurrent.directory);
    const timestamp=(await stat(first.directory)).mtimeMs;
    assert.equal((await installPortableComputer(source,"linux",root)).directory,first.directory);
    assert.equal((await stat(first.directory)).mtimeMs,timestamp);
    await writeFile(join(source,"Tools.json"),"new contract");
    const next=await installPortableComputer(source,"linux",root);
    assert.notEqual(next.directory,first.directory); assert.equal(next.sourceIdentity,first.sourceIdentity);
    assert.equal(await readFile(join(first.directory,"Tools.json"),"utf8"),"Tools.json");
    await writeFile(join(next.directory,"service.py"),"tampered");
    await assert.rejects(installPortableComputer(source,"linux",root),/damaged/);
    await rm(join(next.directory,"service.py"));
    await assert.rejects(installPortableComputer(source,"linux",root),/damaged/);
    await rm(next.directory,{recursive:true});
    await symlink(first.directory,next.directory,process.platform === "win32" ? "junction" : "dir");
    await assert.rejects(installPortableComputer(source,"linux",root),/linked/);
    await rm(join(root,".aiclaw/computer-use/linux"),{recursive:true});
    await symlink(source,join(root,".aiclaw/computer-use/linux"),process.platform === "win32" ? "junction" : "dir");
    await assert.rejects(installPortableComputer(source,"linux",root),/owned directory/);
  } finally {await rm(root,{recursive:true,force:true});}
});
