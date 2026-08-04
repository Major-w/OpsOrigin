// Package vmware 实现 OpsOrigin 云资源纳管中 VMware(vCenter) 的 Provider。
// 采用 vCenter REST API（/api/vcenter/*），仅依赖标准库，
// 与仓库内 AWS/Aliyun 的“纯标准库、无第三方 SDK”风格保持一致。
//
// 纳管范围（MVP）：
//   - 计算：虚拟机（/api/vcenter/vm）→ TypeVM
//   - 网络：标准端口组/分布式端口组（/api/vcenter/network）→ TypeVPC
//   - 存储：数据存储（/api/vcenter/datastore）→ TypeDisk
//
// 凭证约定（复用 cloud.Credential）：
//
//	AccessKey = vCenter 用户名
//	SecretKey = vCenter 密码
//	Extra["endpoint"] = vCenter 基址，如 https://vcsa.example.com
package vmware

import (
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
	cloud.RegisterKinds("vmware", cloud.TypeVM, cloud.TypeVPC, cloud.TypeDisk)
}

// Name 返回提供商标识。
func (p *provider) Name() string { return "vmware" }

// SupportedVersions 声明本 Provider 适配/验证过的 vCenter 版本。
func (p *provider) SupportedVersions() []cloud.ProviderVersion {
	return []cloud.ProviderVersion{
		{Version: "6.5", Status: cloud.VersionVerified, Note: "REST API 适配（含 6.5 内存字段兼容）"},
		{Version: "6.7", Status: cloud.VersionVerified},
		{Version: "7.0", Status: cloud.VersionVerified},
		{Version: "8.0", Status: cloud.VersionVerified},
		{Version: "< 6.5", Status: cloud.VersionUntested, Note: "vCenter REST API 自 6.5 起提供，旧版未适配"},
	}
}

// ListResources 列举指定地域、指定类型的 VMware 资源。
// 单类资源发现失败仅记录日志并跳过，不影响其他类（参照 k8s Discoverer 容错范式）。
func (p *provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	endpoint := strings.TrimRight(cred.Extra["endpoint"], "/")
	if endpoint == "" {
		return nil, fmt.Errorf("vmware: credential extra.endpoint is required")
	}
	token, err := p.login(ctx, endpoint, cred.AccessKey, cred.SecretKey)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = "vcenter"
	}
	var out []cloud.Resource
	for _, k := range kinds {
		var (
			res []cloud.Resource
			e   error
		)
		switch k {
		case cloud.TypeVM:
			res, e = p.listVMs(ctx, endpoint, token, region)
		case cloud.TypeVPC:
			res, e = p.listNetworks(ctx, endpoint, token, region)
		case cloud.TypeDisk:
			res, e = p.listDatastores(ctx, endpoint, token, region)
		default:
			continue
		}
		if e != nil {
			slog.Error("vmware list kind failed", "kind", k, "err", e)
			continue
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个资源详情（MVP 仅虚拟机支持详情）。
func (p *provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	endpoint := strings.TrimRight(cred.Extra["endpoint"], "/")
	if endpoint == "" {
		return nil, fmt.Errorf("vmware: credential extra.endpoint is required")
	}
	token, err := p.login(ctx, endpoint, cred.AccessKey, cred.SecretKey)
	if err != nil {
		return nil, err
	}
	if kind != string(cloud.TypeVM) {
		return nil, &cloud.NoSuchResource{Provider: "vmware", ID: id}
	}
	var d vmDetail
	if err := p.get(ctx, endpoint+"/api/vcenter/vm/"+id, token, &d); err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, &cloud.NoSuchResource{Provider: "vmware", ID: id}
		}
		return nil, err
	}
	mem := d.MemoryMiB
	if mem == 0 {
		mem = d.MemoryMB // 6.5 老版本回退
	}
	res := &cloud.Resource{
		Provider:  "vmware",
		Type:      cloud.TypeVM,
		ID:        d.VM,
		Name:      d.Name,
		Region:    firstNonEmpty(region, "vcenter"),
		Status:    strings.ToLower(d.PowerState),
		PrivateIP: firstIPv4(d.Nics),
		Extra: map[string]any{
			"cpu_count":    d.CPUCount,
			"memory_MiB":   mem,
			"guest_os":     d.GuestOS,
			"num_networks": len(d.Nics),
			"num_disks":    len(d.Disks),
		},
		DiscoveredAt: time.Now(),
	}
	return res, nil
}

