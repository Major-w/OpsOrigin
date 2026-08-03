package component

import (
	"fmt"
	"time"
)

// ComponentMarketItem 组件市场项目
type ComponentMarketItem struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Category    string                 `json:"category"` // basic, form, layout, chart, advanced
	Type        string                 `json:"type"`     // input, button, table, etc.
	Version     string                 `json:"version"`
	Author      string                 `json:"author"`
	Description string                 `json:"description"`
	Icon        string                 `json:"icon"`
	Preview     string                 `json:"preview"`     // 预览图URL
	Download    int                    `json:"download"`    // 下载次数
	Rating      float64                `json:"rating"`      // 评分
	IsOfficial  bool                   `json:"is_official"` // 是否官方组件
	IsFree      bool                   `json:"is_free"`     // 是否免费
	Price       float64                `json:"price"`       // 价格（积分）
	Tags        []string               `json:"tags"`
	Config      map[string]interface{} `json:"config"` // 组件配置
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// ComponentCategory 组件分类
type ComponentCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	SortOrder   int    `json:"sort_order"`
}

// MarketService 组件市场服务
type MarketService struct {
	categories []ComponentCategory
	components []ComponentMarketItem
}

// NewMarketService 创建组件市场服务
func NewMarketService() *MarketService {
	svc := &MarketService{
		categories: make([]ComponentCategory, 0),
		components: make([]ComponentMarketItem, 0),
	}
	svc.initDefaultData()
	return svc
}

