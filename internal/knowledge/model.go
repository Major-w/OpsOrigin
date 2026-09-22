package knowledge

import (
	"sort"
	"strings"
	"time"
)

// 可见性 / 编辑权限取值(与前端表单下拉一致)。
const (
	ViewPermissionAll   = "all"   // 公开: AI 检索可召回
	ViewPermissionTeam  = "team"  // 团队可见: AI 不召回(无归属信息可判定)
	ViewPermissionOwner = "owner" // 仅作者: AI 不召回

	EditPermissionOwner = "owner"
	EditPermissionTeam  = "team"
	EditPermissionAll   = "all"
)

type Article struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	Title          string     `json:"title"`
	Content        string     `json:"content"`
	Category       string     `json:"category"`
	Tags           []string   `json:"tags,omitempty"`
	Author         string     `json:"author"`
	Views          int        `json:"views"`
	Status         string     `json:"status"`               // draft(默认) | published
	Version        int        `json:"version"`              // 当前版本号, 每次更新 +1
	ExpiresAt      *time.Time `json:"expires_at,omitempty"` // 知识有效期, nil=永不过期
	ViewPermission string     `json:"view_permission"`      // all(默认) | team | owner
	EditPermission string     `json:"edit_permission"`      // owner(默认) | team | all
	// AuthorID 创建人用户 ID, 是 owner 可见性的唯一判定依据(库中无此值则该分支永不成立)。
	AuthorID string `json:"author_id"`
	// OwnerGroupIDs 归属团队(iam 用户组 ID)列表; view_permission=team 时生效, 属于其中任一团队即可见。
	// 与「可见范围」的语义分工: view_permission 决定"怎么判定", OwnerGroupIDs 决定"判定给谁"。
	OwnerGroupIDs []string  `json:"owner_group_ids"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// normalizeVisibility 把留空的可见性/编辑权限补成文档声明的默认值。
//
// 必要性: 该列语义是 "all(默认)"(建表 DDL 默认值同为 'all'), 但写入路径并不保证带这两个字段
// —— 历史数据与按 API 直接创建的文章大量是空串而非 'all'。检索侧对 view_permission 是
// fail-closed 判定(只有公开可召回), 留空会让「已发布」的文章被静默判定为受限而不参与 AI 检索。
// 因此在写入前统一归一, 让库里的取值始终是显式值, 不依赖读取侧的兜底。
func (a *Article) normalizeVisibility() {
	if strings.TrimSpace(a.ViewPermission) == "" {
		a.ViewPermission = ViewPermissionAll
	}
	if strings.TrimSpace(a.EditPermission) == "" {
		a.EditPermission = EditPermissionOwner
	}
	a.normalizeOwnership()
}

// normalizeOwnerGroupIDs 归一归属团队: trim / 去空串 / 去重 / 升序。
//
// 两个必须遵守的约束:
//  1. 返回**非 nil** 切片。owner_group_ids 在库里是 NOT NULL DEFAULT '{}'::text[],
//     而 pq.Array(nil) 会编码成 SQL NULL 直接撞约束报错; pq.Array([]string{}) 才是 '{}'。
//  2. 排序必须稳定。数组值会参与检索谓词的数组重叠匹配, 并间接影响缓存 key 的可预测性。
func normalizeOwnerGroupIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// normalizeOwnership 归一归属信息, 并保证归属只在 team 语义下存在。
//
// 非 team 可见时清空归属是安全必需: 若保留, 用户把可见范围从 team 切成 all 再切回 team 时,
// 会浮现上一轮残留的团队归属 —— 编辑器里看不到却已生效的越权可见面。
func (a *Article) normalizeOwnership() {
	a.OwnerGroupIDs = normalizeOwnerGroupIDs(a.OwnerGroupIDs)
	if a.ViewPermission != ViewPermissionTeam {
		a.OwnerGroupIDs = []string{}
	}
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

// ChunkRef 知识块引用(用于附件抽取结果预览 / 引用溯源定位)。
type ChunkRef struct {
	ID      string `json:"id"`
	Heading string `json:"heading,omitempty"`
	Content string `json:"content"`
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
