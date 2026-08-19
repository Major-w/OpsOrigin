package cloud

import (
	"fmt"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
)

// resourceCredential 是请求体中携带的临时凭证（不持久化）。
type resourceCredential struct {
	AccessKey string            `json:"access_key"`
	SecretKey string            `json:"secret_key"`
	Region    string            `json:"region"`
	Extra     map[string]string `json:"extra"`
}

// resourceListRequest 是 POST /api/v1/cloud/resources/list 的请求体。
type resourceListRequest struct {
	Provider     string              `json:"provider"`
	Region       string              `json:"region"`
	Types        []string            `json:"types"`
	Credential   *resourceCredential `json:"credential"`
	CredentialID string              `json:"credential_id"`
	AgentID      string              `json:"agent_id"`      // k8s：目标集群网关 Agent
	K8s          K8sDiscoverOptions  `json:"k8s,omitempty"` // k8s：发现选项（kubeconfig/context）
}

// handleListResources 按厂商/地域/类型发现云资源并返回。
// 凭证可经请求体临时携带（credential），亦可引用已保存的凭证（credential_id）。
// provider=="k8s" 时走 Agent 通道发现（见 handleListK8sResources）。
func (s *Service) handleListResources(w http.ResponseWriter, r *http.Request) {
	var req resourceListRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Provider == "" {
		httputil.WriteError(w, http.StatusBadRequest, "provider is required")
		return
	}
	if req.Provider == K8sProviderName {
		s.handleListK8sResources(w, r, req)
		return
	}
	if !Exists(req.Provider) {
		httputil.WriteError(w, http.StatusBadRequest, "unknown provider: "+req.Provider)
		return
	}
	p, err := Get(req.Provider)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	cred, cerr := s.resolveCredential(req)
	if cerr != nil {
		httputil.WriteError(w, http.StatusBadRequest, cerr.Error())
		return
	}
	kinds := parseKinds(req.Types)
	if len(kinds) == 0 {
		kinds = []ResourceType{TypeVM, TypeDatabase, TypeLoadBalancer}
	}
	region := firstNonEmpty(req.Region, cred.Region)
	resources, err := p.ListResources(r.Context(), cred, region, kinds)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "failed to list resources: "+err.Error())
		return
	}
	if resources == nil {
		resources = []Resource{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"resources": resources})
}

// resolveCredential 解析请求中的凭证：优先用 credential_id 从 Store 取，
// 否则用请求体临时凭证构造。两者皆无则报错。
func (s *Service) resolveCredential(req resourceListRequest) (Credential, error) {
	if req.CredentialID != "" {
		provider, credID := splitCredentialID(req.CredentialID)
		if provider == "" {
			return Credential{}, fmt.Errorf("invalid credential_id")
		}
		c, err := s.store.Get(provider, credID)
		if err != nil {
			return Credential{}, err
		}
		if c == nil {
			return Credential{}, fmt.Errorf("credential not found")
		}
		return *c, nil
	}
	if req.Credential != nil {
		return Credential{
			Provider:  req.Provider,
			Region:    firstNonEmpty(req.Region, req.Credential.Region),
			AccessKey: req.Credential.AccessKey,
			SecretKey: req.Credential.SecretKey,
			Extra:     req.Credential.Extra,
		}, nil
	}
	return Credential{}, fmt.Errorf("credential or credential_id is required")
}

// handleListK8sResources 经 Agent 通道发现 K8s 资源并返回。
// discoverer 未注入（agentmgr 未就绪）或 agent_id 缺失 → 400。
func (s *Service) handleListK8sResources(w http.ResponseWriter, r *http.Request, req resourceListRequest) {
	if req.AgentID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "agent_id is required for k8s discovery")
		return
	}
	if s.k8sDiscoverer == nil {
		httputil.WriteError(w, http.StatusBadRequest, "k8s discovery not configured")
		return
	}
	kinds := parseKinds(req.Types)
	if len(kinds) == 0 {
		kinds = K8sResourceTypes()
	}
	resources, err := s.k8sDiscoverer.Discover(r.Context(), auth.TenantFromContext(r.Context()), req.AgentID, kinds, req.K8s)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "failed to discover k8s resources: "+err.Error())
		return
	}
	if resources == nil {
		resources = []Resource{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"resources": resources})
}

// parseKinds 把字符串类型列表转换为 ResourceType 切片，过滤掉不支持的类型。
// 返回空切片时由调用方回退到 MVP 默认三类。
func parseKinds(types []string) []ResourceType {
	var kinds []ResourceType
	for _, t := range types {
		switch ResourceType(t) {
		case TypeVM, TypeDatabase, TypeLoadBalancer, TypeVPC, TypeBucket, TypeDisk, TypeFirewall, TypeNAT, TypeCDN, TypeDNS:
			kinds = append(kinds, ResourceType(t))
		case TypeK8sCluster, TypeK8sNamespace, TypeK8sWorkload, TypeK8sPod, TypeK8sContainer:
			kinds = append(kinds, ResourceType(t))
		}
	}
	return kinds
}
