package cmdb

import (
	"testing"
	"time"
)

// TestParseFrequency 验证频率字符串解析：Go duration、别名、非法回退 1h。
func TestParseFrequency(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", time.Hour},
		{"15m", 15 * time.Minute},
		{"1h", time.Hour},
		{"30s", 30 * time.Second},
		{"hourly", time.Hour},
		{"daily", 24 * time.Hour},
		{"garbage", time.Hour}, // 非法字符串回退默认
	}
	for _, c := range cases {
		got := parseFrequency(c.in)
		if got != c.want {
			t.Errorf("parseFrequency(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// TestResolveSwitch 验证「全局默认 + 逐资源覆盖」开关解析逻辑。
func TestResolveSwitch(t *testing.T) {
	def := true
	off := false
	cases := []struct {
		v    *bool
		def  bool
		want bool
	}{
		{nil, true, true},
		{nil, false, false},
		{&def, false, true}, // 显式覆盖默认
		{&off, true, false}, // 显式关闭
	}
	for i, c := range cases {
		if got := resolveSwitch(c.v, c.def); got != c.want {
			t.Errorf("case %d: resolveSwitch(%v, %v) = %v, want %v", i, c.v, c.def, got, c.want)
		}
	}
}
