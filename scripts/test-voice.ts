import { strict as assert } from "node:assert";
import { test } from "node:test";

import { downmix, encodeWav, formatDuration, isSilent, resample, TARGET_RATE } from "../apps/desktop/src/renderer/voice.ts";

/**
 * 语音输入的编码部分。录音本身要真麦克风，测不了；但「交给听写模型的是一段合法的
 * 16kHz 单声道 PCM16 WAV」这件事错了没有任何报错——听写接口只会回一句「格式不支持」
 * 或者干脆听出一堆乱码。所以把字节级的格式钉住。
 */

test("WAV 头：RIFF / fmt / data，16kHz 单声道 16 位", () => {
  const samples = new Float32Array([0, 0.5, -0.5, 1, -1, 2, -2]);
  const wav = encodeWav(samples, TARGET_RATE);
  const view = new DataView(wav.buffer);
  const text = (offset: number, length: number) => String.fromCharCode(...wav.slice(offset, offset + length));
  assert.equal(text(0, 4), "RIFF");
  assert.equal(view.getUint32(4, true), 36 + samples.length * 2);
  assert.equal(text(8, 4), "WAVE");
  assert.equal(text(12, 4), "fmt ");
  assert.equal(view.getUint16(20, true), 1, "PCM");
  assert.equal(view.getUint16(22, true), 1, "单声道");
  assert.equal(view.getUint32(24, true), 16000);
  assert.equal(view.getUint32(28, true), 32000, "每秒字节数");
  assert.equal(view.getUint16(34, true), 16);
  assert.equal(text(36, 4), "data");
  assert.equal(view.getUint32(40, true), samples.length * 2);
  assert.equal(wav.length, 44 + samples.length * 2);
  const pcm = Array.from({ length: samples.length }, (_, i) => view.getInt16(44 + i * 2, true));
  assert.deepEqual(pcm, [0, 16383, -16384, 32767, -32768, 32767, -32768], "超出范围的截断");
});

test("48kHz 降到 16kHz：长度按比例、低频波形保留", () => {
  const from = 48000;
  const seconds = 0.5;
  const tone = new Float32Array(from * seconds).map((_, i) => Math.sin((2 * Math.PI * 440 * i) / from));
  const out = resample(tone, from, TARGET_RATE);
  assert.equal(out.length, TARGET_RATE * seconds);
  for (const index of [100, 1234, 5000]) {
    const expected = Math.sin((2 * Math.PI * 440 * index) / TARGET_RATE);
    assert.ok(Math.abs(out[index]! - expected) < 0.05, `第 ${index} 个样本偏差太大`);
  }
  assert.equal(resample(tone, TARGET_RATE, TARGET_RATE), tone, "同采样率原样返回");
});

test("双声道混成单声道取平均，长度按短的那一路", () => {
  const mixed = downmix([new Float32Array([1, 0, 0.5]), new Float32Array([0, 1])]);
  assert.deepEqual(Array.from(mixed), [0.5, 0.5]);
  assert.deepEqual(Array.from(downmix([])), []);
});

test("几乎没声音的一段不拿去听写", () => {
  assert.equal(isSilent(new Float32Array(16000).fill(0.002)), true);
  const speech = new Float32Array(16000);
  speech[8000] = 0.3;
  assert.equal(isSilent(speech), false);
});

test("时长写成 分:秒", () => {
  assert.equal(formatDuration(0), "0:00");
  assert.equal(formatDuration(7.9), "0:07");
  assert.equal(formatDuration(125), "2:05");
});
