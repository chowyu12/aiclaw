/**
 * ZIP 解包的测试，重点是 **zip slip**。
 *
 * 技能包来自内部平台，而内部平台上的包是别人上传的。一个不校验路径的解压器会让
 * 「安装一个技能」变成「往任意位置写文件」。下面前四条就是这条攻击路径的
 * 各种形态，改 unzip.ts 必须先跑通它们。
 *
 * 跑法：make test-renderer（与渲染层测试一起）
 */
import test from "node:test";
import assert from "node:assert/strict";
import { deflateRawSync } from "node:zlib";
import { mkdtempSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  unzipInto,
  safeJoin,
  commonTopLevelDir,
  readTextEntry,
} from "../apps/desktop/src/main/unzip.ts";

/**
 * 造一个最小可用的 ZIP。
 * store=true 表示不压缩；method 可以显式指定，用来构造"不支持的压缩方式"。
 */
function makeZip(
  entries: { name: string; content: string; store?: boolean; method?: number }[],
): Buffer {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;

  for (const entry of entries) {
    const nameBuf = Buffer.from(entry.name, "utf8");
    const raw = Buffer.from(entry.content, "utf8");
    const method = entry.method ?? (entry.store ? 0 : 8);
    const body = entry.store ? raw : deflateRawSync(raw);

    const local = Buffer.alloc(30 + nameBuf.length);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(method, 8);
    local.writeUInt32LE(0, 14); // crc，本实现不校验
    local.writeUInt32LE(body.length, 18);
    local.writeUInt32LE(raw.length, 22);
    local.writeUInt16LE(nameBuf.length, 26);
    local.writeUInt16LE(0, 28);
    nameBuf.copy(local, 30);

    const central = Buffer.alloc(46 + nameBuf.length);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(20, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(method, 10);
    central.writeUInt32LE(0, 16);
    central.writeUInt32LE(body.length, 20);
    central.writeUInt32LE(raw.length, 24);
    central.writeUInt16LE(nameBuf.length, 28);
    central.writeUInt32LE(offset, 42);
    nameBuf.copy(central, 46);

    locals.push(local, body);
    centrals.push(central);
    offset += local.length + body.length;
  }

  const centralBuf = Buffer.concat(centrals);
  const eocd = Buffer.alloc(22);
  eocd.writeUInt32LE(0x06054b50, 0);
  eocd.writeUInt16LE(entries.length, 8);
  eocd.writeUInt16LE(entries.length, 10);
  eocd.writeUInt32LE(centralBuf.length, 12);
  eocd.writeUInt32LE(offset, 16);

  return Buffer.concat([...locals, centralBuf, eocd]);
}

function tempDir(): string {
  return mkdtempSync(join(tmpdir(), "unzip-test-"));
}

// ---------- zip slip ----------

test("拒绝 ../ 跳出目标目录", () => {
  assert.throws(() => safeJoin("/tmp/skills", "../../.ssh/authorized_keys"), /目标目录之外/);
});

test("拒绝绝对路径", () => {
  assert.throws(() => safeJoin("/tmp/skills", "/etc/passwd"), /绝对路径/);
});

test("拒绝 Windows 盘符", () => {
  // 只查 ".." 的写法会放过这个。
  assert.throws(() => safeJoin("/tmp/skills", "C:/Windows/system32/x.dll"), /绝对路径/);
});

test("拒绝反斜杠形式的跳出", () => {
  assert.throws(() => safeJoin("/tmp/skills", "..\\..\\evil.txt"), /目标目录之外/);
});

test("前缀相同但不是子目录的路径也要拒绝", () => {
  // /tmp/skills-evil 以 /tmp/skills 开头，但不在它里面。
  assert.throws(() => safeJoin("/tmp/skills", "../skills-evil/x"), /目标目录之外/);
});

test("正常的相对路径放行", () => {
  assert.equal(safeJoin("/tmp/skills", "daily/SKILL.md"), "/tmp/skills/daily/SKILL.md");
});

test("解包时带 ../ 的条目会让整次解包失败，而不是被悄悄跳过", () => {
  const dir = tempDir();
  const zip = makeZip([
    { name: "ok.txt", content: "正常" },
    { name: "../escaped.txt", content: "坏的" },
  ]);
  // 失败比"跳过坏条目"好：跳过的话用户以为装成功了，实际少文件。
  assert.throws(() => unzipInto(zip, dir), /目标目录之外/);
  assert.ok(!existsSync(join(dir, "..", "escaped.txt")), "不该写到目录外");
});

// ---------- 正常解包 ----------

test("deflate 与 stored 两种条目都能解", () => {
  const dir = tempDir();
  const zip = makeZip([
    { name: "SKILL.md", content: "---\nname: 测试\n---\n正文" },
    { name: "scripts/run.sh", content: "echo hi", store: true },
  ]);

  const result = unzipInto(zip, dir);

  assert.equal(result.files.length, 2);
  assert.equal(readFileSync(join(dir, "SKILL.md"), "utf8"), "---\nname: 测试\n---\n正文");
  assert.equal(readFileSync(join(dir, "scripts", "run.sh"), "utf8"), "echo hi");
});

test("目录条目本身不写文件", () => {
  const dir = tempDir();
  const zip = makeZip([
    { name: "sub/", content: "", store: true },
    { name: "sub/a.txt", content: "内容" },
  ]);
  const result = unzipInto(zip, dir);
  assert.deepEqual(result.files, ["sub/a.txt"]);
});

test("中文文件名", () => {
  const dir = tempDir();
  unzipInto(makeZip([{ name: "说明/技能.md", content: "中文内容" }]), dir);
  assert.equal(readFileSync(join(dir, "说明", "技能.md"), "utf8"), "中文内容");
});

test("不是 ZIP 时报错而不是解出垃圾", () => {
  assert.throws(() => unzipInto(Buffer.from("not a zip at all"), tempDir()), /合法的 ZIP/);
});

test("不支持的压缩方式直接报错", () => {
  // 解出一个缺文件的技能还以为成功了，比失败更糟。
  const zip = makeZip([{ name: "a.txt", content: "x", store: true, method: 99 }]);
  assert.throws(() => unzipInto(zip, tempDir()), /不支持的压缩方式/);
});

// ---------- 辅助 ----------

test("找出公共顶层目录", () => {
  assert.equal(commonTopLevelDir(["pkg/SKILL.md", "pkg/scripts/a.sh"]), "pkg");
  assert.equal(commonTopLevelDir(["SKILL.md", "scripts/a.sh"]), "");
  assert.equal(commonTopLevelDir([]), "");
});

test("不落盘读出 SKILL.md", () => {
  const zip = makeZip([
    { name: "pkg/README.md", content: "readme" },
    { name: "pkg/SKILL.md", content: "技能正文" },
  ]);
  const found = readTextEntry(zip, (name) => name.endsWith("SKILL.md"));
  assert.equal(found, "技能正文");
  assert.equal(readTextEntry(zip, (name) => name.endsWith("NOPE.md")), null);
});
