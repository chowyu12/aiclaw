/**
 * 渲染层纯函数的测试：Markdown 渲染、错误文案。
 *
 * Markdown 渲染器的输入是**模型输出**，而模型输出会被它读到的文件、命令结果、
 * MCP 返回的内容影响。所以下面前半段全是注入用例——渲染器出一次洞，
 * 相当于给「读一个文件」开了执行任意脚本的口子。
 *
 * 只放不依赖 vue 与 window 的纯函数：那两样在 node 里 import 不了，
 * 这也是 describeError 单独成文件而不是留在 store.ts 里的原因。
 *
 * 跑法：make test-renderer
 */
import test from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

// DOMPurify 要一个 DOM 才能工作（它是按白名单重建节点，不是正则替换）。
// 渲染层里本来就有 window；测试在 node 里跑，所以先把 jsdom 的 window
// 挂上去，再 import 渲染器——**必须在 import 之前**，模块初始化时就要用。
if (!(globalThis as { window?: unknown }).window) {
  const dom = new JSDOM("");
  (globalThis as { window?: unknown }).window = dom.window;
  (globalThis as { document?: unknown }).document = dom.window.document;
}

const { renderMarkdown, looksLikePath } = await import("../apps/desktop/src/renderer/markdown.ts");

/**
 * 把块之间的换行挤掉再比对。
 *
 * markdown-it 会在块级标签之间插换行（`<p>a</p>\n<hr>\n`），那是排版不是语义。
 * 断言里逐个写 `\n?` 只会让用例难读，而且换个版本缩进变了就全红。
 */
/**
 * 把产出真的解析成 DOM 再遍历元素。
 *
 * **不用正则扫标签。** 试过两次都栽了：一次把正文里的 `onmouseover="x"`
 * 当成了属性，一次把 `alt="<img onerror=y>"` 里的内容当成了标签——后者其实是
 * 合规输出，HTML 的属性值本来就不要求转义 `<`/`>`，浏览器会原样解析回字符串。
 * 既然判断的是「浏览器会把这段变成什么」，那就让浏览器（jsdom）去解析。
 */
function elementsOf(html: string): Element[] {
  const dom = new JSDOM(`<div id="root">${html}</div>`);
  return [...dom.window.document.querySelectorAll("#root *")];
}

function squeeze(html: string): string {
  return html.replace(/>\s+</g, "><").trim();
}
import { describeError } from "../apps/desktop/src/renderer/errors.ts";
import {
  MAX_IMAGES,
  MAX_TEXT_BYTES,
  clampText,
  MAX_AUDIO,
  classifyFile,
  fitSize,
  inlineText,
} from "../apps/desktop/src/renderer/attachments.ts";

test("script 整个被剥掉，不是转义成文字", () => {
  // 现在是 html: true + DOMPurify：内联 HTML 会真的渲染，危险的那些被**删除**，
  // 而不是以前那样转义成 &lt;script&gt; 显示出来。
  const html = renderMarkdown("<script>alert(1)</script>");
  assert.ok(!/<script/i.test(html), `script 漏出来了：${html}`);
  assert.ok(!html.includes("alert(1)"), `脚本正文还在：${html}`);
});

test("事件属性一律不留", () => {
  for (const source of [
    '<img src="https://a.com/1.png" onerror="alert(1)">',
    '<div onclick="alert(1)">点</div>',
    '<a href="https://a.com" onmouseover="x">链</a>',
    '<details ontoggle="x"><summary>s</summary>c</details>',
  ]) {
    const html = renderMarkdown(source);
    for (const element of elementsOf(html)) {
      for (const attr of element.getAttributeNames()) {
        assert.ok(
          !attr.toLowerCase().startsWith("on"),
          `<${element.tagName.toLowerCase()}> 上留了事件属性 ${attr}：${html}`,
        );
      }
    }
  }
});

test("能嵌进来的东西一个都不留", () => {
  // iframe/object/embed 能把外部页面塞进同一个窗口；style 能覆盖整页外观、
  // 伪造出一个假的审批弹窗；form 能骗输入。
  for (const source of [
    "<iframe src=//evil></iframe>",
    "<object data=evil></object>",
    "<embed src=evil>",
    "<style>body{display:none}</style>",
    "<form action=//evil><input name=pw type=password></form>",
  ]) {
    const html = renderMarkdown(source);
    assert.ok(
      !/<(iframe|object|embed|style|form)/i.test(html),
      `危险标签漏出来了：${html}`,
    );
  }
  // 表单里的密码框也不能剩下——只有只读复选框能留。
  assert.ok(!/type="password"/.test(renderMarkdown("<input type=password>")));
});

