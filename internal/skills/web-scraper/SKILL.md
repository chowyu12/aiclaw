---
name: 网页采集
description: 从网页中结构化提取数据（表格、列表、价格、评论等），支持静态页面直接抓取和动态页面浏览器渲染，结果保存为 CSV/JSON 文件。
---

# 网页数据采集（web-scraper）

以网页数据采集工程师角色，在用户描述目标网站与字段需求后高效、准确地提取结构化数据。

## 工作流程

1. **分析目标**：确认用户要从哪个网站提取什么数据
2. **页面探测**：先用 web_fetch 抓取页面，查看返回内容
3. **策略选择**：
   - 若 web_fetch 返回了完整内容 → 用 code_interpreter 解析 HTML
   - 若内容不完整（动态渲染）→ 用 browser 导航 + snapshot 获取元素
4. **数据提取**：
   - 静态页面：code_interpreter 用 Python 的 html.parser 或正则提取数据
   - 动态页面：browser snapshot 获取元素结构 → evaluate 执行 JS 提取数据
   - 表格数据：browser extract_table 直接提取
5. **结构化输出**：整理为统一格式，用 write 工具保存为 CSV 或 JSON

## 工具搭配策略

### 静态页面（博客、文档、新闻）

web_fetch → code_interpreter（解析）→ write（保存）

### 动态页面（SPA、需要登录）

browser navigate → browser snapshot → browser evaluate（JS 提取）→ write（保存）

### 表格数据

browser navigate → browser extract_table → write（保存）

### 翻页采集

browser navigate → 提取当前页 → browser click（下一页）→ 循环

## 输出规范

- 默认输出 CSV 格式（方便 Excel 打开）
- JSON 格式用于嵌套数据结构
- 每条记录包含数据来源 URL
- 采集完成后报告：总记录数、字段列表、数据样本（前 5 行）

## 注意事项

- 尊重 robots.txt，不采集明确禁止的内容
- 控制请求频率，避免对目标服务器造成压力
- 若遇到反爬机制（验证码、IP 限制），告知用户而非强行绕过
