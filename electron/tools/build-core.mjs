// Cross-compiles the Go core for a packaging target.
//
// The core needs no cgo, so every platform builds from one machine — verified
// across darwin, windows and linux. The macOS core is still built as a
// universal binary: the shell ships per-architecture packages, and one core
// that runs on both is simpler than threading the architecture through
// packaging for no size saving worth having.
//
// Usage: node tools/build-core.mjs <darwin|win32|linux> [outDir]

import { execFileSync } from "node:child_process";
import { mkdirSync, rmSync } from "node:fs";
import { join, resolve } from "node:path";

const [platform, outArg] = process.argv.slice(2);
const targets = {
  darwin: [
    { goos: "darwin", goarch: "amd64" },
    { goos: "darwin", goarch: "arm64" },
  ],
  win32: [{ goos: "windows", goarch: "amd64" }],
  linux: [{ goos: "linux", goarch: "amd64" }],
};

if (!targets[platform]) {
  console.error(`usage: build-core.mjs <${Object.keys(targets).join("|")}> [outDir]`);
  process.exit(1);
}

const repoRoot = resolve(import.meta.dirname, "..", "..");
const outDir = resolve(outArg ?? join(import.meta.dirname, "..", "core"));
mkdirSync(outDir, { recursive: true });

const name = platform === "win32" ? "aiclaw-core.exe" : "aiclaw-core";
const built = [];

for (const { goos, goarch } of targets[platform]) {
  const output = join(outDir, `${name}-${goarch}`);
  console.log(`building ${goos}/${goarch}`);
  execFileSync(
    "go",
    ["build", "-trimpath", "-ldflags", "-s -w", "-o", output, "./cmd/aiclaw-core"],
    {
      cwd: repoRoot,
      // No cgo: this is what lets one machine produce every target.
      env: { ...process.env, CGO_ENABLED: "0", GOOS: goos, GOARCH: goarch },
      stdio: "inherit",
    },
  );
  built.push(output);
}

const final = join(outDir, name);
if (built.length === 1) {
  execFileSync("mv", [built[0], final]);
} else {
  // The Electron package is universal, so a single-architecture core beside
  // it would fail on half the machines that can run the app.
  execFileSync("lipo", ["-create", "-output", final, ...built]);
  for (const file of built) rmSync(file);
}

console.log(`wrote ${final}`);
