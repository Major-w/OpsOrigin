package knowledge

import "time"

type Article struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Category  string    `json:"category"`
	Tags      []string  `json:"tags,omitempty"`
	Author    string    `json:"author"`
	Views     int       `json:"views"`
	Status    string    `json:"status"`  // draft(默认) | published
	Version   int       `json:"version"` // 当前版本号, 每次更新 +1
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Category 知识库分类元数据(替代自由字符串分类, 成为真正的"知识域")。
type Category struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	SortOrder   int       `json:"sort_order"`
	Color       string    `json:"color,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ArticleVersion 文章历史版本快照(编辑历史可回滚)。
type ArticleVersion struct {
	ID        string    `json:"id"`
	ArticleID string    `json:"article_id"`
	TenantID  string    `json:"tenant_id"`
	Version   int       `json:"version"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Category  string    `json:"category"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
