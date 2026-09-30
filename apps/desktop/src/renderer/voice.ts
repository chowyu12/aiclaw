/**
 * 语音输入：录一段音，转成 16kHz 单声道 WAV，交给听写模型。
 *
 * 为什么不直接把 MediaRecorder 录出来的 webm 发过去：各家听写接口认的格式不一样，
 * 有的不收 webm/opus。WAV（PCM16）谁都认，16kHz 单声道一分钟不到 2MB，
 * 远在接口的 25MB 上限之下。转码在这里用 Web Audio 做，不用带 ffmpeg。
 *
 * 转出来的字放进输入框、不直接发：听写会听错，发出去之前用户得看一眼。
 */

/** 听写模型要的采样率。Whisper、qwen-asr 都按 16kHz 处理，更高只是更大。 */
export const TARGET_RATE = 16000;

/** 一次最长录多久。再长就该用附件发音频文件了，而且听写按时长计费。 */
export const MAX_SECONDS = 180;

/** 把几路声道混成一路。 */
export function downmix(channels: Float32Array[]): Float32Array {
  if (channels.length === 0) return new Float32Array(0);
  if (channels.length === 1) return channels[0]!;
  const length = Math.min(...channels.map((channel) => channel.length));
  const mixed = new Float32Array(length);
  for (let i = 0; i < length; i++) {
    let sum = 0;
    for (const channel of channels) sum += channel[i]!;
    mixed[i] = sum / channels.length;
  }
  return mixed;
}

/**
 * 线性插值重采样。
 *
 * 语音降到 16kHz 用线性插值足够：听写模型本来就在 8k 以下的频段里认字，
 * 这里要的是「格式对、别失真到听不清」，不是高保真。
 */
export function resample(samples: Float32Array, fromRate: number, toRate: number): Float32Array {
  if (fromRate === toRate || samples.length === 0) return samples;
  const ratio = fromRate / toRate;
  const length = Math.max(1, Math.round(samples.length / ratio));
  const out = new Float32Array(length);
  for (let i = 0; i < length; i++) {
    const position = i * ratio;
    const left = Math.floor(position);
    const right = Math.min(left + 1, samples.length - 1);
    const weight = position - left;
    out[i] = samples[left]! * (1 - weight) + samples[right]! * weight;
  }
  return out;
}

/** 编成 PCM16 单声道 WAV。超出 [-1, 1] 的样本截断。 */
export function encodeWav(samples: Float32Array, sampleRate: number): Uint8Array {
  const dataBytes = samples.length * 2;
  const buffer = new ArrayBuffer(44 + dataBytes);
  const view = new DataView(buffer);
  const writeText = (offset: number, text: string) => {
    for (let i = 0; i < text.length; i++) view.setUint8(offset + i, text.charCodeAt(i));
  };
  writeText(0, "RIFF");
  view.setUint32(4, 36 + dataBytes, true);
  writeText(8, "WAVE");
  writeText(12, "fmt ");
  view.setUint32(16, 16, true); // fmt 块长度
  view.setUint16(20, 1, true); // PCM
  view.setUint16(22, 1, true); // 单声道
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true); // 每秒字节数
  view.setUint16(32, 2, true); // 每帧字节数
  view.setUint16(34, 16, true); // 位深
  writeText(36, "data");
  view.setUint32(40, dataBytes, true);
  for (let i = 0; i < samples.length; i++) {
    const clamped = Math.max(-1, Math.min(1, samples[i]!));
    view.setInt16(44 + i * 2, clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff, true);
  }
  return new Uint8Array(buffer);
}

/** 整段几乎没声音：多半是麦克风没开对或者没说话，别拿去花钱听写。 */
export function isSilent(samples: Float32Array, threshold = 0.01): boolean {
  let peak = 0;
  for (let i = 0; i < samples.length; i++) {
    const value = Math.abs(samples[i]!);
    if (value > peak) peak = value;
    if (peak >= threshold) return false;
  }
  return true;
}

/** 秒数写成 0:07 这样。 */
export function formatDuration(seconds: number): string {
  const whole = Math.max(0, Math.floor(seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
}

/**
 * 一次录音。start() 开麦，stop() 收尾并给出 WAV；cancel() 丢掉。
 *
 * 用 MediaRecorder 录、录完再整段解码，而不是用 ScriptProcessor 边录边取样：
 * 后者在 Electron 里已经废弃，而且每 4096 个样本回调一次，界面一卡就丢帧。
 */
export class VoiceRecorder {
  private stream: MediaStream | null = null;
  private recorder: MediaRecorder | null = null;
  private chunks: Blob[] = [];
  private analyser: AnalyserNode | null = null;
  private meterContext: AudioContext | null = null;
  startedAt = 0;

  async start(): Promise<void> {
    this.stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true },
    });
    const mimeType = MediaRecorder.isTypeSupported("audio/webm;codecs=opus") ? "audio/webm;codecs=opus" : "";
    this.recorder = new MediaRecorder(this.stream, mimeType ? { mimeType } : undefined);
    this.chunks = [];
    this.recorder.ondataavailable = (event) => {
      if (event.data.size > 0) this.chunks.push(event.data);
    };
    this.recorder.start(250);
    this.startedAt = Date.now();
    // 音量表：让用户看得见麦克风确实在收音。
    this.meterContext = new AudioContext();
    this.analyser = this.meterContext.createAnalyser();
    this.analyser.fftSize = 512;
    this.meterContext.createMediaStreamSource(this.stream).connect(this.analyser);
  }

  /** 0～1 的当前音量。 */
  level(): number {
    if (!this.analyser) return 0;
    const data = new Uint8Array(this.analyser.fftSize);
    this.analyser.getByteTimeDomainData(data);
    let peak = 0;
    for (const value of data) peak = Math.max(peak, Math.abs(value - 128) / 128);
    return Math.min(1, peak * 1.6);
  }

  elapsed(): number {
    return this.startedAt ? (Date.now() - this.startedAt) / 1000 : 0;
  }

  /** 停下并转成 WAV。没录到声音时返回 null。 */
  async stop(): Promise<{ wav: Uint8Array; seconds: number } | null> {
    const recorder = this.recorder;
    if (!recorder) return null;
    const stopped = new Promise<void>((resolve) => {
      recorder.onstop = () => resolve();
    });
    if (recorder.state !== "inactive") recorder.stop();
    await stopped;
    this.release();
    const blob = new Blob(this.chunks, { type: recorder.mimeType || "audio/webm" });
    this.chunks = [];
    if (blob.size === 0) return null;
    const decoder = new AudioContext();
    try {
      const audio = await decoder.decodeAudioData(await blob.arrayBuffer());
      const channels = Array.from({ length: audio.numberOfChannels }, (_, index) => audio.getChannelData(index));
      const samples = resample(downmix(channels), audio.sampleRate, TARGET_RATE);
      if (isSilent(samples)) return null;
      return { wav: encodeWav(samples, TARGET_RATE), seconds: audio.duration };
    } finally {
      void decoder.close();
    }
  }

  cancel(): void {
    if (this.recorder && this.recorder.state !== "inactive") {
      this.recorder.onstop = null;
      this.recorder.stop();
    }
    this.chunks = [];
    this.release();
  }

  private release(): void {
    for (const track of this.stream?.getTracks() ?? []) track.stop();
    this.stream = null;
    this.recorder = null;
    this.analyser = null;
    void this.meterContext?.close();
    this.meterContext = null;
    this.startedAt = 0;
  }
}
