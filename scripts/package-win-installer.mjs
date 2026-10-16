#!/usr/bin/env node
// Reuse the directory produced by package-app.mjs; do not rebundle the application.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { build, Platform, Arch } from "electron-builder";

const prepackaged = resolve("release/AIClaw-win32-x64");
const appDirectory = resolve(prepackaged, "resources/app");
const { version } = JSON.parse(readFileSync(resolve(appDirectory, "package.json"), "utf8"));
if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) throw new Error("Invalid packaged version");

await build({
  targets: Platform.WINDOWS.createTarget("nsis", Arch.x64),
  prepackaged,
  publish: "never",
  config: {
    appId: "com.github.chowyu12.aiclaw",
    productName: "AIClaw",
    directories: { app: appDirectory, output: "dist-release", buildResources: "apps/desktop/assets" },
    extraMetadata: { version },
    electronVersion: JSON.parse(readFileSync("node_modules/electron/package.json", "utf8")).version,
    publish: null,
    win: { icon: "apps/desktop/assets/icon.ico", signAndEditExecutable: false },
    nsis: {
      artifactName: `AIClaw-v${version}-win-x64-setup.exe`,
      oneClick: false,
      perMachine: false,
      allowElevation: false,
      allowToChangeInstallationDirectory: false,
      deleteAppDataOnUninstall: false,
      differentialPackage: false,
    },
  },
});
