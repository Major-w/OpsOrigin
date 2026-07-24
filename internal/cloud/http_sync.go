package cloud

import (
	"encoding/json"
	"net/http"
)

// handleSyncResources 把云资源发现并同步进 CMDB。
// 若请求体携带 resources[] 则直接同步；否则先按条件发现再同步。
// 返回 SyncResult{created,updated,skipped,failed,errors} 供前端展示。
func (s *Service) handleSyncResources(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider     string              `json:"provider"`
		Region       string              `json:"region"`
		Types        []string            `json:"types"`
		Credential   *resourceCredential `json:"credential"`
		CredentialID string              `json:"credential_id"`
		Resources    []Resource          `json:"resources"`
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

	resources := req.Resources
	if len(resources) == 0 {
		// 未携带资源 → 先发现
		p, err := Get(req.Provider)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		cred, cerr := s.resolveCredential(resourceListRequest{
			Provider:     req.Provider,
			Region:       req.Region,
			Types:        req.Types,
			Credential:   req.Credential,
			CredentialID: req.CredentialID,
		})
		if cerr != nil {
			writeError(w, http.StatusBadRequest, cerr.Error())
			return
		}
		kinds := parseKinds(req.Types)
		if len(kinds) == 0 {
			kinds = []ResourceType{TypeVM, TypeDatabase, TypeLoadBalancer}
		}
		region := firstNonEmpty(req.Region, cred.Region)
		resources, err = p.ListResources(r.Context(), cred, region, kinds)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to discover resources: "+err.Error())
			return
		}
	}

	tenantID := tenantFromCtx(r.Context())
	result := s.cmdb.SyncResources(r.Context(), tenantID, resources)
	writeJSON(w, http.StatusOK, result)
}
