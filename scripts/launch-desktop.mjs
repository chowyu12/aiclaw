import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { constants } from "node:os";
import { Transform } from "node:stream";

const requireDesktop = createRequire(new URL("../apps/desktop/package.json", import.meta.url));
const electron = requireDesktop("electron");
const onMac = process.platform === "darwin";
const child = spawn(electron, [".", ...process.argv.slice(2)], {
  stdio: ["inherit", "inherit", onMac ? "pipe" : "inherit"],
});

if (onMac) {
  const layoutMessage = /^(?:Keyboard Layouts: duplicate keyboard layout identifier -?\d+\.|Keyboard Layouts: keyboard layout identifier -?\d+ has been replaced with -?\d+\.)$/;
  const cacheMessage = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+ Electron\[\d+:[\da-fA-F]+\] TISFileInterrogator updateSystemInputSources false but old data invalid: currentCacheHeaderPtr nonNULL\? \d+, ->cacheFormatVersion \d+, ->magicCookie [\da-fA-F]+, inputSourceTableCountSys \d+$/;
  const noisy = line => {
    const message = line.toString().replace(/\r?\n$/, "");
    return layoutMessage.test(message) || cacheMessage.test(message);
  };
  let pending = Buffer.alloc(0);
  const filter = new Transform({
    transform(chunk, _encoding, done) {
      const data = pending.length ? Buffer.concat([pending, chunk]) : chunk;
      let start = 0;
      for (let newline = data.indexOf(10); newline !== -1; newline = data.indexOf(10, start)) {
        const line = data.subarray(start, newline + 1);
        if (!noisy(line)) this.push(line);
        start = newline + 1;
      }
      pending = data.subarray(start);
      done();
    },
    flush(done) {
      if (pending.length && !noisy(pending)) this.push(pending);
      done();
    },
  });
  child.stderr.pipe(filter).pipe(process.stderr, { end: false });
}

const forward = signal => child.kill(signal);
process.on("SIGINT", forward);
process.on("SIGTERM", forward);
child.once("error", error => {
  console.error(error.message);
});
child.once("close", (code, signal) => {
  process.removeListener("SIGINT", forward);
  process.removeListener("SIGTERM", forward);
  process.exitCode = code ?? (signal ? 128 + constants.signals[signal] : 1);
});
