import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  PROTOCOL_VERSION,
  Sidecar,
  type CoreEvent,
  type HostCall,
  type SidecarState,
} from "./sidecar";

/**
 * A stand-in for the Go core. Tests never spawn the real executable: the
 * failure paths that matter here — a core that never handshakes, dies
 * mid-turn, or refuses to exit — are impossible to provoke reliably with a
 * healthy real process.
 */
class FakeChild extends EventEmitter {
  readonly stdout = new PassThrough();
  readonly stderr = new PassThrough();
  readonly stdin = new PassThrough();
  readonly written: string[] = [];
  killed: NodeJS.Signals[] = [];
  /** When true, the child ignores stdin closing, like a wedged process. */
  ignoreStdinClose = false;

  constructor() {
    super();
    this.stdin.on("data", (chunk: Buffer) => {
      for (const line of chunk.toString().split("\n")) {
        if (line.trim()) this.written.push(line);
      }
    });
    this.stdin.on("finish", () => {
      if (!this.ignoreStdinClose) this.exit(0);
    });
  }

  /** Writes one protocol line as the core would. */
  say(message: unknown): void {
    this.stdout.write(`${JSON.stringify(message)}\n`);
  }

  handshake(protocol = PROTOCOL_VERSION, commands = ["Status", "Plugins"]): void {
    this.say({ ready: true, protocol, commands });
  }

  kill(signal: NodeJS.Signals): boolean {
    this.killed.push(signal);
    if (signal === "SIGKILL") this.exit(null, signal);
    return true;
  }

  exit(code: number | null, signal: NodeJS.Signals | null = null): void {
    this.emit("exit", code, signal);
  }

  /** Parsed messages this child received. */
  received(): Record<string, unknown>[] {
    return this.written.map((line) => JSON.parse(line));
  }
}

interface Harness {
  sidecar: Sidecar;
  children: FakeChild[];
  states: { state: SidecarState; detail?: string }[];
  events: CoreEvent[];
  hostCalls: HostCall[];
  answerHostCall: (value: unknown) => void;
  hostCallFails: { value: boolean };
}

function harness(overrides: { handshakeTimeoutMs?: number } = {}): Harness {
  const children: FakeChild[] = [];
  const states: { state: SidecarState; detail?: string }[] = [];
  const events: CoreEvent[] = [];
  const hostCalls: HostCall[] = [];
  let hostAnswer: unknown = ["/tmp/a.png"];
  const hostCallFails = { value: false };

  const sidecar = new Sidecar({
    executable: "/fake/aiclaw-core",
    hooks: {
      onEvent: (event) => events.push(event),
      onHostCall: async (call) => {
        hostCalls.push(call);
        if (hostCallFails.value) throw new Error("no window");
        return hostAnswer;
      },
      onState: (state, detail) => states.push({ state, detail }),
    },
    spawnFn: (() => {
      const child = new FakeChild();
      children.push(child);
      return child;
    }) as never,
    ...overrides,
  });

  return {
    sidecar,
    children,
    states,
    events,
    hostCalls,
    answerHostCall: (value) => {
      hostAnswer = value;
    },
    hostCallFails,
  };
}

