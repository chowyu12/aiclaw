/** Pin the upstream embedded Driver. No upstream installer,
 * agent auto-connect, telemetry setup, PATH changes or permission prompts. */
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import {
  cp,
  readFile,
  mkdir,
  mkdtemp,
  rm,
  rename,
  access,
} from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
export async function stageComputerHostDependencies({ repo, destination }) {
  // Same-process SDK owns daemon lifetime and parent-liveness, not tool dispatch.
  for (const name of ["@trycua/cua-driver", "@ubjs/core", "@ubjs/node"]) {
    await cp(
      join(repo, "node_modules", name),
      join(destination, "node_modules", name),
      { recursive: true },
    );
  }
}
export async function stageComputerRuntime({
  repo,
  destination,
  platform,
  arch,
}) {
  const manifest = JSON.parse(
    await readFile(join(repo, "tools/cua-driver/runtime.json"), "utf8"),
  );
  const asset =
    manifest.assets[platform === "darwin" ? platform : `${platform}-${arch}`];
  if (!asset) throw Error(`Unsupported Cua Driver target: ${platform}-${arch}`);
  const root = join(repo, "build/cua-driver", asset.sha256);
  try {
    const cached = JSON.parse(
      await readFile(join(root, "runtime.json"), "utf8"),
    );
    if (
      cached.version !== manifest.version ||
      cached.sha256 !== asset.sha256 ||
      !cached.files
    )
      throw Error(
        "Unverified Cua cache; remove build/cua-driver and stage again",
      );
    for (const [name, expected] of Object.entries(cached.files)) {
      const digest = createHash("sha256")
        .update(await readFile(join(root, name)))
        .digest("hex");
      if (digest !== expected) throw Error(`Damaged Cua cache: ${name}`);
    }
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    await mkdir(dirname(root), { recursive: true });
    const temporary = await mkdtemp(join(dirname(root), "stage-"));
    try {
      const archive = join(temporary, asset.name);
      // Bounded retries and timeout; never fetch an unpinned latest release.
      execFileSync(
        "curl",
        [
          "--fail",
          "--location",
          "--retry",
          "3",
          "--max-time",
          "300",
          "--output",
          archive,
          `https://github.com/${manifest.repository}/releases/download/${manifest.tag}/${asset.name}`,
        ],
        { stdio: "inherit" },
      );
      const digest = createHash("sha256")
        .update(await readFile(archive))
        .digest("hex");
      if (digest !== asset.sha256) throw Error("Cua Driver checksum mismatch");
      const extracted = join(temporary, "extracted");
      await mkdir(extracted);
      execFileSync(
        asset.name.endsWith(".zip") ? "unzip" : "tar",
        asset.name.endsWith(".zip")
          ? ["-q", archive, "-d", extracted]
          : ["-xzf", archive, "-C", extracted],
      );
      const { readdir, writeFile } = await import("node:fs/promises");
      const entries = await readdir(extracted);
      const source =
        entries.length === 1 && entries[0].startsWith("cua-driver-rs-")
          ? join(extracted, entries[0])
          : extracted;
      await cp(source, join(temporary, "runtime"), { recursive: true });
      await cp(
        join(repo, "tools/claw-agent/internal/agent/computer-tools.json"),
        join(temporary, "runtime/tools.json"),
      );
      const files = {};
      for (const name of await readdir(join(temporary, "runtime"))) {
        if (
          !/^(cua-driver(?:\.exe)?|libcua_driver_sdk\.(?:dylib|so)|cua_driver_sdk\.dll|cua_driver_node_runtime\.node|LICENSE|THIRD_PARTY_NOTICES\.md)$/.test(
            name,
          )
        )
          continue;
        files[name] = createHash("sha256")
          .update(await readFile(join(temporary, "runtime", name)))
          .digest("hex");
      }
      await writeFile(
        join(temporary, "runtime/runtime.json"),
        JSON.stringify({
          version: manifest.version,
          asset: asset.name,
          sha256: asset.sha256,
          files,
        }),
      );
      await rename(join(temporary, "runtime"), root);
    } finally {
      await rm(temporary, { recursive: true, force: true });
    }
  }
  const target = join(destination, "cua-driver");
  await rm(target, { recursive: true, force: true });
  await mkdir(target, { recursive: true });
  for (const name of [
    platform === "win32" ? "cua-driver.exe" : "cua-driver",
    "LICENSE",
    "THIRD_PARTY_NOTICES.md",
    "runtime.json",
  ]) {
    await cp(join(root, name), join(target, name));
  }
  // The SDK's platform module is supplied by the same verified release archive.
  const platformName = platform === "win32" ? "win32" : platform;
  const suffix =
    platform === "linux" ? "-gnu" : platform === "win32" ? "-msvc" : "";
  const module = join(
    destination,
    "node_modules/@trycua",
    `cua-driver-${platformName}-${arch}${suffix}`,
  );
  await mkdir(module, { recursive: true });
  const library =
    platform === "darwin"
      ? "libcua_driver_sdk.dylib"
      : platform === "win32"
        ? "cua_driver_sdk.dll"
        : "libcua_driver_sdk.so";
  for (const name of [library, "cua_driver_node_runtime.node"])
    await cp(join(root, name), join(module, name));
  const { writeFile } = await import("node:fs/promises");
  await writeFile(
    join(module, "package.json"),
    JSON.stringify({
      name: `@trycua/cua-driver-${platformName}-${arch}${suffix}`,
      version: manifest.version,
      license: "MIT AND MPL-2.0",
    }),
  );
  await cp(
    join(root, "THIRD_PARTY_NOTICES.md"),
    join(module, "THIRD_PARTY_NOTICES.md"),
  );
  await cp(
    join(repo, "tools/cua-driver/node-runtime-NOTICE.md"),
    join(module, "node-runtime-NOTICE.md"),
  );
  // Refresh host contract even on a cached artifact.
  await cp(
    join(repo, "tools/claw-agent/internal/agent/computer-tools.json"),
    join(target, "tools.json"),
  );
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
  await stageComputerRuntime({
    repo,
    destination: join(repo, "apps/desktop"),
    platform: process.platform,
    arch: process.arch,
  });
}
