/**
 * 渲染层写 IPC 时的「可克隆」回归测试。
 *
 * Electron 的 invoke 走结构化克隆，**克隆不了 Proxy**。Vue 的 reactive() 里
 * 读出来的每个对象都是代理，所以 `[...state.list, newItem]` 这种写法在列表
 * 非空时会抛「An object could not be cloned」。
 *
 * 这个 bug 的表现极具迷惑性：**第一条能加进去**（那时列表是空的，新数组里只有
 * 刚建的纯对象），第二条才开始失败。所以下面这几条都要从「列表已经有东西」
 * 的状态开始测。
 *
 * 跑法：make test-renderer
 */
import test from "node:test";
import assert from "node:assert/strict";
import { reactive } from "vue";

/** 与 store.ts 里那个同形。改那边要改这边。 */
function plain<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

interface Row {
  type: string;
  id: string;
  name: string;
  enabled: boolean;
  allowWrite?: boolean;
}

/** 模拟 Electron 的 IPC 边界：结构化克隆，克隆不了就抛。 */
function sendOverIpc<T>(payload: T): T {
  return structuredClone(payload);
}

test("结构化克隆确实克隆不了响应式代理——bug 的根因", () => {
  const state = reactive({ list: [{ type: "data-api", id: "1", name: "甲", enabled: true }] });
  // 直接展开会把代理带进新数组。
  assert.throws(() => sendOverIpc([...state.list]), /could not be cloned|DataCloneError/i);
});

test("往非空列表里追加一条，过 plain() 之后能送出去", () => {
  const state = reactive({
    list: [{ type: "data-api", id: "1", name: "甲", enabled: true }] as Row[],
  });
  const next = [...state.list, { type: "data-api", id: "2", name: "乙", enabled: true }];

  const sent = sendOverIpc(plain(next));

  assert.equal(sent.length, 2);
  assert.deepEqual(sent.map((r) => r.id), ["1", "2"]);
});

test("连着加三条都能送出去（第一条能过不代表没问题）", () => {
  const state = reactive({ list: [] as Row[] });
  for (const id of ["1", "2", "3"]) {
    const next = [...state.list, { type: "data-api", id, name: `第${id}个`, enabled: true }];
    state.list = sendOverIpc(plain(next));
  }
  assert.deepEqual(state.list.map((r) => r.id), ["1", "2", "3"]);
});

test("filter 删除也会把代理带过去，同样要过 plain()", () => {
  const state = reactive({
    list: [
      { type: "data-api", id: "1", name: "甲", enabled: true },
      { type: "data-api", id: "2", name: "乙", enabled: true },
    ] as Row[],
  });
  const next = state.list.filter((row) => row.id !== "1");

  assert.throws(() => sendOverIpc(next), /could not be cloned|DataCloneError/i);
  assert.deepEqual(sendOverIpc(plain(next)).map((r) => r.id), ["2"]);
});

test("map + 展开重建了对象，本来就是安全的", () => {
  const state = reactive({
    list: [{ type: "data-api", id: "1", name: "甲", enabled: true }] as Row[],
  });
  // 这条记下来是为了说明为什么开关能用、添加不能用——两者走的是不同写法。
  const next = state.list.map((row) => (row.id === "1" ? { ...row, enabled: false } : row));
  const sent = sendOverIpc(next);
  assert.equal(sent[0]?.enabled, false);
});

test("plain() 不丢字段，可选字段也保得住", () => {
  const state = reactive({
    list: [
      { type: "api-operation", id: "7", name: "建单", enabled: true, allowWrite: true },
    ] as Row[],
  });
  const sent = sendOverIpc(plain([...state.list]));
  assert.deepEqual(sent[0], {
    type: "api-operation",
    id: "7",
    name: "建单",
    enabled: true,
    allowWrite: true,
  });
});

test("嵌套的数组与对象也剥得干净", () => {
  const state = reactive({
    servers: [
      {
        id: "m1",
        label: "fs",
        transport: "stdio",
        args: ["-y", "pkg"],
        env: { TOKEN: "x" },
        enabled: true,
      },
    ],
  });
  const sent = sendOverIpc(plain([...state.servers]));
  assert.deepEqual(sent[0]?.args, ["-y", "pkg"]);
  assert.deepEqual(sent[0]?.env, { TOKEN: "x" });
});
