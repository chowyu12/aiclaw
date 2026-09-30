import { createHash, createHmac, randomBytes, randomInt, timingSafeEqual } from "node:crypto";
import { EventEmitter } from "node:events";
import { createServer, type IncomingMessage, type Server } from "node:http";
import type { Duplex } from "node:stream";

import { tr } from "../shared/i18n.js";

/**
 * 与「AIClaw 浏览器助手」扩展（apps/browser-extension）之间的连接。
 *
 * 扩展装在用户自己的 Chrome / Edge 里，连到这里（ws://127.0.0.1:17891/extension），
 * 替我们开后台标签页、把 CDP 命令转给 chrome.debugger。于是浏览器工具可以在用户
 * **自己的浏览器**里、用他已有的登录态、在后台标签页里干活，不抢鼠标。
 *
 * 为什么手写 WebSocket 服务端：Node 自带的只有客户端；这里只有一个本机连接、只收发
 * 文本帧，为它引一个依赖不值得。实现覆盖 RFC 6455 里用得到的部分：握手、掩码、
 * 7/16/64 位长度、分片、ping/pong、close。
 *
 * 守三道门：
 *   1. 只听 127.0.0.1。
 *   2. **Origin 必须是这个扩展**（manifest 里固定了 key，所以 ID 固定）。网页也能连
 *      ws://127.0.0.1，但它伪造不了 Origin——这一道挡的是「随便打开一个网页就能
 *      冒充扩展」。
 *   3. **双方用配对码做挑战-应答**（HMAC-SHA256），配对码本身不上线。本机进程能伪造
 *      Origin，这一道挡它；反过来扩展也核对我们，挡的是「AIClaw 没开时别的程序占住
 *      端口冒充 AIClaw」。
 */

export const BRIDGE_PORT = 17891;
/** 扩展的 ID，由 manifest.json 里的 key 决定。改 key 就要改这里。 */
export const EXTENSION_ID = "doofgbncfflbmekanpfocimbideeiadd";
/**
 * 认哪些扩展。本地加载的那个 ID 由 manifest 里的 key 固定；上架之后商店会给扩展
 * 另分配 ID（Chrome 应用商店、Edge 加载项各一个），拿到之后加在这里——Origin 校验
 * 靠这张表，漏了的话商店版装上也连不进来。
 */
export const EXTENSION_IDS: readonly string[] = [EXTENSION_ID];
const EXTENSION_ORIGINS = EXTENSION_IDS.map((id) => `chrome-extension://${id}`);
/** 一条消息的上限：一张全屏截图的 base64 也就几 MB。 */
const MAX_MESSAGE_BYTES = 32 * 1024 * 1024;
const HEARTBEAT_MS = 20_000;
const REQUEST_TIMEOUT_MS = 45_000;
const HANDSHAKE_TIMEOUT_MS = 10_000;
/** 配对页开着等用户点「允许」的上限。 */
const PAIR_TIMEOUT_MS = 3 * 60_000;

export interface BridgeStatus {
  listening: boolean;
  port: number;
  /** 没能开始监听的原因（端口被占等）。 */
  error?: string;
  /** 已认证的扩展。没连上是 null。 */
  connected: { userAgent: string; extension: string } | null;
  /** 正在配对：浏览器那边开着配对页，等用户核对这个代码后点「允许」。 */
  pairing: { code: string; userAgent: string } | null;
}

interface Pending {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
  timer: NodeJS.Timeout;
}

/** 一条 WebSocket 连接（服务端这一侧）。 */
class Connection extends EventEmitter {
  private buffer = Buffer.alloc(0);
  private fragments: Buffer[] = [];
  private fragmentBytes = 0;
  private closed = false;
  private readonly socket: Duplex;

  constructor(socket: Duplex) {
    super();
    this.socket = socket;
    socket.on("data", (chunk: Buffer) => this.onData(chunk));
    socket.on("close", () => this.finish());
    socket.on("error", () => this.finish());
  }

  send(text: string): void {
    if (this.closed) return;
    this.socket.write(encodeFrame(0x1, Buffer.from(text, "utf8")));
  }

