/**
 * computer use 里两处容易写错的纯函数：按键翻译与自点防护。
 *
 * 按键翻译错了不会报错，只会按下别的键——比如把 cmd+s 按成一个字母 s，
 * 在一个正在编辑的窗口里那就是往文档里插字符。所以这两组表值得钉住。
 *
 * 跑法：make test-renderer
 */
import test from "node:test";
import assert from "node:assert/strict";
import {
  appleString,
  escapeSendKeys,
  isSelf,
  macKeyScript,
  windowsKeys,
} from "../apps/desktop/src/main/computer-keys.ts";

// ---------- 自点审批的防护 ----------

test("最前面是自己时要拒绝——否则模型能点自己的审批弹窗", () => {
  assert.equal(isSelf("Electron"), true);
  assert.equal(isSelf("AIClaw"), true);
  assert.equal(isSelf("aiclaw"), true);
});

test("别的应用放行", () => {
  assert.equal(isSelf("Safari"), false);
  assert.equal(isSelf("Terminal"), false);
});

test("查不到时不阻断：这道防护是额外的，不该让整个功能不可用", () => {
  assert.equal(isSelf(""), false);
  assert.equal(isSelf("   "), false);
});

// ---------- macOS 按键 ----------

test("macOS：组合键翻成 key code + 修饰键", () => {
  const script = macKeyScript("cmd+s");
  assert.ok(script.includes("keystroke \"s\""), script);
  assert.ok(script.includes("command down"), script);
});

test("macOS：功能键走 key code 而不是 keystroke", () => {
  // keystroke "Return" 会原样输入这七个字母，不是按回车。
  const script = macKeyScript("Return");
  assert.ok(script.includes("key code 36"), script);
  assert.ok(!script.includes("keystroke"), script);
});

test("macOS：多个修饰键", () => {
  const script = macKeyScript("cmd+shift+p");
  assert.ok(script.includes("command down"), script);
  assert.ok(script.includes("shift down"), script);
});

test("macOS：不认识的键报错，不静默按下别的东西", () => {
  assert.throws(() => macKeyScript("cmd+nonexistent"), /不认识的按键/);
  assert.throws(() => macKeyScript("super+a"), /不认识的修饰键/);
  assert.throws(() => macKeyScript(""), /为空/);
});

test("AppleScript 字符串里的引号与反斜杠要转义", () => {
  // 不转义的话一段带引号的文本会把脚本截断，后面的内容变成可执行的 AppleScript。
  assert.equal(appleString('他说"你好"'), '"他说\\"你好\\""');
  assert.equal(appleString("路径 C:\\x"), '"路径 C:\\\\x"');
});

// ---------- Windows 按键 ----------

test("Windows：修饰键翻成 SendKeys 的符号", () => {
  assert.equal(windowsKeys("ctrl+s"), "^s");
  assert.equal(windowsKeys("alt+tab"), "%{TAB}");
  assert.equal(windowsKeys("ctrl+shift+n"), "^+n");
});

test("Windows：cmd 当成 ctrl——模型经常按 macOS 的习惯给", () => {
  assert.equal(windowsKeys("cmd+c"), "^c");
});

test("Windows：功能键用大括号形式", () => {
  assert.equal(windowsKeys("Return"), "{ENTER}");
  assert.equal(windowsKeys("escape"), "{ESC}");
});

test("Windows：不认识的键报错", () => {
  assert.throws(() => windowsKeys("ctrl+nope"), /不认识的按键/);
});

test("SendKeys 的控制字符要转义", () => {
  // 不转义的话输入 "a+b" 会被当成「按住 shift 再按 b」。
  assert.equal(escapeSendKeys("a+b"), "a{+}b");
  assert.equal(escapeSendKeys("100%"), "100{%}");
  assert.equal(escapeSendKeys("x^y~z"), "x{^}y{~}z");
  assert.equal(escapeSendKeys("普通文本"), "普通文本");
});