/** Waits for a condition, failing the test rather than hanging forever. */
async function waitFor(condition: () => boolean, message: string): Promise<void> {
  const deadline = Date.now() + 2_000;
  while (Date.now() < deadline) {
    if (condition()) return;
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
  throw new Error(message);
}

afterEach(() => {
  vi.useRealTimers();
});

describe("start", () => {
  it("resolves once the core reports ready", async () => {
    const h = harness();
    const started = h.sidecar.start();
    await waitFor(() => h.children.length === 1, "no child was spawned");
    h.children[0].handshake();

    const result = await started;
    expect(result.protocol).toBe(PROTOCOL_VERSION);
    expect(h.sidecar.availableCommands).toEqual(["Status", "Plugins"]);
    expect(h.sidecar.currentState).toBe("ready");
  });

  it("fails instead of hanging when the core never reports ready", async () => {
    const h = harness({ handshakeTimeoutMs: 50 });
    await expect(h.sidecar.start()).rejects.toThrow(/did not report ready/);
    expect(h.sidecar.currentState).toBe("failed");
  });

  // A core speaking another protocol would answer some calls and quietly
  // misbehave on others, which is worse than having no core at all.
  it("refuses a core that speaks a different protocol", async () => {
    const h = harness();
    const started = h.sidecar.start();
    await waitFor(() => h.children.length === 1, "no child");
    h.children[0].handshake(PROTOCOL_VERSION + 1);

    await expect(started).rejects.toThrow(/protocol/);
    expect(h.sidecar.currentState).toBe("failed");
  });
});

describe("invoke", () => {
  async function ready() {
    const h = harness();
    const started = h.sidecar.start();
    await waitFor(() => h.children.length === 1, "no child");
    h.children[0].handshake();
    await started;
    return h;
  }

  it("matches a reply to its request by id", async () => {
    const h = await ready();
    const first = h.sidecar.invoke("Plugins");
    const second = h.sidecar.invoke("Status");

    await waitFor(() => h.children[0].received().length === 2, "requests not sent");
    const [a, b] = h.children[0].received();
    // Replying out of order must still resolve the right promises: background
    // turns finish whenever they finish.
    h.children[0].say({ id: b.id, ok: true, result: "healthy" });
    h.children[0].say({ id: a.id, ok: true, result: [{ uuid: "p1" }] });

    await expect(second).resolves.toBe("healthy");
    await expect(first).resolves.toEqual([{ uuid: "p1" }]);
  });

  it("rejects with the error the core reported", async () => {
    const h = await ready();
    const call = h.sidecar.invoke("DeletePlugin", ["p1"]);
    await waitFor(() => h.children[0].received().length === 1, "no request");
    const [sent] = h.children[0].received();
    h.children[0].say({ id: sent.id, ok: false, error: "plugin ships with the application" });

    await expect(call).rejects.toThrow(/ships with the application/);
  });

  it("refuses to invoke while the core is not ready", async () => {
    const h = harness();
    await expect(h.sidecar.invoke("Status")).rejects.toThrow(/stopped/);
  });

  // A caller must not wait forever for a reply that can never come.
  it("rejects in-flight calls when the core dies", async () => {
    const h = await ready();
    const call = h.sidecar.invoke("Plugins");
    await waitFor(() => h.children[0].received().length === 1, "no request");
    h.children[0].exit(1);

    await expect(call).rejects.toThrow(/the core stopped/);
  });
});

describe("events and host calls", () => {
  async function ready() {
    const h = harness();
    const started = h.sidecar.start();
    await waitFor(() => h.children.length === 1, "no child");
    h.children[0].handshake();
    await started;
    return h;
  }

  it("forwards events to the interface", async () => {
    const h = await ready();
    h.children[0].say({ id: "r1", event: "chat:delta", data: [{ content: "hi" }] });
    await waitFor(() => h.events.length === 1, "no event forwarded");
    expect(h.events[0].event).toBe("chat:delta");
    expect(h.events[0].id).toBe("r1");
  });

  // The core is headless; a native dialog has to come back to this process.
  it("answers a host call and sends the result back", async () => {
    const h = await ready();
    h.answerHostCall(["/tmp/pick.png"]);
    h.children[0].say({ id: "host-1", host: "dialog.pickFiles", params: { title: "choose" } });

    await waitFor(() => h.hostCalls.length === 1, "host call not delivered");
    await waitFor(
      () => h.children[0].received().some((m) => m.id === "host-1"),
      "no reply sent to the core",
    );
    const reply = h.children[0].received().find((m) => m.id === "host-1") as {
      reply: { ok: boolean; result: unknown };
    };
    expect(reply.reply.ok).toBe(true);
    expect(reply.reply.result).toEqual(["/tmp/pick.png"]);
  });

  it("reports a failed host call rather than leaving the core waiting", async () => {
    const h = await ready();
    h.hostCallFails.value = true;
    h.children[0].say({ id: "host-2", host: "dialog.pickDirectory" });

    await waitFor(
      () => h.children[0].received().some((m) => m.id === "host-2"),
      "no reply sent",
    );
    const reply = h.children[0].received().find((m) => m.id === "host-2") as {
      reply: { ok: boolean; error: string };
    };
    expect(reply.reply.ok).toBe(false);
    expect(reply.reply.error).toMatch(/no window/);
  });

  it("survives an unreadable line instead of dropping the connection", async () => {
    const h = await ready();
    h.children[0].stdout.write("this is not json\n");
    h.children[0].say({ id: "r1", event: "chat:progress" });
    await waitFor(() => h.events.length === 1, "the reader stopped after bad input");
    expect(h.sidecar.currentState).toBe("ready");
  });
});

describe("restart", () => {
  it("restarts with backoff and gives up after repeated failures", async () => {
    vi.useFakeTimers();
    const h = harness({ handshakeTimeoutMs: 10 });
    const started = h.sidecar.start();
    await vi.advanceTimersByTimeAsync(1);
    h.children[0].handshake();
    await started;

    // Each crash schedules the next attempt; the child never handshakes
    // again, so the supervisor keeps escalating.
    for (let attempt = 0; attempt < 6; attempt += 1) {
      h.children[h.children.length - 1].exit(1);
      await vi.advanceTimersByTimeAsync(RESTART_CEILING);
    }

    expect(h.sidecar.currentState).toBe("failed");
    const restarting = h.states.filter((s) => s.state === "restarting");
    expect(restarting.length).toBeGreaterThan(0);
    // A failure the user can act on says why.
    expect(h.states.at(-1)?.detail).toMatch(/times in a row|did not report ready/);
  });
});

describe("stop", () => {
  it("closes stdin and lets the core exit on its own", async () => {
    const h = harness();
    const started = h.sidecar.start();
    await waitFor(() => h.children.length === 1, "no child");
    h.children[0].handshake();
    await started;

    await h.sidecar.stop();
    expect(h.sidecar.currentState).toBe("stopped");
    // Escalation is unnecessary when the core leaves politely.
    expect(h.children[0].killed).toEqual([]);
  });

  // A wedged core must not keep the application alive.
  it("escalates to SIGTERM then SIGKILL when the core will not exit", async () => {
    vi.useFakeTimers();
    const h = harness();
    const started = h.sidecar.start();
    await vi.advanceTimersByTimeAsync(1);
    h.children[0].handshake();
    await started;

    h.children[0].ignoreStdinClose = true;
    const stopping = h.sidecar.stop();
    await vi.advanceTimersByTimeAsync(5_000);
    expect(h.children[0].killed).toContain("SIGTERM");
    await vi.advanceTimersByTimeAsync(2_000);
    expect(h.children[0].killed).toContain("SIGKILL");
    await stopping;
    expect(h.sidecar.currentState).toBe("stopped");
  });

  it("does not restart a core it stopped on purpose", async () => {
    const h = harness();
    const started = h.sidecar.start();
    await waitFor(() => h.children.length === 1, "no child");
    h.children[0].handshake();
    await started;

    await h.sidecar.stop();
    await new Promise((resolve) => setTimeout(resolve, 60));
    expect(h.children).toHaveLength(1);
    expect(h.sidecar.currentState).toBe("stopped");
  });
});

/** Longest backoff the supervisor uses, so tests can step past it. */
const RESTART_CEILING = 30_000;
