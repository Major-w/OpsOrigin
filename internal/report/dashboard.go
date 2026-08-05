package report

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

// Layout 描述看板内一个 Widget 在网格中的位置与尺寸。
// 坐标体系沿用主流看板库(如 gridstack)：以列宽为单位，(x,y) 为左上角起点。
type Layout struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Widget 是一个看板内的可视化单元。
// 它的查询是受统一报表模块约束的 Query（只能引用已注册数据集的维度/指标），
// 前端 ReportRenderer 拿到后直接渲染，无需关心底层数据来源。
type Widget struct {
	ID     string `json:"id"`     // 看板内唯一，作为 React key 与布局锚点
	Title  string `json:"title"`  // 卡片标题
	Type   string `json:"type"`   // 图表类型: line/bar/pie/table/number/gauge...
	Query  Query  `json:"query"`  // 受约束的报表查询（引用注册数据集）
	Layout Layout `json:"layout"` // 网格布局
}

// Validate 校验单个 Widget 的结构与查询引用是否合法。
// reg 用于确认 Query 引用的数据集存在且字段在白名单内（提前拒绝非法看板配置）。
func (w *Widget) Validate(reg *Registry) error {
	if w.ID == "" {
		return fmt.Errorf("report: widget id is required")
	}
	if err := w.checkID(); err != nil {
		return err
	}
	if w.Type == "" {
		return fmt.Errorf("report: widget %q type is required", w.ID)
	}
	if w.Layout.W <= 0 || w.Layout.H <= 0 {
		return fmt.Errorf("report: widget %q has invalid layout size (w=%d h=%d)", w.ID, w.Layout.W, w.Layout.H)
	}
	if w.Query.Dataset == "" {
		return fmt.Errorf("report: widget %q query must specify a dataset", w.ID)
	}
	ds, err := reg.Get(w.Query.Dataset)
	if err != nil {
		return err
	}
	// 复制一份避免就地修改调用方传入的查询
	q := w.Query
	if err := q.Normalize(ds); err != nil {
		return fmt.Errorf("report: widget %q query invalid: %w", w.ID, err)
	}
	w.Query = q
	return nil
}

// Dashboard 是一个看板定义，由若干 Widget 组合而成。
// 看板本身只存"声明"（JSON），渲染时由引擎逐 Widget 跑 query 取数，
// 因此同一看板可被多租户各自保存、互不影响。
type Dashboard struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenantId"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Widgets     []Widget  `json:"widgets"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	CreatedBy   string    `json:"createdBy,omitempty"`
}

// Validate 校验看板整体结构。reg 用于逐个校验 Widget 查询。
func (d *Dashboard) Validate(reg *Registry) error {
	if d.Name == "" {
		return fmt.Errorf("report: dashboard name is required")
	}
	// 允许 widgets 为空：新建看板时先落一个空壳，再从数据探索里逐个加图表。
	if d.Widgets == nil {
		d.Widgets = []Widget{}
	}
	seen := make(map[string]bool, len(d.Widgets))
	for i := range d.Widgets {
		if seen[d.Widgets[i].ID] {
			return fmt.Errorf("report: duplicate widget id %q", d.Widgets[i].ID)
		}
		seen[d.Widgets[i].ID] = true
		if err := d.Widgets[i].Validate(reg); err != nil {
			return err
		}
	}
	return nil
}

// widgetIDRe 限制 Widget.ID，避免奇怪字符流入前端 key 与 JSON 路径。
var widgetIDRe = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]*$`)

// Validate 校验 Widget.ID 命名（供 service 层调用）。
func (w *Widget) checkID() error {
	if !widgetIDRe.MatchString(w.ID) {
		return fmt.Errorf("report: invalid widget id %q (expect like w_ticket_trend)", w.ID)
	}
	return nil
}

// DashboardStore 看板持久化接口，内存与 PG 双实现。
type DashboardStore interface {
	Create(ctx context.Context, d *Dashboard) error
	Get(ctx context.Context, id, tenantID string) (*Dashboard, error)
	List(ctx context.Context, tenantID string) ([]*Dashboard, error)
	Update(ctx context.Context, d *Dashboard) error
	Delete(ctx context.Context, id, tenantID string) error
}
