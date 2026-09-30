package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
)

const (
	// xlsxDefaultRows 是不传 range 时读的行数。两百行足够看清一张表的结构，
	// 真要全量分析时模型会按提示分段读。
	xlsxDefaultRows = 200
	// xlsxMaxCols 是一次最多展示的列数。宽表（几百列的导出数据）全展开，
	// 一行就能吃掉上限的大半。
	xlsxMaxCols = 50
	// xlsxMaxRows 是一次最多读的行数，显式 range 也不超过它。
	xlsxMaxRows = 1000
	// xlsxMaxListed 是合并单元格、公式这类附注最多列多少条。
	xlsxMaxListed = 50
)

// ============================================================
// 读 xlsx
// ============================================================

func readXlsx(target, sheet, cellRange string) (string, error) {
	file, err := excelize.OpenFile(target)
	if err != nil {
		return "", fmt.Errorf("打不开 %s：%w", target, err)
	}
	defer file.Close()

	sheets := file.GetSheetList()
	if len(sheets) == 0 {
		return "（工作簿里没有工作表）", nil
	}
	if sheet == "" {
		sheet = sheets[0]
	}
	if index, err := file.GetSheetIndex(sheet); err != nil || index < 0 {
		return "", fmt.Errorf("没有名为 %q 的工作表；现有：%s", sheet, strings.Join(sheets, "、"))
	}

	// 先走一遍迭代器算出真实的行列数。<dimension> 元素是生成器自己写的，
	// 常常不准（有的干脆没有），不能拿来当「共多少行」。
	totalRows, totalCols, err := xlsxExtent(file, sheet)
	if err != nil {
		return "", err
	}

	firstRow, lastRow, firstCol, lastCol := 1, xlsxDefaultRows, 1, 0
	if strings.TrimSpace(cellRange) != "" {
		firstCol, firstRow, lastCol, lastRow, err = parseCellRange(cellRange)
		if err != nil {
			return "", err
		}
	}
	if lastRow > totalRows {
		lastRow = totalRows
	}
	if lastCol == 0 || lastCol > totalCols {
		lastCol = totalCols
	}
	rowsClipped := false
	if lastRow-firstRow+1 > xlsxMaxRows {
		// 显式给了很大的 range 也要收住：几万行逐格取值很慢，而结果反正会被截断。
		lastRow = firstRow + xlsxMaxRows - 1
		rowsClipped = true
	}
	colsClipped := false
	if lastCol-firstCol+1 > xlsxMaxCols {
		lastCol = firstCol + xlsxMaxCols - 1
		colsClipped = true
	}

	var out strings.Builder
	out.WriteString("工作表：")
	for index, name := range sheets {
		if index > 0 {
			out.WriteString("、")
		}
		if name == sheet {
			out.WriteString("[" + name + "]")
		} else {
			out.WriteString(name)
		}
	}
	fmt.Fprintf(&out, "\n当前工作表 %q：共 %d 行 × %d 列", sheet, totalRows, totalCols)
	if totalRows > 0 && totalCols > 0 {
		lastName, _ := excelize.CoordinatesToCellName(totalCols, totalRows)
		out.WriteString("（A1:" + lastName + "）")
	}
	out.WriteString("\n")

	if totalRows == 0 || firstRow > lastRow || firstCol > lastCol {
		out.WriteString("\n（这个区域没有数据）")
		return out.String(), nil
	}

	rows, formulas, err := xlsxBlock(file, sheet, firstRow, lastRow, firstCol, lastCol)
	if err != nil {
		return "", err
	}
	startName, _ := excelize.CoordinatesToCellName(firstCol, firstRow)
	endName, _ := excelize.CoordinatesToCellName(lastCol, lastRow)
	fmt.Fprintf(&out, "区域 %s:%s：\n\n", startName, endName)
	out.WriteString(mdTable(rows))

	if len(formulas) > 0 {
		out.WriteString("\n\n公式（单元格里显示的是计算值）：")
		for index, item := range formulas {
			if index >= xlsxMaxListed {
				fmt.Fprintf(&out, "\n……另有 %d 个公式未列出", len(formulas)-xlsxMaxListed)
				break
			}
			out.WriteString("\n- " + item)
		}
	}
	if merged, err := file.GetMergeCells(sheet); err == nil && len(merged) > 0 {
		out.WriteString("\n\n合并单元格（值只在左上角那一格）：")
		for index, cell := range merged {
			if index >= xlsxMaxListed {
				fmt.Fprintf(&out, "\n……另有 %d 处未列出", len(merged)-xlsxMaxListed)
				break
			}
			out.WriteString("\n- " + cell.GetStartAxis() + ":" + cell.GetEndAxis())
			if value := strings.TrimSpace(cell.GetCellValue()); value != "" {
				out.WriteString("（" + shortText(value, 40) + "）")
			}
		}
	}

	var more []string
	if rowsClipped || lastRow < totalRows {
		nextStart, _ := excelize.CoordinatesToCellName(firstCol, lastRow+1)
		nextEnd, _ := excelize.CoordinatesToCellName(lastCol, min(lastRow+xlsxDefaultRows, totalRows))
		more = append(more, fmt.Sprintf("只显示到第 %d 行（共 %d 行），用 range=%s:%s 读后面的", lastRow, totalRows, nextStart, nextEnd))
	}
	if colsClipped {
		more = append(more, fmt.Sprintf("一次最多显示 %d 列（共 %d 列），用 range 指定列区间读其余列", xlsxMaxCols, totalCols))
	}
	if len(more) > 0 {
		out.WriteString("\n\n[" + strings.Join(more, "；") + "]")
	}
	hint := fmt.Sprintf("用更小的 range 分段读，例如 %s:%s", startName, mustCellName(lastCol, firstRow+(lastRow-firstRow)/2))
	return clipOffice(out.String(), hint), nil
}

