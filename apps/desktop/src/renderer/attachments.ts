/**
 * 往对话里贴东西：截图、图片文件、音频、文本文件。
 *
 * 分两半写是为了能测：**判断**（这个文件要怎么处理、能不能收）是纯函数，
 * 在 node 里跑得起来；**转换**（解码、缩放、读文件）要 DOM，只能在渲染层跑。
 * 会出错的恰恰是判断那一半——收下一个 200MB 的视频、或者把二进制当文本
 * 塞进提示词，都是判断错了。
 */

/** 一张要随消息发出去的图片。data 是 base64，不带 `data:` 前缀。 */
export interface ImageAttachment {
  kind: "image";
  name: string;
  /** base64 的 PNG/JPEG 字节。 */
  data: string;
  /** 界面上显示缩略图用的完整 data URL。 */
  preview: string;
}

/** 一个以正文形式并进消息的文本文件。 */
export interface TextAttachment {
  kind: "text";
  name: string;
  text: string;
  /** 内容被截断了。截断要让用户看见，不然他以为模型读到了全文。 */
  truncated: boolean;
}

/**
 * 一段随消息发出去的音频。
 *
 * 模型读不了音频：发送时先交给主进程落盘，再把路径给内核，由听写模型转成
 * 文字并进消息。所以这里留的是原始字节（base64），不做任何转换——转码要
 * 解码器，而浏览器里没有一个能覆盖 m4a/opus/flac 的。
 */
export interface AudioAttachment {
  kind: "audio";
  name: string;
  /** base64 的原始音频字节。 */
  data: string;
  /** 字节数，界面上显示大小用。 */
  size: number;
}

export type Attachment = ImageAttachment | TextAttachment | AudioAttachment;

/**
 * 模型一次最多看几张图。与内核里的上限一致（`limitImages`），
 * 在这边先挡住是为了能给出一句人话，而不是让内核默默丢掉。
 */
export const MAX_IMAGES = 4;

/** 图片解码前的大小上限。缩放之后一般只有几百 KB，这里挡的是原始文件。 */
export const MAX_IMAGE_BYTES = 20 * 1024 * 1024;

/** 文本文件并进正文的字节上限。超了截断并注明。 */
export const MAX_TEXT_BYTES = 128 * 1024;

/** 音频大小上限。与内核转写的上限一致（多数服务也卡在 25MB）。 */
export const MAX_AUDIO_BYTES = 25 * 1024 * 1024;

/**
 * 一条消息最多附几段音频。
 *
 * 每一段都要打一次听写模型，四段就是四次网络往返卡在发送那一下；而真要处理
 * 一堆录音，把它们放进工作区让模型逐个调 transcribe_audio 是更好的做法。
 */
export const MAX_AUDIO = 2;

const AUDIO_EXTENSIONS = new Set(["mp3", "m4a", "mp4", "wav", "ogg", "opus", "webm", "flac", "aac"]);

/** 缩放后图片的最长边。再大对模型的识别没有帮助，只是更贵。 */
export const MAX_IMAGE_EDGE = 1568;

const TEXT_EXTENSIONS = new Set([
  "txt", "md", "markdown", "json", "jsonl", "yaml", "yml", "toml", "ini", "conf", "env",
  "csv", "tsv", "log", "sql", "sh", "bash", "zsh", "py", "go", "rs", "java", "kt", "c", "h",
  "cc", "cpp", "hpp", "cs", "rb", "php", "swift", "ts", "tsx", "js", "jsx", "mjs", "cjs",
  "vue", "css", "scss", "less", "html", "htm", "xml", "svg", "gradle", "properties", "diff",
  "patch", "gitignore", "dockerfile", "makefile",
]);

export type Verdict =
  | { accept: "image" }
  | { accept: "text" }
  | { accept: "audio" }
  | { accept: "no"; reason: string };

/**
 * 判断一个拖进来/选中的文件怎么处理。
 *
 * 不认识的一律拒绝并说清楚为什么：默默忽略的话，用户会以为模型看过了那份
 * PDF，然后基于一个它根本没读到的东西讨论下去。
 */
