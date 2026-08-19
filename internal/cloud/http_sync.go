package cloud

import (
	"encoding/json"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
)

// syncRequest 是 POST /api/v1/cloud/resources/sync 的请求体。
type syncRequest struct {
	Provider     string              `json:"provider"`
	Region       string              `json:"region"`
	Types        []string            `json:"types"`
	Credential   *resourceCredential `json:"credential"`
	CredentialID string              `json:"credential_id"`
	AgentID      string              `json:"agent_id"`      // k8s：目标集群网关 Agent
	K8s          K8sDiscoverOptions  `json:"k8s,omitempty"` // k8s：发现选项
	Resources    []Resource          `json:"resources"`
}

// handleSyncResources 把云资源发现并同步进 CMDB。
// 若请求体携带 resources[] 则直接同步；否则先按条件发现再同步。
// provider=="k8s" 时走 Agent 通道（见 handleSyncK8sResources）。
// 返回 SyncResult{created,updated,skipped,failed,errors} 供前端展示。
func (s *Service) handleSyncResources(w http.ResponseWriter, r *http.Request) {
	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Provider == "" {
		httputil.WriteError(w, http.StatusBadRequest, "provider is required")
		return
	}
	if req.Provider == K8sProviderName {
		s.handleSyncK8sResources(w, r, req)
		return
	}
	if !Exists(req.Provider) {
		httputil.WriteError(w, http.StatusBadRequest, "unknown provider: "+req.Provider)
		return
	}

	resources := req.Resources
	if len(resources) == 0 {
		// 未携带资源 → 先发现
		p, err := Get(req.Provider)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
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
			httputil.WriteError(w, http.StatusBadRequest, cerr.Error())
			return
		}
		kinds := parseKinds(req.Types)
		if len(kinds) == 0 {
			kinds = []ResourceType{TypeVM, TypeDatabase, TypeLoadBalancer}
		}
		region := firstNonEmpty(req.Region, cred.Region)
		resources, err = p.ListResources(r.Context(), cred, region, kinds)
		if err != nil {
			httputil.WriteError(w, http.StatusBadGateway, "failed to discover resources: "+err.Error())
			return
		}
	}

	tenantID := tenantFromCtx(r.Context())
	result := s.cmdb.SyncResources(r.Context(), tenantID, resources)
	httputil.WriteJSON(w, http.StatusOK, result)
}

// handleSyncK8sResources 把 K8s 资源同步进 CMDB。
// 请求体带 resources[] 则直接同步（前端「先发现再纳管」主路径）；
// 否则带 agent_id+types 经 discoverer 重新发现再同步。
// discoverer 未注入 → 400。
func (s *Service) handleSyncK8sResources(w http.ResponseWriter, r *http.Request, req syncRequest) {
	if s.k8sDiscoverer == nil {
		httputil.WriteError(w, http.StatusBadRequest, "k8s discovery not configured")
		return
	}
	resources := req.Resources
	if len(resources) == 0 {
		if req.AgentID == "" {
			httputil.WriteError(w, http.StatusBadRequest, "agent_id or resources is required for k8s sync")
			return
		}
		kinds := parseKinds(req.Types)
		if len(kinds) == 0 {
			kinds = K8sResourceTypes()
		}
		var err error
		resources, err = s.k8sDiscoverer.Discover(r.Context(), tenantFromCtx(r.Context()), req.AgentID, kinds, req.K8s)
		if err != nil {
			httputil.WriteError(w, http.StatusBadGateway, "failed to discover k8s resources: "+err.Error())
			return
		}
	}
	if resources == nil {
		resources = []Resource{}
	}
	tenantID := tenantFromCtx(r.Context())
	result := s.cmdb.SyncResources(r.Context(), tenantID, resources)
	httputil.WriteJSON(w, http.StatusOK, result)
}
