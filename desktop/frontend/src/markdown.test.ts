// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import { renderMarkdown } from "./markdown";

describe("renderMarkdown", () => {
  it("renders GFM structure, code and safe inline HTML", () => {
    const output = renderMarkdown(`## 审查结果

**重点**和<mark>原始 HTML</mark>

- 第一项
- 第二项

| 文件 | 状态 |
| --- | --- |
| report.pdf | 通过 |

\`inline()\`

\`\`\`go
fmt.Println("ok")
\`\`\``);

    expect(output).toContain("<h2>审查结果</h2>");
    expect(output).toContain("<strong>重点</strong>");
    expect(output).toContain("<mark>原始 HTML</mark>");
    expect(output).toContain("<ul>");
    expect(output).toContain("<table>");
    expect(output).toContain('<code class="language-go">');
  });

  it("removes executable and layout-breaking HTML", () => {
    const output = renderMarkdown(`<script>alert(1)</script>
<img src="x" onerror="alert(1)" style="position:fixed">
<a href="javascript:alert(1)" onclick="alert(1)">bad</a>
<iframe src="https://example.com"></iframe>
<form><input value="secret"></form>`);

    expect(output).not.toMatch(/script|onerror|onclick|javascript:|style=|iframe|form|input/i);
    expect(output).toContain('<img src="x">');
    expect(output).toContain("<a>bad</a>");
  });
});
