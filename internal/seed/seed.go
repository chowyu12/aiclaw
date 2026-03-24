package seed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/store"
	"github.com/chowyu12/aiclaw/internal/workspace"
)

func Init(ctx context.Context, s store.Store) {
	seedTools(ctx, s)
	seedSkillDirs()
	log.Info("seed data initialized")
}

func seedTools(ctx context.Context, s store.Store) {
	for _, def := range defaultTools() {
		existing, _, _ := s.ListTools(ctx, model.ListQuery{Page: 1, PageSize: 1, Keyword: def.Name})
		for _, t := range existing {
			if t.Name == def.Name {
				goto next
			}
		}
		if err := s.CreateTool(ctx, &def); err != nil {
			log.WithFields(log.Fields{"name": def.Name, "error": err}).Warn("seed tool failed")
		} else {
			log.WithField("name", def.Name).Info("seed tool created")
		}
	next:
	}
}

func seedSkillDirs() {
	skillsDir := workspace.Skills()
	if skillsDir == "" {
		log.Warn("workspace not initialized, skip filesystem skill seed")
		return
	}

	for _, def := range builtinSkillDefs() {
		dirPath := filepath.Join(skillsDir, def.DirName)

		if _, err := os.Stat(filepath.Join(dirPath, "manifest.json")); err == nil {
			continue
		}

		os.MkdirAll(dirPath, 0o755)

		manifest := model.SkillManifest{
			Name:        def.Name,
			Version:     "1.0.0",
			Description: def.Description,
			Author:      "system",
		}
		if data, err := json.MarshalIndent(manifest, "", "  "); err == nil {
			os.WriteFile(filepath.Join(dirPath, "manifest.json"), data, 0o644)
		}
		if def.Instruction != "" {
			os.WriteFile(filepath.Join(dirPath, "SKILL.md"), []byte(def.Instruction), 0o644)
		}

		log.WithField("name", def.Name).Info("seed skill dir created")
	}
}

func mustJSON(v any) model.JSON {
	data, _ := json.Marshal(v)
	return model.JSON(data)
}

type builtinSkill struct {
	Name        string
	DirName     string
	Description string
	Instruction string
}

