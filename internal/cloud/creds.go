package cloud

import (
	"context"
	"sync"
	"time"
)

// Store 负责云凭证的持久化与读取。
// 当前为内存实现（重启即失）；生产环境应替换为加密的 DB 存储
// （见优化报告 2.4 多服务持久化）。
type Store interface {
	// Save 保存（新增或更新）一条凭证。
	Save(cred Credential) error
	// Get 读取指定厂商、指定标识的凭证。
	Get(provider, id string) (*Credential, error)
	// List 列出某厂商全部凭证。
	List(provider string) ([]Credential, error)
	// Delete 删除凭证。
	Delete(provider, id string) error
}

// MemStore 是基于内存的凭证存储（开发/演示用）。
type MemStore struct {
	mu     sync.RWMutex
	creds  map[string]map[string]Credential // provider -> id -> cred
}

// NewMemStore 创建内存凭证存储。
func NewMemStore() *MemStore {
	return &MemStore{creds: make(map[string]map[string]Credential)}
}

// firstNonEmpty 返回第一个非空字符串，用于合并既有地域等字段。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// idOf 由凭证内容推导稳定 ID（同一厂商+AK 视为同一凭证）。
func idOf(c Credential) string {
	id := c.AccessKey
	if id == "" {
		id = c.Region
	}
	if id == "" {
		id = "default"
	}
	return c.Provider + ":" + id
}

func (s *MemStore) Save(c Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creds[c.Provider] == nil {
		s.creds[c.Provider] = make(map[string]Credential)
	}
	creds := s.creds[c.Provider]
	existing, ok := creds[idOf(c)]
	if ok {
		c.Region = firstNonEmpty(c.Region, existing.Region)
	}
	creds[idOf(c)] = c
	return nil
}

func (s *MemStore) Get(provider, id string) (*Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.creds[provider]
	if !ok {
		return nil, nil
	}
	c, ok := m[id]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (s *MemStore) List(provider string) ([]Credential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.creds[provider]
	if !ok {
		return nil, nil
	}
	out := make([]Credential, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	return out, nil
}

func (s *MemStore) Delete(provider, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.creds[provider]; ok {
		delete(m, id)
	}
	return nil
}

// Collector 统筹多厂商资源的采集，结果可衔接 CMDB。
type Collector struct {
	store Store
}

// NewCollector 创建采集器。
func NewCollector(store Store) *Collector {
	return &Collector{store: store}
}

// Collect 对单个厂商凭证执行采集，合并资源与指标。
func (c *Collector) Collect(provider string) ([]Resource, []Metric, error) {
	p, err := Get(provider)
	if err != nil {
		return nil, nil, err
	}
	creds, err := c.store.List(provider)
	if err != nil {
		return nil, nil, err
	}
	var allRes []Resource
	var allMet []Metric
	for _, cr := range creds {
		res, err := p.ListResources(context.Background(), cr, cr.Region, AllResourceTypes())
		if err != nil {
			return allRes, allMet, err
		}
		for i := range res {
			res[i].DiscoveredAt = time.Now()
			allRes = append(allRes, res[i])
		}
		met, err := p.CollectMetrics(context.Background(), cr, cr.Region, res)
		if err != nil {
			// 指标失败不阻断资源采集
			continue
		}
			allMet = append(allMet, met...)
	}
	return allRes, allMet, nil
}
