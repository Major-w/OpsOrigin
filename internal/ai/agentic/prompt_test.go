package agentic

import (
	"context"
	"strings"
	"testing"
)

// TestPromptVariableParsing 覆盖占位变量解析与渲染: 去重保序、未提供值的占位原样保留。
func TestPromptVariableParsing(t *testing.T) {
	content := "现象: {{现象}}, 系统: {{ 系统 }}, 再提一次 {{现象}}, 英文 {{host_name}}"
	vars := parsePromptVariables(content)
	want := []string{"现象", "系统", "host_name"}
	if len(vars) != len(want) {
		t.Fatalf("变量数不符: got %v want %v", vars, want)
	}
	for i := range want {
		if vars[i] != want[i] {
			t.Fatalf("变量顺序/内容不符: got %v want %v", vars, want)
		}
	}

	got := renderPromptContent(content, map[string]string{"现象": "接口 5xx 突增", "host_name": "web-01"})
	if !strings.Contains(got, "接口 5xx 突增") || !strings.Contains(got, "web-01") {
		t.Fatalf("已提供的变量未被替换: %s", got)
	}
	if !strings.Contains(got, "{{ 系统 }}") {
		t.Fatalf("未提供值的占位应原样保留, 便于人工补全: %s", got)
	}
	// 空变量表: 原样返回
	if renderPromptContent(content, nil) != content {
		t.Fatal("空变量表应原样返回")
	}
}

// TestPromptSeedIdempotent 覆盖预置提示词幂等: 重复 seed 不新增, 且不覆盖用户改过的正文。
func TestPromptSeedIdempotent(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t)
	e.SetStore(newMemStore())

	SeedPromptTemplates(ctx, e)
	first := e.ListPrompts(ctx, "default", "")
	if len(first) == 0 {
		t.Fatal("seed 后提示词库不应为空")
	}

	// 用户修改默认提示词正文
	def, err := e.GetPrompt(ctx, "default", DefaultSystemPromptID)
	if err != nil {
		t.Fatalf("默认提示词应存在: %v", err)
	}
	def.Content = "自定义兜底人设"
	if _, err := e.SavePrompt(ctx, "default", def); err != nil {
		t.Fatalf("保存默认提示词失败: %v", err)
	}

	// 二次 seed: 不新增条目, 也不回退用户改动
	SeedPromptTemplates(ctx, e)
	second := e.ListPrompts(ctx, "default", "")
	if len(second) != len(first) {
		t.Fatalf("重复 seed 不应新增条目: %d -> %d", len(first), len(second))
	}
	after, _ := e.GetPrompt(ctx, "default", DefaultSystemPromptID)
	if after.Content != "自定义兜底人设" {
		t.Fatalf("重复 seed 覆盖了用户改动: %s", after.Content)
	}
	if e.resolveSystemPrompt("") != "自定义兜底人设" {
		t.Fatal("默认提示词应热生效到引擎兜底值")
	}
}

// TestPromptCRUDAndGuards 覆盖增删改查、默认唯一性、预置/默认删除保护、渲染计数。
func TestPromptCRUDAndGuards(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t)
	e.SetStore(newMemStore())
	SeedPromptTemplates(ctx, e)

	// 必填校验
	if _, err := e.SavePrompt(ctx, "default", &PromptTemplate{Content: "x"}); err == nil {
		t.Fatal("缺少 name 应报错")
	}
	if _, err := e.SavePrompt(ctx, "default", &PromptTemplate{Name: "x"}); err == nil {
		t.Fatal("缺少 content 应报错")
	}

	// 新建
	created, err := e.SavePrompt(ctx, "default", &PromptTemplate{
		Name: "巡检小结", Scene: "知识沉淀", Content: "请总结 {{对象}} 的巡检结论。",
	})
	if err != nil {
		t.Fatalf("新建失败: %v", err)
	}
	if created.Builtin {
		t.Fatal("用户自建提示词不应带 builtin 标记")
	}
	if len(created.Variables) != 1 || created.Variables[0] != "对象" {
		t.Fatalf("变量解析异常: %v", created.Variables)
	}

	// 场景过滤
	if got := e.ListPrompts(ctx, "default", "知识沉淀"); len(got) == 0 {
		t.Fatal("按场景过滤应命中新建提示词")
	}
	for _, p := range e.ListPrompts(ctx, "default", "知识沉淀") {
		if p.Scene != "知识沉淀" {
			t.Fatalf("场景过滤不生效: %s", p.Scene)
		}
	}

	// 渲染并累计引用
	text, err := e.RenderPrompt(ctx, "default", created.ID, map[string]string{"对象": "web-01"})
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if !strings.Contains(text, "web-01") {
		t.Fatalf("渲染未替换变量: %s", text)
	}
	if after, _ := e.GetPrompt(ctx, "default", created.ID); after.Usage != 1 {
		t.Fatalf("引用次数未累计: %d", after.Usage)
	}

	// 设为默认: 旧默认自动摘牌, 且热生效
	created.IsDefault = true
	if _, err := e.SavePrompt(ctx, "default", created); err != nil {
		t.Fatalf("设为默认失败: %v", err)
	}
	defaults := 0
	for _, p := range e.ListPrompts(ctx, "default", "") {
		if p.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("默认提示词应租户内唯一, got %d", defaults)
	}
	if !strings.Contains(e.resolveSystemPrompt(""), "巡检结论") {
		t.Fatal("新默认提示词未热生效")
	}

	// 删除保护: 默认不可删
	if err := e.DeletePrompt(ctx, "default", created.ID); err == nil {
		t.Fatal("默认提示词不应可删")
	}
	// 删除保护: 预置不可删
	if err := e.DeletePrompt(ctx, "default", "prompt-troubleshoot"); err == nil {
		t.Fatal("预置提示词不应可删")
	}

	// 普通自建可删
	tmp, _ := e.SavePrompt(ctx, "default", &PromptTemplate{Name: "临时", Content: "临时内容"})
	if err := e.DeletePrompt(ctx, "default", tmp.ID); err != nil {
		t.Fatalf("自建提示词删除失败: %v", err)
	}
	if _, err := e.GetPrompt(ctx, "default", tmp.ID); err == nil {
		t.Fatal("删除后不应还能读到")
	}
}

// TestPromptBuiltinFlagNotForgeable 覆盖: 前端无法伪造 builtin 标记, 更新时沿用既有值。
func TestPromptBuiltinFlagNotForgeable(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t)
	e.SetStore(newMemStore())
	SeedPromptTemplates(ctx, e)

	// 新建时传 builtin=true 也应被忽略(SavePrompt 不读入参的 Builtin)
	p, err := e.SavePrompt(ctx, "default", &PromptTemplate{Name: "伪装预置", Content: "内容", Builtin: true})
	if err != nil {
		t.Fatalf("新建失败: %v", err)
	}
	if p.Builtin {
		t.Fatal("前端不应能把自建提示词标记为预置")
	}

	// 更新预置提示词: 正文可改, builtin 标记保持
	bi, _ := e.GetPrompt(ctx, "default", "prompt-troubleshoot")
	bi.Content = "改过的排查话术 {{现象}}"
	bi.Builtin = false // 尝试摘牌
	updated, err := e.SavePrompt(ctx, "default", bi)
	if err != nil {
		t.Fatalf("更新预置提示词失败: %v", err)
	}
	if !updated.Builtin {
		t.Fatal("预置标记不应被前端摘掉")
	}
	if updated.Content != "改过的排查话术 {{现象}}" {
		t.Fatal("预置提示词正文应可编辑")
	}
}
