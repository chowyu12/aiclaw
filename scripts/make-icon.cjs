// 从品牌标识生成应用图标 PNG：electron scripts/make-icon.cjs
//
// 为什么用 Electron 渲染而不是手写 PNG 编码器：图标要和界面左上角那个标识
// 一模一样，而那个标识是 SVG。让浏览器去光栅化，换标识时重跑一次就行，
// 不用担心手写的近似版本和真身慢慢分叉。
//
// 输出三个文件，都在 apps/desktop/assets 下：
//   icon.png   1024×1024，运行期 dock.setIcon 与 Linux 用
//   icon.icns  macOS 打包用（@electron/packager 只认这个格式）
//   icon.ico   Windows 打包用
// 后两个是容器格式，装的就是若干张不同尺寸的 PNG，所以在这里一并生成，
// 不必依赖 iconutil——那是 macOS 独有的，而 CI 在 Linux 上跑。
// 产物要提交进仓库：应用启动时直接读它，不能依赖构建机跑过这个脚本。
const { app, BrowserWindow } = require("electron");
const path = require("path");
const fs = require("fs");

const SIZE = 1024;
// 放 assets/ 而不是 build/：.gitignore 忽略 build/，产物在那里根本提交不进去，
// 而新克隆出来的仓库没有图标——dock 里就又是那个原子。
const ASSETS = path.join(__dirname, "..", "apps", "desktop", "assets");
const OUT = path.join(ASSETS, "icon.png");

// 与 renderer/views/BrandLogo.vue 同一份形状。改标识时两边一起改。
//
// **图案不铺满画布。** macOS 的图标网格里，1024 的画布上圆角方块只占
// 824×824（四周各留 100），下面再留一点投影的位置。铺满画布的图标在 Dock 里
// 会比左右邻居明显大一圈——第一版就是这样，看起来不是「更醒目」，是「没做对」。
// Windows 与 Linux 那边留白也不吃亏，那两处同样不指望图标顶到边。
const BODY = 824; // 圆角方块的边长
const INSET = (SIZE - BODY) / 2; // = 100
const RADIUS = 185.4; // Apple 的圆角半径，约 0.225 × 边长
// 投影往下偏一点：光从上面来，这是 macOS 上所有图标的共同约定。
const SHADOW_DY = 10;
const SHADOW_BLUR = 22;

const SVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${SIZE} ${SIZE}" width="${SIZE}" height="${SIZE}">
  <defs>
    <linearGradient id="g" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" stop-color="#0052ff"/><stop offset="100%" stop-color="#578bfa"/>
    </linearGradient>
    <linearGradient id="s" x1="0%" y1="0%" x2="0%" y2="100%">
      <stop offset="0%" stop-color="#ffffff" stop-opacity="0.35"/>
      <stop offset="55%" stop-color="#ffffff" stop-opacity="0"/>
    </linearGradient>
    <filter id="shadow" x="-20%" y="-20%" width="140%" height="140%">
      <feDropShadow dx="0" dy="${SHADOW_DY}" stdDeviation="${SHADOW_BLUR}"
                    flood-color="#0b2a6b" flood-opacity="0.28"/>
    </filter>
  </defs>

  <g filter="url(#shadow)">
    <rect x="${INSET}" y="${INSET}" width="${BODY}" height="${BODY}" rx="${RADIUS}" fill="url(#g)"/>
  </g>
  <rect x="${INSET}" y="${INSET}" width="${BODY}" height="${BODY}" rx="${RADIUS}" fill="url(#s)"/>

  <!-- 星形按 32 格坐标画的，整体缩放到圆角方块里。 -->
  <g transform="translate(${INSET} ${INSET}) scale(${BODY / 32})">
    <path d="M16 6.5 C 16.6 11.4 18.6 13.4 23.5 14 C 18.6 14.6 16.6 16.6 16 21.5
             C 15.4 16.6 13.4 14.6 8.5 14 C 13.4 13.4 15.4 11.4 16 6.5 Z" fill="#ffffff"/>
    <path d="M23.4 20.6 C 23.55 22 24.05 22.5 25.45 22.65 C 24.05 22.8 23.55 23.3 23.4 24.7
             C 23.25 23.3 22.75 22.8 21.35 22.65 C 22.75 22.5 23.25 22 23.4 20.6 Z"
          fill="#ffffff" opacity="0.85"/>
  </g>
