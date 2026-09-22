import createDOMPurify from "dompurify";
import MarkdownIt from "markdown-it";
import footnote from "markdown-it-footnote";

/**
 * 把模型输出渲染成 HTML。Markdown 与内联 HTML 都渲染。
 *
 * ## 这里的威胁模型
 *
 * **渲染的是模型输出，而模型输出会被它读到的文件、命令结果、内部平台返回的内容
 * 影响。**渲染层跑在 Electron 里，`window.aiclaw` 就在旁边——那上面挂着发消息、
 * 写凭据、删数据、装技能。所以一段执行得起来的脚本不是「页面花了」，
 * 是把审批那道闸整个绕过去。
 *
 * ## 于是：渲染 HTML，但先过一遍 DOMPurify
 *
 * 早先是 `html: false`，原始 HTML 一律当文本——安全，但模型输出的 `<details>`、
 * `<span style>`、内联表格全变成尖括号原文。现在放开渲染，代价用 DOMPurify
 * 抵掉：它按白名单重建 DOM，`<script>`、`on*=` 事件属性、`<iframe>`、`<object>`、
 * `<form>` 这些一律不留。
 *
 * **没有选择「直接 html: true 不做净化」**，那等于把上面那个 IPC 面交给
 * 模型输出。这一条不是产品取舍，是这个应用没有沙箱之后仅剩的那道闸——
 * 要真的需要跑任意 HTML/脚本（比如预览模型生成的图表），正确做法是塞进一个
 * sandbox iframe，而不是让它和主界面同一个 origin。
 */

/** 链接放行的协议。 */
const LINK_SCHEMES = new Set(["http:", "https:", "mailto:"]);

/**
 * `file:` 不放。
 *
 * 界面里点链接会走主进程的 `shell.openExternal`（见 main/index.ts），
 * 也就是交给系统用默认程序打开。`file:///…/x.command` 这种一点就执行，
 * 而链接文字是模型写的，看起来可以完全无害。
 */
function isSafeHref(href: string): boolean {
  // 页内锚点（脚注的跳转与回跳）放行，它不出这个文档。
  if (href.startsWith("#")) return href.length > 1;
  try {
    // **不给 base。** 给了的话相对地址（`x`、`../a`）会被解析成
    // `https://invalid.local/x`，协议判断直接失真——而相对链接在这个应用里
    // 会被 shell.openExternal 当成本地文件打开。没有协议就是不合规。
    return LINK_SCHEMES.has(new URL(href).protocol);
  } catch {
    return false;
  }
}

/** 图片只认 http/https。`data:` 不放——它能塞进整个文件，也绕开下面的来源判断。 */
/**
 * 这段行内代码看着像不像一个文件路径。
 *
 * 宁可漏不可滥：判错了会把 `npm run build` 这种命令变成一个点下去报错的链接。
 * 所以两条硬要求——要么末尾是**字母开头的扩展名**（`.md`、`.json`、`.tar`），
 * 要么里面有路径分隔符；并且不能带 `<>|*?"` 这类路径里不合法的字符。
 *
 * 扩展名要求字母开头是为了挡住版本号：`v0.1.5` 的 `.5` 不算扩展名。
 */
