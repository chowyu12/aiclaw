import { strict as assert } from "node:assert";
import { chmodSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { commonBinDirs, extractPath, loginShellPath, mergePath } from "../apps/desktop/src/main/shell-path.ts";

/**
 * 给内核的 PATH。从程序坞打开的应用只有系统那几个目录，nvm / Homebrew 装的 CLI
 * 一律找不到——技能认出来了、一跑就「command not found」。
 */

test("只认标记之间的那段：shell 启动时打印的横幅不能混进 PATH", () => {
  const output = "Welcome to zsh!\nlast login…\n__AICLAW_PATH__/a/bin:/usr/bin__AICLAW_PATH__\n";
  assert.equal(extractPath(output), "/a/bin:/usr/bin");
  assert.equal(extractPath("没有标记"), "");
});

test("合并时先出现的优先，去重、去空项", () => {
  assert.equal(mergePath("/login/bin:/usr/bin", "/usr/bin:/bin::", ["/opt/homebrew/bin", "/login/bin"]), "/login/bin:/usr/bin:/bin:/opt/homebrew/bin");
});

test("nvm 的每个版本都补上，新版本在前；不存在的目录不要", () => {
  const home = mkdtempSync(join(tmpdir(), "aiclaw-path-"));
  for (const version of ["v18.20.0", "v22.22.0", "v20.1.0"]) mkdirSync(join(home, ".nvm", "versions", "node", version, "bin"), { recursive: true });
  mkdirSync(join(home, ".local", "bin"), { recursive: true });
  const dirs = commonBinDirs(home).filter((dir) => dir.startsWith(home));
  assert.deepEqual(dirs, [
    join(home, ".nvm/versions/node/v22.22.0/bin"),
    join(home, ".nvm/versions/node/v20.1.0/bin"),
    join(home, ".nvm/versions/node/v18.20.0/bin"),
    join(home, ".local/bin"),
  ]);
});

test("从登录 shell 读 PATH；shell 出错时返回空串而不是抛", { skip: process.platform === "win32" }, async () => {
  const dir = mkdtempSync(join(tmpdir(), "aiclaw-shell-"));
  const fake = join(dir, "fake-shell");
  // 像 zsh -ilc 一样执行最后一个参数，但先把 PATH 设成 nvm 初始化之后的样子。
  writeFileSync(fake, `#!/bin/sh\necho "banner"\nPATH="/home/me/.nvm/versions/node/v22/bin:/usr/bin" eval "$2"\n`);
  chmodSync(fake, 0o755);
  assert.equal(await loginShellPath(fake), "/home/me/.nvm/versions/node/v22/bin:/usr/bin");
  assert.equal(await loginShellPath(join(dir, "no-such-shell")), "");
});
