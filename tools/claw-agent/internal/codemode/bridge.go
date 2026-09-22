package codemode

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dop251/goja"
)

/*
脚本里那个 `tools` 对象。

做成动态对象而不是一个普通的 `vm.NewObject()`，是为了让**取错名字**这件事
当场说清楚。实测里模型把描述里那行 `- tools.corpus__4(...)` 整串抄进了表：

	const libs = [["tools.corpus__3", "业务术语"], …];
	await tools[name]({ … })          // name 是 "tools.corpus__3"

普通对象上这就是 `undefined`，调用它得到的是「undefined is not a function」——
那句话里没有任何线索指向「键不该带 tools. 前缀」。模型又把它 try/catch 收掉了，
于是四个库全部记成 ERR，它据此判定「语料库工具坏了」，接着花三轮去试探，
最后才偶然改成 `tools.corpus__4` 这种直接写法。三轮白跑，而错误就在那个前缀上。

所以两件事一起做：带 `tools.` 前缀的键**直接认**（模型的意思毫无歧义），
认不出来的键返回一个「一调就抛」的函数，异常里带上名字与最接近的几个候选。
*/

// reservedKeys 是不能当成工具名解释的属性。
//
// `then` 尤其要排掉：返回一个函数会让 `tools` 变成 thenable，
// 任何 await 到它的地方都会永远挂着。其余几个是 JS 引擎与 JSON 序列化
// 会顺手探一探的属性，返回抛异常的函数会把本来正常的代码弄崩。
var reservedKeys = map[string]bool{
	"then": true, "catch": true, "finally": true,
	"constructor": true, "prototype": true, "toJSON": true,
	"toString": true, "valueOf": true, "inspect": true,
	"length": true, "name": true, "call": true, "apply": true, "bind": true,
}

type toolBridge struct {
	vm      *goja.Runtime
	entries map[string]goja.Value
	names   []string
}

func newToolBridge(vm *goja.Runtime) *toolBridge {
	return &toolBridge{vm: vm, entries: map[string]goja.Value{}}
}

func (b *toolBridge) add(ident string, fn goja.Value) {
	b.entries[ident] = fn
	b.names = append(b.names, ident)
}

func (b *toolBridge) Get(key string) goja.Value {
	if fn, ok := b.entries[key]; ok {
		return fn
	}
	// 文档里写的是 `tools.corpus__4(...)`，模型按变量取值时经常把整串当键。
	if trimmed, cut := strings.CutPrefix(key, "tools."); cut {
		if fn, ok := b.entries[trimmed]; ok {
			return fn
		}
	}
	if !looksLikeToolKey(key) {
		return nil
	}
	return b.missing(key)
}

func (b *toolBridge) Set(string, goja.Value) bool { return false }
func (b *toolBridge) Delete(string) bool          { return false }
func (b *toolBridge) Keys() []string              { return b.names }

func (b *toolBridge) Has(key string) bool {
	if _, ok := b.entries[key]; ok {
		return true
	}
	trimmed, cut := strings.CutPrefix(key, "tools.")
	if !cut {
		return false
	}
	_, ok := b.entries[trimmed]
	return ok
}

// missing 返回一个「一调就抛」的函数，异常里说清是哪个名字错了。
//
// 不返回 undefined 是因为那条路的报错（undefined is not a function）
// 指向调用点，而真正错的是名字。
func (b *toolBridge) missing(key string) goja.Value {
	message := fmt.Sprintf("没有名为 %s 的工具", key)
	if strings.HasPrefix(key, "tools.") {
		message += "（tools[名字] 里的名字不带 tools. 前缀）"
	}
	if candidates := b.suggest(key); len(candidates) > 0 {
		message += "；最接近的是 " + strings.Join(candidates, "、")
	} else {
		message += "；用 ALL_TOOLS 查有哪些工具"
	}
	return b.vm.ToValue(func(goja.FunctionCall) goja.Value {
		panic(b.vm.NewTypeError(message))
	})
}

// suggest 挑几个名字上沾边的工具。
//
// 按「最长公共前缀」排，因为错法几乎都发生在名字**后半段**：模型记住了
// server 前缀（corpus__、web__），编错的是后面那个 id。包含匹配对这种
// 错法完全无效——corpus__99 里并不含 corpus__4。
func (b *toolBridge) suggest(key string) []string {
	needle := strings.ToLower(strings.TrimPrefix(key, "tools."))
	if needle == "" {
		return nil
	}
	type scored struct {
		name  string
		match int
	}
	var hits []scored
	for _, name := range b.names {
		lower := strings.ToLower(name)
		match := commonPrefix(lower, needle)
		if strings.Contains(lower, needle) || strings.Contains(needle, lower) {
			match = max(match, len(needle))
		}
		if match >= 3 {
			hits = append(hits, scored{name: name, match: match})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].match != hits[j].match {
			return hits[i].match > hits[j].match
		}
		return hits[i].name < hits[j].name
	})
	var names []string
	for _, hit := range hits {
		names = append(names, hit.name)
		if len(names) == 3 {
			break
		}
	}
	return names
}

func commonPrefix(a, b string) int {
	limit := min(len(a), len(b))
	index := 0
	for index < limit && a[index] == b[index] {
		index++
	}
	return index
}

// looksLikeToolKey 判断这个属性名是不是「模型想调一个工具」。
//
// 不像的（symbol 的字符串形态、引擎内部属性）返回 undefined 走原路，
// 免得把正常代码变成异常。
func looksLikeToolKey(key string) bool {
	if key == "" || reservedKeys[key] {
		return false
	}
	for index, char := range key {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char == '_':
		case char >= '0' && char <= '9' && index > 0:
		case char == '.' && index > 0:
		default:
			return false
		}
	}
	return true
}
