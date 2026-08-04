// Package openstack 实现 OpsOrigin 云资源纳管中 OpenStack 的 Provider。
// 采用 Keystone v3 认证 + Nova/Neutron REST API，仅依赖标准库，
// 与仓库内 AWS/Aliyun/VMware 的“纯标准库”风格保持一致。
//
// 纳管范围（MVP）：
//   - 计算：Nova 实例（/v2.1/servers）→ TypeVM
//   - 网络：Neutron 网络（/v2.0/networks）→ TypeVPC
//
// 凭证约定（复用 cloud.Credential）：
//
//	AccessKey = OpenStack 用户名
//	SecretKey = OpenStack 密码
//	Extra["auth_url"]   = Keystone 基址，如 http://keystone:5000/v3（缺省自动补 /v3）
//	Extra["project_id"]  = 项目名（scope，缺省回退到 username）
//	Extra["domain_name"] = 域（缺省 Default）
//	Extra["compute_url"] = Nova 基址（可选；缺省由 auth_url 路径 /v3 → /v2.1 推导）
//	Extra["network_url"] = Neutron 基址（可选；缺省由 auth_url 路径 /v3 → /v2.0 推导）
package openstack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cloud"
)

// provider 实现 cloud.Provider 接口。
type provider struct {
	http *http.Client
}

func init() {
	cloud.Register(&provider{http: &http.Client{Timeout: 30 * time.Second}})
	cloud.RegisterKinds("openstack", cloud.TypeVM, cloud.TypeVPC)
}

// Name 返回提供商标识。
func (p *provider) Name() string { return "openstack" }

// SupportedVersions 声明本 Provider 适配/验证过的 OpenStack 版本。
func (p *provider) SupportedVersions() []cloud.ProviderVersion {
	return []cloud.ProviderVersion{
		{Version: "Rocky / Stein / Train / Ussuri / Victoria+", Status: cloud.VersionVerified, Note: "Keystone v3 认证，REST 兼容"},
		{Version: "Queens 及更早", Status: cloud.VersionUntested, Note: "未实测，建议 Rocky 及以上"},
	}
}

// ListResources 列举指定地域、指定类型的 OpenStack 资源。
// 单类资源发现失败仅记录日志并跳过，不影响其他类。
func (p *provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	authURL := normalizeKeystone(strings.TrimRight(firstNonEmpty(cred.Extra["auth_url"], cred.Extra["endpoint"]), "/"))
	if authURL == "" {
		return nil, fmt.Errorf("openstack: credential extra.auth_url is required")
	}
	domain := firstNonEmpty(cred.Extra["domain_name"], "Default")
	project := firstNonEmpty(cred.Extra["project_id"], cred.Extra["project_name"], cred.AccessKey)
	token, err := p.login(ctx, authURL, cred.AccessKey, cred.SecretKey, project, domain)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = "RegionOne"
	}

	computeURL := computeBase(cred, authURL)
	neutronURL := neutronBase(cred, authURL)

	var out []cloud.Resource
	for _, k := range kinds {
		var (
			res []cloud.Resource
			e   error
		)
		switch k {
		case cloud.TypeVM:
			res, e = p.listServers(ctx, computeURL, token, region)
		case cloud.TypeVPC:
			res, e = p.listNetworks(ctx, neutronURL, token, region)
		default:
			continue
		}
		if e != nil {
			slog.Error("openstack list kind failed", "kind", k, "err", e)
			continue
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个资源详情（MVP 仅虚拟机支持详情）。
func (p *provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	authURL := normalizeKeystone(strings.TrimRight(firstNonEmpty(cred.Extra["auth_url"], cred.Extra["endpoint"]), "/"))
	if authURL == "" {
		return nil, fmt.Errorf("openstack: credential extra.auth_url is required")
	}
	domain := firstNonEmpty(cred.Extra["domain_name"], "Default")
	project := firstNonEmpty(cred.Extra["project_id"], cred.Extra["project_name"], cred.AccessKey)
	token, err := p.login(ctx, authURL, cred.AccessKey, cred.SecretKey, project, domain)
	if err != nil {
		return nil, err
	}
	if kind != string(cloud.TypeVM) {
		return nil, &cloud.NoSuchResource{Provider: "openstack", ID: id}
	}
	computeURL := computeBase(cred, authURL)
	var srv novaServerDetail
	if err := p.get(ctx, computeURL+"/servers/"+id, token, &srv); err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, &cloud.NoSuchResource{Provider: "openstack", ID: id}
		}
		return nil, err
	}
	res := &cloud.Resource{
		Provider:     "openstack",
		Type:         cloud.TypeVM,
		ID:           srv.Server.ID,
		Name:         srv.Server.Name,
		Region:       firstNonEmpty(region, "RegionOne"),
		Status:       strings.ToLower(srv.Server.Status),
		PrivateIP:    firstServerIP(srv.Server.Addresses),
		Extra:        map[string]any{"tenant_id": srv.Server.TenantID},
		DiscoveredAt: time.Now(),
	}
	return res, nil
}

