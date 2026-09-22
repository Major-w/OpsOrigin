package agentic

import (
	"testing"
	"time"
)

func TestTTLCacheSetGet(t *testing.T) {
	c := newTTLCache(time.Minute, 16)
	c.set("a", "v1")
	if v, ok := c.get("a"); !ok || v.(string) != "v1" {
		t.Fatalf("expected v1, got %v ok=%v", v, ok)
	}
	if _, ok := c.get("missing"); ok {
		t.Fatalf("expected cache miss for unknown key")
	}
}

func TestTTLCacheExpiry(t *testing.T) {
	c := newTTLCache(time.Nanosecond, 16)
	c.set("a", "v1")
	time.Sleep(2 * time.Millisecond)
	if _, ok := c.get("a"); ok {
		t.Fatalf("expected entry to be expired")
	}
}

func TestTTLCacheEviction(t *testing.T) {
	c := newTTLCache(time.Minute, 2)
	c.set("a", 1)
	c.set("b", 2)
	c.set("c", 3) // 命中容量上限, 应驱逐至少一条
	if len(c.m) > 2 {
		t.Fatalf("expected <=2 keys after eviction, got %d", len(c.m))
	}
}

func TestCanonicalArgsKeyOrderIndependent(t *testing.T) {
	a := map[string]any{"x": "1", "y": "2"}
	b := map[string]any{"y": "2", "x": "1"}
	if canonicalArgsKey(a) != canonicalArgsKey(b) {
		t.Fatalf("param order must not affect key: %q vs %q", canonicalArgsKey(a), canonicalArgsKey(b))
	}
	if canonicalArgsKey(map[string]any{}) != "∅" {
		t.Fatalf("empty args should map to sentinel key, got %q", canonicalArgsKey(map[string]any{}))
	}
}

func TestSearchCacheKeyDistinguishesMode(t *testing.T) {
	ts := &MCPToolSet{}
	kVec := ts.searchCacheKey("t1", "q", "vector", "s1", 5, 500, nil)
	kIL := ts.searchCacheKey("t1", "q", "ilike", "s1", 5, 500, nil)
	if kVec == kIL {
		t.Fatalf("vector vs ilike must produce distinct keys")
	}
	if ts.searchCacheKey("t1", "q", "vector", "s1", 5, 500, nil) != kVec {
		t.Fatalf("key must be stable for identical inputs")
	}
	// 分类绑定必须进入缓存键, 避免不同绑定的结果串用
	kCat := ts.searchCacheKey("t1", "q", "vector", "s1", 5, 500, []string{"故障处理"})
	if kCat == kVec {
		t.Fatalf("category-bound key must differ from unbounded key")
	}
	// 可见范围必须进入缓存键: 谓词已按身份变化, 少了它 A 的结果会被 B 直接复用(越权)
	kOtherScope := ts.searchCacheKey("t1", "q", "vector", "s2", 5, 500, nil)
	if kOtherScope == kVec {
		t.Fatalf("different visibility scope must produce a different cache key")
	}
}

// —— 缓存失效(可见性修复的配套) ——
//
// 检索结果缓存 5 分钟。文章下架/收紧可见性/删除后若不失效, 同一提问在 TTL 内仍会拿到
// 旧结果, 受限内容照样进入模型上下文 —— 等于给 fail-closed 的可见性过滤开了个时间窗。

func TestTTLCacheClearDropsEverything(t *testing.T) {
	c := newTTLCache(time.Minute, 16)
	c.set("a", 1)
	c.set("b", 2)
	c.clear()
	if len(c.m) != 0 {
		t.Fatalf("clear 应清空全部条目, 实际剩 %d", len(c.m))
	}
	if _, ok := c.get("a"); ok {
		t.Fatalf("clear 后不应命中")
	}
	// 清空后仍可正常写入(容量与 TTL 不因清空而失效)
	c.set("c", 3)
	if v, ok := c.get("c"); !ok || v.(int) != 3 {
		t.Fatalf("clear 后写入应可命中, got %v ok=%v", v, ok)
	}
}

func TestTTLCacheClearPrefixOnlyMatchingKeys(t *testing.T) {
	c := newTTLCache(time.Minute, 16)
	c.set("knowledge_search\x1ft1\x1f{}\n", 1)
	c.set("knowledge_search\x1ft2\x1f{}\n", 2)
	c.set("cmdb_query\x1ft1\x1f{}", 3)

	n := c.clearPrefix(knowledgeSearchCachePrefix)
	if n != 2 {
		t.Fatalf("应删掉 2 个 knowledge_search 条目, 实际 %d", n)
	}
	// 其它工具的缓存必须保住(否则每次知识库写操作都会连带打掉 CMDB 命中率)
	if _, ok := c.get("cmdb_query\x1ft1\x1f{}"); !ok {
		t.Fatalf("clearPrefix 不得误删其它前缀的条目")
	}
	if _, ok := c.get("knowledge_search\x1ft1\x1f{}\n"); ok {
		t.Fatalf("knowledge_search 条目应已失效")
	}
	// 再次调用应为 0, 保证幂等
	if n2 := c.clearPrefix(knowledgeSearchCachePrefix); n2 != 0 {
		t.Fatalf("重复调用应返回 0, 实际 %d", n2)
	}
}

// TestFlushSearchCacheNilSafe 保证未初始化缓存时不 panic(内存模式/测试装配)。
func TestFlushSearchCacheNilSafe(t *testing.T) {
	ts := &MCPToolSet{}
	ts.FlushSearchCache() // searchCache 为 nil, 不应 panic

	(&Engine{}).FlushKnowledgeSearchCache() // toolCache 为 nil, 不应 panic
	var nilEng *Engine
	nilEng.FlushKnowledgeSearchCache() // 接收者为 nil 也不应 panic
}
