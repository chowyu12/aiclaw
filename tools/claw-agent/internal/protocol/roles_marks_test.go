package protocol

import "testing"

// 清单项的解析。写错了不会报错，只会「我明明标了视觉，它还是走转述」或者
// 「窗口填了却还是被动压缩」——那两种用户都无从排查，所以逐条钉。

func TestParseModelMark(t *testing.T) {
	for _, tc := range []struct {
		entry   string
		name    string
		roles   []ModelRole
		context int
		why     string
	}{
		{"gpt-4.1", "gpt-4.1", nil, 0, "光名字：旧记录，只做对话、窗口未知"},
		{" gpt-4.1 ", "gpt-4.1", nil, 0, "首尾空格是手写清单里最常见的"},
		{"qwen3-vl#vision", "qwen3-vl", []ModelRole{RoleVision}, 0, "只有能力"},
		{"m@131072", "m", nil, 131072, "只有窗口"},
		{"m#vision,image@200000", "m", []ModelRole{RoleVision, RoleImage}, 200000, "两段都有"},
		{"m#VISION", "m", []ModelRole{RoleVision}, 0, "大小写不该影响"},
		{"m# vision , image ", "m", []ModelRole{RoleVision, RoleImage}, 0, "标记里的空格"},
		{"m#vision,vision", "m", []ModelRole{RoleVision}, 0, "重复的只算一次"},
		{"m#nonsense", "m", nil, 0, "不认识的标记丢掉，不是报错——清单是手写的"},
		{"m#", "m", nil, 0, "空标记"},
		{"m@", "m", nil, 0, "空窗口"},
		{"m@abc", "m", nil, 0, "窗口不是数字时当作没写，而不是 0 以外的怪值"},
		{"m@-5", "m", nil, 0, "负数窗口没有意义"},
	} {
		got := ParseModelMark(tc.entry)
		if got.Name != tc.name {
			t.Errorf("%q 的名字 = %q，想要 %q（%s）", tc.entry, got.Name, tc.name, tc.why)
		}
		if got.Context != tc.context {
			t.Errorf("%q 的窗口 = %d，想要 %d（%s）", tc.entry, got.Context, tc.context, tc.why)
		}
		if len(got.Roles) != len(tc.roles) {
			t.Errorf("%q 的能力 = %v，想要 %v（%s）", tc.entry, got.Roles, tc.roles, tc.why)
			continue
		}
		for i := range got.Roles {
			if got.Roles[i] != tc.roles[i] {
				t.Errorf("%q 的能力 = %v，想要 %v（%s）", tc.entry, got.Roles, tc.roles, tc.why)
				break
			}
		}
	}
}

func TestFormatModelMarkRoundTrips(t *testing.T) {
	// 顺序固定：同一份配置每次写出来都一样，否则每次同步都让配置库无谓地变动。
	got := FormatModelMark(ParsedModel{Name: "m", Roles: []ModelRole{RoleImage, RoleVision}, Context: 8192})
	if got != "m#vision,image@8192" {
		t.Errorf("顺序应当按 KnownRoles、窗口在最后：%q", got)
	}
	if got := FormatModelMark(ParsedModel{Name: "m"}); got != "m" {
		t.Errorf("什么都没有时不该留下 # 或 @：%q", got)
	}
	if got := FormatModelMark(ParsedModel{Name: "  m  ", Roles: []ModelRole{RoleTTS}}); got != "m#tts" {
		t.Errorf("名字要去空格：%q", got)
	}

	entry := FormatModelMark(ParsedModel{Name: "qwen-tts", Roles: []ModelRole{RoleTTS}, Context: 32768})
	back := ParseModelMark(entry)
	if back.Name != "qwen-tts" || len(back.Roles) != 1 || back.Roles[0] != RoleTTS || back.Context != 32768 {
		t.Errorf("往返之后变了：%q → %+v", entry, back)
	}
}

func TestModelHasRole(t *testing.T) {
	if !ModelHasRole("m#vision@100", RoleVision) {
		t.Error("标了 vision 的应当认出来，窗口不该干扰")
	}
	if ModelHasRole("m", RoleVision) {
		t.Error("没标的不该认成有")
	}
	if ModelHasRole("m#image", RoleVision) {
		t.Error("标了别的能力不等于有 vision")
	}
}
