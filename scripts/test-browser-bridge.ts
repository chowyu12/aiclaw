import { strict as assert } from "node:assert";
import { test } from "node:test";

import { ExtensionBridge, decodeFrame, encodeFrame, hmacHex } from "../apps/desktop/src/main/browser-bridge.ts";

/**
 * 浏览器扩展的连接：谁能连上来、握手怎么验、消息怎么往返。
 *
 * 用 Node 自带的 WebSocket 客户端当扩展——它和浏览器里的是同一套协议实现
 *（undici），掩码、分片这些都按真的来。
 */

const ORIGIN = "chrome-extension://testextensionid";
const TOKEN = "pair-secret-123";
let nextPort = 27891;

async function withBridge(fn: (bridge: ExtensionBridge, port: number) => Promise<void>): Promise<void> {
  const port = nextPort++;
  const bridge = new ExtensionBridge(() => TOKEN, port, ORIGIN);
  await bridge.start();
  try {
    await fn(bridge, port);
  } finally {
    bridge.stop();
  }
}

/** 一个按扩展的方式握手的客户端。token 给错就是冒充者。 */
function extension(port: number, options: { origin?: string; token?: string } = {}) {
  const ws = new WebSocket(`ws://127.0.0.1:${port}/extension`, {
    headers: { Origin: options.origin ?? ORIGIN },
  } as unknown as string[]);
  const token = options.token ?? TOKEN;
  const clientNonce = "c0ffee";
  const state = { welcomed: false, serverProofOk: false, closedCode: 0 };
  const handlers: Array<(message: Record<string, unknown>) => void> = [];
  const opened = new Promise<void>((resolve, reject) => {
    ws.onopen = () => resolve();
    ws.onerror = () => reject(new Error("连不上"));
  });
  const closed = new Promise<number>((resolve) => {
    ws.onclose = (event) => {
      state.closedCode = event.code;
      resolve(event.code);
    };
  });
  ws.onmessage = (event) => {
    const message = JSON.parse(String(event.data)) as Record<string, unknown>;
    if (message.type === "challenge") {
      ws.send(
        JSON.stringify({
          type: "hello",
          nonce: clientNonce,
          proof: hmacHex(token, "ext:" + String(message.nonce)),
          extension: "1.0.0",
          userAgent: "TestBrowser/1",
        }),
      );
      return;
    }
    if (message.type === "welcome") {
      state.welcomed = true;
      state.serverProofOk = message.proof === hmacHex(TOKEN, "app:" + clientNonce);
      return;
    }
    for (const handler of handlers) handler(message);
  };
  return {
    ws,
    state,
    opened,
    closed,
    onRequest(handler: (message: Record<string, unknown>) => void) {
      handlers.push(handler);
    },
  };
}

async function until(condition: () => boolean, ms = 2000): Promise<void> {
  const deadline = Date.now() + ms;
  while (!condition()) {
    if (Date.now() > deadline) throw new Error("等不到");
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

test("网页连不上：Origin 不是这个扩展就在握手前拒掉", async () => {
  await withBridge(async (bridge, port) => {
    const page = extension(port, { origin: "https://evil.example" });
    await assert.rejects(page.opened);
    assert.equal(bridge.connected, false);
  });
});

test("配对码不对的扩展被断开，也拿不到任何命令", async () => {
  await withBridge(async (bridge, port) => {
    const impostor = extension(port, { token: "guessed" });
    await impostor.opened;
    assert.equal(await impostor.closed, 4001);
    assert.equal(bridge.connected, false);
    assert.equal(impostor.state.welcomed, false);
  });
});

test("正确配对：双方都证明自己知道配对码，命令能往返", async () => {
  await withBridge(async (bridge, port) => {
    const client = extension(port);
    client.onRequest((message) => {
      if (message.op === "tabs.list") {
        client.ws.send(JSON.stringify({ id: message.id, ok: true, result: [{ tabId: 7, title: "首页" }] }));
      } else {
        client.ws.send(JSON.stringify({ id: message.id, ok: false, error: "不认识" }));
      }
    });
    await until(() => bridge.connected);
    assert.equal(client.state.serverProofOk, true, "AIClaw 的证明扩展要能核对上");
    assert.deepEqual(bridge.status().connected, { userAgent: "TestBrowser/1", extension: "1.0.0" });

    assert.deepEqual(await bridge.request("tabs.list"), [{ tabId: 7, title: "首页" }]);
    await assert.rejects(bridge.request("nope"), /不认识/);
  });
});

test("大消息（截图）走 64 位长度的帧，原样往返", async () => {
  await withBridge(async (bridge, port) => {
    const client = extension(port);
    const big = "x".repeat(3 * 1024 * 1024);
    client.onRequest((message) => {
      client.ws.send(JSON.stringify({ id: message.id, ok: true, result: { data: big } }));
    });
    await until(() => bridge.connected);
    const result = (await bridge.request("cdp", {})) as { data: string };
    assert.equal(result.data.length, big.length);
  });
});

test("扩展断开时，等着的命令立刻失败，而不是等到超时", async () => {
  await withBridge(async (bridge, port) => {
    const client = extension(port);
    await until(() => bridge.connected);
    const waiting = bridge.request("tabs.list");
    client.ws.close();
    await assert.rejects(waiting, /断开/);
    await until(() => !bridge.connected);
  });
});

test("没连上时请求直接说明怎么办", async () => {
  await withBridge(async (bridge) => {
    await assert.rejects(bridge.request("tabs.list"), /配对码/);
  });
});

test("帧编解码：客户端的帧必须带掩码", () => {
  const unmasked = encodeFrame(0x1, Buffer.from("hi"));
  assert.equal(decodeFrame(unmasked), "invalid");
  // 手工加掩码。
  const payload = Buffer.from("你好");
  const mask = Buffer.from([1, 2, 3, 4]);
  const masked = Buffer.concat([
    Buffer.from([0x81, 0x80 | payload.length]),
    mask,
    Buffer.from(payload.map((b, i) => b ^ mask[i % 4]!)),
  ]);
  const frame = decodeFrame(masked);
  assert.ok(frame && frame !== "invalid");
  assert.equal(frame.payload.toString(), "你好");
  assert.equal(decodeFrame(masked.subarray(0, 4)), null, "数据不够时等下一块");
});