// CollectMetrics 占位：Ceilometer 指标采集需在 collector 层接入，此处返回 nil。
func (p *provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// ——— Keystone / Nova / Neutron REST 辅助 ———

// normalizeKeystone 确保 auth 基址以 /v3 结尾。
func normalizeKeystone(u string) string {
	if u == "" {
		return ""
	}
	if !strings.HasSuffix(u, "/v3") {
		u = strings.TrimRight(u, "/") + "/v3"
	}
	return u
}

// computeBase 推导 Nova 基址：优先 Extra["compute_url"]，否则将 auth_url 的 /v3 替换为 /v2.1。
func computeBase(cred cloud.Credential, authURL string) string {
	if u := cred.Extra["compute_url"]; u != "" {
		return strings.TrimRight(u, "/")
	}
	return strings.Replace(authURL, "/v3", "/v2.1", 1)
}

// neutronBase 推导 Neutron 基址：优先 Extra["network_url"]，否则将 auth_url 的 /v3 替换为 /v2.0。
func neutronBase(cred cloud.Credential, authURL string) string {
	if u := cred.Extra["network_url"]; u != "" {
		return strings.TrimRight(u, "/")
	}
	return strings.Replace(authURL, "/v3", "/v2.0", 1)
}

// login 通过 Keystone v3 密码认证获取 X-Subject-Token。
func (p *provider) login(ctx context.Context, authURL, user, pass, project, domain string) (string, error) {
	payload := map[string]any{
		"auth": map[string]any{
			"identity": map[string]any{
				"methods": []string{"password"},
				"password": map[string]any{
					"user": map[string]any{
						"name":     user,
						"domain":   map[string]any{"name": domain},
						"password": pass,
					},
				},
			},
			"scope": map[string]any{
				"project": map[string]any{
					"name":   project,
					"domain": map[string]any{"name": domain},
				},
			},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL+"/auth/tokens", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openstack auth: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("openstack auth failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	token := resp.Header.Get("X-Subject-Token")
	if token == "" {
		return "", fmt.Errorf("openstack auth: empty token")
	}
	return token, nil
}

// get 携带 X-Auth-Token 发起 GET 并解码 JSON。
func (p *provider) get(ctx context.Context, url, token string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("openstack GET %s: %s %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// novaAddr 是 Nova 实例 addresses 映射中的一条地址。
type novaAddr struct {
	Addr    string `json:"addr"`
	Version int    `json:"version"`
}

// novaServer 对应 Nova server 摘要 / 详情中的 server 对象。
type novaServer struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Status    string                `json:"status"`
	TenantID  string                `json:"tenant_id"`
	Addresses map[string][]novaAddr `json:"addresses"`
}

type novaServerList struct {
	Servers []novaServer `json:"servers"`
}

type novaServerDetail struct {
	Server novaServer `json:"server"`
}

func (p *provider) listServers(ctx context.Context, base, token, region string) ([]cloud.Resource, error) {
	var lst novaServerList
	if err := p.get(ctx, base+"/servers", token, &lst); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(lst.Servers))
	now := time.Now()
	for _, s := range lst.Servers {
		out = append(out, cloud.Resource{
			Provider:     "openstack",
			Type:         cloud.TypeVM,
			ID:           s.ID,
			Name:         s.Name,
			Region:       region,
			Status:       strings.ToLower(s.Status),
			PrivateIP:    firstServerIP(s.Addresses),
			Extra:        map[string]any{"tenant_id": s.TenantID},
			DiscoveredAt: now,
		})
	}
	return out, nil
}

type neutronNet struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Subnets []string `json:"subnets"`
}

type neutronNetList struct {
	Networks []neutronNet `json:"networks"`
}

func (p *provider) listNetworks(ctx context.Context, base, token, region string) ([]cloud.Resource, error) {
	var lst neutronNetList
	if err := p.get(ctx, base+"/networks", token, &lst); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(lst.Networks))
	now := time.Now()
	for _, n := range lst.Networks {
		out = append(out, cloud.Resource{
			Provider:     "openstack",
			Type:         cloud.TypeVPC,
			ID:           n.ID,
			Name:         n.Name,
			Region:       region,
			Status:       strings.ToLower(n.Status),
			Extra:        map[string]any{"subnets": n.Subnets},
			DiscoveredAt: now,
		})
	}
	return out, nil
}

// firstServerIP 从 Nova addresses 映射中取第一个地址。
func firstServerIP(addrs map[string][]novaAddr) string {
	for _, list := range addrs {
		for _, a := range list {
			if a.Addr != "" {
				return a.Addr
			}
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
