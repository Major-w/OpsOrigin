package knowledge

import "testing"

func TestRenderTemplate(t *testing.T) {
	vars := map[string]string{"对象": "主机A", "事件": "磁盘告警"}
	got := renderTemplate("关于{{对象}}的{{事件}}处理", vars)
	want := "关于主机A的磁盘告警处理"
	if got != want {
		t.Fatalf("renderTemplate = %q, want %q", got, want)
	}
	// 占位符未提供时原样保留
	partial := renderTemplate("{{对象}} 发生 {{缺失}}", vars)
	if partial != "主机A 发生 {{缺失}}" {
		t.Fatalf("renderTemplate partial = %q", partial)
	}
	// 空模板
	if renderTemplate("", vars) != "" {
		t.Fatal("empty template should stay empty")
	}
}
