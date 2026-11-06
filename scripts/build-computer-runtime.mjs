/** Build our independent, universal macOS app. Never downloads a native runtime. */
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, copyFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { loadMacSigning, signMacBundle, verifyMacBundle } from "./mac-signing.mjs";
const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
export const COMPUTER_BUNDLE_NAME = "AIClaw Computer Use.app";
const identifier="com.github.chowyu12.aiclaw.computer-use";
const protocolVersion="AIClawComputerUseIPC-1";
const hash=bytes=>createHash("sha256").update(bytes).digest("hex");
export function computerBuildFingerprint({sourceRoot=repo,architectures,certificateSHA256,toolchain}) {
  const files=["tools/computer-use/runtime.json","tools/claw-agent/internal/agent/computer-tools.json","scripts/build-computer-runtime.mjs","scripts/mac-signing.mjs",...readdirSync(join(sourceRoot,"tools/computer-use/Sources")).filter(name=>name.endsWith(".swift")).sort().map(name=>`tools/computer-use/Sources/${name}`)];
  return hash(JSON.stringify({format:1,architectures,certificateSHA256,toolchain,files:files.map(name=>[name,hash(readFileSync(join(sourceRoot,name)))])}));
}
export function computerBuildConfiguration(universal=false) {
  const signing=loadMacSigning();
  const architectures=universal?["arm64","x86_64"]:[process.arch==="x64"?"x86_64":"arm64"];
  const toolchain={swift:execFileSync("xcrun",["swiftc","--version"],{encoding:"utf8"}),sdk:execFileSync("xcrun",["--sdk","macosx","--show-sdk-version"],{encoding:"utf8"}),sdkBuild:execFileSync("xcrun",["--sdk","macosx","--show-sdk-build-version"],{encoding:"utf8"})};
  return {signing,architectures,fingerprint:computerBuildFingerprint({architectures,certificateSHA256:signing.certificateSHA256,toolchain})};
}
export function buildComputerRuntime({ output = join(repo, "apps/desktop/computer-use"), universal = false } = {}) {
  if (process.platform !== "darwin") { console.log("Native Computer Use: macOS-only; no runtime staged on this platform."); return; }
  const {signing,architectures,fingerprint}=computerBuildConfiguration(universal);
  const target=join(output,COMPUTER_BUNDLE_NAME), metadataPath=join(output,"AIClaw Computer Use.build.json");
  if (existsSync(metadataPath)) {
    const previous=JSON.parse(readFileSync(metadataPath,"utf8"));
    if (previous.fingerprint===fingerprint) {
      verifyMacBundle(target,identifier,signing);
      const sealed=JSON.parse(readFileSync(join(target,"Contents/Resources/Build.json"),"utf8"));
      if (sealed.fingerprint!==fingerprint || hash(readFileSync(join(target,"Contents/MacOS/AIClawComputerUse")))!==previous.executableSHA256) throw Error("Cached native runtime identity changed; refusing reuse");
      console.log(`Reused unchanged ${target} (${fingerprint})`); return target;
    }
  }
  mkdirSync(output,{recursive:true});
  const temporary=mkdtempSync(join(output,".build-"));
  try {
    const bundle = join(temporary, COMPUTER_BUNDLE_NAME), contents = join(bundle, "Contents");
    const binary = join(contents, "MacOS", "AIClawComputerUse");
    mkdirSync(join(contents, "MacOS"), { recursive: true }); mkdirSync(join(contents, "Resources"), { recursive: true });
    const source = join(repo, "tools/computer-use/Sources");
    const sources = readdirSync(source).filter(name => name.endsWith(".swift")).sort().map(name => join(source, name));
    const slices = [];
    for (const architecture of architectures) {
      const slice = `${binary}.${architecture}`; slices.push(slice);
      execFileSync("xcrun", ["swiftc", "-swift-version", "5", "-O", "-target", `${architecture}-apple-macos14.4`, ...sources, "-o", slice], { stdio: "inherit" });
    }
    if (slices.length > 1) execFileSync("xcrun", ["lipo", "-create", ...slices, "-output", binary], { stdio: "inherit" });
    else copyFileSync(slices[0], binary);
    for (const slice of slices) rmSync(slice);
    const version = JSON.parse(readFileSync(join(repo, "tools/computer-use/runtime.json"), "utf8")).version;
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
    writeFileSync(join(contents,"Resources/Build.json"),JSON.stringify({fingerprint,version,certificateSHA256:signing.certificateSHA256})+"\n");
    signMacBundle(bundle,identifier,signing);
    if (computerBuildConfiguration(universal).fingerprint!==fingerprint) throw Error("Native build inputs changed during compilation; retry from a stable source tree");
    const metadata={fingerprint,version,architectures:architectures.map(arch=>arch==="x86_64"?"x64":arch),executableSHA256:hash(readFileSync(binary)),protocolVersion,signature:signing.identity==="-"?"ad-hoc":signing.selfSigned?"self-signed":"configured",certificateSHA256:signing.certificateSHA256};
    if (existsSync(target)) rmSync(target,{recursive:true});
    renameSync(bundle,target);
    writeFileSync(metadataPath,JSON.stringify(metadata)+"\n");
    console.log(`Built ${target} (${architectures.join(", ")}; ${metadata.signature})`);
    return target;
  } finally {rmSync(temporary,{recursive:true,force:true});}
}
if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const universal=process.argv.includes("--universal");
  if (process.argv.includes("--fingerprint")) console.log(computerBuildConfiguration(universal).fingerprint);
  else buildComputerRuntime({universal});
}
