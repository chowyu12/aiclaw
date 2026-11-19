import test from "node:test";
import assert from "node:assert/strict";
import {
  mkdtemp,
  mkdir,
  readFile,
  rm,
  writeFile,
  readdir,
  copyFile,
  chmod,
  rename,
  stat,
} from "node:fs/promises";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { stageComputerRuntime } from "./stage-computer-runtime.mjs";
import { stageHostDependencies } from "./stage-host-dependencies.mjs";
import { execFileSync } from "node:child_process";
import { thinMacComputerRuntime } from "./mac-signing.mjs";
const manifest = JSON.parse(
  await readFile(
    new URL("../tools/cua-driver/runtime.json", import.meta.url),
    "utf8",
  ),
);
test("Mac packages keep only the target Cua architecture, permissions and repeatability", {skip:process.platform!=="darwin"}, async () => {
  const app=await mkdtemp(join(tmpdir(),"aiclaw-thin-"));
  try {
    const objects: Record<string,string>={};
    for (const arch of ["arm64","x86_64"]) {
      const path=join(app,`${arch}.o`);
      execFileSync("xcrun",["clang","-target",`${arch}-apple-macos14`,"-x","c","-c","-o",path,"-"],{input:"int fixture(void) { return 0; }"});
      objects[arch]=path;
    }
    const fat=join(app,"universal.o");
    execFileSync("/usr/bin/lipo",["-create",...Object.values(objects),"-output",fat]);
    const main=join(app,"Contents/MacOS/AIClaw");
    await mkdir(join(app,"Contents/MacOS"),{recursive:true});
    for (const [arch,moduleArch] of [["arm64","arm64"],["x86_64","x64"]]) {
      await copyFile(objects[arch!]!,main);
      const root=join(app,"Contents/Resources/app");
      const sdk=join(root,`node_modules/@trycua/cua-driver-darwin-${moduleArch}`);
      await mkdir(sdk,{recursive:true});
      await mkdir(join(root,"cua-driver"),{recursive:true});
      const files=[join(root,"cua-driver/cua-driver"),join(sdk,"libcua_driver_sdk.dylib"),join(sdk,"cua_driver_node_runtime.node")];
      for (const path of files) {await copyFile(fat,path);await chmod(path,0o755);}
      // Wrong architecture must fail before any other runtime file is changed.
      await copyFile(objects[arch==="arm64"?"x86_64":"arm64"]!,files[2]!);
      const before=await readFile(files[0]!);
      assert.throws(()=>thinMacComputerRuntime(app),/missing/);
      assert.deepEqual(await readFile(files[0]!),before);
      await copyFile(fat,files[2]!);
      await chmod(files[2]!,0o755);
      thinMacComputerRuntime(app);
      for (const path of files) {
        assert.equal(execFileSync("/usr/bin/lipo",["-archs",path],{encoding:"utf8"}).trim(),arch);
        assert.equal((await stat(path)).mode & 0o777,0o755);
        assert.ok((await stat(path)).size < (await stat(fat)).size);
      }
      const thinned=await readFile(files[0]!);
      thinMacComputerRuntime(app);
      assert.deepEqual(await readFile(files[0]!),thinned);
    }
  } finally {await rm(app,{recursive:true,force:true});}
});
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
test("a concurrent cache publisher is reused only if its files pass verification", { skip: process.platform === "win32" }, async () => {
  const originalPath = process.env.PATH ?? "";
  for (const corrupt of [false, true]) {
    const f = await fixture("darwin", "arm64");
    try {
      const winner = join(f.repo, "winner"), archive = join(f.repo, "driver.tar.gz");
      await rename(f.root, winner);
      const cached = JSON.parse(await readFile(join(winner, "runtime.json"), "utf8"));
      await rm(join(winner, "runtime.json"));
      execFileSync("tar", ["-czf", archive, "-C", winner, "."]);
      const sha256 = createHash("sha256").update(await readFile(archive)).digest("hex");
      const pin = { ...manifest, assets: { darwin: { name: "driver.tar.gz", sha256 } } };
      await writeFile(join(f.repo, "tools/cua-driver/runtime.json"), JSON.stringify(pin));
      await writeFile(join(winner, "runtime.json"), JSON.stringify({ ...cached, sha256 }));
      if (corrupt) await writeFile(join(winner, f.binary), "tampered");

      // Publish the other process's cache after the initial miss, before rename.
      const curl = join(f.repo, "curl");
      await writeFile(curl, `#!/bin/sh
set -eu
repo=$(dirname "$0")
while [ "$1" != --output ]; do shift; done
cp "$repo/driver.tar.gz" "$2"
mv "$repo/winner" "$repo/build/cua-driver/${sha256}"
`, { mode: 0o755 });
      process.env.PATH = `${f.repo}:${originalPath}`;
      const staged = stageComputerRuntime({ ...f, platform: "darwin", arch: "arm64" });
      if (corrupt) {
        await assert.rejects(staged, /Damaged Cua cache/);
      } else {
        await staged;
        assert.equal(await readFile(join(f.destination, "cua-driver", f.binary), "utf8"), f.binary);
      }
    } finally {
      process.env.PATH = originalPath;
      await rm(f.repo, { recursive: true, force: true });
    }
  }
});
test("packaging includes SDK JS and its exact resolver dependencies", async () => {
  const destination = await mkdtemp(join(tmpdir(), "aiclaw-cua-modules-"));
  try {
    await stageHostDependencies({
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


test("packaged diagnostics ZIP dependency loads outside the repository", async () => {
  const destination = await mkdtemp(join(tmpdir(), "aiclaw-host-dependency-test-"));
  try {
    await stageHostDependencies({
      repo: fileURLToPath(new URL("../", import.meta.url)),
      destination,
    });
    await writeFile(join(destination, "probe.mjs"), `
      import { zipSync, unzipSync, strToU8, strFromU8 } from 'fflate';
      const result = unzipSync(zipSync({'report.txt': strToU8('isolated diagnostics')}));
      if (strFromU8(result['report.txt']) !== 'isolated diagnostics') throw Error('ZIP round trip failed');
    `);
    execFileSync(process.execPath, [join(destination, "probe.mjs")], {
      cwd: destination,
      env: { ...process.env, NODE_PATH: "" },
      stdio: "pipe",
    });
  } finally {
    await rm(destination, { recursive: true, force: true });
  }
});