// shortText 取第一行、截到 limit 个字符，用在附注里。
func shortText(text string, limit int) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index] + "…"
	}
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

// xlsxExtent 数出有内容的最后一行和最宽的一行。
func xlsxExtent(file *excelize.File, sheet string) (int, int, error) {
	rows, err := file.Rows(sheet)
	if err != nil {
		return 0, 0, fmt.Errorf("读取工作表失败：%w", err)
	}
	defer rows.Close()
	lastRow, maxCols, current := 0, 0, 0
	for rows.Next() {
		current++
		columns, err := rows.Columns()
		if err != nil {
			return 0, 0, fmt.Errorf("读取工作表失败：%w", err)
		}
		// Columns 会去掉行尾空格，这里再确认一下整行是不是真的有字。
		width := 0
		for index, value := range columns {
			if value != "" {
				width = index + 1
			}
		}
		if width > 0 {
			lastRow = current
			maxCols = max(maxCols, width)
		}
	}
	return lastRow, maxCols, nil
}

// xlsxBlock 取出一块区域，带行号与列字母。
//
// 公式格：excelize 自己写的文件没有缓存值（它不算公式），Excel 保存过的才有；
// 没有缓存值时用 CalcCellValue 现算一次，算不出来就显示公式本身。
func xlsxBlock(file *excelize.File, sheet string, firstRow, lastRow, firstCol, lastCol int) ([][]string, []string, error) {
	header := []string{"行"}
	for col := firstCol; col <= lastCol; col++ {
		name, _ := excelize.ColumnNumberToName(col)
		header = append(header, name)
	}
	rows := [][]string{header}
	var formulas []string
	for row := firstRow; row <= lastRow; row++ {
		line := []string{fmt.Sprint(row)}
		for col := firstCol; col <= lastCol; col++ {
			cell, _ := excelize.CoordinatesToCellName(col, row)
			value, err := file.GetCellValue(sheet, cell)
			if err != nil {
				return nil, nil, fmt.Errorf("读取 %s 失败：%w", cell, err)
			}
			if formula, _ := file.GetCellFormula(sheet, cell); formula != "" {
				if value == "" {
					if computed, err := file.CalcCellValue(sheet, cell); err == nil {
						value = computed
					} else {
						value = "=" + formula
					}
				}
				formulas = append(formulas, fmt.Sprintf("%s = %s → %s", cell, formula, shortText(value, 60)))
			}
			line = append(line, value)
		}
		rows = append(rows, line)
	}
	return rows, formulas, nil
}

