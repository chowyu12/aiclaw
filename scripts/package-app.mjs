#!/usr/bin/env node
/**
 * 打出一个平台的安装包目录：
 *
 *   node scripts/package-app.mjs --platform=darwin --arch=arm64
 *   node scripts/package-app.mjs --platform=win32  --arch=x64
 *
 * 产物在 `release/<平台>-<架构>/`，由 CI 打成 zip 挂到 Release 上。
 *
 * 为什么先搭一个 staging 目录再交给 packager，而不是直接打 apps/desktop：
 * 这是 npm workspaces 仓库，`apps/desktop/node_modules/@aiclaw/agent-client`
 * 是一条指向 `packages/agent-client` 的软链，而整棵 node_modules 里还躺着
 * electron、vite、typescript 这些**只在构建时用**的几百兆依赖。直接打包要么
 * 把软链打断，要么把构建工具链一起塞进安装包。staging 目录里只放三样：
 * 编译产物、一个没有 dependencies 的 package.json、agent-client 的 dist。
 *
 * Go 二进制走 extraResource 放进 Resources/，主进程按 process.resourcesPath
 * 去找（session.ts 的 resolveBin）。
 */
import { execFileSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { packager } from "@electron/packager";

const REPO = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const STAGING = join(REPO, "build", "app");
const OUT = join(REPO, "release");

/** 用户看到的名字。可执行文件、.app 目录、Dock 上显示的都是它。 */
const APP_NAME = "内部平台";
const BUNDLE_ID = "com.example.aiclaw";

function arg(name, fallback) {
  const hit = process.argv.find((value) => value.startsWith(`--${name}=`));
  return hit ? hit.slice(name.length + 3) : fallback;
}

const platform = arg("platform", process.platform);
const arch = arg("arch", process.arch);
// 版本号优先取 tag：CI 里 Release 的版本必须与 tag 一致，而不是与某次忘了
// 提交的 package.json 一致。tag 形如 v0.2.0，去掉前缀的 v。
const version = (process.env.CI_COMMIT_TAG ?? "").replace(/^v/, "") || rootVersion();

/**
 * 给 exe / .app 用的版本号，只留数字段。
 *
 * `0.0.1-rc1` 这种预发布版本号在这两处会被拒：Windows 那边 rcedit 写的是
 * VERSIONINFO 资源，它**只收 x.y.z.w 的数字**；macOS 的 CFBundleVersion
 * 同理。完整的 tag 另外放进 buildVersion，信息不丢。
 *
 * 不做这一步的结果是第一次打预发布 tag 时流水线才失败，而那时 Release 已经
 * 建了一半。
 */
const numericVersion = (version.match(/^\d+(\.\d+)*/) ?? ["0.0.0"])[0];

function rootVersion() {
  return JSON.parse(readFileSync(join(REPO, "package.json"), "utf8")).version;
}

/**
 * 打包用的 Electron 版本，取**实际装上的那个**。
 *
 * 不让 packager 自己从 staging 的 package.json 里推：那份里故意没有
 * dependencies（见 stage() 的说明），推不出来。而写死一个版本号迟早会与
 * apps/desktop 装的那个对不上——本机跑的和打出来的是两个 Electron，
 * 这种偏差只会在用户手里暴露。
 */
function electronVersion() {
  const paths = [
    join(REPO, "node_modules", "electron", "package.json"),
    join(REPO, "apps", "desktop", "node_modules", "electron", "package.json"),
  ];
  for (const path of paths) {
    if (existsSync(path)) return JSON.parse(readFileSync(path, "utf8")).version;
  }
  throw new Error("找不到已安装的 electron，先跑 npm ci");
}

/** Go 二进制在这个平台上叫什么。 */
function binName(name) {
  return platform === "win32" ? `${name}.exe` : name;
}

function stage() {
  rmSync(STAGING, { recursive: true, force: true });
  mkdirSync(STAGING, { recursive: true });

  const desktopDist = join(REPO, "apps", "desktop", "dist");
  if (!existsSync(join(desktopDist, "main", "index.js"))) {
    throw new Error(`没有找到 ${desktopDist}/main/index.js，先跑 make build-desktop`);
  }
  cpSync(desktopDist, join(STAGING, "dist"), { recursive: true });

  // 图标同时也要进包内：运行期 app.dock.setIcon 读的是这个相对路径。
  const assets = join(REPO, "apps", "desktop", "assets");
  cpSync(assets, join(STAGING, "assets"), { recursive: true });

  // agent-client 拍平成一个真实目录，不留软链。
  const client = join(STAGING, "node_modules", "@aiclaw", "agent-client");
  mkdirSync(client, { recursive: true });
  cpSync(join(REPO, "packages", "agent-client", "dist"), join(client, "dist"), {
    recursive: true,
  });
  cpSync(
    join(REPO, "packages", "agent-client", "package.json"),
    join(client, "package.json"),
  );

  writeFileSync(
    join(STAGING, "package.json"),
    `${JSON.stringify(
      {
        name: "aiclaw",
        productName: APP_NAME,
        version,
        private: true,
        type: "module",
        main: "dist/main/index.js",
        // Windows 打包必须有 author：packager 拿它填 exe 的 CompanyName，
        // 缺了会在推断阶段直接失败（与图标那一步无关，先于它发生）。
        author: "示例公司",
        // 没有 dependencies：运行期要的东西都已经在 dist 与上面那份 agent-client 里。
        // 留着 dependencies 会让 packager 去装一遍构建期依赖。
      },
      null,
      2,
    )}\n`,
    "utf8",
  );
}

function goBinaries() {
  const paths = [];
  for (const name of ["claw-agent", "claw-mcp"]) {
    // CI 里交叉编译的产物放在 build/bin/<平台>-<架构>/；本机打包时退回
    // tools/<name>/ 下那个（只在同平台同架构时才对，脚本会核对）。
    const fromCI = join(REPO, "build", "bin", `${platform}-${arch}`, binName(name));
    const local = join(REPO, "tools", name, binName(name));
    const found = existsSync(fromCI) ? fromCI : local;
    if (!existsSync(found)) {
      throw new Error(
        `缺少 ${binName(name)}。交叉编译：` +
          `GOOS=${goos()} GOARCH=${goarch()} CGO_ENABLED=0 go build -o ${fromCI} ./tools/${name}`,
      );
    }
    if (found === local && (platform !== process.platform || arch !== process.arch)) {
      throw new Error(
        `要打 ${platform}-${arch} 的包，但只找到本机（${process.platform}-${process.arch}）` +
          `编译的 ${name}。先交叉编译到 build/bin/${platform}-${arch}/。`,
      );
    }
    paths.push(found);
  }
  return paths;
}

function goos() {
  return platform === "win32" ? "windows" : platform;
}

function goarch() {
  return arch === "x64" ? "amd64" : arch;
}

/**
 * Windows 的 exe 图标与版本信息**不走 packager**。
 *
 * packager 那条路用 rcedit，而 rcedit 是个 Windows 程序，在 Linux / macOS 上
 * 要靠 wine 跑。CI 里装 wine 意味着从 debian-security 拉十几个包，而那个源
 * 在这里是坏的（bullseye 在归档，.deb 全是 404）。为一张图标背上十几个会
 * 404 的依赖不划算。
 *
 * 改成打完包之后用 resedit（纯 JS 的 PE 资源编辑器）直接改 exe，
 * 不需要任何外部程序。electron-builder 也是出于同样的理由从 rcedit 换到它的。
 */
function stampWindowsExe() {
  const exe = join(OUT, `${APP_NAME}-${platform}-${arch}`, `${APP_NAME}.exe`);
  if (!existsSync(exe)) throw new Error(`打包产物里没有 ${exe}`);
  const ico = join(REPO, "apps", "desktop", "assets", "icon.ico");
  execFileSync(
    process.execPath,
    [join(REPO, "scripts", "stamp-exe.mjs"), exe, ico, numericVersion, version],
    { stdio: "inherit" },
  );
}

async function main() {
  stage();

  const icon = join(REPO, "apps", "desktop", "assets", "icon.icns");
  if (platform === "darwin" && !existsSync(icon)) {
    throw new Error(`缺少图标 ${icon}，先跑 make icon`);
  }

  rmSync(join(OUT, `${platform}-${arch}`), { recursive: true, force: true });

  const paths = await packager({
    dir: STAGING,
    out: OUT,
    electronVersion: electronVersion(),
    platform,
    arch,
    name: APP_NAME,
    appVersion: numericVersion,
    // 完整的 tag（含 -rc1 这类后缀）放这里：macOS 的 CFBundleVersion、
    // Windows 的 ProductVersion 都会带上，装好之后还查得出是哪个构建。
    buildVersion: version,
    appBundleId: BUNDLE_ID,
    // 只给 macOS。Windows 的图标在下面由 resedit 写，交给 packager 会触发
    // rcedit，那条路要 wine。
    icon: platform === "darwin" ? icon : undefined,
    overwrite: true,
    // 不做 prune。packager 默认会把 node_modules 里没出现在 dependencies 中的
    // 包全删掉，而 staging 的 package.json 故意没有 dependencies——结果是我们
    // 刚放进去的 @aiclaw/agent-client 被剪掉，打出来的包一启动就退出、
    // **什么都不打印**（ESM 解析失败发生在主脚本加载前）。
    // staging 里只有我们自己放的那一个包，本来也没有要剪的东西。
    prune: false,
    // 不打 asar：主进程要按 process.resourcesPath 找并**执行** Go 二进制，
    // 而 extraResource 本来就在 asar 外面；关掉 asar 让排障时能直接看目录。
    // 代价是文件多一些，对内部分发无所谓。
    asar: false,
    extraResource: goBinaries(),
    appCopyright: "示例公司",
    // macOS 这边**没有签名**：签名与公证都只能在 macOS 上做，而这条流水线
    // 跑在 Linux。用户第一次打开要右键「打开」，或者
    // `xattr -dr com.apple.quarantine /Applications/内部平台.app`。
    // 以后有 macOS runner 了，在这里加 osxSign / osxNotarize。
    darwinDarkModeSupport: true,
    // win32metadata 同理：给了它 packager 就会去调 rcedit。
  });

  if (platform === "win32") stampWindowsExe();

  for (const path of paths) console.log(`packaged ${path}`);
}

await main();
