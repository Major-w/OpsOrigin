package agentic

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// 缓存 TTL 配置。embedding 语义稳定用长 TTL; 检索结果与工具结果可能因数据更新而变,
// 用较短 TTL 在"省钱"与"新鲜度"之间取舍。
const (
	embedCacheTTL  = 30 * time.Minute // 同一 query 的向量表示稳定, 长 TTL 最大化去重收益
	searchCacheTTL = 5 * time.Minute  // 知识库可能更新, 较短 TTL
	toolCacheTTL   = 5 * time.Minute  // 工具结果(尤其 CMDB/知识库类只读)较短 TTL
)

// ttlCache 是并发安全的 TTL 键值缓存, 用于工具结果/向量检索/embedding 去重。
// value 类型为 any; 调用方需保证同一 key 的 value 类型一致(本项目中均为确定的结构体/切片)。
type ttlCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	maxKeys int
	m       map[string]ttlEntry
}

type ttlEntry struct {
	value  any
	expire time.Time
}

func newTTLCache(ttl time.Duration, maxKeys int) *ttlCache {
	if maxKeys <= 0 {
		maxKeys = 1024
	}
	return &ttlCache{ttl: ttl, maxKeys: maxKeys, m: make(map[string]ttlEntry, maxKeys)}
}

// get 返回未过期的值; 若已过期则顺手清理并返回 false。
func (c *ttlCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expire) {
		delete(c.m, key)
		return nil, false
	}
	return e.value, true
}

// set 写入带 TTL 的值; 命中容量上限时先清理过期条目, 仍满则驱逐任一(简单 LRU 近似)。
func (c *ttlCache) set(key string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.maxKeys {
		for k, e := range c.m {
			if time.Now().After(e.expire) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= c.maxKeys {
			for k := range c.m {
				delete(c.m, k)
				break
			}
		}
	}
	c.m[key] = ttlEntry{value: v, expire: time.Now().Add(c.ttl)}
}

// canonicalArgsKey 将工具参数规范化为稳定 key: 经 JSON 序列化(map 键天然有序),
// 使"参数相同但顺序不同"的请求命中同一缓存。空参数返回哨兵值。
func canonicalArgsKey(args map[string]any) string {
	if len(args) == 0 {
		return "∅"
	}
	b, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("%v", args)
	}
	return string(b)
}