// parseCellRange 解析 A1:F200 / A1 / A:F / 3:10 这几种写法。
func parseCellRange(text string) (firstCol, firstRow, lastCol, lastRow int, err error) {
	text = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(text), "$", ""))
	if index := strings.LastIndex(text, "!"); index >= 0 {
		text = text[index+1:] // 容忍 Sheet1!A1:B2 这种带表名的写法
	}
	parts := strings.Split(text, ":")
	if len(parts) > 2 {
		return 0, 0, 0, 0, fmt.Errorf("range %q 不合法，应当形如 A1:F200", text)
	}
	parse := func(ref string, isEnd bool) (int, int, error) {
		letters := strings.TrimRightFunc(ref, unicode.IsDigit)
		digits := ref[len(letters):]
		col, row := 0, 0
		if letters != "" {
			n, err := excelize.ColumnNameToNumber(letters)
			if err != nil {
				return 0, 0, fmt.Errorf("range 里的列 %q 不合法", letters)
			}
			col = n
		}
		if digits != "" {
			if _, err := fmt.Sscan(digits, &row); err != nil || row < 1 {
				return 0, 0, fmt.Errorf("range 里的行 %q 不合法", digits)
			}
		}
		if col == 0 && row == 0 {
			return 0, 0, fmt.Errorf("range %q 不合法，应当形如 A1:F200", text)
		}
		if col == 0 && !isEnd {
			col = 1
		}
		if row == 0 {
			if isEnd {
				row = math.MaxInt32
			} else {
				row = 1
			}
		}
		return col, row, nil
	}
	if firstCol, firstRow, err = parse(parts[0], false); err != nil {
		return
	}
	if len(parts) == 1 {
		return firstCol, firstRow, firstCol, firstRow, nil
	}
	if lastCol, lastRow, err = parse(parts[1], true); err != nil {
		return
	}
	if lastCol != 0 && lastCol < firstCol {
		firstCol, lastCol = lastCol, firstCol
	}
	if lastRow < firstRow {
		firstRow, lastRow = lastRow, firstRow
	}
	return firstCol, firstRow, lastCol, lastRow, nil
}

func mustCellName(col, row int) string {
	name, err := excelize.CoordinatesToCellName(max(col, 1), max(row, 1))
	if err != nil {
		return "A1"
	}
	return name
}

// ============================================================
// 写 xlsx
// ============================================================

type xlsxSheetSpec struct {
	Name  string  `json:"name"`
	Rows  [][]any `json:"rows"`
	Start string  `json:"start"`
	Mode  string  `json:"mode"`
}

func writeXlsxTool() Tool {
	cell := map[string]any{
		"description": "单元格：字符串、数字、布尔或 null。以 = 开头的字符串是公式（如 =SUM(B2:B10)）；要写字面上的 = 开头文字，前面加一个单引号",
	}
	return Tool{
		Name: "write_xlsx",
		Description: "写 Excel 文件（.xlsx）。文件不存在就新建；已存在就只改 sheets 里列出的工作表，其余工作表原样保留。" +
			"同名工作表默认先清空再写（mode=replace），mode=update 只覆盖写到的单元格。" +
			"公式写成以 = 开头的字符串，打开文件时 Excel 会自动重算。写到工作区之外会先请用户确认。",
		Effect: EffectWrite,
		Schema: schema(map[string]any{
			"path": map[string]any{"type": "string", "description": "输出路径，扩展名 .xlsx 或 .xlsm。相对路径按工作区解析"},
			"sheets": map[string]any{
				"type": "array", "minItems": 1,
				"description": "要写的工作表",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string", "description": "工作表名称（最多 31 个字符，不能含 : \\ / ? * [ ]）"},
						"rows": map[string]any{
							"type": "array", "description": "按行给出的单元格值，二维数组",
							"items": map[string]any{"type": "array", "items": cell},
						},
						"start": map[string]any{"type": "string", "description": "左上角单元格，默认 A1"},
						"mode": map[string]any{
							"type": "string", "enum": []string{"replace", "update"},
							"description": "工作表已存在时：replace 先清空（默认），update 只覆盖写到的格子",
						},
					},
					"required":             []string{"name", "rows"},
					"additionalProperties": false,
				},
			},
			"header_bold": map[string]any{"type": "boolean", "description": "每个工作表写入区域的第一行加粗（当表头），默认 false"},
			"auto_width":  map[string]any{"type": "boolean", "description": "按内容粗略调整列宽，默认 true"},
		}, "path", "sheets"),
		Handler: func(ctx context.Context, raw json.RawMessage, env *Env) (string, error) {
			var args struct {
				Path       string          `json:"path"`
				Sheets     []xlsxSheetSpec `json:"sheets"`
				HeaderBold bool            `json:"header_bold"`
				AutoWidth  *bool           `json:"auto_width"`
			}
			if err := decodeArgs(raw, &args); err != nil {
				return "", err
			}
			if err := requireExt(args.Path, ".xlsx", ".xlsm"); err != nil {
				return "", err
			}
			if len(args.Sheets) == 0 {
				return "", errors.New("sheets 不能为空")
			}
			seen := map[string]bool{}
			for _, spec := range args.Sheets {
				key := strings.ToLower(strings.TrimSpace(spec.Name))
				if key == "" {
					return "", errors.New("每个工作表都要有 name")
				}
				if seen[key] {
					return "", fmt.Errorf("工作表 %q 重复出现", spec.Name)
				}
				seen[key] = true
				if spec.Mode != "" && spec.Mode != "replace" && spec.Mode != "update" {
					return "", fmt.Errorf("mode 只能是 replace 或 update，收到 %q", spec.Mode)
				}
			}
			autoWidth := args.AutoWidth == nil || *args.AutoWidth
			target, err := approveOfficeWrite(ctx, env, args.Path, "写入 Excel 文件", func(target string) error {
				// 已有文件是改了扩展名的 .xls 时，excelize 只会报一句看不懂的 zip 错误。
				if fileExists(target) {
					return checkNotOLE(target)
				}
				return nil
			})
			if err != nil {
				return "", err
			}
			summary, err := writeXlsx(target, args.Sheets, args.HeaderBold, autoWidth)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("已写入 %s：%s", args.Path, summary), nil
		},
	}
}