</svg>`;

const PAGE = `<!doctype html><meta charset="utf-8">
<style>html,body{margin:0;padding:0;background:transparent}
svg{display:block;width:${SIZE}px;height:${SIZE}px}</style>${SVG}`;

app.disableHardwareAcceleration();

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    width: SIZE,
    height: SIZE,
    show: false,
    // 透明底：图标四角之外必须是透明的，否则 dock 上是一个白方块。
    transparent: true,
    frame: false,
    webPreferences: { offscreen: true },
  });
  await win.loadURL("data:text/html;charset=utf-8," + encodeURIComponent(PAGE));
  // 等一帧，否则截到的是还没绘制的空白页。
  await new Promise((resolve) => setTimeout(resolve, 400));
  const image = await win.webContents.capturePage();
  fs.mkdirSync(ASSETS, { recursive: true });
  fs.writeFileSync(OUT, image.toPNG());

  // 各尺寸切一份，两个容器格式共用。缩放交给 Electron，比自己插值稳。
  const png = (size) => image.resize({ width: size, height: size, quality: "best" }).toPNG();
  // 小尺寸那两段要的是原始像素（见 buildIcns 里 ic04/ic05 的说明）。
  const bitmap = (size) => {
    const scaled = image.resize({ width: size, height: size, quality: "best" });
    return { size, data: scaled.toBitmap() };
  };

  const icns = path.join(ASSETS, "icon.icns");
  fs.writeFileSync(icns, buildIcns(png, bitmap));
  const ico = path.join(ASSETS, "icon.ico");
  fs.writeFileSync(ico, buildIco(png));

  const { width, height } = image.getSize();
  console.log(`source ${width}x${height}`);
  for (const file of [OUT, icns, ico]) {
    console.log(`wrote ${file} ${fs.statSync(file).size} bytes`);
  }
  app.exit(0);
});

/**
 * 组一个 .icns。
 *
 * 格式很简单：`icns` 魔数 + 总长度，后面一串「OSType + 段长 + 数据」，
 * 段长**含那 8 字节头**。大尺寸那几段直接塞 PNG。
 *
 * **16 与 32 必须用 ic04 / ic05，而它们只收 ARGB。** 早先这里图省事用了
 * icp4/icp5（能塞 PNG），结果访达列表视图里显示的不是我们的图标——那个
 * 视图用的正是 16/32 这两档。对照苹果自己的 iconutil 产出才看出来：
 * 它写的是 ic04/ic05，压根没有 icp*。这类问题不会报错，只会「图标不对」，
 * 而大图（dock、访达图标视图）一直是好的，所以很容易以为是缓存。
 */
function buildIcns(png, bitmap) {
  const types = [
    ["ic07", 128], ["ic08", 256], ["ic09", 512], ["ic10", 1024],
    ["ic11", 32], ["ic12", 64], ["ic13", 256], ["ic14", 512],
  ];
  const chunks = [];
  // 小尺寸走 ARGB，顺序与苹果的产出一致（小的在前）。
  for (const [osType, size] of [["ic04", 16], ["ic05", 32]]) {
    const data = encodeArgb(bitmap(size));
    const header = Buffer.alloc(8);
    header.write(osType, 0, 4, "ascii");
    header.writeUInt32BE(data.length + 8, 4);
    chunks.push(header, data);
  }
  for (const [osType, size] of types) {
    const data = png(size);
    const header = Buffer.alloc(8);
    header.write(osType, 0, 4, "ascii");
    header.writeUInt32BE(data.length + 8, 4);
    chunks.push(header, data);
  }
  const body = Buffer.concat(chunks);
  const head = Buffer.alloc(8);
  head.write("icns", 0, 4, "ascii");
  head.writeUInt32BE(body.length + 8, 4);
  return Buffer.concat([head, body]);
}

/**
 * 把像素编成 icns 的 ARGB 段：`ARGB` 魔数 + 四个通道各自 PackBits 压缩。
 *
 * Electron 给的 toBitmap() 是 BGRA 顺序（macOS 上），这里要按 A、R、G、B
 * 四个平面分别压——不是逐像素交错。顺序搞错不会报错，只会得到一张
 * 颜色错乱的图标。
 */
function encodeArgb({ size, data }) {
  const count = size * size;
  const planes = [[], [], [], []]; // A, R, G, B
  for (let i = 0; i < count; i++) {
    const offset = i * 4;
    planes[1].push(data[offset + 2]); // R
    planes[2].push(data[offset + 1]); // G
    planes[3].push(data[offset]); // B
    planes[0].push(data[offset + 3]); // A
  }
  return Buffer.concat([
    Buffer.from("ARGB", "ascii"),
    ...planes.map((plane) => packBits(Buffer.from(plane))),
  ]);
}

/**
 * PackBits（苹果那一版）。
 *
 * 规则：0x00–0x7F 表示后面跟 n+1 个原样字节；0x80–0xFF 表示把下一个字节
 * 重复 (n - 125) 次。重复段最少 3 个字节才划算，短于 3 的并进原样段。
 */
function packBits(input) {
  const out = [];
  let index = 0;
  while (index < input.length) {
    let run = 1;
    while (
      index + run < input.length &&
      run < 130 &&
      input[index + run] === input[index]
    ) {
      run++;
    }
    if (run >= 3) {
      out.push(run + 125, input[index]);
      index += run;
      continue;
    }
    // 原样段：一直收到下一个「连着 3 个相同」为止。
    const start = index;
    index++;
    while (index < input.length && index - start < 128) {
      if (
        index + 2 < input.length &&
        input[index] === input[index + 1] &&
        input[index] === input[index + 2]
      ) {
        break;
      }
      index++;
    }
    const literal = input.subarray(start, index);
    out.push(literal.length - 1, ...literal);
  }
  return Buffer.from(out);
}

/**
 * 组一个 .ico。
 *
 * 目录项里宽高各只占一个字节，**256 要写成 0**——这是格式规定的编码，
 * 写别的值 Windows 会认为目录项坏了，然后整张图都不显示。Vista 之后每一项
 * 可以直接是 PNG，不必转 BMP。
 */
function buildIco(png) {
  const sizes = [16, 24, 32, 48, 64, 128, 256];
  const images = sizes.map((size) => png(size));

  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0); // reserved
  header.writeUInt16LE(1, 2); // 1 = 图标
  header.writeUInt16LE(images.length, 4);

  const entries = Buffer.alloc(16 * images.length);
  let offset = header.length + entries.length;
  images.forEach((data, index) => {
    const at = index * 16;
    const side = sizes[index] >= 256 ? 0 : sizes[index];
    entries.writeUInt8(side, at);
    entries.writeUInt8(side, at + 1);
    entries.writeUInt8(0, at + 2); // 调色板数，真彩为 0
    entries.writeUInt8(0, at + 3); // reserved
    entries.writeUInt16LE(1, at + 4); // planes
    entries.writeUInt16LE(32, at + 6); // 位深
    entries.writeUInt32LE(data.length, at + 8);
    entries.writeUInt32LE(offset, at + 12);
    offset += data.length;
  });

  return Buffer.concat([header, entries, ...images]);
}
