import { existsSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { isAbsolute, join, resolve } from "node:path";
import { shell } from "electron";

import { classifyOpen, type OpenVerdict } from "./open-file-rules.js";

/**
 * 打开模型在对话里提到的文件。
 *
 * 它连的是两头都不可信的东西：**路径来自模型输出**（而模型读得到文件、命令
 * 结果、MCP 数据，那些都可能被注入），**动作是交给系统用默认程序打开**——
 * 在 macOS 上「用默认程序打开」对 .command/.app/.pkg 这类就是**执行**。
 * 判断规则在 open-file-rules.ts，那边能在 node 里逐条测。
 */

export interface OpenResult {
  ok: boolean;
  /** 实际处理方式，界面据此给一句提示。 */
  action: OpenVerdict["action"] | "missing";
  detail: string;
}

/**
 * 打开对话里提到的那个路径。
 *
 * base 是会话工作区（没设就是主目录）——模型说的「工作区根目录下的那个文件」
 * 只有按这个基准解析才找得到。
 */
export async function openFromChat(
  raw: string,
  options: { base: string; protectedPaths: string[] },
): Promise<OpenResult> {
  const text = raw.trim();
  if (!text) return { ok: false, action: "missing", detail: "路径是空的" };

  const home = homedir();
  const expanded = text === "~" || text.startsWith("~/") ? join(home, text.slice(1)) : text;
  const base = options.base.trim() || home;
  const path = isAbsolute(expanded) ? resolve(expanded) : resolve(base, expanded);

  if (!existsSync(path)) {
    return { ok: false, action: "missing", detail: `找不到 ${path}` };
  }

  let executable = false;
  try {
    const info = statSync(path);
    // 目录不看执行位：目录的 x 位是「能进去」，不是「能跑」。
    executable = info.isFile() && (info.mode & 0o111) !== 0;
  } catch {
    // 取不到属性就按可执行处理——不确定的时候选更保守的那条。
    executable = true;
  }

  const verdict = classifyOpen(path, { home, protectedPaths: options.protectedPaths, executable });
  if (verdict.action === "refuse") {
    return { ok: false, action: "refuse", detail: verdict.reason };
  }
  if (verdict.action === "reveal") {
    shell.showItemInFolder(path);
    return { ok: true, action: "reveal", detail: verdict.reason };
  }

  const error = await shell.openPath(path);
  if (error) {
    // 打不开（没有默认程序之类）退回到在访达里选中，总比什么都没发生强。
    shell.showItemInFolder(path);
    return { ok: true, action: "reveal", detail: `系统打不开它（${error}），已在访达里标出来` };
  }
  return { ok: true, action: "open", detail: path };
}
