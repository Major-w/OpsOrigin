package iam

// 组织架构（部门树）HTTP 端点。
// 权限：/api/v1/iam 前缀已映射 user:read / user:write（rbac.go 前缀回退），
// 与 groups/roles 同模式，无需单独登记。

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleListDepartments(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	depts, err := s.store.ListDepartments(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if depts == nil {
		depts = []*Department{}
	}
	httputil.WriteJSON(w, http.StatusOK, depts)
}

func (s *Service) handleCreateDepartment(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	var req struct {
		Name        string `json:"name"`
		ParentID    string `json:"parent_id"`
		Description string `json:"description"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "部门名称不能为空")
		return
	}
	if req.ParentID != "" {
		parent, err := s.store.GetDepartment(r.Context(), req.ParentID, tenantID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if parent == nil {
			httputil.WriteError(w, http.StatusBadRequest, "上级部门不存在")
			return
		}
	}
	d := &Department{
		TenantID:    tenantID,
		Name:        req.Name,
		ParentID:    req.ParentID,
		Description: strings.TrimSpace(req.Description),
		Source:      "local",
	}
	if err := s.store.CreateDepartment(r.Context(), d); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, d)
}

func (s *Service) handleUpdateDepartment(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	id := r.PathValue("id")
	existing, err := s.store.GetDepartment(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "部门不存在")
		return
	}
	var req struct {
		Name        string  `json:"name"`
		ParentID    *string `json:"parent_id"` // 指针语义：未传=不动
		Description *string `json:"description"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		existing.Name = name
	}
	if req.ParentID != nil {
		pid := strings.TrimSpace(*req.ParentID)
		if pid == id {
			httputil.WriteError(w, http.StatusBadRequest, "上级部门不能是自己")
			return
		}
		if pid != "" {
			parent, err := s.store.GetDepartment(r.Context(), pid, tenantID)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if parent == nil {
				httputil.WriteError(w, http.StatusBadRequest, "上级部门不存在")
				return
			}
			// 环检测：沿 parent 链向上走，若回到自己则成环
			walk, depth := pid, 0
			for walk != "" && depth < maxDeptDepth {
				p, err := s.store.GetDepartment(r.Context(), walk, tenantID)
				if err != nil || p == nil {
					break
				}
				if p.ID == id {
					httputil.WriteError(w, http.StatusBadRequest, "不能把部门挂到自己的子孙节点下")
					return
				}
				walk = p.ParentID
				depth++
			}
			if depth >= maxDeptDepth {
				httputil.WriteError(w, http.StatusBadRequest, "部门层级过深（疑似环路）")
				return
			}
		}
		existing.ParentID = pid
	}
	if req.Description != nil {
		existing.Description = strings.TrimSpace(*req.Description)
	}
	if err := s.store.UpdateDepartment(r.Context(), existing); err != nil {
		httputil.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, existing)
}

func (s *Service) handleDeleteDepartment(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	if err := s.store.DeleteDepartment(r.Context(), r.PathValue("id"), tenantID); err != nil {
		if strings.Contains(err.Error(), "子部门") {
			httputil.WriteError(w, http.StatusConflict, err.Error())
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleImportDepartments 批量导入部门（CSV multipart）。
// 列：path[,description]。path 为路径式部门链（如 "总部/华东/门店A"），逐级幂等建档：
// 已存在且父级一致→复用；已存在但父级冲突→该行报错。description 仅作用于路径末级。
func (s *Service) handleImportDepartments(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantIDFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to parse form")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "failed to parse CSV: "+err.Error())
		return
	}

	type importError struct {
		Row     int    `json:"row"`
		Message string `json:"message"`
	}
	resp := struct {
		Created int           `json:"created"`
		Reused  int           `json:"reused"`
		Failed  int           `json:"failed"`
		Errors  []importError `json:"errors,omitempty"`
	}{Errors: []importError{}}

	for i, row := range records {
		if i == 0 && strings.EqualFold(strings.TrimSpace(row[0]), "path") {
			continue // 表头
		}
		path := strings.TrimSpace(row[0])
		if path == "" {
			resp.Errors = append(resp.Errors, importError{Row: i + 1, Message: "path 为空"})
			resp.Failed++
			continue
		}
		desc := ""
		if len(row) > 1 {
			desc = strings.TrimSpace(row[1])
		}

		// 路径逐级建档，父 ID 串起来
		var parentID string
		segments := strings.Split(path, "/")
		for j, seg := range segments {
			name := strings.TrimSpace(seg)
			if name == "" {
				resp.Errors = append(resp.Errors, importError{Row: i + 1, Message: fmt.Sprintf("路径第 %d 级名称为空", j+1)})
				resp.Failed++
				parentID = "" // 标记失败
				break
			}
			id, created, err := s.store.EnsureDepartmentUnder(r.Context(), tenantID, name, parentID, "local")
			if err != nil {
				resp.Errors = append(resp.Errors, importError{Row: i + 1, Message: err.Error()})
				resp.Failed++
				parentID = ""
				break
			}
			if created {
				resp.Created++
			} else {
				resp.Reused++
			}
			parentID = id
		}
		// 末级补描述
		if parentID != "" && desc != "" {
			if d, err := s.store.GetDepartment(r.Context(), parentID, tenantID); err == nil && d != nil {
				d.Description = desc
				if err := s.store.UpdateDepartment(r.Context(), d); err != nil {
					resp.Errors = append(resp.Errors, importError{Row: i + 1, Message: "写描述失败: " + err.Error()})
				}
			}
		}
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

// maxDeptDepth 部门树深度上限（防脏数据环路）。
const maxDeptDepth = 8