test("相对地址的链接与图片不放行", () => {
  // 相对地址在这个应用里会被 shell.openExternal 当成本地文件打开。
  // 判断协议时不能给 base URL，否则 `x` 会被解析成 https://…/x 而显得安全。
  assert.doesNotMatch(renderMarkdown('<a href="relative/x">看</a>'), /href=/);
  assert.doesNotMatch(renderMarkdown('<a href="../../etc/passwd">看</a>'), /href=/);
  assert.equal(renderMarkdown("<img src=x onerror=alert(1)>").trim(), "");
});

test("file: 链接不放行", () => {
  // 点链接走的是主进程的 shell.openExternal，file:///…/x.command 一点就执行。
  assert.doesNotMatch(renderMarkdown('<a href="file:///etc/passwd">看</a>'), /href=/);
});

test("图片渲染成可点开的缩略图", () => {
  const html = renderMarkdown("![猫](https://a.com/c.png)");
  assert.match(html, /<img src="https:\/\/a\.com\/c\.png"/);
  // 不带 referrer：图片地址来自模型输出，而模型读得到本地文件与MCP 数据。
  assert.match(html, /referrerpolicy="no-referrer"/);
  assert.match(html, /class="image-thumb"/);
});

test("正常的展示型 HTML 会真的渲染", () => {
  // 放开 html 的意义就在这儿：模型写的 <details>、<span> 不再是尖括号原文。
  const html = renderMarkdown("<details><summary>点开</summary><b>内容</b></details>");
  assert.match(html, /<details><summary>点开<\/summary><b>内容<\/b><\/details>/);
});

test("javascript: 链接退化成纯文字", () => {
  const html = renderMarkdown("[点我](javascript:alert(1))");
  assert.ok(!html.includes("href"), `不该生成链接：${html}`);
  assert.ok(html.includes("点我"), "文字要留着");
});

test("data: 链接退化成纯文字", () => {
  const html = renderMarkdown("[看图](data:text/html;base64,PHNjcmlwdD4=)");
  assert.ok(!html.includes("href"), `不该生成链接：${html}`);
});

test("http(s) 链接正常渲染并带上 noopener", () => {
  const html = renderMarkdown("见 [文档](https://example.com/a?b=1&c=2)");
  assert.ok(html.includes('href="https://example.com/a?b=1&amp;c=2"'), html);
  assert.ok(html.includes('rel="noreferrer noopener"'), "外链必须带 noopener");
});

test("代码块里的标签只是文字", () => {
  const html = renderMarkdown("```html\n<script>x</script>\n```");
  assert.ok(html.includes("<pre"), "应当是代码块");
  assert.ok(!html.includes("<script>"), `代码块里的标签漏出来了：${html}`);
  assert.ok(html.includes('data-lang="html"'), "语言标记应当带上");
});

test("行内代码里的星号不被当成粗体", () => {
  const html = renderMarkdown("用 `**kwargs` 传参");
  assert.ok(html.includes("<code>**kwargs</code>"), html);
  assert.ok(!html.includes("<strong>"), `代码里的星号被吃掉了：${html}`);
});

test("无序列表成 ul，星号列表符不会被当成斜体", () => {
  const html = renderMarkdown("- 第一项\n- 第二项\n* 第三项");
  assert.ok(html.includes("<ul>"), html);
  assert.equal((html.match(/<li>/g) ?? []).length, 3, html);
  assert.ok(!html.includes("<em>"), `列表符被当成斜体了：${html}`);
});

test("有序列表成 ol", () => {
  const html = renderMarkdown("1. 先这样\n2. 再那样");
  assert.ok(html.includes("<ol>"), html);
  assert.equal((html.match(/<li>/g) ?? []).length, 2, html);
});

test("标题从 h3 起步，不会在气泡里大得离谱", () => {
  const html = renderMarkdown("# 大标题\n## 小标题");
  assert.ok(html.includes("<h3>大标题</h3>"), html);
  assert.ok(html.includes("<h4>小标题</h4>"), html);
});

