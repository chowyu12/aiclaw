import { mkdtempSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { extname, isAbsolute, join, resolve } from "node:path";

/**
 * 界面要显示的两类文件：用户发出去的音频、模型生成的图与音频。
 *
 * 两件事都涉及路径，而路径的两头都不完全可信：**产出物的路径来自内核转发的
 * 模型输出**（模型读得到文件、命令结果、MCP 数据，那些都可能被注入），
 * 所以读之前要按与 open-file 相同的规则判一遍——那边判的是「能不能执行它」，
 * 这边判的是「能不能把它的字节交给渲染层」。渲染层拿到 data URL 会直接塞进
 * <img>/<audio>，所以只放行确定是图或音频的扩展名，其余一律拒绝。
 */

/** 渲染层能内联显示的图片。 */
const IMAGE_EXTENSIONS = new Map([
  [".png", "image/png"],
  [".jpg", "image/jpeg"],
  [".jpeg", "image/jpeg"],
  [".webp", "image/webp"],
  [".gif", "image/gif"],
]);

/** 渲染层能播放的音频。与 <audio> 在 Chromium 上支持的格式一致。 */
const AUDIO_EXTENSIONS = new Map([
  [".mp3", "audio/mpeg"],
  [".m4a", "audio/mp4"],
  [".mp4", "audio/mp4"],
  [".wav", "audio/wav"],
  [".ogg", "audio/ogg"],
  [".opus", "audio/ogg"],
  [".webm", "audio/webm"],
  [".flac", "audio/flac"],
  [".aac", "audio/aac"],
]);

/**
 * 内联显示的大小上限。
 *
 * data URL 整份进渲染进程的内存，一张 20MB 的图会让那一帧卡住；而这里要显示
 * 的是生成的图（几百 KB）与一段语音（几 MB），超过这个数基本不是我们生成的东西。
 */
const MAX_INLINE_BYTES = 24 * 1024 * 1024;

/** 音频文件的大小上限，与内核里转写的上限一致。 */
export const MAX_AUDIO_BYTES = 25 * 1024 * 1024;

export interface MediaFile {
  /** `data:<type>;base64,…`，可以直接给 <img src> 或 <audio src>。 */
  dataUrl: string;
  kind: "image" | "audio";
}

/**
 * 读一个要内联显示的文件。
 *
 * base 是会话工作区（没设就是主目录）——产出物的路径是相对它给的。
 */
export function readMedia(
  raw: string,
  options: { base: string; protectedPaths: string[] },
): MediaFile {
  const text = raw.trim();
  if (!text) throw new Error("路径是空的");

  const home = homedir();
  const expanded = text === "~" || text.startsWith("~/") ? join(home, text.slice(1)) : text;
  const base = options.base.trim() || home;
  const path = isAbsolute(expanded) ? resolve(expanded) : resolve(base, expanded);

  // 凭据目录一律拒绝，与工具那边同一条线：读到之后可以顺着任何一个对外的
  // 通道送出去，而「显示一张图」没有任何理由碰那里。
  for (const guarded of options.protectedPaths) {
    const root = resolve(guarded);
    if (path === root || path.startsWith(root + "/")) {
      throw new Error("这个目录里的文件不能显示");
    }
  }

  const extension = extname(path).toLowerCase();
  const image = IMAGE_EXTENSIONS.get(extension);
  const audio = AUDIO_EXTENSIONS.get(extension);
  if (!image && !audio) {
    // 说清楚而不是静默返回空：界面上一个不显示的图和一个加载失败的图，
    // 用户看到的是同一件事，但原因完全不同。
    throw new Error(`不支持内联显示 ${extension || "这种文件"}`);
  }

  const info = statSync(path);
  if (!info.isFile()) throw new Error("这不是一个文件");
  if (info.size > MAX_INLINE_BYTES) {
    throw new Error(`文件太大（${(info.size / (1 << 20)).toFixed(1)} MB），在访达里打开看吧`);
  }

  const type = image ?? audio!;
  return {
    kind: image ? "image" : "audio",
    dataUrl: `data:${type};base64,${readFileSync(path).toString("base64")}`,
  };
}

/**
 * 把用户贴进来的音频落到磁盘，返回路径。
 *
 * 为什么不直接把字节塞进 turn/start：内核的行协议单帧上限是 16MB，一段几分钟
 * 的录音 base64 之后就超了，而超了的表现是「协议帧解析失败」——没人能从那句
 * 话联想到是附件太大。落盘之后协议里只有一个路径。
 *
 * 落在系统临时目录：这些文件转写完就没用了，不该混进用户的工作区。
 */
export function stageAudio(name: string, base64: string): string {
  const data = Buffer.from(base64, "base64");
  if (data.length === 0) throw new Error("音频是空的");
  if (data.length > MAX_AUDIO_BYTES) {
    throw new Error(`音频太大（${(data.length / (1 << 20)).toFixed(1)} MB，上限 25 MB）`);
  }
  const extension = extname(name).toLowerCase();
  if (!AUDIO_EXTENSIONS.has(extension)) {
    throw new Error(`不支持这种音频格式（${extension || name}）`);
  }
  // 一次一个目录：同名文件不会互相覆盖，而用文件名加时间戳仍然可能撞上。
  const directory = mkdtempSync(join(tmpdir(), "aiclaw-audio-"));
  const path = join(directory, sanitize(name) || `audio${extension}`);
  writeFileSync(path, data);
  return path;
}

/** 文件名只留安全字符，顺带挡掉 `..` 与分隔符。 */
function sanitize(raw: string): string {
  return raw
    .trim()
    .replace(/[/\\]/g, "-")
    .replace(/^\.+/, "")
    .slice(0, 80);
}

/** 这个文件名看起来是不是音频。渲染层判断附件类型时也用它。 */
export function isAudioName(name: string): boolean {
  return AUDIO_EXTENSIONS.has(extname(name).toLowerCase());
}
