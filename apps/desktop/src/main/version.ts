/**
 * 版本号比较。
 *
 * 单独成文件是为了能在 node 里直接测：比错了的表现是「有新版不提示」或者
 * 「装完还一直提示」，两种都不会报错，只会让人觉得这功能坏了。
 *
 * 规则只取需要的那部分语义化版本：
 *   - 数字段逐段比，段数不同时缺的那段按 0（1.2 == 1.2.0）；
 *   - 带预发布后缀的小于同号正式版（0.1.0-rc1 < 0.1.0）；
 *   - 两个都有后缀就按后缀字符串比（rc1 < rc2）。
 */

/** 去掉 tag 的 v 前缀与首尾空白。`v0.1.3` → `0.1.3`。 */
export function normalizeVersion(raw: string): string {
  return raw.trim().replace(/^[vV]/, "");
}

interface Parsed {
  numbers: number[];
  /** 预发布后缀，没有就是空串。 */
  pre: string;
}

function parse(raw: string): Parsed {
  const text = normalizeVersion(raw);
  // 预发布后缀按第一个 - 切开。构建元数据（+xxx）不参与比较，直接丢。
  const [core = "", ...rest] = text.split("+")[0]!.split("-");
  return {
    numbers: core.split(".").map((part) => {
      const n = Number.parseInt(part, 10);
      return Number.isFinite(n) ? n : 0;
    }),
    pre: rest.join("-"),
  };
}

/** a 比 b 新返回 1，旧返回 -1，一样返回 0。 */
export function compareVersions(a: string, b: string): number {
  const left = parse(a);
  const right = parse(b);

  const length = Math.max(left.numbers.length, right.numbers.length);
  for (let i = 0; i < length; i++) {
    const diff = (left.numbers[i] ?? 0) - (right.numbers[i] ?? 0);
    if (diff !== 0) return diff > 0 ? 1 : -1;
  }

  // 数字段相同：没有后缀的是正式版，比预发布新。
  if (left.pre === right.pre) return 0;
  if (!left.pre) return 1;
  if (!right.pre) return -1;
  return left.pre > right.pre ? 1 : -1;
}

/** latest 是不是比 current 新。版本号解析不出来时返回 false——宁可不提示。 */
export function isNewer(latest: string, current: string): boolean {
  if (!normalizeVersion(latest) || !normalizeVersion(current)) return false;
  return compareVersions(latest, current) > 0;
}
