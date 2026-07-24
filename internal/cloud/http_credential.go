package cloud

import (
	"encoding/json"
	"net/http"
	"strings"
)

// providerInfo 描述一个已注册云商及其支持的资源类型。
type providerInfo struct {
	Name  string   `json:"name"`
	Types []string `json:"types"`
}

// credentialView 是凭证对外视图，刻意不包含 SecretKey，避免密钥回显/泄漏。
type credentialView struct {
	ID        string            `json:"id"`
	Provider  string            `json:"provider"`
	Region    string            `json:"region"`
	AccessKey string            `json:"access_key"`
	Extra     map[string]string `json:"extra,omitempty"`
	HasSecret bool              `json:"has_secret"`
}

// handleListProviders 返回全部已注册云商及其支持的资源类型。
// MVP 全局支持 vm / database / load_balancer 三类。
func (s *Service) handleListProviders(w http.ResponseWriter, r *http.Request) {
	names := Names()
	types := []string{string(TypeVM), string(TypeDatabase), string(TypeLoadBalancer)}
	providers := make([]providerInfo, 0, len(names))
	for _, n := range names {
		providers = append(providers, providerInfo{Name: n, Types: types})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

// handleListCredentials 列出本作用域已保存的凭证（不返回 SecretKey）。
func (s *Service) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	names := Names()
	views := make([]credentialView, 0)
	for _, p := range names {
		creds, err := s.store.List(p)
		if err != nil || creds == nil {
			continue
		}
		for _, c := range creds {
			views = append(views, credentialView{
				ID:        idOf(c),
				Provider:  c.Provider,
				Region:    c.Region,
				AccessKey: c.AccessKey,
				Extra:     c.Extra,
				HasSecret: c.SecretKey != "",
			})
		}
	}
	if views == nil {
		views = []credentialView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": views})
}

// handleCreateCredential 保存一条云凭证到 Store，响应中不回显 SecretKey。
func (s *Service) handleCreateCredential(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider  string            `json:"provider"`
		Region    string            `json:"region"`
		AccessKey string            `json:"access_key"`
		SecretKey string            `json:"secret_key"`
		Extra     map[string]string `json:"extra"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}
	if !Exists(req.Provider) {
		writeError(w, http.StatusBadRequest, "unknown provider: "+req.Provider)
		return
	}
	cred := Credential{
		Provider:  req.Provider,
		Region:    req.Region,
		AccessKey: req.AccessKey,
		SecretKey: req.SecretKey,
		Extra:     req.Extra,
	}
	if err := s.store.Save(cred); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save credential")
		return
	}
	// 安全：响应中不携带 SecretKey，仅返回脱敏视图。
	writeJSON(w, http.StatusCreated, credentialView{
		ID:        idOf(cred),
		Provider:  cred.Provider,
		Region:    cred.Region,
		AccessKey: cred.AccessKey,
		Extra:     cred.Extra,
		HasSecret: cred.SecretKey != "",
	})
}

// handleDeleteCredential 按 id 删除一条已保存凭证。
func (s *Service) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	provider, credID := splitCredentialID(id)
	if provider == "" {
		writeError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	if err := s.store.Delete(provider, credID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete credential")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// splitCredentialID 从凭证 id（格式 "<provider>:<id>"）拆分出 provider 与内部 id。
func splitCredentialID(id string) (string, string) {
	idx := strings.Index(id, ":")
	if idx < 0 {
		return "", id
	}
	return id[:idx], id[idx+1:]
}