  close(code = 1000, reason = ""): void {
    if (this.closed) return;
    const payload = Buffer.alloc(2 + Buffer.byteLength(reason));
    payload.writeUInt16BE(code, 0);
    payload.write(reason, 2);
    this.socket.write(encodeFrame(0x8, payload));
    this.socket.end();
    this.finish();
  }

  private finish(): void {
    if (this.closed) return;
    this.closed = true;
    this.socket.destroy();
    this.emit("close");
  }

  private onData(chunk: Buffer): void {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    for (;;) {
      const frame = decodeFrame(this.buffer);
      if (frame === null) return;
      if (frame === "invalid") {
        this.close(1002, "protocol error");
        return;
      }
      this.buffer = this.buffer.subarray(frame.consumed);
      if (!this.onFrame(frame.fin, frame.opcode, frame.payload)) return;
    }
  }

  /** 返回 false 表示连接已关。 */
  private onFrame(fin: boolean, opcode: number, payload: Buffer): boolean {
    switch (opcode) {
      case 0x8:
        this.close(1000);
        return false;
      case 0x9:
        this.socket.write(encodeFrame(0xa, payload));
        return true;
      case 0xa:
        return true;
      case 0x1:
      case 0x0: {
        this.fragmentBytes += payload.length;
        if (this.fragmentBytes > MAX_MESSAGE_BYTES) {
          this.close(1009, "message too big");
          return false;
        }
        this.fragments.push(payload);
        if (fin) {
          const text = Buffer.concat(this.fragments).toString("utf8");
          this.fragments = [];
          this.fragmentBytes = 0;
          this.emit("message", text);
        }
        return true;
      }
      default:
        // 二进制帧与未知操作码：我们不用，按协议错误处理。
        this.close(1003, "unsupported");
        return false;
    }
  }
}

export function encodeFrame(opcode: number, payload: Buffer): Buffer {
  const length = payload.length;
  let header: Buffer;
  if (length < 126) {
    header = Buffer.from([0x80 | opcode, length]);
  } else if (length < 65536) {
    header = Buffer.alloc(4);
    header[0] = 0x80 | opcode;
    header[1] = 126;
    header.writeUInt16BE(length, 2);
  } else {
    header = Buffer.alloc(10);
    header[0] = 0x80 | opcode;
    header[1] = 127;
    header.writeBigUInt64BE(BigInt(length), 2);
  }
  return Buffer.concat([header, payload]);
}

/**
 * 从缓冲区里解出一帧。数据不够返回 null；不合法（客户端没加掩码、长度离谱）返回 "invalid"。
 */
export function decodeFrame(
  buffer: Buffer,
): { fin: boolean; opcode: number; payload: Buffer; consumed: number } | null | "invalid" {
  if (buffer.length < 2) return null;
  const fin = (buffer[0]! & 0x80) !== 0;
  const opcode = buffer[0]! & 0x0f;
  const masked = (buffer[1]! & 0x80) !== 0;
  // 客户端发来的帧必须加掩码（RFC 6455 5.1）。
  if (!masked) return "invalid";
  let length = buffer[1]! & 0x7f;
  let offset = 2;
  if (length === 126) {
    if (buffer.length < 4) return null;
    length = buffer.readUInt16BE(2);
    offset = 4;
  } else if (length === 127) {
    if (buffer.length < 10) return null;
    const big = buffer.readBigUInt64BE(2);
    if (big > BigInt(MAX_MESSAGE_BYTES)) return "invalid";
    length = Number(big);
    offset = 10;
  }
  if (buffer.length < offset + 4 + length) return null;
  const mask = buffer.subarray(offset, offset + 4);
  const payload = Buffer.alloc(length);
  for (let i = 0; i < length; i++) payload[i] = buffer[offset + 4 + i]! ^ mask[i % 4]!;
  return { fin, opcode, payload, consumed: offset + 4 + length };
}

export function hmacHex(secret: string, text: string): string {
  return createHmac("sha256", secret).update(text).digest("hex");
}

function sameHex(a: unknown, b: string): boolean {
  if (typeof a !== "string" || a.length !== b.length) return false;
  return timingSafeEqual(Buffer.from(a), Buffer.from(b));
}