test("粗体与斜体", () => {
  const html = renderMarkdown("这是 **重点**，这是 _强调_。");
  assert.ok(html.includes("<strong>重点</strong>"), html);
  assert.ok(html.includes("<em>强调</em>"), html);
});

test("段落内的换行保留成 br", () => {
  // `<br>` 后面那个换行是 markdown-it 为了源码可读加的，HTML 里会被折叠掉。
  const html = squeeze(renderMarkdown("第一行\n第二行"));
  assert.match(html, /第一行<br>\s*第二行/);
});

test("空行分段", () => {
  const html = renderMarkdown("第一段\n\n第二段");
  assert.equal((html.match(/<p>/g) ?? []).length, 2, html);
});

test("没闭合的代码块照常渲染，不等它闭合", () => {
  // 流式输出到一半就是这个样子。等闭合的话用户会看见代码凭空消失又出现。
  const html = renderMarkdown("看这段：\n```go\nfunc main() {");
  assert.ok(html.includes("<pre"), `半截代码块也要显示：${html}`);
  assert.ok(html.includes("func main() {"), html);
});

test("引用块", () => {
  // 引用里的内容是递归渲染的，所以里面会有一层 <p>——那是对的：
  // 引用里可以是列表、代码块甚至再一层引用。
  const html = squeeze(renderMarkdown("> 注意这里"));
  assert.ok(html.includes("<blockquote><p>注意这里</p></blockquote>"), html);
});

test("空输入不产生垃圾节点", () => {
  assert.equal(renderMarkdown(""), "");
  assert.equal(renderMarkdown("\n\n  \n"), "");
});

test("真实形状的一段回答", () => {
  const source = [
    "你好！我是你的本地文件助手，可以直接读写 `/Users/x/Workspace` 里的文件。",
    "",
    "需要我做什么？比如：",
    "- 看看某个目录里有什么",
    "- 搜索代码或文本",
    "",
    "告诉我目标就行。",
  ].join("\n");

  const html = renderMarkdown(source);
  // 目录也是路径，点了就在访达里打开——这正是用户下一步想做的事。
  assert.ok(html.includes('<code class="file-ref" title="点击打开">/Users/x/Workspace</code>'), html);
  assert.ok(html.includes("<ul>"), html);
  assert.equal((html.match(/<li>/g) ?? []).length, 2, html);
  // 反引号和列表符号不该以原样出现在输出里——那正是没渲染的样子。
  assert.ok(!html.includes("`"), `反引号漏出来了：${html}`);
});

// ---------- 错误文案 ----------

test("剥掉 Electron 的 IPC 包装，只留真正的原因", () => {
  const raw = new Error(
    "Error invoking remote method 'runtime:start': Error: claw-agent 退出码 1",
  );
  assert.equal(describeError(raw), "claw-agent 退出码 1");
});

test("普通 Error 只去掉前缀", () => {
  assert.equal(describeError(new Error("模型 Key 未配置")), "模型 Key 未配置");
});

test("非 Error 值也能处理", () => {
  assert.equal(describeError("坏了"), "坏了");
  assert.equal(describeError(null), "null");
});

test("空消息给一句兜底，不显示空白", () => {
  assert.equal(describeError(new Error("")), "未知错误");
});

test("反斜杠转义的星号是字面量，不参与粗体", () => {
  // 信用代码里的 91310000MA1FL\*\*\* 这种，不处理的话反斜杠会原样显示出来。
  const html = renderMarkdown("代码 91310000MA1FL\\*\\*\\*");
  assert.ok(html.includes("91310000MA1FL***"), html);
  assert.ok(!html.includes("\\"), `反斜杠漏出来了：${html}`);
  assert.ok(!html.includes("<em>"), `转义的星号不该变成斜体：${html}`);
});

test("反斜杠后面不是可转义字符时原样保留", () => {
  // 路径里的 \n 之类不该被吃掉。
  const html = renderMarkdown("路径 C:\\temp");
  assert.ok(html.includes("C:\\temp"), html);
});

// ---------- 表格 / 分割线 / 删除线 / 嵌套列表 / 任务列表 ----------
//
// 这几样以前不支持，模型输出的股东表在界面上是一堆竖线原文。补的时候要守住
// 同一条死规矩：先转义、只在转义后的文本上套规则。所以除了「渲染对不对」，
// 下面还有一条**按白名单审计产出**的用例——比逐个写注入样本更难绕过去。