func builtinSkillDefs() []builtinSkill {
	return []builtinSkill{
		{
			Name:        "深度研究",
			DirName:     "deep-research",
			Description: "针对指定主题进行多源信息采集与整合，自动抓取多个网页、交叉验证、整理为结构化研究报告并保存到文件。",
			Instruction: `---
name: deep-research
description: Conducts systematic web research with multi-source gathering, cross-validation, and structured reports saved to files. Use when the user asks for deep research, topic investigation, source comparison, or evidence-backed analysis.
---

# 深度研究（deep-research）

以研究分析师角色，在用户给出主题后系统性地搜集、验证并整合信息。

## 工作流程

1. **拆解问题**：将主题拆解为 3–5 个子问题或关键维度
2. **多源采集**：对每个子问题，用 web_fetch 抓取 2–3 个不同来源的页面
3. **交叉验证**：对比多个来源的信息，标注一致和矛盾之处
4. **深度补充**：若 web_fetch 返回内容不完整，用 browser 导航到页面提取更多细节
5. **整合输出**：将所有发现整合为结构化报告，用 write 工具保存到文件

## 报告格式

- **主题概述**：一段话概括研究对象
- **关键发现**：按子问题分节，每节含事实、数据、来源
- **对比分析**：不同来源的一致/矛盾之处
- **结论与建议**：基于事实的判断和建议
- **参考来源**：所有访问过的 URL 列表

## 原则

- 只引用从工具获取的真实信息，不编造
- 明确标注信息来源
- 对不确定的内容标记为「待验证」
- 若某个来源无法访问，记录并尝试替代来源
`,
		},
		{
			Name:        "定时任务",
			DirName:     "cron-task",
			Description: "根据用户的自然语言描述，自动生成 Shell 脚本并配置 cron 定时执行，支持设置唤醒提醒事件。",
			Instruction: `---
name: cron-task
description: Turns natural-language scheduling needs into Linux cron jobs using the cron tool (schedule, list, remove, add_event). Use when the user wants crontab-style tasks, periodic scripts, or timed reminders.
---

# Linux 定时任务（cron-task）

以 Linux 定时任务专家角色，将用户用自然语言描述的需求落地为可调度脚本与 cron 配置。

## 工作流程

1. **理解需求**：确认要做什么、多久执行一次、在哪个时区
2. **编写脚本并调度**：用 cron 工具的 `schedule` 动作，传入 `expression` + `content`（脚本内容）+ `name`（脚本名）一步完成
3. **确认结果**：用 cron 工具的 `list` 动作展示当前所有定时任务，让用户确认
4. **提醒类需求**：若用户只想设置提醒/闹钟，用 cron 工具的 `add_event` 动作创建唤醒事件

## 脚本编写规范

- 开头加 `set -eo pipefail`，遇到错误立即停止
- 关键操作前后加 `echo` 打印进度日志（带时间戳）
- 涉及文件操作时先检查路径是否存在
- 涉及清理/删除操作时加安全校验（路径非空、非根目录等）
- 需要的环境变量在脚本顶部用变量声明
- 添加简要注释说明脚本用途

## 输出格式

完成后汇总告知用户：

- 脚本路径
- Cron 表达式及含义
- 如何查看/修改/删除该定时任务（`cron list` / `cron remove`）

## 安全原则

- 不要执行 `rm -rf /` 等危险命令
- 清理任务要限定明确的目录范围
- 涉及数据库操作建议先备份
- 建议用户开启 `log_output` 以便排查问题
`,
		},
		{
			Name:        "系统运维",
			DirName:     "sysops",
			Description: "系统健康检查、日志分析、进程管理和故障排查。通过 exec/process/read/grep 工具组合，快速定位和解决服务器问题。",
			Instruction: `---
name: sysops
description: Diagnoses and resolves Linux system issues using shell commands, logs, systemd, and network tooling. Use when troubleshooting servers, CPU/memory/disk, processes, ports, services, or journal logs.
---

# Linux 系统运维（sysops）

以资深 Linux 系统运维工程师角色，在用户描述系统问题后系统性地诊断与处理。

## 诊断工具箱

按以下顺序使用工具排查问题。

### 系统概览

- exec: `uname -a && uptime`（系统版本和运行时间）
- exec: `free -h`（内存使用）
- exec: `df -h`（磁盘使用）
- exec: `top -bn1 | head -20`（CPU 和进程概览）

### 进程排查

- exec: `ps aux --sort=-%mem | head -20`（内存占用 Top）
- exec: `ps aux --sort=-%cpu | head -20`（CPU 占用 Top）
- exec: `lsof -i :端口号`（端口占用排查）
- exec: `netstat -tlnp` 或 `ss -tlnp`（监听端口列表）

### 日志分析

- grep: 在日志文件中搜索 error/fatal/panic 等关键词
- read: 读取关键日志文件的最后若干行（tail 效果）
- exec: `journalctl -u 服务名 --since "1 hour ago"`（systemd 服务日志）

### 服务管理

- exec: `systemctl status 服务名`（服务状态）
- process: 启动后台监控命令（如 `tail -f`）

## 工作原则

1. **先诊断后操作**：先收集足够的信息再给出解决方案
2. **最小影响**：优先选择影响最小的修复手段
3. **操作确认**：执行任何修改操作前先告知用户具体命令和影响
4. **留痕记录**：重要操作用 write 工具记录操作日志
5. **安全兜底**：修改配置前建议先备份（`cp 原文件 原文件.bak`）
`,
		},
		{
			Name:        "数据处理",
			DirName:     "data-pipeline",
			Description: "CSV/JSON/Excel 等数据文件的导入、清洗、转换、统计和导出。通过 code_interpreter 编写处理脚本，结合 read/write 工具完成端到端的数据流水线。",
			Instruction: `---
name: data-pipeline
description: Builds data pipelines with Python (e.g. pandas) for cleaning, transformation, statistics, visualization, and file export. Use when the user provides datasets or asks for ETL, aggregation, format conversion, or batch analysis.
---

# 数据流水线（data-pipeline）

以数据工程专家角色，在用户给出数据文件或处理需求后编写代码完成流水线。

## 工作流程

1. **理解数据**：用 read 工具查看数据文件前几行，了解结构和字段
2. **确认需求**：与用户确认要做的处理（清洗、转换、统计、合并等）
3. **编写脚本**：用 code_interpreter 编写 Python 代码完成处理
4. **验证结果**：输出处理后的数据样本，让用户确认
5. **导出文件**：用 write 工具将最终结果保存为用户需要的格式

## 常见任务

### 数据清洗

- 去重、空值处理、类型转换
- 异常值检测和处理
- 字段标准化（日期格式、编码等）

### 数据转换

- CSV ↔ JSON ↔ Excel 格式转换
- 字段映射、拆分、合并
- 数据透视和聚合

### 统计分析

- 描述性统计（均值、中位数、标准差、分位数）
- 分组统计和交叉分析
- 趋势分析和同比/环比计算

### 数据可视化

- 用 code_interpreter 生成 matplotlib/plotly 图表
- 统计报表输出

## 编码规范

- Python 优先，使用 pandas 进行数据处理
- 代码中添加数据校验步骤（行数、列数、空值统计）
- 大文件分块读取，避免内存溢出
- 输出前打印数据样本和统计摘要供用户确认
`,
		},
		{
			Name:        "网页采集",
			DirName:     "web-scraper",
			Description: "从网页中结构化提取数据（表格、列表、价格、评论等），支持静态页面直接抓取和动态页面浏览器渲染，结果保存为 CSV/JSON 文件。",
			Instruction: `---
name: web-scraper
description: Extracts structured data from websites using web_fetch, browser automation, and Python parsing. Use when the user wants web scraping, crawling, table extraction, or data from static or dynamic (SPA) pages.
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
`,
		},
	}
}
