/**
 * 给 node --test 用的解析钩子：源码里写的是编译后的路径（`./schedule.js`），
 * 测试直接跑 .ts 源码时那个 .js 并不存在。找不到 .js 时退回同名的 .ts。
 *
 * 只在测试里用（package.json 的 test:renderer 带 --import 这个文件）；
 * 构建走 tsc / vite，不经过这里。
 */
import { register } from "node:module";

register(
  "data:text/javascript," +
    encodeURIComponent(`
      export async function resolve(specifier, context, next) {
        try {
          return await next(specifier, context);
        } catch (error) {
          if (error?.code === "ERR_MODULE_NOT_FOUND" && /^\\.{1,2}\\//.test(specifier) && specifier.endsWith(".js")) {
            return next(specifier.slice(0, -3) + ".ts", context);
          }
          throw error;
        }
      }
    `),
);
