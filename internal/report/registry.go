package report

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Registry 保存已注册的数据集。
// 各业务模块在装配阶段调用 Register 声明自己对外可查询的数据，
// report 包不反向 import 业务模块，保证依赖单向。
type Registry struct {
	mu       sync.RWMutex
	datasets map[string]*Dataset
}

// NewRegistry 创建一个独立注册表(测试与多实例场景使用)。
func NewRegistry() *Registry {
	return &Registry{datasets: make(map[string]*Dataset)}
}

// Register 注册数据集，声明非法或重名会返回错误。
func (r *Registry) Register(ds *Dataset) error {
	if err := ds.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.datasets[ds.Name]; ok {
		return fmt.Errorf("report: dataset %q already registered", ds.Name)
	}
	r.datasets[ds.Name] = ds
	return nil
}

// MustRegister 注册失败直接 panic，用于包 init 或装配期的硬性错误。
func (r *Registry) MustRegister(ds *Dataset) {
	if err := r.Register(ds); err != nil {
		panic(err)
	}
}

// Get 按名取数据集。
func (r *Registry) Get(name string) (*Dataset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ds, ok := r.datasets[name]
	if !ok {
		return nil, &DatasetNotFound{Name: name}
	}
	return ds, nil
}

// Exists 判断数据集是否已注册。
func (r *Registry) Exists(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.datasets[name]
	return ok
}

// Names 返回全部数据集名(排序)。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.datasets))
	for n := range r.datasets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// List 返回全部数据集(按模块、名称排序)，供前端数据集浏览器使用。
func (r *Registry) List() []*Dataset {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Dataset, 0, len(r.datasets))
	for _, ds := range r.datasets {
		out = append(out, ds)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module != out[j].Module {
			return out[i].Module < out[j].Module
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Query 执行一次统一查询：白名单校验 → 交给数据集自己的解析器取数。
func (r *Registry) Query(ctx context.Context, tenantID string, q *Query) (*Result, error) {
	if q == nil {
		return nil, fmt.Errorf("report: nil query")
	}
	ds, err := r.Get(q.Dataset)
	if err != nil {
		return nil, err
	}
	if err := q.Normalize(ds); err != nil {
		return nil, err
	}
	start := time.Now()
	res, err := ds.Resolver.Resolve(ctx, tenantID, q, ds)
	if err != nil {
		return nil, err
	}
	if res == nil {
		res = &Result{}
	}
	if res.Columns == nil {
		res.Columns = q.Columns(ds)
	}
	if res.Rows == nil {
		res.Rows = [][]any{}
	}
	res.RowCount = len(res.Rows)
	if res.RowCount >= q.Limit {
		res.Truncated = true
	}
	res.ElapsedMs = time.Since(start).Milliseconds()
	return res, nil
}

// DatasetNotFound 表示请求了未注册的数据集。
type DatasetNotFound struct{ Name string }

func (e *DatasetNotFound) Error() string {
	return fmt.Sprintf("report: dataset %q not found", e.Name)
}

// defaultRegistry 是进程级默认注册表。
var defaultRegistry = NewRegistry()

// Default 返回默认注册表。
func Default() *Registry { return defaultRegistry }

// Register 向默认注册表注册数据集。
func Register(ds *Dataset) error { return defaultRegistry.Register(ds) }

// MustRegister 向默认注册表注册数据集，失败 panic。
func MustRegister(ds *Dataset) { defaultRegistry.MustRegister(ds) }

// Get 从默认注册表取数据集。
func Get(name string) (*Dataset, error) { return defaultRegistry.Get(name) }

// Names 返回默认注册表的全部数据集名。
func Names() []string { return defaultRegistry.Names() }

// List 返回默认注册表的全部数据集。
func List() []*Dataset { return defaultRegistry.List() }

// Exists 判断默认注册表中数据集是否存在。
func Exists(name string) bool { return defaultRegistry.Exists(name) }
