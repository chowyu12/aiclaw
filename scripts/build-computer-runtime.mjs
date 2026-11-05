/** Build our independent, universal macOS app. Never downloads a native runtime. */
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, readdirSync, copyFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
export const COMPUTER_BUNDLE_NAME = "AIClaw Computer Use.app";
export function buildComputerRuntime({ output = join(repo, "apps/desktop/computer-use"), universal = false } = {}) {
  if (process.platform !== "darwin") { console.log("Native Computer Use: macOS-only; no runtime staged on this platform."); return; }
  const bundle = join(output, COMPUTER_BUNDLE_NAME), contents = join(bundle, "Contents");
  const binary = join(contents, "MacOS", "AIClawComputerUse");
  mkdirSync(join(contents, "MacOS"), { recursive: true }); mkdirSync(join(contents, "Resources"), { recursive: true });
  const source = join(repo, "tools/computer-use/Sources");
  const sources = readdirSync(source).filter(name => name.endsWith(".swift")).sort().map(name => join(source, name));
  const architectures = universal ? ["arm64", "x86_64"] : [process.arch === "x64" ? "x86_64" : "arm64"];
  const slices = [];
  for (const architecture of architectures) {
    const slice = `${binary}.${architecture}`; slices.push(slice);
    execFileSync("xcrun", ["swiftc", "-swift-version", "5", "-O", "-target", `${architecture}-apple-macos14.4`, ...sources, "-o", slice], { stdio: "inherit" });
  }
  if (slices.length > 1) execFileSync("xcrun", ["lipo", "-create", ...slices, "-output", binary], { stdio: "inherit" });
  else copyFileSync(slices[0], binary);
  for (const slice of slices) rmSync(slice);
  const version = JSON.parse(readFileSync(join(repo, "package.json"), "utf8")).version;
  writeFileSync(join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.github.chowyu12.aiclaw.computer-use</string>
<key>CFBundleName</key><string>AIClaw Computer Use</string><key>CFBundleDisplayName</key><string>AIClaw Computer Use</string>
<key>CFBundleExecutable</key><string>AIClawComputerUse</string><key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>${version}</string><key>CFBundleVersion</key><string>${version}</string>
<key>LSMinimumSystemVersion</key><string>14.4</string><key>LSUIElement</key><true/>
<key>NSHighResolutionCapable</key><true/>
<key>NSScreenCaptureUsageDescription</key><string>Capture only the application window you choose so AIClaw can observe and verify desktop actions.</string>
</dict></plist>`);
  copyFileSync(join(repo, "tools/claw-agent/internal/agent/computer-tools.json"), join(contents, "Resources", "Tools.json"));
  const identity = process.env.COMPUTER_USE_SIGN_IDENTITY || "-";
  execFileSync("/usr/bin/codesign", ["--force", "--sign", identity, "--identifier", "com.github.chowyu12.aiclaw.computer-use", ...(identity === "-" ? [] : ["--options", "runtime", "--timestamp"]), bundle], { stdio: "inherit" });
  execFileSync("/usr/bin/codesign", ["--verify", "--strict", bundle], { stdio: "inherit" });
  writeFileSync(join(output, "AIClaw Computer Use.build.json"), JSON.stringify({ architectures: architectures.map(arch => arch === "x86_64" ? "x64" : arch), executableSHA256: createHash("sha256").update(readFileSync(binary)).digest("hex"), protocolVersion: "AIClawComputerUseIPC-1", signature: identity === "-" ? "ad-hoc" : "configured" }) + "\n");
  console.log(`Built ${bundle} (${architectures.join(", ")}; ${identity === "-" ? "ad-hoc development signature" : "configured signing identity"})`);
  return bundle;
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) buildComputerRuntime({ universal: process.argv.includes("--universal") });