export function looksLikePath(raw: string): boolean {
  const text = raw.trim();
  if (!text || text.length > 400) return false;
  // 带协议的是链接不是路径，交给 markdown 的链接那条路。
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(text)) return false;
  if (/[<>|*?"\n\r]/.test(text)) return false;
  // 纯粹的命令行：有空格且不以扩展名收尾的，一律不认。
  const hasExtension = /\.[A-Za-z][A-Za-z0-9]{0,7}$/.test(text);
  const hasSeparator = text.includes("/") || /^[A-Za-z]:\\/.test(text);
  if (!hasExtension && !hasSeparator) return false;
  if (/\s/.test(text) && !hasExtension) return false;
  return true;
}

function isSafeImage(src: string): boolean {
  try {
    // 同样不给 base：`<img src=x>` 里的 `x` 不是一个可判断来源的地址。
    const url = new URL(src);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false;
  }
}

const md = new MarkdownIt({
  // 渲染内联 HTML。安全由下面的 DOMPurify 兜底，不是靠这里关着。
  html: true,
  // 单个换行渲染成 <br>：模型经常靠换行分层次，按 CommonMark 合并掉会读不通。
  breaks: true,
  linkify: true,
  // 不做引号/破折号的「智能」替换——它会把代码、路径、命令里的字符换掉。
  typographer: false,
});

md.use(footnote);

// 只认带协议的链接。默认的 fuzzyLink 会把 `config.json`、`a.b` 当成域名，
// 在一个满屏文件名和包名的工具里那是纯噪音。
md.linkify.set({ fuzzyLink: false });

md.validateLink = isSafeHref;

/** 标题压到 h3 起步：对话气泡里的 h1 会大得离谱，而模型很爱用 `#` 开头。 */
md.renderer.rules.heading_open = (tokens, index) => `<h${headingTag(tokens[index]!.tag)}>`;
md.renderer.rules.heading_close = (tokens, index) => `</h${headingTag(tokens[index]!.tag)}>`;

function headingTag(tag: string): number {
  return Math.min(6, Number.parseInt(tag.slice(1), 10) + 2);
}

/** 链接一律新窗口打开，并且带上 noreferrer noopener。 */
md.renderer.rules.link_open = (tokens, index, options, _env, self) => {
  const token = tokens[index]!;
  if (!String(token.attrGet("href") ?? "").startsWith("#")) {
    token.attrSet("target", "_blank");
    token.attrSet("rel", "noreferrer noopener");
  }
  return self.renderToken(tokens, index, options);
};

/**
 * 图片。
 *
 * 显示出来，但**不带 referrer**：图片地址来自模型输出，而模型读得到本地文件
 * 与内部平台数据。不加这一条的话，请求头里会把当前页面信息一并送给对端；
 * 加了之后对端最多知道「有人加载了这张图」。
 * 地址协议不对就退化成纯文字，不留空图标。
 */
md.renderer.rules.image = (tokens, index) => {
  const token = tokens[index]!;
  const src = String(token.attrGet("src") ?? "");
  const alt = token.content || "";
  if (!isSafeImage(src)) return md.utils.escapeHtml(alt || "图片");
  const safeSrc = md.utils.escapeHtml(src);
  // 缩略图 + 可点开原图。对话气泡里塞一张原尺寸的图会把整屏顶掉，
  // 而这里的图多半只是「看一眼是不是这个」，尺寸限制交给 CSS。
  return (
    `<a href="${safeSrc}" target="_blank" rel="noreferrer noopener" class="image-thumb">` +
    `<img src="${safeSrc}" alt="${md.utils.escapeHtml(alt)}"` +
    ` referrerpolicy="no-referrer" loading="lazy"></a>`
  );
};

/** 围栏代码块：语言标记放 `data-lang`，样式那边靠它显示语言角标。 */
md.renderer.rules.fence = (tokens, index) => {
  const token = tokens[index]!;
  const lang = token.info.trim().split(/\s+/)[0] ?? "";
  const attr = lang ? ` data-lang="${md.utils.escapeHtml(lang)}"` : "";
  return `<pre${attr}><code>${md.utils.escapeHtml(token.content)}</code></pre>\n`;
};

/**
 * 行内代码里如果是个文件路径，渲染成可点的。
 *
 * 模型写完文件会说「已生成 `双色球最近30期开奖号码.md`」，而用户要做的下一件
 * 事十有八九就是打开它看看——在没有这条之前他得自己去 Finder 里找。
 *
 * 判断只看形状（末尾是扩展名，或者带 /），**不看文件存不存在**——渲染层不该
 * 去碰文件系统。真正的判断在主进程：路径按会话工作区解析、必须真的存在、
 * 可执行的那几类不给直接打开。所以这里认错了最多是点下去提示「找不到」，
 * 而模型伪造一个 `class="file-ref"` 也拿不到任何额外能力。
 */
md.renderer.rules.code_inline = (tokens, index) => {
  const text = tokens[index]!.content;
  const escaped = md.utils.escapeHtml(text);
  if (!looksLikePath(text)) return `<code>${escaped}</code>`;
  return `<code class="file-ref" title="点击打开">${escaped}</code>`;
};

/** 删除线统一成 `<del>`：markdown-it 默认出 `<s>`，样式那边只认 del。 */
md.renderer.rules.s_open = () => "<del>";
md.renderer.rules.s_close = () => "</del>";

/** 表格外面套一层能横向滚的容器，否则列多的表会把整页撑得横向滚动。 */
md.renderer.rules.table_open = () => '<div class="table-wrap"><table>';
md.renderer.rules.table_close = () => "</table></div>";

/** 任务列表：`- [ ]` / `- [x]` 渲染成只读复选框。 */
md.core.ruler.after("inline", "task-lists", (state) => {
  const tokens = state.tokens;
  for (let i = 2; i < tokens.length; i++) {
    if (tokens[i]!.type !== "inline") continue;
    if (tokens[i - 1]?.type !== "paragraph_open") continue;
    const item = tokens[i - 2];
    if (item?.type !== "list_item_open") continue;

    const match = /^\[([ xX])\]\s+/.exec(tokens[i]!.content);
    if (!match) continue;

    tokens[i]!.content = tokens[i]!.content.slice(match[0].length);
    const children = tokens[i]!.children;
    const first = children?.[0];
    if (first?.type === "text") {
      first.content = first.content.replace(/^\[([ xX])\]\s+/, "");
    }
    const box = new state.Token("html_inline", "", 0);
    box.content = `<input type="checkbox" disabled${match[1] === " " ? "" : " checked"}>`;
    children?.unshift(box);
    item.attrJoin("class", "task-item");
  }
  return true;
});

/**
 * 净化器。
 *
 * 白名单是**展示用的标签**：排版、表格、代码、列表、图片。刻意排除的几类：
 *   - `script` / `style`：一个执行代码，一个能改整页外观（覆盖弹窗、伪造界面）；
 *   - `iframe` / `object` / `embed`：能把外部页面嵌进同一个窗口；
 *   - `form` / `input`（复选框除外）/ `button`：能伪装成应用自己的表单骗输入；
 *   - 所有 `on*` 事件属性——DOMPurify 默认就不留，这里不放开。
 */
const PURIFY_CONFIG = {
  ALLOWED_TAGS: [
    "p", "br", "hr", "span", "div",
    "strong", "b", "em", "i", "del", "s", "mark", "sub", "sup", "small",
    "h1", "h2", "h3", "h4", "h5", "h6",
    "ul", "ol", "li", "dl", "dt", "dd",
    "blockquote", "pre", "code", "kbd", "samp", "var",
    "a", "img",
    "table", "thead", "tbody", "tfoot", "tr", "th", "td", "caption", "colgroup", "col",
    "details", "summary", "section", "article", "figure", "figcaption",
    "input", "abbr", "time",
  ],
  ALLOWED_ATTR: [
    "href", "target", "rel", "title",
    "src", "alt", "width", "height", "loading", "referrerpolicy",
    "class", "id", "style", "data-lang",
    "colspan", "rowspan", "align", "start", "reversed", "open",
    "type", "checked", "disabled", "datetime",
  ],
  // **不用 ALLOWED_URI_REGEXP。** 它不只作用在 href/src 上——DOMPurify 拿它去
  // 校验所有「看起来像 URI」的属性值，于是 `type="checkbox"` 会被一并删掉，
  // 任务列表的复选框就没了（实际踩过）。协议判断统一放在下面的钩子里，
  // 用文件顶上那两个函数，一处说了算。
  // 不允许自定义元素，免得混进 Vue 认得的标签名。
  CUSTOM_ELEMENT_HANDLING: { tagNameCheck: null, attributeNameCheck: null },
};

let purify: ReturnType<typeof createDOMPurify> | null = null;

function sanitizer(): ReturnType<typeof createDOMPurify> {
  if (purify) return purify;
  purify = createDOMPurify(globalThis.window as unknown as Window & typeof globalThis);

  // 净化之后再补一遍链接与图片的属性。DOMPurify 会重建节点，
  // markdown-it 加的 target/rel 在只写内联 HTML（`<a href=…>`）的情况下没有。
  purify.addHook("afterSanitizeAttributes", (node) => {
    if (node.tagName === "A") {
      // 协议不合规就把 href 摘掉，文字留着——变成不可点的普通文本，
      // 比留一个点了会出事的链接好，也比整段消失好。
      const href = node.getAttribute("href");
      if (href !== null && !isSafeHref(href)) node.removeAttribute("href");
      const kept = node.getAttribute("href");
      // 页内锚点不能加 target=_blank：那会走 setWindowOpenHandler，
      // 被当成外链交给系统打开，而它其实只是想跳到脚注那一行。
      if (kept !== null && !kept.startsWith("#")) {
        node.setAttribute("target", "_blank");
        node.setAttribute("rel", "noreferrer noopener");
      }
    }
    if (node.tagName === "IMG") {
      // 没有合规 src 的图整个删掉。留着是一个碎图标，比没有更难看，
      // 而且那通常正是注入尝试被剥干净之后的残骸。
      const src = node.getAttribute("src");
      if (src === null || !isSafeImage(src)) {
        node.remove();
        return;
      }
      node.setAttribute("referrerpolicy", "no-referrer");
      node.setAttribute("loading", "lazy");
    }
    // `<input>` 只留只读复选框：别的表单控件能伪装成应用自己的输入框。
    if (node.tagName === "INPUT") {
      if (node.getAttribute("type") !== "checkbox") node.remove();
      else node.setAttribute("disabled", "");
    }
  });
  return purify;
}

/**
 * 渲染成 HTML 片段，输出可以直接 v-html。
 *
 * 顺序是死的：**先 markdown-it 出 HTML，再整体过 DOMPurify**。
 * 反过来（先净化原文再解析）不行——净化器不认识 Markdown，
 * 而解析器会把净化后的实体又还原回标签。
 */
export function renderMarkdown(source: string): string {
  return sanitizer().sanitize(md.render(source), PURIFY_CONFIG) as unknown as string;
}
