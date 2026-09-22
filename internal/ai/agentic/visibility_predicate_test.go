package agentic

import (
	"strings"
	"testing"
)

// kbPublicOnlyPredicate 回归测试。
//
// 该常量是「未认证路径」(ChatOps 桥接、无身份的定时任务) 与「身份解析失败」时的唯一防线,
// 它同时决定了哪些文章能被召回, 写错一点就会静默改变召回集合:
//
//	实测故障: 第一版只写了 COALESCE(a.view_permission,'all') —— 只兜住 NULL, 没兜住空串。
//	而写入路径历史上一律原样落库, 库中大量「已发布」文章的该字段是 ''(不是 'all'),
//	于是这批文章被判成"受限"而整体退出检索(published 文章仅 2/5 可召回), 且没有任何报错。
//
// 这里把「空串等价于公开」钉成契约, 防止有人把它改回只兜 NULL。
func TestKbPublicOnlyPredicateTreatsBlankViewPermissionAsPublic(t *testing.T) {
	p := kbPublicOnlyPredicate

	// 空串必须被归一为 'all': 要么显式 NULLIF(view_permission,''), 要么等价写法。
	if !strings.Contains(p, "NULLIF(a.view_permission, '')") {
		t.Fatalf("谓词必须把空串 view_permission 当作公开(需含 NULLIF(a.view_permission, '')), 实际:\n%s", p)
	}
	if strings.Contains(p, "COALESCE(a.view_permission, 'all')") {
		t.Fatalf("不允许退回只兜 NULL 的写法(会让 view_permission='' 的已发布文章静默不可召回):\n%s", p)
	}
	// 归一后的判定目标仍是 all(显式 team/owner 依旧不召回, 保持 fail-closed)
	if !strings.Contains(p, "= 'all'") {
		t.Fatalf("谓词最终仍须判定为 'all', 实际:\n%s", p)
	}
}

// TestKbPublicOnlyPredicateCoversStatusAndExpiry 锁住退化谓词的另外两个必要条件。
func TestKbPublicOnlyPredicateCoversStatusAndExpiry(t *testing.T) {
	p := kbPublicOnlyPredicate
	if !strings.Contains(p, "a.status = 'published'") {
		t.Fatalf("谓词必须限定 status='published'(草稿/待评审/归档不得召回):\n%s", p)
	}
	if !strings.Contains(p, "a.expires_at IS NULL OR a.expires_at > now()") {
		t.Fatalf("谓词必须排除已过期知识:\n%s", p)
	}
	// 该片段会被直接拼到 SQL 末尾, 不能带占位符(否则参数位错乱)
	if strings.Contains(p, "$") {
		t.Fatalf("谓词不得包含占位符($n), 否则拼接后参数位会错乱:\n%s", p)
	}
}
