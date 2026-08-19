package ai

import (
	"encoding/json"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
	"strings"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/auth"
)

// handlePublishSkill 把指定数字员工打包为技能包并发布到市场目录。
// 需 ai:tool:exec 权限(发布属于运维能力管理操作)。
// body: {agent_id, name?, version?, author?, description?, category?, tags?}
func (s *Service) handlePublishSkill(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		AgentID     string   `json:"agent_id"`
		Name        string   `json:"name"`
		Version     string   `json:"version"`
		Author      string   `json:"author"`
		Description string   `json:"description"`
		Category    string   `json:"category"`
		Tags        []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AgentID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	meta := agentic.SkillPackageMeta{
		Name:        req.Name,
		Version:     req.Version,
		Author:      req.Author,
		Description: req.Description,
		Category:    req.Category,
		Tags:        req.Tags,
	}
	pkg, err := s.Engine.PublishSkill(r.Context(), tenantFromCtx(r.Context()), req.AgentID, meta)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, pkg)
}

// handleListSkills 列出当前租户的技能包目录(可按 category / tag 过滤)。
func (s *Service) handleListSkills(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	tag := r.URL.Query().Get("tag")
	pkgs := s.Engine.ListSkillPackages(r.Context(), tenantFromCtx(r.Context()), category, tag)
	if pkgs == nil {
		pkgs = []*agentic.SkillPackage{}
	}
	httputil.WriteJSON(w, http.StatusOK, pkgs)
}

// handleGetSkill 返回单个技能包详情。
func (s *Service) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	pkg, err := s.Engine.GetSkillPackage(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id"))
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, pkg)
}

// handleDeleteSkill 下架一个技能包。需 ai:tool:exec 权限。
func (s *Service) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	if err := s.Engine.DeleteSkillPackage(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id")); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleInstallSkill 从技能包实例化一个新的数字员工。需 ai:tool:exec 权限。
// body: {name?} 自定义安装后的 Agent 名称(缺省自动加 " (市场)" 后缀)。
func (s *Service) handleInstallSkill(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	ag, err := s.Engine.InstallSkill(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id"), req.Name)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, ag)
}

// handleExportSkill 导出技能包为可移植 JSON(浏览器下载附件)。
func (s *Service) handleExportSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data, err := s.Engine.ExportSkill(r.Context(), tenantFromCtx(r.Context()), id)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	fn := "skill-package.json"
	if pkg, gerr := s.Engine.GetSkillPackage(r.Context(), tenantFromCtx(r.Context()), id); gerr == nil && pkg != nil {
		fn = slugify(pkg.Name) + ".json"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fn+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleImportSkill 从可移植 JSON 导入一个技能包。需 ai:tool:exec 权限。
// body: {data: <json string or object>} —— data 为技能包完整 JSON。
func (s *Service) handleImportSkill(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, auth.PermissionAIToolExec) {
		return
	}
	var req struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Data) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "data is required")
		return
	}
	// 允许传入 JSON 字符串(前端读文件后 JSON.stringify)或直接对象
	raw := req.Data
	if len(raw) > 1 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			raw = []byte(s)
		}
	}
	pkg, err := s.Engine.ImportSkill(r.Context(), tenantFromCtx(r.Context()), raw)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, pkg)
}

// handleRateSkill 对技能包打分(1~5 星)。用户身份从请求上下文注入, 无需特殊权限。
// body: {score}
func (s *Service) handleRateSkill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Score int `json:"score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Score < 1 || req.Score > 5 {
		httputil.WriteError(w, http.StatusBadRequest, "score must be between 1 and 5")
		return
	}
	if err := s.Engine.RateSkill(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id"), userFromCtx(r), req.Score); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "rated"})
}

// handleGetSkillRating 返回技能包评分均值 / 次数 / 当前用户打分。
func (s *Service) handleGetSkillRating(w http.ResponseWriter, r *http.Request) {
	avg, count, my, err := s.Engine.GetSkillRating(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id"), userFromCtx(r))
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"avg": avg, "count": count, "my_score": my})
}

// handleToggleFavorite 切换当前用户对技能包的收藏状态。
func (s *Service) handleToggleFavorite(w http.ResponseWriter, r *http.Request) {
	fav, err := s.Engine.ToggleFavorite(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id"), userFromCtx(r))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"favorited": fav})
}

// handleIsFavorite 返回当前用户是否已收藏该技能包。
func (s *Service) handleIsFavorite(w http.ResponseWriter, r *http.Request) {
	fav, err := s.Engine.IsFavorite(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id"), userFromCtx(r))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]bool{"favorited": fav})
}

// handleListFavorites 返回当前用户收藏的技能包列表。
func (s *Service) handleListFavorites(w http.ResponseWriter, r *http.Request) {
	pkgs := s.Engine.ListFavorites(r.Context(), tenantFromCtx(r.Context()), userFromCtx(r))
	if pkgs == nil {
		pkgs = []*agentic.SkillPackage{}
	}
	httputil.WriteJSON(w, http.StatusOK, pkgs)
}

// handleListVersions 返回与指定技能包同名的全部版本。
func (s *Service) handleListVersions(w http.ResponseWriter, r *http.Request) {
	vs := s.Engine.ListVersions(r.Context(), tenantFromCtx(r.Context()), r.PathValue("id"))
	if vs == nil {
		vs = []*agentic.SkillPackage{}
	}
	httputil.WriteJSON(w, http.StatusOK, vs)
}

// slugify 将名称转为安全的下载文件名(空格→-, 去除非常规字符)。
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "skill-package"
	}
	return out
}
