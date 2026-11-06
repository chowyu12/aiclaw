/** Bounded native capture logic only; never requests permissions or observes UI. */
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
if (process.platform !== "darwin") { console.log("Native stream tests require macOS"); process.exit(0); }
const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const temporary = mkdtempSync(join(tmpdir(), "aiclaw-capture-logic-"));
try {
  const binary = join(temporary, "capture-tests");
  const sources = ["Sources/Models.swift", "Sources/Observation.swift", "Sources/Capture.swift", "Tests/CaptureTests.swift"].map(path => join(repo, "tools/computer-use", path));
  execFileSync("xcrun", ["swiftc", "-swift-version", "5", "-target", `${process.arch === "x64" ? "x86_64" : "arm64"}-apple-macos14.4`, ...sources, "-o", binary], {stdio: "inherit"});
  execFileSync(binary, [], {stdio: "inherit"});
} finally { rmSync(temporary, {recursive: true, force: true}); }