func fileExists(target string) bool {
	info, err := os.Stat(target)
	return err == nil && !info.IsDir()
}

// writeXlsx 新建或更新工作簿。更新时用 excelize 打开再保存，没列出的工作表、
// 样式、定义名称都原样留着；写完落盘同样走「临时文件 + 改名」。
func writeXlsx(target string, specs []xlsxSheetSpec, headerBold, autoWidth bool) (string, error) {
	existed := fileExists(target)
	var file *excelize.File
	if existed {
		opened, err := excelize.OpenFile(target)
		if err != nil {
			return "", fmt.Errorf("打不开已有的 %s：%w", target, err)
		}
		file = opened
	} else {
		file = excelize.NewFile()
	}
	defer file.Close()

	// 新工作簿自带一个空的 Sheet1。第一个要写的表直接占用它，
	// 否则成品里会多出一张没人要的空表。
	if !existed && !strings.EqualFold(specs[0].Name, "Sheet1") {
		if err := file.SetSheetName("Sheet1", specs[0].Name); err != nil {
			return "", fmt.Errorf("工作表名 %q 不可用：%w", specs[0].Name, err)
		}
	}

	boldStyle := 0
	if headerBold {
		style, err := file.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
		if err != nil {
			return "", err
		}
		boldStyle = style
	}

	var notes []string
	for _, spec := range specs {
		note, err := writeXlsxSheet(file, spec, boldStyle, autoWidth)
		if err != nil {
			return "", err
		}
		notes = append(notes, note)
	}
	// excelize 不计算公式、也不写缓存值；让 Excel/WPS 打开时全量重算，
	// 否则公式格会显示成空白直到用户手动按一次重算。
	fullCalc := true
	if err := file.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: &fullCalc}); err != nil {
		return "", err
	}
	buffer, err := file.WriteToBuffer()
	if err != nil {
		return "", fmt.Errorf("生成 xlsx 失败：%w", err)
	}
	if err := writeFileAtomic(target, buffer.Bytes()); err != nil {
		return "", err
	}
	action := "新建工作簿"
	if existed {
		action = "更新工作簿"
	}
	return action + "，" + strings.Join(notes, "；"), nil
}

func writeXlsxSheet(file *excelize.File, spec xlsxSheetSpec, boldStyle int, autoWidth bool) (string, error) {
	name := strings.TrimSpace(spec.Name)
	index, err := file.GetSheetIndex(name)
	if err != nil {
		return "", fmt.Errorf("工作表名 %q 不可用：%w", name, err)
	}
	created := index < 0
	if created {
		if _, err := file.NewSheet(name); err != nil {
			return "", fmt.Errorf("工作表名 %q 不可用：%w", name, err)
		}
	} else if spec.Mode != "update" {
		if err := clearXlsxSheet(file, name); err != nil {
			return "", err
		}
	}

	startCol, startRow := 1, 1
	if strings.TrimSpace(spec.Start) != "" {
		col, row, err := excelize.CellNameToCoordinates(strings.ToUpper(strings.TrimSpace(spec.Start)))
		if err != nil {
			return "", fmt.Errorf("start %q 不是合法的单元格地址", spec.Start)
		}
		startCol, startRow = col, row
	}

	widths := map[int]float64{}
	formulas, cells, maxCols := 0, 0, 0
	for r, row := range spec.Rows {
		maxCols = max(maxCols, len(row))
		for c, value := range row {
			cell, err := excelize.CoordinatesToCellName(startCol+c, startRow+r)
			if err != nil {
				return "", fmt.Errorf("单元格超出表格范围：%w", err)
			}
			display, isFormula, err := setXlsxCell(file, name, cell, value)
			if err != nil {
				return "", fmt.Errorf("写 %s!%s 失败：%w", name, cell, err)
			}
			if value != nil {
				cells++
			}
			if isFormula {
				formulas++
				display = "0000000000" // 公式的结果长度未知，按一个普通数字的宽度估
			}
			widths[startCol+c] = max(widths[startCol+c], displayWidth(display))
		}
	}
	if boldStyle != 0 && len(spec.Rows) > 0 && len(spec.Rows[0]) > 0 {
		first, _ := excelize.CoordinatesToCellName(startCol, startRow)
		last, _ := excelize.CoordinatesToCellName(startCol+len(spec.Rows[0])-1, startRow)
		if err := file.SetCellStyle(name, first, last, boldStyle); err != nil {
			return "", err
		}
	}
	if autoWidth {
		for col, width := range widths {
			letters, _ := excelize.ColumnNumberToName(col)
			// 宽度单位约等于一个西文字符；两头留点余量，太宽的截在 60。
			if err := file.SetColWidth(name, letters, letters, math.Min(math.Max(width+2, 8), 60)); err != nil {
				return "", err
			}
		}
	}
	action := "覆盖"
	switch {
	case created:
		action = "新建"
	case spec.Mode == "update":
		action = "更新"
	}
	note := fmt.Sprintf("%s工作表 %q（%d 行 × %d 列，%d 个单元格", action, name, len(spec.Rows), maxCols, cells)
	if formulas > 0 {
		note += fmt.Sprintf("，其中 %d 个公式", formulas)
	}
	return note + "）", nil
}

