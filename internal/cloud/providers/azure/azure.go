// Package azure 实现 Microsoft Azure 资源纳管，遵循 cloud.Provider 契约。
//
// 鉴权采用 OAuth2 client_credentials 流程获取 ARM Bearer Token（基于 tenant_id /
// client_id / client_secret），再调用 Azure Resource Manager REST API 列举资源。
// 仅依赖标准库。
//
// 说明：本实现代码已按 Azure ARM REST 结构编写，但尚未接入真实订阅联调，
// SupportedVersions 标记为 untested（待测），待拿到凭据实测后转正为 verified。
//
// 覆盖资源：虚拟机（Microsoft.Compute/virtualMachines）、SQL 服务器
// （Microsoft.Sql/servers）、负载均衡（Microsoft.Network/loadBalancers）。
package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cloud"
)

// Provider Azure 纳管实现。
type Provider struct {
	http *http.Client
}

func init() {
	cloud.Register(&Provider{http: &http.Client{Timeout: 30 * time.Second}})
	cloud.RegisterKinds("azure", cloud.TypeVM, cloud.TypeDatabase, cloud.TypeLoadBalancer)
}

// Name 厂商标识。
func (p *Provider) Name() string { return "azure" }

// SupportedVersions 声明本 Provider 适配的 Azure 版本（代码已适配，待真实联调）。
func (p *Provider) SupportedVersions() []cloud.ProviderVersion {
	return []cloud.ProviderVersion{
		{Version: "ARM REST API（全域）", Status: cloud.VersionUntested, Note: "OAuth2 client_credentials + Bearer，待真实订阅联调验证"},
	}
}

// ListResources 按类型列举资源（订阅级，region 仅作提示）。
func (p *Provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	sub := cred.Extra["subscription_id"]
	if sub == "" {
		return nil, fmt.Errorf("azure: Extra[\"subscription_id\"] required")
	}
	token, err := p.token(ctx, cred)
	if err != nil {
		return nil, err
	}
	var out []cloud.Resource
	for _, k := range kinds {
		var (
			res []cloud.Resource
			e   error
		)
		switch k {
		case cloud.TypeVM:
			res, e = p.listVMs(ctx, token, sub, region)
		case cloud.TypeDatabase:
			res, e = p.listSQL(ctx, token, sub, region)
		case cloud.TypeLoadBalancer:
			res, e = p.listLBs(ctx, token, sub, region)
		default:
			continue
		}
		if e != nil {
			return out, fmt.Errorf("azure %s: %w", k, e)
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个虚拟机详情。
func (p *Provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	if kind != string(cloud.TypeVM) {
		return nil, fmt.Errorf("azure: GetResource only supports vm, got %q", kind)
	}
	sub := cred.Extra["subscription_id"]
	token, err := p.token(ctx, cred)
	if err != nil {
		return nil, err
	}
	res, err := p.listVMs(ctx, token, sub, region)
	if err != nil {
		return nil, err
	}
	for i := range res {
		if res[i].ID == id {
			return &res[i], nil
		}
	}
	return nil, &cloud.NoSuchResource{Provider: "azure", ID: id}
}

// CollectMetrics Azure 指标需对接 Azure Monitor，此处返回 nil（接口允许）。
func (p *Provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// token 通过 OAuth2 client_credentials 获取 ARM Bearer Token。
func (p *Provider) token(ctx context.Context, cred cloud.Credential) (string, error) {
	tenant := cred.Extra["tenant_id"]
	if tenant == "" {
		return "", fmt.Errorf("azure: Extra[\"tenant_id\"] required")
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", cred.AccessKey) // AccessKey = client_id
	form.Set("client_secret", cred.SecretKey)
	form.Set("scope", "https://management.azure.com/.default")

	u := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenant)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("azure: token endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var t struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &t); err != nil {
		return "", fmt.Errorf("azure: parse token: %w", err)
	}
	if t.AccessToken == "" {
		return "", fmt.Errorf("azure: get token failed: %s %s", t.Error, t.ErrorDesc)
	}
	return t.AccessToken, nil
}

// armGet 携带 Bearer Token 调用 ARM REST。
func (p *Provider) armGet(ctx context.Context, token, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("azure: GET %s returned %d: %s", rawURL, resp.StatusCode, string(body))
	}
	return body, nil
}

// ---------- Compute / VMs ----------

func (p *Provider) listVMs(ctx context.Context, token, sub, region string) ([]cloud.Resource, error) {
	api := "2024-03-01"
	rawURL := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/Microsoft.Compute/virtualMachines?api-version=%s", sub, api)
	body, err := p.armGet(ctx, token, rawURL)
	if err != nil {
		return nil, err
	}
	var r struct {
		Value []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Location   string `json:"location"`
			Properties struct {
				ProvisioningState string `json:"provisioningState"`
				HardwareProfile   struct {
					VMSize string `json:"vmSize"`
				} `json:"hardwareProfile"`
			} `json:"properties"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("azure: parse VMs: %w", err)
	}
	out := make([]cloud.Resource, 0, len(r.Value))
	for _, it := range r.Value {
		if region != "" && it.Location != region {
			continue
		}
		out = append(out, cloud.Resource{
			Provider: "azure", Type: cloud.TypeVM, ID: it.ID,
			Name: it.Name, Region: it.Location, Status: it.Properties.ProvisioningState,
			Extra: map[string]any{"vm_size": it.Properties.HardwareProfile.VMSize},
		})
	}
	return out, nil
}

// ---------- SQL servers ----------

func (p *Provider) listSQL(ctx context.Context, token, sub, region string) ([]cloud.Resource, error) {
	api := "2021-02-01-preview"
	rawURL := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/Microsoft.Sql/servers?api-version=%s", sub, api)
	body, err := p.armGet(ctx, token, rawURL)
	if err != nil {
		return nil, err
	}
	var r struct {
		Value []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Location   string `json:"location"`
			Properties struct {
				State string `json:"state"`
			} `json:"properties"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("azure: parse SQL: %w", err)
	}
	out := make([]cloud.Resource, 0, len(r.Value))
	for _, it := range r.Value {
		if region != "" && it.Location != region {
			continue
		}
		out = append(out, cloud.Resource{
			Provider: "azure", Type: cloud.TypeDatabase, ID: it.ID,
			Name: it.Name, Region: it.Location, Status: it.Properties.State,
		})
	}
	return out, nil
}

// ---------- Network / Load Balancers ----------

func (p *Provider) listLBs(ctx context.Context, token, sub, region string) ([]cloud.Resource, error) {
	api := "2023-04-01"
	rawURL := fmt.Sprintf("https://management.azure.com/subscriptions/%s/providers/Microsoft.Network/loadBalancers?api-version=%s", sub, api)
	body, err := p.armGet(ctx, token, rawURL)
	if err != nil {
		return nil, err
	}
	var r struct {
		Value []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Location   string `json:"location"`
			Properties struct {
				ProvisioningState string `json:"provisioningState"`
			} `json:"properties"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("azure: parse LBs: %w", err)
	}
	out := make([]cloud.Resource, 0, len(r.Value))
	for _, it := range r.Value {
		if region != "" && it.Location != region {
			continue
		}
		out = append(out, cloud.Resource{
			Provider: "azure", Type: cloud.TypeLoadBalancer, ID: it.ID,
			Name: it.Name, Region: it.Location, Status: it.Properties.ProvisioningState,
		})
	}
	return out, nil
}