// CollectMetrics 占位：vCenter 性能计数器采集需在 collector 层接入，此处返回 nil。
func (p *provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// ——— vCenter REST 辅助 ———

// login 通过 Basic Auth 创建 CIS session，返回 vmware-api-session-id。
func (p *provider) login(ctx context.Context, endpoint, user, pass string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/rest/com/vmware/cis/session", nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(user, pass)
	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("vmware login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vmware login failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if tok := resp.Header.Get("vmware-api-session-id"); tok != "" {
		return tok, nil
	}
	// 部分版本在 body 中返回 token
	var b struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &b)
	if b.Token != "" {
		return b.Token, nil
	}
	return "", fmt.Errorf("vmware login: empty session token")
}

// get 携带 session token 发起 GET 并解码 JSON。
func (p *provider) get(ctx context.Context, url, token string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("vmware-api-session-id", token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("vmware GET %s: %s %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

type vmSummary struct {
	VM         string `json:"vm"`
	Name       string `json:"name"`
	PowerState string `json:"power_state"`
	CPUCount   int    `json:"cpu_count"`
	MemoryMiB  int    `json:"memory_size_MiB"` // 6.7+ 字段名
	MemoryMB   int    `json:"memory_size_MB"`  // 6.5 老版本字段名兼容
	GuestOS    string `json:"guest_OS"`
}

func (p *provider) listVMs(ctx context.Context, endpoint, token, region string) ([]cloud.Resource, error) {
	var vms []vmSummary
	if err := p.get(ctx, endpoint+"/api/vcenter/vm", token, &vms); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(vms))
	now := time.Now()
	for _, v := range vms {
		mem := v.MemoryMiB
		if mem == 0 {
			mem = v.MemoryMB // 6.5 老版本回退
		}
		out = append(out, cloud.Resource{
			Provider: "vmware",
			Type:     cloud.TypeVM,
			ID:       v.VM,
			Name:     v.Name,
			Region:   region,
			Status:   strings.ToLower(v.PowerState),
			Extra: map[string]any{
				"cpu_count":  v.CPUCount,
				"memory_MiB": mem,
				"guest_os":   v.GuestOS,
			},
			DiscoveredAt: now,
		})
	}
	return out, nil
}

type netSummary struct {
	Network string `json:"network"`
	Name    string `json:"name"`
	Type    string `json:"type"`
}

func (p *provider) listNetworks(ctx context.Context, endpoint, token, region string) ([]cloud.Resource, error) {
	var nets []netSummary
	if err := p.get(ctx, endpoint+"/api/vcenter/network", token, &nets); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(nets))
	now := time.Now()
	for _, n := range nets {
		out = append(out, cloud.Resource{
			Provider:     "vmware",
			Type:         cloud.TypeVPC,
			ID:           n.Network,
			Name:         n.Name,
			Region:       region,
			Status:       "available",
			Labels:       map[string]string{"network_type": n.Type},
			DiscoveredAt: now,
		})
	}
	return out, nil
}

type dsSummary struct {
	Datastore string `json:"datastore"`
	Name      string `json:"name"`
	Type      string `json:"type"`
}

func (p *provider) listDatastores(ctx context.Context, endpoint, token, region string) ([]cloud.Resource, error) {
	var dss []dsSummary
	if err := p.get(ctx, endpoint+"/api/vcenter/datastore", token, &dss); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(dss))
	now := time.Now()
	for _, d := range dss {
		out = append(out, cloud.Resource{
			Provider:     "vmware",
			Type:         cloud.TypeDisk,
			ID:           d.Datastore,
			Name:         d.Name,
			Region:       region,
			Status:       "available",
			Labels:       map[string]string{"datastore_type": d.Type},
			DiscoveredAt: now,
		})
	}
	return out, nil
}

// vmDetail 对应 /api/vcenter/vm/{vm} 的返回结构（仅取纳管所需字段）。
type vmDetail struct {
	VM         string         `json:"vm"`
	Name       string         `json:"name"`
	PowerState string         `json:"power_state"`
	CPUCount   int            `json:"cpu_count"`
	MemoryMiB  int            `json:"memory_size_MiB"` // 6.7+ 字段名
	MemoryMB   int            `json:"memory_size_MB"`  // 6.5 老版本字段名兼容
	GuestOS    string         `json:"guest_OS"`
	Nics       []vmDetailNics `json:"nics"`
	Disks      []struct {
		Type string `json:"type"`
	} `json:"disks"`
}

// vmDetailNics 是 vmDetail.Nics 的元素类型。
type vmDetailNics struct {
	MacAddress  string `json:"mac_address"`
	IPv4        string `json:"ipv4"`
	IPAddresses []struct {
		IPv4 string `json:"ipv4"`
	} `json:"ip_addresses"`
}

// firstIPv4 从网卡列表里取第一个 IPv4 地址。
func firstIPv4(nics []vmDetailNics) string {
	for _, n := range nics {
		if n.IPv4 != "" {
			return n.IPv4
		}
		for _, a := range n.IPAddresses {
			if a.IPv4 != "" {
				return a.IPv4
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