// setXlsxCell 按 JSON 值的类型写一个格子，返回用来估列宽的显示文字。
func setXlsxCell(file *excelize.File, sheet, cell string, value any) (string, bool, error) {
	switch v := value.(type) {
	case nil:
		return "", false, file.SetCellValue(sheet, cell, nil)
	case string:
		if strings.HasPrefix(v, "=") && len(v) > 1 {
			return v, true, file.SetCellFormula(sheet, cell, v[1:])
		}
		if strings.HasPrefix(v, "'=") {
			v = v[1:] // 单引号开头是「按字面写」，与 Excel 自己的约定一致
		}
		return v, false, file.SetCellStr(sheet, cell, v)
	case float64:
		// JSON 数字一律解成 float64；整数就按整数写，免得出现 3.0000000001 这类显示。
		if v == math.Trunc(v) && math.Abs(v) < 1e15 {
			return fmt.Sprint(int64(v)), false, file.SetCellInt(sheet, cell, int64(v))
		}
		return fmt.Sprint(v), false, file.SetCellFloat(sheet, cell, v, -1, 64)
	case bool:
		return fmt.Sprint(v), false, file.SetCellBool(sheet, cell, v)
	default:
		encoded, _ := json.Marshal(v)
		return string(encoded), false, file.SetCellStr(sheet, cell, string(encoded))
	}
}

// clearXlsxSheet 清空一张已有工作表的内容与合并单元格，工作表本身（位置、
// 名字、别的表对它的引用）保留。
//
// 不用「删表再建」：删表会让别的表里指向它的公式与定义名称失效，
// 而用户要的只是「这张表的数据换掉」。
func clearXlsxSheet(file *excelize.File, sheet string) error {
	if merged, err := file.GetMergeCells(sheet, true); err == nil {
		for _, cell := range merged {
			if err := file.UnmergeCell(sheet, cell.GetStartAxis(), cell.GetEndAxis()); err != nil {
				return err
			}
		}
	}
	lastRow, lastCol, err := xlsxExtent(file, sheet)
	if err != nil {
		return err
	}
	// 迭代器只看得到有值的格子；没有缓存值的公式格（excelize 写的就是这样）
	// 看起来是空的，所以再和 <dimension> 取并集，免得漏清公式。
	if dimension, err := file.GetSheetDimension(sheet); err == nil && dimension != "" {
		if _, _, col, row, err := parseCellRange(dimension); err == nil && row < math.MaxInt32 {
			lastRow, lastCol = max(lastRow, row), max(lastCol, col)
		}
	}
	for row := 1; row <= lastRow; row++ {
		for col := 1; col <= lastCol; col++ {
			cell, _ := excelize.CoordinatesToCellName(col, row)
			if err := file.SetCellValue(sheet, cell, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// displayWidth 粗估显示宽度：中日韩文字算两个字符宽。
func displayWidth(text string) float64 {
	width := 0.0
	for _, line := range strings.Split(text, "\n") {
		current := 0.0
		for _, r := range line {
			if r > 0x2E80 {
				current += 2
			} else {
				current++
			}
		}
		width = math.Max(width, current)
	}
	return width
}
