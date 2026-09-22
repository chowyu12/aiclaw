import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { resolve } from "node:path";

export default defineConfig({
  plugins: [vue()],
  // Electron 用 file:// 加载构建产物，必须相对路径。
  base: "./",
  build: {
    outDir: "dist/renderer",
    emptyOutDir: true,
  },
  resolve: {
    alias: {
      "@shared": resolve(import.meta.dirname, "src/shared"),
    },
  },
});
