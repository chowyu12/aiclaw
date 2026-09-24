import { strict as assert } from "node:assert";
import { test } from "node:test";

import { answerText, answerTime, formatMessageTime, fullMessageTime } from "../apps/desktop/src/renderer/message-meta.ts";

/** 消息下面那一行：时间怎么写、复制的是什么。 */

const now = new Date(2026, 8, 24, 15, 30, 0); // 2026-09-24 15:30

test("今天的只写时分，今年的带月日，更早的带年份", () => {
  assert.equal(formatMessageTime(new Date(2026, 8, 24, 9, 5).getTime(), now), "09:05");
  assert.equal(formatMessageTime(new Date(2026, 8, 23, 22, 1).getTime(), now), "9月23日 22:01");
  assert.equal(formatMessageTime(new Date(2025, 11, 31, 8, 0).getTime(), now), "2025年12月31日 08:00");
});

test("旧存档没有时间：不显示，而不是显示成 1970 年", () => {
  assert.equal(formatMessageTime(undefined, now), "");
  assert.equal(formatMessageTime(0, now), "");
  assert.equal(formatMessageTime(Number.NaN, now), "");
  assert.equal(fullMessageTime(0), "");
});

test("悬停的完整时间精确到秒", () => {
  assert.equal(fullMessageTime(new Date(2026, 8, 24, 9, 5, 7).getTime()), "2026-09-24 09:05:07");
});

test("复制整轮回答：几截拼成一段，空的那截不留空行", () => {
  const text = answerText([{ text: "先找找装在哪。" }, { text: "   " }, { text: "找到 **UURemote.app**。" }]);
  assert.equal(text, "先找找装在哪。\n\n找到 **UURemote.app**。");
});

test("回答的时间取最后一截：那是答完的时候", () => {
  assert.equal(answerTime([{ at: 100 }, { at: 300 }, {}]), 300);
  assert.equal(answerTime([{}, {}]), undefined);
});