// initDefaultData 初始化默认数据
func (s *MarketService) initDefaultData() {
	// 初始化分类
	s.categories = []ComponentCategory{
		{ID: "basic", Name: "基础组件", Description: "常用基础UI组件", Icon: "Box", SortOrder: 1},
		{ID: "form", Name: "表单组件", Description: "表单输入相关组件", Icon: "FileText", SortOrder: 2},
		{ID: "layout", Name: "布局组件", Description: "页面布局组件", Icon: "Layout", SortOrder: 3},
		{ID: "chart", Name: "图表组件", Description: "数据可视化组件", Icon: "BarChart", SortOrder: 4},
		{ID: "advanced", Name: "高级组件", Description: "复杂业务组件", Icon: "Star", SortOrder: 5},
	}

	// 初始化组件
	s.components = []ComponentMarketItem{
		// 基础组件
		{
			ID:          "btn-001",
			Name:        "按钮",
			Category:    "basic",
			Type:        "button",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "基础按钮组件，支持多种样式和状态",
			Icon:        "MousePointerClick",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"按钮", "点击", "操作"},
			Config: map[string]interface{}{
				"text":     "按钮",
				"variant":  "default",
				"size":     "default",
				"disabled": false,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:          "input-001",
			Name:        "输入框",
			Category:    "basic",
			Type:        "input",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "文本输入框组件",
			Icon:        "TextCursorInput",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"输入", "文本", "表单"},
			Config: map[string]interface{}{
				"placeholder": "请输入",
				"type":        "text",
				"disabled":    false,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:          "select-001",
			Name:        "下拉选择",
			Category:    "basic",
			Type:        "select",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "下拉选择组件",
			Icon:        "ChevronDown",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"选择", "下拉", "表单"},
			Config: map[string]interface{}{
				"placeholder": "请选择",
				"options":     []interface{}{},
				"disabled":    false,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		// 表单组件
		{
			ID:          "form-001",
			Name:        "表单",
			Category:    "form",
			Type:        "form",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "表单容器组件",
			Icon:        "FileText",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"表单", "容器", "布局"},
			Config: map[string]interface{}{
				"layout":    "vertical",
				"labelSpan": 24,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:          "datepicker-001",
			Name:        "日期选择",
			Category:    "form",
			Type:        "datepicker",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "日期时间选择组件",
			Icon:        "Calendar",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"日期", "时间", "选择"},
			Config: map[string]interface{}{
				"format":   "YYYY-MM-DD",
				"showTime": false,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:          "upload-001",
			Name:        "文件上传",
			Category:    "form",
			Type:        "upload",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "文件上传组件",
			Icon:        "Upload",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"上传", "文件", "附件"},
			Config: map[string]interface{}{
				"multiple": false,
				"maxSize":  10,
				"accept":   "*",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		// 布局组件
		{
			ID:          "grid-001",
			Name:        "栅格布局",
			Category:    "layout",
			Type:        "grid",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "栅格布局组件",
			Icon:        "Grid",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"栅格", "布局", "响应式"},
			Config: map[string]interface{}{
				"columns": 24,
				"gutter":  16,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:          "tabs-001",
			Name:        "标签页",
			Category:    "layout",
			Type:        "tabs",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "标签页布局组件",
			Icon:        "Folder",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"标签", "选项卡", "布局"},
			Config: map[string]interface{}{
				"type":     "line",
				"animated": false,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		// 图表组件
		{
			ID:          "chart-line-001",
			Name:        "折线图",
			Category:    "chart",
			Type:        "chart-line",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "折线图组件",
			Icon:        "TrendingUp",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"图表", "折线", "趋势"},
			Config: map[string]interface{}{
				"xField": "x",
				"yField": "y",
				"smooth": true,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:          "chart-bar-001",
			Name:        "柱状图",
			Category:    "chart",
			Type:        "chart-bar",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "柱状图组件",
			Icon:        "BarChart",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"图表", "柱状", "对比"},
			Config: map[string]interface{}{
				"xField":           "x",
				"yField":           "y",
				"columnWidthRatio": 0.5,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:          "chart-pie-001",
			Name:        "饼图",
			Category:    "chart",
			Type:        "chart-pie",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "饼图组件",
			Icon:        "PieChart",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"图表", "饼图", "占比"},
			Config: map[string]interface{}{
				"angleField": "value",
				"colorField": "type",
				"radius":     0.8,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		// 高级组件
		{
			ID:          "table-001",
			Name:        "表格",
			Category:    "advanced",
			Type:        "table",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "数据表格组件",
			Icon:        "Table",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"表格", "数据", "列表"},
			Config: map[string]interface{}{
				"pagination": true,
				"pageSize":   10,
				"bordered":   true,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:          "modal-001",
			Name:        "对话框",
			Category:    "advanced",
			Type:        "modal",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "模态对话框组件",
			Icon:        "Maximize2",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"对话框", "弹窗", "模态"},
			Config: map[string]interface{}{
				"title":        "对话框",
				"width":        520,
				"maskClosable": true,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:          "tree-001",
			Name:        "树形控件",
			Category:    "advanced",
			Type:        "tree",
			Version:     "1.0.0",
			Author:      "OpsOrigin",
			Description: "树形结构展示组件",
			Icon:        "GitBranch",
			IsOfficial:  true,
			IsFree:      true,
			Tags:        []string{"树", "层级", "结构"},
			Config: map[string]interface{}{
				"showLine":   true,
				"selectable": true,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
}

// GetCategories 获取分类列表
func (s *MarketService) GetCategories() []ComponentCategory {
	return s.categories
}

// GetComponents 获取组件列表
func (s *MarketService) GetComponents(category string, keyword string) []ComponentMarketItem {
	var result []ComponentMarketItem

	for _, comp := range s.components {
		if category != "" && comp.Category != category {
			continue
		}
		if keyword != "" {
			found := false
			for _, tag := range comp.Tags {
				if contains(tag, keyword) {
					found = true
					break
				}
			}
			if !found && !contains(comp.Name, keyword) && !contains(comp.Description, keyword) {
				continue
			}
		}
		result = append(result, comp)
	}

	return result
}

// GetComponent 获取单个组件
func (s *MarketService) GetComponent(id string) (*ComponentMarketItem, error) {
	for _, comp := range s.components {
		if comp.ID == id {
			return &comp, nil
		}
	}
	return nil, fmt.Errorf("component not found: %s", id)
}

// InstallComponent 安装组件
func (s *MarketService) InstallComponent(id string) error {
	comp, err := s.GetComponent(id)
	if err != nil {
		return err
	}
	comp.Download++
	return nil
}

// RateComponent 评分组件
func (s *MarketService) RateComponent(id string, rating float64) error {
	if rating < 0 || rating > 5 {
		return fmt.Errorf("rating must be between 0 and 5")
	}
	comp, err := s.GetComponent(id)
	if err != nil {
		return err
	}
	comp.Rating = rating
	return nil
}

// contains 字符串包含检查
func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) &&
		(s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