test("表格：表头 + 对齐 + 单元格", () => {
  const html = renderMarkdown("| 排名 | 名称 | 比例 |\n|---|:-:|---:|\n| 1 | 曾烨 | 24.77% |");
  assert.match(html, /<table>/);
  assert.match(html, /<th>排名<\/th>/);
  assert.match(html, /<th style="text-align:center">名称<\/th>/);
  assert.match(html, /<th style="text-align:right">比例<\/th>/);
  assert.match(html, /<td>1<\/td>/);
  // 列多的表要能自己横向滚，不然整页跟着横滚。
  assert.match(html, /<div class="table-wrap">/);
});

test("表格：单元格数量与表头对不齐时按表头补齐", () => {
  // 模型少写一个竖线很常见。宁可缺一格，也不要整张表塌成一行文字。
  const html = renderMarkdown("| a | b | c |\n|---|---|---|\n| 1 | 2 |");
  assert.equal((html.match(/<td/g) ?? []).length, 3);
});

test("正文里的竖线不该被当成表格", () => {
  // 只看「这一行有竖线」会把 `a | b` 这种正文认成表格，所以判据是下一行
  // 必须是分隔行。
  const html = renderMarkdown("选 a | b 都行\n然后继续");
  assert.doesNotMatch(html, /<table>/);
});

test("分割线", () => {
  assert.match(squeeze(renderMarkdown("上\n\n---\n\n下")), /<p>上<\/p><hr><p>下<\/p>/);
  assert.match(renderMarkdown("***"), /<hr>/);
  // `- 一` 是列表不是分割线。
  assert.doesNotMatch(renderMarkdown("- 一"), /<hr>/);
});

test("删除线", () => {
  assert.match(squeeze(renderMarkdown("~~删~~")), /<del>删<\/del>/);
});

test("嵌套列表：子列表放在上一个 li 里，且标签配平", () => {
  // `<ul>` 直接套 `<ul>` 浏览器能显示，但那是无效 HTML、缩进语义也不对。
  const html = squeeze(renderMarkdown("- 一\n  - 一点一\n    - 更深\n- 二"));
  assert.doesNotMatch(html, /<ul><ul>|<\/li><ul>/);
  assert.equal((html.match(/<li>/g) ?? []).length, (html.match(/<\/li>/g) ?? []).length);
  assert.equal((html.match(/<ul>/g) ?? []).length, (html.match(/<\/ul>/g) ?? []).length);
  // 文字与子列表之间可能有个换行，那是排版；要紧的是子列表在 li 里面。
  assert.match(html, /<li>一\s*<ul><li>一点一/);
});

test("任务列表渲染成只读复选框", () => {
  const html = renderMarkdown("- [ ] 待办\n- [x] 做完");
  // DOMPurify 重建节点时把布尔属性写成 disabled=""，那是等价的。
  assert.match(html, /<input type="checkbox" disabled(?:="")?[^>]*>待办/);
  assert.match(html, /<input type="checkbox" disabled(?:="")? checked(?:="")?[^>]*>做完/);
});

