import test from "node:test";
import assert from "node:assert/strict";
import {
  mkdtemp,
  mkdir,
  readFile,
  rm,
  writeFile,
  readdir,
} from "node:fs/promises";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import {
  stageComputerRuntime,
  stageComputerHostDependencies,
} from "./stage-computer-runtime.mjs";
const manifest = JSON.parse(
  await readFile(
    new URL("../tools/cua-driver/runtime.json", import.meta.url),
    "utf8",
  ),
);
async function fixture(platform: string, arch: string) {
  const repo = await mkdtemp(join(tmpdir(), "aiclaw-cua-stage-")),
    destination = join(repo, "app");
  const asset =
    manifest.assets[platform === "darwin" ? platform : `${platform}-${arch}`];
  await mkdir(join(repo, "tools/cua-driver"), { recursive: true });
  await writeFile(
    join(repo, "tools/cua-driver/runtime.json"),
    JSON.stringify(manifest),
  );
  await writeFile(
    join(repo, "tools/cua-driver/node-runtime-NOTICE.md"),
    "MPL-2.0 source notice",
  );
  await mkdir(join(repo, "tools/claw-agent/internal/agent"), {
    recursive: true,
  });
  await writeFile(
    join(repo, "tools/claw-agent/internal/agent/computer-tools.json"),
    "[]",
  );
  const root = join(repo, "build/cua-driver", asset.sha256);
  await mkdir(root, { recursive: true });
  const binary = platform === "win32" ? "cua-driver.exe" : "cua-driver";
  const library =
    platform === "darwin"
      ? "libcua_driver_sdk.dylib"
      : platform === "win32"
        ? "cua_driver_sdk.dll"
        : "libcua_driver_sdk.so";
  const files: Record<string, string> = {};
  for (const name of [
    binary,
    library,
    "cua_driver_node_runtime.node",
    "LICENSE",
    "THIRD_PARTY_NOTICES.md",
  ]) {
    await writeFile(join(root, name), name);
    files[name] = createHash("sha256").update(name).digest("hex");
  }
  // Even if the upstream archive contains an app, packaging must exclude it.
  await mkdir(join(root, "CuaDriver.app"));
  await writeFile(
    join(root, "runtime.json"),
    JSON.stringify({ version: manifest.version, sha256: asset.sha256, files }),
  );
  return { repo, destination, root, binary };
}
test("every target packages embedded executable and matching native SDK without an independent app", async () => {
  for (const [platform, arch] of [
    ["darwin", "arm64"],
    ["darwin", "x64"],
    ["linux", "x64"],
    ["linux", "arm64"],
    ["win32", "x64"],
    ["win32", "arm64"],
  ]) {
    const f = await fixture(platform!, arch!);
    try {
      await stageComputerRuntime({ ...f, platform, arch });
      assert.deepEqual(
        (await readdir(join(f.destination, "cua-driver"))).sort(),
        [
          f.binary,
          "LICENSE",
          "THIRD_PARTY_NOTICES.md",
          "runtime.json",
          "tools.json",
        ].sort(),
      );
      const modules = await readdir(
        join(f.destination, "node_modules/@trycua"),
      );
      assert.equal(modules.length, 1);
      assert.ok(modules[0]!.includes(arch!));
      assert.equal(
        await readFile(
          join(
            f.destination,
            "node_modules/@trycua",
            modules[0]!,
            "cua_driver_node_runtime.node",
          ),
          "utf8",
        ),
        "cua_driver_node_runtime.node",
      );
    } finally {
      await rm(f.repo, { recursive: true, force: true });
    }
  }
});
test("a corrupted cached Driver or SDK is rejected before packaging", async () => {
  const f = await fixture("darwin", "arm64");
  try {
    await writeFile(join(f.root, f.binary), "tampered");
    await assert.rejects(
      stageComputerRuntime({ ...f, platform: "darwin", arch: "arm64" }),
      /Damaged Cua cache/,
    );
  } finally {
    await rm(f.repo, { recursive: true, force: true });
  }
});
test("packaging includes SDK JS and its exact resolver dependencies", async () => {
  const destination = await mkdtemp(join(tmpdir(), "aiclaw-cua-modules-"));
  try {
    await stageComputerHostDependencies({
      repo: fileURLToPath(new URL("../", import.meta.url)),
      destination,
    });
    for (const name of ["@trycua/cua-driver", "@ubjs/core", "@ubjs/node"])
      assert.ok(
        JSON.parse(
          await readFile(
            join(destination, "node_modules", name, "package.json"),
            "utf8",
          ),
        ).version,
      );
    assert.match(
      await readFile(
        join(destination, "node_modules/@trycua/cua-driver/dist/embedded.js"),
        "utf8",
      ),
      /EmbeddedCuaDriverHost/,
    );
  } finally {
    await rm(destination, { recursive: true, force: true });
  }
});
