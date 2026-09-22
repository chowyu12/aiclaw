/**
 * 「点开对话里的文件」这条路的判断。
 *
 * 它连着两头都不可信的东西：路径来自**模型输出**，动作是**交给系统用默认
 * 程序打开**——而在 macOS 上，用默认程序打开 .command / .app 就是执行。
 * 所以这里每一条都是在钉「什么情况下不能直接打开」。
 *
 * 跑法：make test-renderer
 */
import { strict as assert } from "node:assert";
import { test } from "node:test";
import { join } from "node:path";

import { classifyOpen, extensionOf } from "../apps/desktop/src/main/open-file-rules.ts";

const HOME = "/Users/someone";
const plain = { home: HOME, protectedPaths: [] as string[], executable: false };

test("普通文档直接打开", () => {
  for (const name of ["a.md", "报表.xlsx", "data.json", "图.png", "说明.txt"]) {
    assert.deepEqual(
      classifyOpen(join(HOME, "work", name), plain),
      { action: "open" },
      name,
    );
  }
});

test("打开等于执行的那些只在访达里显示", () => {
  // macOS 上双击 .command / .app 就是跑起来；Windows 那几个同理。
  // .dmg/.pkg 不直接执行，但一路点下去就是装东西。
  for (const name of ["install.command", "工具.app", "run.sh", "setup.exe", "x.ps1", "pkg.dmg"]) {
    const verdict = classifyOpen(join(HOME, "work", name), plain);
    assert.equal(verdict.action, "reveal", `${name} 不该直接打开`);
    assert.ok(verdict.action === "reveal" && verdict.reason, "要给出理由");
  }
});

test("带执行位的普通文件也按可执行处理", () => {
  // 扩展名骗得过人，骗不过 chmod +x：`报表` 这种没有扩展名但可执行的，
  // 双击一样会跑。
  const verdict = classifyOpen(join(HOME, "work", "报表"), { ...plain, executable: true });
  assert.equal(verdict.action, "reveal");
});

test("凭据目录连显示都不给", () => {
  // 这些路径没有任何「点开看看」的正当理由，而模型输出里出现它们，
  // 本身就该当成可疑。
  for (const path of [
    join(HOME, ".ssh", "id_rsa"),
    join(HOME, ".aws", "credentials"),
    join(HOME, "Library", "Keychains", "login.keychain-db"),
  ]) {
    assert.equal(classifyOpen(path, plain).action, "refuse", path);
  }
});

test("宿主指定的敏感目录同样拒绝", () => {
  // 应用自己的数据目录：里面有凭据文件与会话库。
  const appData = "/Users/someone/Library/Application Support/aiclaw";
  const verdict = classifyOpen(join(appData, "credentials.bin"), {
    ...plain,
    protectedPaths: [appData],
  });
  assert.equal(verdict.action, "refuse");
});

test("同前缀的邻居目录不算在名单里", () => {
  // ~/.sshfs 不是 ~/.ssh。按字符串前缀判会误伤，必须按路径段判。
  assert.equal(classifyOpen(join(HOME, ".sshfs", "note.md"), plain).action, "open");
});

test("扩展名取最后一段，大小写不敏感", () => {
  assert.equal(extensionOf("/a/b/c.MD"), "md");
  assert.equal(extensionOf("/a/b.c/d"), "");
  assert.equal(extensionOf("/a/.bashrc"), "");
  assert.equal(extensionOf("归档.tar.gz"), "gz");
});