export function classifyFile(
  file: { name: string; type: string; size: number },
  imagesAlready: number,
  audioAlready = 0,
): Verdict {
  const type = file.type.toLowerCase();
  const extension = (file.name.split(".").pop() ?? "").toLowerCase();

  // 音频排在图片前面判：webm 既可能是视频也可能是音频，而 type 常常是空的
  //（拖进来的文件在某些系统上没有 MIME），所以以扩展名为准。
  if (AUDIO_EXTENSIONS.has(extension) || (type.startsWith("audio/") && extension !== "svg")) {
    if (audioAlready >= MAX_AUDIO) {
      return { accept: "no", reason: `一条消息最多 ${MAX_AUDIO} 段音频` };
    }
    if (file.size > MAX_AUDIO_BYTES) {
      return { accept: "no", reason: "音频太大（超过 25MB）" };
    }
    return { accept: "audio" };
  }

  if (type.startsWith("image/")) {
    if (type === "image/svg+xml") {
      // SVG 是可执行文档（脚本、外链），而且模型看的是位图。当文本收。
      return { accept: "text" };
    }
    if (imagesAlready >= MAX_IMAGES) {
      return { accept: "no", reason: `一条消息最多 ${MAX_IMAGES} 张图` };
    }
    if (file.size > MAX_IMAGE_BYTES) {
      return { accept: "no", reason: "图片太大（超过 20MB）" };
    }
    return { accept: "image" };
  }

  if (type.startsWith("text/") || TEXT_EXTENSIONS.has(extension)) {
    return { accept: "text" };
  }

  return {
    accept: "no",
    reason: `不支持这种文件（${file.name}）。把它放进工作目录，然后让我去读它。`,
  };
}

/** 文本文件并进消息正文时的样子。围栏里注明文件名，模型才知道这是什么。 */
export function inlineText(attachment: TextAttachment): string {
  const note = attachment.truncated ? `（只贴了前 ${MAX_TEXT_BYTES / 1024}KB）` : "";
  return `\n\n附件 ${attachment.name}${note}：\n\`\`\`\n${attachment.text}\n\`\`\``;
}

/** 按上限截断文本，并说出有没有截断。 */
export function clampText(raw: string): { text: string; truncated: boolean } {
  // 按字符数近似字节数：中文一个字三字节，这里宁可早截也不要晚截。
  const limit = MAX_TEXT_BYTES;
  if (new TextEncoder().encode(raw).length <= limit) return { text: raw, truncated: false };
  let text = raw.slice(0, limit);
  while (new TextEncoder().encode(text).length > limit) text = text.slice(0, -1024);
  return { text, truncated: true };
}

/** 缩放后的目标尺寸。长边不超过 MAX_IMAGE_EDGE，小图不放大。 */
export function fitSize(width: number, height: number): { width: number; height: number } {
  const longest = Math.max(width, height);
  if (longest <= MAX_IMAGE_EDGE || longest === 0) return { width, height };
  const scale = MAX_IMAGE_EDGE / longest;
  return { width: Math.round(width * scale), height: Math.round(height * scale) };
}

// ---------- 下面这半要 DOM，只在渲染层跑 ----------

/**
 * 把一个图片文件解码、缩放、编码成 JPEG。
 *
 * 为什么一定要缩：贴一张 4K 截图原样发过去是几 MB，而它每一轮都会**重发一遍**
 * （图片在消息历史里），还要原样写进会话库。长边 1568 是识别精度与体积的
 * 折中——再大对模型看清楚没有帮助。
 *
 * 用 JPEG 而不是 PNG：截图里大片纯色 PNG 更小，但照片和带渐变的界面 PNG 会
 * 大几倍，而这里收的两种都有。0.82 的质量下肉眼看不出差别。
 */
export async function readImage(file: File): Promise<ImageAttachment> {
  const bitmap = await createImageBitmap(file);
  const { width, height } = fitSize(bitmap.width, bitmap.height);
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d");
  if (!context) throw new Error("浏览器没给出 2d 画布，无法处理图片");
  context.drawImage(bitmap, 0, 0, width, height);
  bitmap.close();

  const preview = canvas.toDataURL("image/jpeg", 0.82);
  return {
    kind: "image",
    name: file.name || "截图.jpg",
    data: preview.slice(preview.indexOf(",") + 1),
    preview,
  };
}

/** 读一段音频的原始字节。不转码：浏览器里没有覆盖 m4a/opus/flac 的解码器。 */
export async function readAudio(file: File): Promise<AudioAttachment> {
  const buffer = new Uint8Array(await file.arrayBuffer());
  let binary = "";
  // 分块拼：一次 apply 几 MB 会爆调用栈。
  for (let index = 0; index < buffer.length; index += 8192) {
    binary += String.fromCharCode(...buffer.subarray(index, index + 8192));
  }
  return { kind: "audio", name: file.name || "录音", data: btoa(binary), size: buffer.length };
}

/** 读一个文本文件，按上限截断。 */
export async function readTextFile(file: File): Promise<TextAttachment> {
  const { text, truncated } = clampText(await file.text());
  return { kind: "text", name: file.name, text, truncated };
}
