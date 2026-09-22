import { strict as assert } from "node:assert";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { compareVersions, isNewer, normalizeVersion } from "../apps/desktop/src/main/version.ts";

/**
 * 版本比较。
 *
 * 比错了不会报错，只会「有新版不提示」或者「装完还一直提示」——两种都让人
 * 觉得这功能是坏的，而且没人会去查。所以逐条钉。
 */

test("去掉 tag 的 v 前缀", () => {
  assert.equal(normalizeVersion("v0.1.3"), "0.1.3");
  assert.equal(normalizeVersion(" V0.1.3 \n"), "0.1.3");
  assert.equal(normalizeVersion("0.1.3"), "0.1.3");
});

test("数字段逐段比，不是按字符串", () => {
  // 按字符串比的话 "0.1.10" < "0.1.9"，这是这类比较最常见的错。
  assert.equal(compareVersions("0.1.10", "0.1.9"), 1);
  assert.equal(compareVersions("0.2.0", "0.10.0"), -1);
  assert.equal(compareVersions("1.0.0", "0.99.99"), 1);
});

test("段数不同时缺的按 0", () => {
  assert.equal(compareVersions("1.2", "1.2.0"), 0);
  assert.equal(compareVersions("1.2.1", "1.2"), 1);
});

test("预发布小于同号正式版", () => {
  assert.equal(compareVersions("0.1.0-rc1", "0.1.0"), -1);
  assert.equal(compareVersions("0.1.0", "0.1.0-rc1"), 1);
  assert.equal(compareVersions("0.1.0-rc1", "0.1.0-rc2"), -1);
});

test("v 前缀不影响比较", () => {
  assert.equal(compareVersions("v0.1.3", "0.1.2"), 1);
  assert.equal(compareVersions("0.1.3", "v0.1.3"), 0);
});

test("isNewer：真的有新版才返回 true", () => {
  assert.equal(isNewer("v0.1.3", "0.1.2"), true);
  assert.equal(isNewer("v0.1.2", "0.1.2"), false, "同版本不该提示");
  assert.equal(isNewer("v0.1.1", "0.1.2"), false, "旧版本更不该提示");
});

test("解析不出来时不提示——宁可漏也不要天天弹", () => {
  // latest.txt 取回来是一段 HTML 错误页、或者网络抽了拿到空串，
  // 这时候绝不能判成「有新版」。
  assert.equal(isNewer("", "0.1.2"), false);
  assert.equal(isNewer("   ", "0.1.2"), false);
  assert.equal(isNewer("v0.1.3", ""), false);
  assert.equal(isNewer("<!DOCTYPE html>", "0.1.2"), false);
});

test("rc 之间与跨号的组合", () => {
  assert.equal(isNewer("v0.2.0-rc1", "0.1.9"), true, "预发布也比更小的正式版新");
  assert.equal(isNewer("v0.1.0-rc1", "0.1.0"), false, "装了正式版不该被 rc 拉回去");
});

// ---------- 两处版本号必须一致 ----------

test("apps/desktop 的 version 与仓库根一致", () => {
  // 打包时版本号取自**根** package.json（或 CI 的 tag），而 `make dev` 跑的是
  // apps/desktop 里的 electron，`app.getVersion()` 读的是**那一份**。
  // 两边不同步的表现很隐蔽：打出来的包版本号是对的，开发机上却显示成一个
  // 早就发过的旧版本，于是检查更新天天提示「有新版」。实际漂过一次，
  // 从 0.1.0 一路漂到 0.1.4。
  const read = (path: string): string =>
    JSON.parse(readFileSync(new URL(path, import.meta.url), "utf8")).version;
  assert.equal(
    read("../apps/desktop/package.json"),
    read("../package.json"),
    "改版本号要同时改根和 apps/desktop 两个 package.json",
  );
});

// ---------- 自动下载之后的按钮该写什么 ----------
//
// 这条链有四个状态（没下 / 下载中 / 下好了 / 正在更新），写错了不会报错，
// 只会让用户对着一个说谎的按钮：比如已经下好了还写「升级并重启」，他会以为
// 点下去要等几十秒，于是不点。

/** 与 App.vue 里那段三元表达式同构。改一边要改另一边。 */
function updateButtonLabel(state: {
  updating: boolean;
  downloading: boolean;
  ready: boolean;
  fallback: string;
}): string {
  if (state.updating) return "正在更新…";
  if (state.downloading) return "下载中…";
  if (state.ready) return "立即重启更新";
  return state.fallback;
}

test("下好之后按钮说的是「重启」，不是「升级并重启」", () => {
  assert.equal(
    updateButtonLabel({ updating: false, downloading: false, ready: true, fallback: "升级并重启" }),
    "立即重启更新",
  );
});

test("后台下载时按钮不该假装什么都没发生", () => {
  assert.equal(
    updateButtonLabel({ updating: false, downloading: true, ready: false, fallback: "升级并重启" }),
    "下载中…",
  );
});

test("平台不支持自动下载时退回原来的文案", () => {
  // Windows 上没有「重启即更新」这条路，文案由 installLabel 给。
  assert.equal(
    updateButtonLabel({ updating: false, downloading: false, ready: false, fallback: "下载新版本" }),
    "下载新版本",
  );
});