/** 已认证的扩展连接。 */
interface Peer {
  connection: Connection;
  userAgent: string;
  extension: string;
  lastPong: number;
}

export class ExtensionBridge extends EventEmitter {
  private server: Server | null = null;
  private error = "";
  private peer: Peer | null = null;
  private nextId = 1;
  private readonly pending = new Map<number, Pending>();
  private heartbeat: NodeJS.Timeout | null = null;
  /** 正在配对的那条连接（浏览器那边开着配对页）。同一时间只有一个。 */
  private pairingWith: { connection: Connection; code: string; userAgent: string; timer: NodeJS.Timeout } | null = null;
  private readonly pairToken: () => string;
  private readonly port: number;
  private readonly origins: readonly string[];
  private readonly rejectedOrigins = new Map<string, number>();

  /**
   * @param pairToken 取当前配对码。每次握手现取：用户在设置页重新生成之后，
   *   旧的立刻作废。
   */
  constructor(pairToken: () => string, port = BRIDGE_PORT, origins: string | readonly string[] = EXTENSION_ORIGINS) {
    super();
    this.pairToken = pairToken;
    this.port = port;
    this.origins = typeof origins === "string" ? [origins] : origins;
  }

  status(): BridgeStatus {
    return {
      listening: this.server?.listening === true,
      port: this.port,
      error: this.error || undefined,
      connected: this.peer ? { userAgent: this.peer.userAgent, extension: this.peer.extension } : null,
      pairing: this.pairingWith ? { code: this.pairingWith.code, userAgent: this.pairingWith.userAgent } : null,
    };
  }

  get connected(): boolean {
    return this.peer !== null;
  }

  start(): Promise<void> {
    if (this.server) return Promise.resolve();
    const server = createServer((_request, response) => {
      response.writeHead(426, { "Content-Type": "text/plain" });
      response.end("upgrade required");
    });
    server.on("upgrade", (request, socket) => this.onUpgrade(request, socket));
    this.server = server;
    return new Promise((resolve) => {
      server.once("error", (error: NodeJS.ErrnoException) => {
        this.error =
          error.code === "EADDRINUSE"
            ? tr("端口 {port} 被别的程序占着，浏览器扩展连不上来", { port: this.port })
            : tr("没能开始监听：{error}", { error: error.message });
        this.server = null;
        this.emit("status");
        resolve();
      });
      server.listen(this.port, "127.0.0.1", () => {
        this.error = "";
        this.heartbeat = setInterval(() => this.beat(), HEARTBEAT_MS);
        this.emit("status");
        resolve();
      });
    });
  }

  stop(): void {
    if (this.pairingWith) {
      clearTimeout(this.pairingWith.timer);
      this.pairingWith.connection.close(1001, "shutting down");
      this.pairingWith = null;
    }
    if (this.heartbeat) clearInterval(this.heartbeat);
    this.heartbeat = null;
    this.peer?.connection.close(1001, "shutting down");
    this.peer = null;
    this.server?.close();
    this.server = null;
    this.failAll(tr("浏览器扩展的连接已关闭"));
  }

  /** 断开当前扩展（比如用户重新生成了配对码）。 */
  disconnect(): void {
    this.peer?.connection.close(4001, "re-pair");
  }

