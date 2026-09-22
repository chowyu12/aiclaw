package protocol

import "testing"

// 能力标记的解析。写错了不会报错，只会「我明明标了视觉，它还是走转述」——
// 而那种问题用户无从排查，所以逐条钉。

func TestParseModelMark(t *testing.T) {
	for _, tc := range []struct {
		entry string
		name  string
		roles []ModelRole
		why   string
	}{
		{"gpt-4.1", "gpt-4.1", nil, "没有 # 的是旧记录，只做对话"},
		{" gpt-4.1 ", "gpt-4.1", nil, "首尾空格是手写清单里最常见的"},
		{"qwen3-vl#vision", "qwen3-vl", []ModelRole{RoleVision}, "单个能力"},
		{"m#vision,image", "m", []ModelRole{RoleVision, RoleImage}, "多个能力"},
		{"m#VISION", "m", []ModelRole{RoleVision}, "大小写不该影响"},
		{"m# vision , image ", "m", []ModelRole{RoleVision, RoleImage}, "标记里的空格"},
		{"m#vision,vision", "m", []ModelRole{RoleVision}, "重复的只算一次"},
		{"m#nonsense", "m", nil, "不认识的标记丢掉，不是报错——清单是手写的"},
		{"m#", "m", nil, "空标记"},
	} {
		name, roles := ParseModelMark(tc.entry)
		if name != tc.name {
			t.Errorf("%q 的名字 = %q，想要 %q（%s）", tc.entry, name, tc.name, tc.why)
		}
		if len(roles) != len(tc.roles) {
			t.Errorf("%q 的能力 = %v，想要 %v（%s）", tc.entry, roles, tc.roles, tc.why)
			continue
		}
		for i := range roles {
			if roles[i] != tc.roles[i] {
				t.Errorf("%q 的能力 = %v，想要 %v（%s）", tc.entry, roles, tc.roles, tc.why)
				break
			}
		}
	}
}

func TestFormatModelMarkRoundTrips(t *testing.T) {
	// 顺序固定：同一份配置每次写出来都一样，否则配置文件会无谓地变动。
	if got := FormatModelMark("m", []ModelRole{RoleImage, RoleVision}); got != "m#vision,image" {
		t.Errorf("顺序应当按 KnownRoles：%q", got)
	}
	if got := FormatModelMark("m", nil); got != "m" {
		t.Errorf("没有能力时不该留下 #：%q", got)
	}
	entry := FormatModelMark("qwen-tts", []ModelRole{RoleTTS})
	name, roles := ParseModelMark(entry)
	if name != "qwen-tts" || len(roles) != 1 || roles[0] != RoleTTS {
		t.Errorf("往返之后变了：%q → %q %v", entry, name, roles)
	}
}

func TestModelHasRole(t *testing.T) {
	if !ModelHasRole("m#vision", RoleVision) {
		t.Error("标了 vision 的应当认出来")
	}
	if ModelHasRole("m", RoleVision) {
		t.Error("没标的不该认成有")
	}
	if ModelHasRole("m#image", RoleVision) {
		t.Error("标了别的能力不等于有 vision")
	}
}
