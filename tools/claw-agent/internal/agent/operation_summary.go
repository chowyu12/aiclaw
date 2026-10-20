package agent

import (
	"regexp"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"

	"github.com/chowyu12/aiclaw/internal/i18n"
)

var codeToolCall = regexp.MustCompile(`tools\.([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

const codeStringLiteral = "\"(?:\\\\.|[^\"\\\\])*\"|'(?:\\\\.|[^'\\\\])*'|`[^`]*`"

// 只识别直接传入的平铺对象，复杂对象退回动作类型，避免拿到后面别的调用的参数。
var codeObjectArg = regexp.MustCompile("(?s)^\\s*\\{(?:" + codeStringLiteral + "|[^{}'\"`])*\\}\\s*\\)")
var codeOperationArg = regexp.MustCompile("(?:[,{])\\s*[\"']?([A-Za-z_][A-Za-z0-9_]*)[\"']?\\s*:\\s*(" + codeStringLiteral + ")")

// 兼容没有 title 的历史调用：只读静态参数，不执行脚本或猜测动态变量的值。
func summarizeCode(code string) string {
	calls := codeToolCall.FindAllStringSubmatchIndex(code, -1)
	var operations []string
	seen := map[string]bool{}
	for index, call := range calls {
		name := code[call[2]:call[3]]
		if seen[name] {
			continue
		}
		seen[name] = true
		end := len(code)
		if index+1 < len(calls) {
			end = calls[index+1][0]
		}
		args := map[string]string{}
		body := codeObjectArg.FindString(code[call[1]:end])
		for _, match := range codeOperationArg.FindAllStringSubmatchIndex(body, -1) {
			key, literal := body[match[2]:match[3]], body[match[4]:match[5]]
			if rest := strings.TrimSpace(body[match[5]:]); !strings.HasPrefix(rest, ",") && !strings.HasPrefix(rest, "}") {
				continue // 字符串拼接仍是动态表达式，不能只显示其中的常量片段。
			}
			// JS 支持单引号与 Unicode 转义；用现有解析器解码字面量，不运行它。
			program, err := parser.ParseFile(nil, "", "("+literal+")", 0)
			if err != nil || len(program.Body) != 1 {
				continue
			}
			statement, ok := program.Body[0].(*ast.ExpressionStatement)
			if !ok {
				continue
			}
			switch value := statement.Expression.(type) {
			case *ast.StringLiteral:
				args[key] = value.Value.String()
			case *ast.TemplateLiteral:
				if value.Tag == nil && len(value.Expressions) == 0 && len(value.Elements) == 1 && value.Elements[0].Valid {
					args[key] = value.Elements[0].Parsed.String()
				}
			}
		}
		action, detail := "", ""
		switch {
		case name == "run_command":
			if reason := strings.TrimSpace(args["reason"]); reason != "" {
				operations = append(operations, reason)
				continue
			}
			action, detail = i18n.D("执行命令"), args["command"]
		case name == "web_search" || strings.HasSuffix(name, "__web_search"):
			action, detail = i18n.D("联网搜索"), args["query"]
		case name == "read_file":
			action, detail = i18n.D("读取文件"), args["path"]
		case name == "write_file":
			action, detail = i18n.D("写入文件"), args["path"]
		case name == "edit_file":
			action, detail = i18n.D("修改文件"), args["path"]
		case name == "list_dir":
			action, detail = i18n.D("列出目录"), args["path"]
		case name == "search_files":
			action, detail = i18n.D("搜索文件"), args["pattern"]
		default:
			action = i18n.D("调用 {tools}", "tools", name)
		}
		if strings.TrimSpace(detail) != "" {
			action = i18n.D("{action}：{detail}", "action", action, "detail", detail)
		}
		operations = append(operations, action)
	}
	if len(operations) == 0 {
		return i18n.D("运行代码脚本")
	}
	return firstLine(strings.Join(operations, "；"), 100)
}