  /** 发一条命令给扩展，等它回。 */
  request<T = unknown>(op: string, args: Record<string, unknown> = {}, timeoutMs = REQUEST_TIMEOUT_MS): Promise<T> {
    const peer = this.peer;
    if (!peer) {
      return Promise.reject(new Error(notConnected()));
    }
    const id = this.nextId++;
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(tr("浏览器扩展 {seconds} 秒没有回应（{op}）", { seconds: Math.round(timeoutMs / 1000), op })));
      }, timeoutMs);
      this.pending.set(id, { resolve: resolve as (value: unknown) => void, reject, timer });
      peer.connection.send(JSON.stringify({ id, op, args }));
    });
  }

  private onUpgrade(request: IncomingMessage, socket: Duplex): void {
    const reject = (status: number): void => {
      socket.end(`HTTP/1.1 ${status} Forbidden\r\nConnection: close\r\n\r\n`);
    };
    if (request.url !== "/extension") return reject(404);
    // 网页伪造不了 Origin：随便一个网页都能连 ws://127.0.0.1，这里把它们全挡在外面。
    const origin = request.headers.origin ?? "";
    if (!this.origins.includes(origin)) {
      // 记下来（商店版扩展 ID 没登记时就是这样连不上），但同一个来源一分钟只记一次：
      // 任何网页都能来敲这个端口，不能让它把日志刷满。
      const last = this.rejectedOrigins.get(origin) ?? 0;
      if (Date.now() - last > 60_000) {
        this.rejectedOrigins.set(origin, Date.now());
        this.log(`拒绝了一个不认识的来源：${origin || "（没有 Origin）"}`);
      }
      return reject(403);
    }
    const key = request.headers["sec-websocket-key"];
    if (typeof key !== "string" || request.headers.upgrade?.toLowerCase() !== "websocket") return reject(400);
    const accept = createHash("sha1").update(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").digest("base64");
    socket.write(
      "HTTP/1.1 101 Switching Protocols\r\n" +
        "Upgrade: websocket\r\nConnection: Upgrade\r\n" +
        `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
    );
    this.handshake(new Connection(socket));
  }

  private handshake(connection: Connection): void {
    const nonce = randomBytes(16).toString("hex");
    let authed = false;
    let pairingHere = false;
    const timer = setTimeout(() => {
      if (!authed && !pairingHere) {
        // 用 4007 而不是 4001：4001 是「配对码不对」，扩展收到会丢掉自己的码转去配对。
        // 早先超时也用 4001，扩展重新加载时一条慢了半拍的连接就把好好的码删了。
        this.log("浏览器扩展握手超时，断开");
        connection.close(4007, "handshake timeout");
      }
    }, HANDSHAKE_TIMEOUT_MS);
    connection.on("message", (text: string) => {
      let message: Record<string, unknown>;
      try {
        message = JSON.parse(text) as Record<string, unknown>;
      } catch {
        return;
      }
      if (authed) {
        this.onPeerMessage(connection, message);
        return;
      }
      if (message.type === "pong") return;
      if (message.type === "pair" && !pairingHere) {
        clearTimeout(timer);
        pairingHere = true;
        this.offerPairing(connection, String(message.userAgent ?? ""));
        return;
      }
      if (pairingHere && (message.type === "pairAccept" || message.type === "pairReject")) {
        this.finishPairing(connection, message.type === "pairAccept");
        return;
      }
      const token = this.pairToken();
      if (
        !token ||
        message.type !== "hello" ||
        typeof message.nonce !== "string" ||
        !sameHex(message.proof, hmacHex(token, "ext:" + nonce))
      ) {
        // 记下来：不记的话「扩展装好了却连不上」无从查起。多半是配对码对不上
        //（AIClaw 这边重新生成过）——扩展收到 4001 会丢掉旧码、自动重新配对。
        this.log(`浏览器扩展握手失败：${message.type === "hello" ? "配对码对不上" : `收到的是 ${String(message.type)}`}`);
        connection.close(4001, "bad proof");
        return;
      }
      authed = true;
      clearTimeout(timer);
      connection.send(JSON.stringify({ type: "welcome", proof: hmacHex(token, "app:" + message.nonce) }));
      this.adopt({
        connection,
        userAgent: String(message.userAgent ?? ""),
        extension: String(message.extension ?? ""),
        lastPong: Date.now(),
      });
      this.log(`浏览器扩展已连上（扩展 ${String(message.extension ?? "")}）`);
    });
    connection.on("close", () => {
      clearTimeout(timer);
      if (this.pairingWith?.connection === connection) {
        clearTimeout(this.pairingWith.timer);
        this.pairingWith = null;
        this.emit("status");
      }
      if (this.peer?.connection === connection) {
        this.peer = null;
        this.failAll(tr("浏览器扩展断开了"));
        this.log("浏览器扩展断开了");
        this.emit("status");
      }
    });
    connection.send(JSON.stringify({ type: "challenge", nonce }));
  }

  /**
   * 没有配对码的扩展来连：给它一个四位代码，它会在浏览器里开配对页让用户核对后点「允许」。
   *
   * 确认在浏览器那边做，不在这里：没配对的扩展分不清对面是不是真的 AIClaw，
   * 只有让用户在浏览器里对着两边的代码点，冒充 AIClaw 的程序才骗不到它。
   * 设置页同时显示这个代码。同一时间只配一个：新的来了，旧的作废。
   */
  private offerPairing(connection: Connection, userAgent: string): void {
    if (!this.pairToken()) {
      connection.close(4005, "not pairing");
      return;
    }
    if (this.pairingWith) {
      clearTimeout(this.pairingWith.timer);
      this.pairingWith.connection.close(4006, "superseded");
    }
    const code = String(randomInt(1000, 10_000));
    const timer = setTimeout(() => {
      if (this.pairingWith?.connection === connection) {
        this.log("浏览器扩展配对超时（3 分钟没人确认）");
        connection.close(4006, "pair timeout");
      }
    }, PAIR_TIMEOUT_MS);
    this.pairingWith = { connection, code, userAgent, timer };
    connection.send(JSON.stringify({ type: "pairOffer", code }));
    this.log(`浏览器扩展请求配对，代码 ${code}：等用户在浏览器的配对页上确认`);
    this.emit("status");
  }

  private finishPairing(connection: Connection, accepted: boolean): void {
    const pending = this.pairingWith;
    if (pending?.connection !== connection) return;
    clearTimeout(pending.timer);
    this.pairingWith = null;
    if (accepted) {
      // 用户在浏览器里核对过代码、点了允许：把配对码交给它，关掉这条连接，
      // 让它立刻用配对码重连——之后走的仍是不传码的挑战-应答。
      connection.send(JSON.stringify({ type: "paired", token: this.pairToken() }));
      connection.close(4003, "paired");
      this.log("浏览器扩展配对成功");
    } else {
      connection.close(1000, "declined");
      this.log("用户在浏览器里拒绝了配对");
    }
    this.emit("status");
  }

  private log(line: string): void {
    this.emit("log", line);
  }

  /**
   * 认下一个扩展。同时装在两个浏览器里时，后连上的那个接替——用户刚打开的那个
   * 浏览器多半就是他现在要用的。
   */
  private adopt(peer: Peer): void {
    const previous = this.peer;
    this.peer = peer;
    if (previous) {
      this.failAll(tr("换成了另一个浏览器里的扩展"));
      previous.connection.close(4002, "replaced");
    }
    this.emit("status");
  }

  private onPeerMessage(connection: Connection, message: Record<string, unknown>): void {
    if (this.peer?.connection !== connection) return;
    if (message.type === "pong") {
      this.peer.lastPong = Date.now();
      return;
    }
    const id = message.id;
    if (typeof id !== "number") return;
    const pending = this.pending.get(id);
    if (!pending) return;
    this.pending.delete(id);
    clearTimeout(pending.timer);
    if (message.ok === true) pending.resolve(message.result);
    else pending.reject(new Error(String(message.error ?? tr("浏览器扩展报错"))));
  }

  /** 心跳：让扩展的后台脚本保持醒着，也发现悄悄断掉的连接。 */
  private beat(): void {
    // 配对页开着等用户点的时候也要心跳：不然一两分钟里扩展的后台脚本就被浏览器收掉了。
    this.pairingWith?.connection.send(JSON.stringify({ type: "ping" }));
    const peer = this.peer;
    if (!peer) return;
    if (Date.now() - peer.lastPong > HEARTBEAT_MS * 3) {
      peer.connection.close(1001, "heartbeat timeout");
      return;
    }
    peer.connection.send(JSON.stringify({ type: "ping" }));
  }

  private failAll(reason: string): void {
    for (const [id, pending] of this.pending) {
      clearTimeout(pending.timer);
      pending.reject(new Error(reason));
      this.pending.delete(id);
    }
  }
}

/** 扩展没连上时的报错。写成函数：模块加载时界面语言还没设，要到用的时候再翻译。 */
export function notConnected(): string {
  return tr("浏览器扩展没连上：确认 Chrome / Edge 里装了「AIClaw 浏览器助手」并填了配对码（设置 → 浏览器）。");
}