test("产出只包含白名单内的标签与属性", () => {
  // 逐个写注入样本永远写不全。这里反过来审计**产出**：模型文本一律被转义，
  // 所以输出里的每一个标签都只能来自我们自己的模板；出现名单外的东西，
  // 就说明有一条规则把原文塞进了输出。
  const TAGS = new Set([
    "p","br","strong","em","del","code","pre","a","ul","ol","li",
    "h3","h4","h5","h6","blockquote","hr","div","table","thead","tbody",
    "tr","th","td","label","input","sup","section","img",
    "b","i","details","summary","mark","small","kbd","abbr","time",
    "dl","dt","dd","caption","colgroup","col","tfoot","figure","figcaption","article",
    // 名单里**没有** s（删除线统一映射成 del），也没有 script / style /
    // iframe / object / embed / form——那几个是净化器必须剥掉的，
    // 它们哪天冒出来就是有人改松了，这条用例要红。
  ]);
  const ATTRS = new Set([
    "href","target","rel","title",
    "src","alt","width","height","loading","referrerpolicy",
    "class","id","style","data-lang",
    "colspan","rowspan","align","start","reversed","open",
    "type","checked","disabled","datetime",
  ]);
  const samples = [
    "| a | b |\n|---|---|\n| <img src=x onerror=alert(1)> | <script>bad()</script> |",
    "| <svg onload=x> |\n|---|\n| v |",
    '| a |\n|:--" onmouseover="x|\n| v |',
    "| x |\n|---|\n| [点](javascript:alert(1)) |",
    "- [x] <script>alert(1)</script>",
    "~~<img src=x onerror=y>~~",
    "- 一\n  - <iframe src=//evil>",
    "# <style>body{display:none}</style>",
    "> <object data=evil>",
    "文字[^1]\n\n[^1]: <script>x</script>",
    "![alt](javascript:alert(1))",
    "![<img src=x onerror=y>](https://a.com/x.png)",
  ];
  for (const source of samples) {
    const html = renderMarkdown(source);
    for (const element of elementsOf(html)) {
      const tag = element.tagName.toLowerCase();
      assert.ok(TAGS.has(tag), `越界标签 <${tag}>，来自：${source}`);
      for (const name of element.getAttributeNames()) {
        const attr = name.toLowerCase();
        assert.ok(!attr.startsWith("on"), `<${tag}> 上留了事件属性 ${attr}，来自：${source}`);
        assert.ok(ATTRS.has(attr), `越界属性 ${attr} 在 <${tag}>，来自：${source}`);
      }
      // 每个 <img> 都必须带 http(s) 的 src。没有 src 的碎图标通常正是
      // 注入尝试被剥干净之后的残骸，不该留在页面上。
      if (tag === "img") {
        assert.match(
          element.getAttribute("src") ?? "",
          /^https?:\/\//,
          `图片的 src 不合规，来自：${source}`,
        );
      }
      // 链接的 href 只能是 http/https/mailto 或页内锚点。
      if (tag === "a" && element.hasAttribute("href")) {
        assert.match(
          element.getAttribute("href") ?? "",
          /^(?:https?:|mailto:|#)/,
          `链接协议不合规，来自：${source}`,
        );
      }
    }
  }
});

// ---------- 附件：什么收、什么不收 ----------
//
// 判断错的代价不对称：收下一个 200MB 的视频只是慢；把不支持的文件**默默忽略**
// 则会让用户以为模型读过了那份 PDF，然后基于一个它根本没看到的东西讨论下去。

test("图片按图片收", () => {
  assert.deepEqual(classifyFile({ name: "a.png", type: "image/png", size: 1000 }, 0), {
    accept: "image",
  });
});

test("图片张数与体积有上限，超了要说原因而不是默默丢", () => {
  const full = classifyFile({ name: "a.png", type: "image/png", size: 1000 }, MAX_IMAGES);
  assert.equal(full.accept, "no");
  assert.match(full.accept === "no" ? full.reason : "", /最多/);

  const huge = classifyFile({ name: "a.png", type: "image/png", size: 30 * 1024 * 1024 }, 0);
  assert.equal(huge.accept, "no");
  assert.match(huge.accept === "no" ? huge.reason : "", /太大/);
});

test("SVG 当文本收，不当图片", () => {
  // SVG 是可执行文档（脚本、外链），而且模型看的是位图。
  assert.deepEqual(classifyFile({ name: "a.svg", type: "image/svg+xml", size: 100 }, 0), {
    accept: "text",
  });
});

test("认得出没有 MIME 类型的源码与配置文件", () => {
  for (const name of ["a.ts", "b.go", "c.yaml", "Makefile", "Dockerfile", "d.csv"]) {
    assert.equal(
      classifyFile({ name, type: "", size: 100 }, 0).accept,
      "text",
      `${name} 应当按文本收`,
    );
  }
});

test("不支持的文件明确拒绝，并告诉用户怎么办", () => {
  const verdict = classifyFile({ name: "报告.pdf", type: "application/pdf", size: 100 }, 0);
  assert.equal(verdict.accept, "no");
  assert.match(verdict.accept === "no" ? verdict.reason : "", /工作目录/);
});

test("文本超限截断，并且说出来", () => {
  const short = clampText("一二三");
  assert.equal(short.truncated, false);
  assert.equal(short.text, "一二三");

  const long = clampText("啊".repeat(200_000));
  assert.equal(long.truncated, true);
  assert.ok(new TextEncoder().encode(long.text).length <= MAX_TEXT_BYTES);
  // 截断的事实要进正文：模型和用户都得知道自己看的不是全文。
  assert.match(inlineText({ kind: "text", name: "x.log", ...long }), /只贴了前/);
});

test("图片缩放：长边封顶，小图不放大", () => {
  assert.deepEqual(fitSize(3024, 1964), { width: 1568, height: 1018 });
  assert.deepEqual(fitSize(800, 600), { width: 800, height: 600 });
  assert.deepEqual(fitSize(0, 0), { width: 0, height: 0 });
});

// ---------- 对话里的文件名可以点开 ----------
//
// 判断只看形状，不看文件存不存在——渲染层不碰文件系统。认错了最多是点下去
// 提示「找不到」；真正的安全判断（可执行的不直接打开、凭据目录拒绝）在主进程。
// 所以这里要钉的是**别把命令认成文件**，那会变成一堆点了报错的假链接。

test("文件名渲染成可点的", () => {
  for (const text of ["双色球最近30期开奖号码.md", "package.json", "src/main/index.ts", "~/Desktop/a.png"]) {
    assert.ok(looksLikePath(text), `${text} 应当被认成路径`);
  }
  const html = renderMarkdown("文档已生成：`报表.md`（工作区根目录）");
  assert.match(html, /class="file-ref"/);
  assert.match(html, /报表\.md/);
});

test("命令、版本号、普通词不会被认成文件", () => {
  // 认错的代价不对称：把 `npm run build` 变成链接，用户点下去只会得到
  // 一句「找不到」，而他本来就没想点。
  for (const text of [
    "npm run build",
    "git push origin master",
    "v0.1.5",
    "run_command",
    "on-write",
    "foo.bar()",
    "https://example.com/a.md",
  ]) {
    assert.equal(looksLikePath(text), false, `${text} 不该被认成路径`);
  }
  assert.doesNotMatch(renderMarkdown("跑一下 `npm run build`"), /file-ref/);
});

test("带空格的文件名仍然认，但必须有扩展名收尾", () => {
  assert.ok(looksLikePath("我的 报表.xlsx"));
  assert.equal(looksLikePath("ls -la 目录"), false);
});

test("file-ref 这个类名活过净化，而 onclick 之类活不过", () => {
  // 类名要留住，不然样式和点击委托都失效。
  const html = renderMarkdown("`a.md`");
  assert.match(html, /<code class="file-ref"/);
  // 模型自己写一个同名 class 也只是个 class——真正的校验在主进程。
  const forged = renderMarkdown('<code class="file-ref" onclick="alert(1)">x.md</code>');
  assert.doesNotMatch(forged, /onclick/);
});

// ---------- 音频附件 ----------
//
// 音频要交给听写模型转成文字，而转写按时长收费、一段几分钟的录音 base64 之后
// 就超过行协议的单帧上限。所以「收不收、收几段」必须在这里判准。

test("常见音频格式按扩展名认出来——拖进来的文件常常没有 MIME", () => {
  for (const name of ["a.mp3", "a.m4a", "a.wav", "a.ogg", "a.opus", "a.flac", "a.aac"]) {
    assert.deepEqual(classifyFile({ name, type: "", size: 1000 }, 0, 0), { accept: "audio" }, name);
  }
});

test("audio/* 的 MIME 也认", () => {
  assert.deepEqual(classifyFile({ name: "录音", type: "audio/mpeg", size: 1000 }, 0, 0), {
    accept: "audio",
  });
});

test("一条消息最多两段音频——每段都要打一次听写模型", () => {
  const full = classifyFile({ name: "a.mp3", type: "", size: 1000 }, 0, MAX_AUDIO);
  assert.equal(full.accept, "no");
});

test("超过 25MB 拒绝：多数听写服务自己也卡在这儿", () => {
  const huge = classifyFile({ name: "a.mp3", type: "", size: 30 * 1024 * 1024 }, 0, 0);
  assert.equal(huge.accept, "no");
});

test("图片与文本的判断不受音频影响", () => {
  assert.deepEqual(classifyFile({ name: "a.png", type: "image/png", size: 100 }, 0, 0), {
    accept: "image",
  });
  assert.deepEqual(classifyFile({ name: "a.md", type: "", size: 100 }, 0, 0), { accept: "text" });
});

test("mp4 当音频收：模型读不了视频，但录屏配音是常见的输入", () => {
  assert.deepEqual(classifyFile({ name: "a.mp4", type: "video/mp4", size: 100 }, 0, 0), {
    accept: "audio",
  });
});
