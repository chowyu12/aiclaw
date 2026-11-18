import { readFileSync, writeFileSync } from "node:fs";

type WindowSize = { width: number; height: number };

const DEFAULT_SIZE: WindowSize = { width: 1180, height: 800 };
export const WINDOW_MIN_SIZE: WindowSize = { width: 860, height: 600 };

export function loadWindowSize(path: string, workArea: WindowSize): WindowSize {
  let size = DEFAULT_SIZE;
  try {
    const saved = JSON.parse(readFileSync(path, "utf8"));
    if (Number.isSafeInteger(saved?.width) && saved.width > 0 &&
        Number.isSafeInteger(saved?.height) && saved.height > 0) size = saved;
  } catch {}
  return {
    width: Math.max(WINDOW_MIN_SIZE.width, Math.min(size.width, workArea.width)),
    height: Math.max(WINDOW_MIN_SIZE.height, Math.min(size.height, workArea.height)),
  };
}

export function saveWindowSize(path: string, size: WindowSize): void {
  try { writeFileSync(path, JSON.stringify(size)); } catch {}
}
