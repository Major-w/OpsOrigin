package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PrecipitationRule 自动沉淀策略: 定义从某类运维事件/来源自动生成知识库文章的模板与开关。
// 这是原本硬编码在 ITSM 工单完成回调里的"处置案例沉淀"的可配置化替代。
type PrecipitationRule struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Source          string `json:"source"`           // itsm | alert | chat | manual
	Category        string `json:"category"`         // 沉淀到哪个分类
	TitleTemplate   string `json:"title_template"`   // 支持 {{变量}} 占位
	ContentTemplate string `json:"content_template"` // 支持 {{变量}} 占位
	Enabled         bool   `json:"enabled"`
	AutoPublish     bool   `json:"auto_publish"`
	CreatedAt       string `json:"created_at"`
}

// PrecipitationStore 管理自动沉淀策略, 并能将策略按变量渲染为真实文章落库到知识库。
type PrecipitationStore struct {
	db  *sql.DB
	svc *Service
}

func NewPrecipitationStore(db *sql.DB, svc *Service) *PrecipitationStore {
	s := &PrecipitationStore{db: db, svc: svc}
	s.migrate()
	return s
}

func (s *PrecipitationStore) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS knowledge_precipitation_rules (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		source TEXT NOT NULL DEFAULT 'manual',
		category TEXT DEFAULT '',
		title_template TEXT DEFAULT '',
		content_template TEXT DEFAULT '',
		enabled BOOLEAN DEFAULT true,
		auto_publish BOOLEAN DEFAULT false,
		created_at TIMESTAMPTZ DEFAULT NOW()
	)`)
}

func (s *PrecipitationStore) List(ctx context.Context) ([]PrecipitationRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,source,category,title_template,content_template,enabled,auto_publish,created_at FROM knowledge_precipitation_rules ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrecipitationRule
	for rows.Next() {
		var r PrecipitationRule
		rows.Scan(&r.ID, &r.Name, &r.Source, &r.Category, &r.TitleTemplate, &r.ContentTemplate, &r.Enabled, &r.AutoPublish, &r.CreatedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PrecipitationStore) Get(ctx context.Context, id string) (*PrecipitationRule, error) {
	var r PrecipitationRule
	err := s.db.QueryRowContext(ctx, `SELECT id,name,source,category,title_template,content_template,enabled,auto_publish,created_at FROM knowledge_precipitation_rules WHERE id=$1`, id).
		Scan(&r.ID, &r.Name, &r.Source, &r.Category, &r.TitleTemplate, &r.ContentTemplate, &r.Enabled, &r.AutoPublish, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *PrecipitationStore) Save(ctx context.Context, r *PrecipitationRule) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO knowledge_precipitation_rules(id,name,source,category,title_template,content_template,enabled,auto_publish,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT(id) DO UPDATE SET name=$2,source=$3,category=$4,title_template=$5,content_template=$6,enabled=$7,auto_publish=$8`,
		r.ID, r.Name, r.Source, r.Category, r.TitleTemplate, r.ContentTemplate, r.Enabled, r.AutoPublish, time.Now())
	return err
}

func (s *PrecipitationStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM knowledge_precipitation_rules WHERE id=$1`, id)
	return err
}

// RenderAndCreate 按变量渲染模板并创建一篇知识库文章(落库到 svc)。
// vars 形如 {"对象":"主机A","事件":"磁盘告警"}。返回新建文章 ID。
func (s *PrecipitationStore) RenderAndCreate(ctx context.Context, r *PrecipitationRule, vars map[string]string) (string, error) {
	title := renderTemplate(r.TitleTemplate, vars)
	content := renderTemplate(r.ContentTemplate, vars)
	status := "draft"
	if r.AutoPublish {
		status = "published"
	}
	article := &Article{
		TenantID: "default",
		Title:    title,
		Content:  content,
		Category: r.Category,
		Tags:     []string{"auto-precipitated"},
		Status:   status,
		Author:   "precipitation",
	}
	if err := s.svc.CreateArticle(ctx, article); err != nil {
		return "", err
	}
	return article.ID, nil
}

func renderTemplate(tpl string, vars map[string]string) string {
	if tpl == "" {
		return ""
	}
	out := tpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
		out = strings.ReplaceAll(out, "{{ "+k+" }}", v)
	}
	return out
}

func (s *PrecipitationStore) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/knowledge/precipitation-rules", s.handleList)
	mux.HandleFunc("POST /api/v1/knowledge/precipitation-rules", s.handleCreate)
	mux.HandleFunc("PUT /api/v1/knowledge/precipitation-rules/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/knowledge/precipitation-rules/{id}", s.handleDelete)
	mux.HandleFunc("POST /api/v1/knowledge/precipitation-rules/{id}/run", s.handleRun)
}

func (s *PrecipitationStore) handleList(w http.ResponseWriter, r *http.Request) {
	rules, _ := s.List(r.Context())
	httputil.WriteJSON(w, 200, rules)
}

func (s *PrecipitationStore) handleCreate(w http.ResponseWriter, r *http.Request) {
	var rule PrecipitationRule
	json.NewDecoder(r.Body).Decode(&rule)
	if rule.Name == "" {
		httputil.WriteError(w, 400, "name is required")
		return
	}
	s.Save(r.Context(), &rule)
	httputil.WriteJSON(w, 201, rule)
}

func (s *PrecipitationStore) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var rule PrecipitationRule
	json.NewDecoder(r.Body).Decode(&rule)
	rule.ID = r.PathValue("id")
	s.Save(r.Context(), &rule)
	httputil.WriteJSON(w, 200, rule)
}

func (s *PrecipitationStore) handleDelete(w http.ResponseWriter, r *http.Request) {
	s.Delete(r.Context(), r.PathValue("id"))
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *PrecipitationStore) handleRun(w http.ResponseWriter, r *http.Request) {
	rule, err := s.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		httputil.WriteError(w, 404, "rule not found")
		return
	}
	var body struct {
		Vars map[string]string `json:"vars"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Vars == nil {
		body.Vars = map[string]string{}
	}
	id, err := s.RenderAndCreate(r.Context(), rule, body.Vars)
	if err != nil {
		httputil.WriteError(w, 500, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"article_id": id, "status": "created"})
}
