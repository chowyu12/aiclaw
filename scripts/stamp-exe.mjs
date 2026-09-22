#!/usr/bin/env node
/**
 * 给 Windows 的 exe 写图标与版本信息：
 *
 *   node scripts/stamp-exe.mjs <exe 路径> <icon.ico 路径> <版本号> [完整版本号]
 *
 * 为什么不交给 @electron/packager 自带的那条路：它用 rcedit，而 rcedit 是个
 * Windows 程序，在 Linux / macOS 上要靠 wine 跑。CI 里装 wine 意味着从
 * debian-security 拉十几个包（libasound2、libsdl2、gstreamer……），而那个源
 * 在这里是坏的——bullseye 已经在归档，镜像上那些 .deb 全是 404。为了一张图标
 * 背上十几个会 404 的依赖不划算。
 *
 * resedit 是纯 JS 的 PE 资源编辑器，不需要任何外部程序。electron-builder
 * 也是出于同样的理由从 rcedit 换到它的。
 */
import { readFileSync, writeFileSync } from "node:fs";
import { Data, NtExecutable, NtExecutableResource, Resource } from "resedit";

/** en-US + UTF-16。资源表的语言标记，不影响显示的文字。 */
const LANG = { lang: 1033, codepage: 1200 };

const [exePath, iconPath, version, fullVersion] = process.argv.slice(2);
if (!exePath || !iconPath || !version) {
  console.error("用法：node scripts/stamp-exe.mjs <exe> <ico> <版本号> [完整版本号]");
  process.exit(1);
}

/** VERSIONINFO 的版本号是四段 16 位整数，多退少补。 */
function quad(text) {
  const parts = String(text)
    .split(".")
    .map((part) => Number.parseInt(part, 10))
    .map((n) => (Number.isFinite(n) && n >= 0 ? Math.min(n, 65535) : 0));
  while (parts.length < 4) parts.push(0);
  return parts.slice(0, 4);
}

const exe = NtExecutable.from(readFileSync(exePath));
const res = NtExecutableResource.from(exe);

// 图标。ico 里每个尺寸都塞进去，Windows 按场景挑（任务栏用小的，
// 桌面用大的）——只放一个尺寸的话，缩放出来的边缘会糊。
const icon = Data.IconFile.from(readFileSync(iconPath));
Resource.IconGroupEntry.replaceIconsForResource(
  res.entries,
  1, // 资源 id，exe 默认用第一个图标组
  LANG.lang,
  icon.icons.map((item) => item.data),
);

// 从 exe 里已有的版本信息接着改，而不是新造一份。
//
// Electron 的 exe 自带一整套字段（CompanyName 是 "GitHub, Inc."、
// InternalName 是 "electron"……）。造一份新的塞进去，那些旧字段仍会留在资源
// 表里——第一版就是这样，改完之后 exe 属性里公司名还写着 GitHub。
// 所以先把现有的语言与字符串**全部清掉**，再按我们的写一遍。
const info = Resource.VersionInfo.fromEntries(res.entries)[0] ?? Resource.VersionInfo.createEmpty();
for (const lang of info.getAvailableLanguages()) info.removeAllStringValues(lang);
info.replaceAvailableLanguages([LANG]);
// 先数字后字符串：setFileVersion / setProductVersion 会顺手写同名字符串，
// 我们要的带后缀的版本号得排在它们后面才不会被盖掉。
info.setFileVersion(...quad(version), LANG.lang);
info.setProductVersion(...quad(version), LANG.lang);
info.setStringValues(LANG, {
  CompanyName: "示例公司",
  FileDescription: "内部平台",
  ProductName: "内部平台",
  OriginalFilename: "内部平台.exe",
  LegalCopyright: "示例公司",
  // 完整 tag（可能带 -rc1 这种后缀）只能放字符串字段：上面那两个数字版本
  // 只收整数，预发布后缀在那里表达不了。
  FileVersion: fullVersion || version,
  ProductVersion: fullVersion || version,
});
info.outputToResourceEntries(res.entries);

res.outputResource(exe);
writeFileSync(exePath, Buffer.from(exe.generate()));
console.log(`stamped ${exePath}: icon ${icon.icons.length} 个尺寸, 版本 ${fullVersion || version}`);
