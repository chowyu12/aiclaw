/** Native pure-logic tests; never observes or inputs into any application. */
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
if (process.platform === "darwin") {
  const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
  const temporary = mkdtempSync(join(tmpdir(), "aiclaw-native-logic-"));
  try {
    const binary = join(temporary, "tests");
    const sources = ["Sources/Markdown.swift", "Sources/Models.swift", "Sources/Observation.swift", "Sources/Actions.swift", "Tests/ActionsTests.swift"].map(path => join(repo, "tools/computer-use", path));
    execFileSync("xcrun", ["swiftc", "-swift-version", "5", "-target", `${process.arch === "x64" ? "x86_64" : "arm64"}-apple-macos14.4`, ...sources, "-o", binary], { stdio: "inherit" });
    execFileSync(binary, [], { stdio: "inherit" });
  } finally { rmSync(temporary, { recursive: true, force: true }); }
} else console.log("Native Computer Use logic tests require macOS; no native component on this OS.");
