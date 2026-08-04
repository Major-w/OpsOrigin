// Package zstack 实现 OpsOrigin 云资源纳管中 ZStack 的 Provider。
// 采用 ZStack 标准 REST API（/zstack/v1/...），仅依赖标准库，
// 与仓库内私有云(OpenStack/VMware)的"纯标准库"风格一致。
//
// 纳管范围（MVP）：
//   - 计算：云主机(vm-instances) → TypeVM
//   - 网络：三层网络(l3-networks) → TypeVPC
//   - 存储：云盘(volumes) → TypeDisk
//
// 凭证约定（复用 cloud.Credential）：
//   AccessKey = 账户名（如 admin）
//   SecretKey = 密码
//   Extra["endpoint"] = ZStack 管理节点基址(含端口)，如 http://172.16.0.1:8080
package zstack

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
	cloud.RegisterKinds("zstack", cloud.TypeVM, cloud.TypeVPC, cloud.TypeDisk)
}

// Name 返回提供商标识。
func (p *provider) Name() string { return "zstack" }

// SupportedVersions 声明本 Provider 适配/验证过的 ZStack 版本。
func (p *provider) SupportedVersions() []cloud.ProviderVersion {
	return []cloud.ProviderVersion{
		{Version: "ZStack 3.x / 4.x", Status: cloud.VersionUntested, Note: "MVP 占位，接口路径待联调"},
	}
}

// ListResources 列举指定地域、指定类型的 ZStack 资源。
// 单类资源发现失败仅记录日志并跳过，不影响其他类（参照 VMware/OpenStack 容错范式）。
func (p *provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	endpoint := strings.TrimRight(cred.Extra["endpoint"], "/")
	if endpoint == "" {
		return nil, fmt.Errorf("zstack: credential extra.endpoint is required")
	}
	token, err := p.login(ctx, endpoint, cred.AccessKey, cred.SecretKey)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = "default"
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
			res, e = p.listL3Networks(ctx, endpoint, token, region)
		case cloud.TypeDisk:
			res, e = p.listVolumes(ctx, endpoint, token, region)
		default:
			continue
		}
		if e != nil {
			slog.Error("zstack list kind failed", "kind", k, "err", e)
			continue
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个资源详情（MVP 仅云主机支持详情）。
func (p *provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	endpoint := strings.TrimRight(cred.Extra["endpoint"], "/")
	if endpoint == "" {
		return nil, fmt.Errorf("zstack: credential extra.endpoint is required")
	}
	token, err := p.login(ctx, endpoint, cred.AccessKey, cred.SecretKey)
	if err != nil {
		return nil, err
	}
	if kind != string(cloud.TypeVM) {
		return nil, &cloud.NoSuchResource{Provider: "zstack", ID: id}
	}
	var d zstackVMDetail
	if err := p.get(ctx, endpoint+"/zstack/v1/vm-instances/"+id, token, &d); err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, &cloud.NoSuchResource{Provider: "zstack", ID: id}
		}
		return nil, err
	}
	return d.Inventory.toResource(firstNonEmpty(region, "default")), nil
}

// CollectMetrics 占位：ZStack 性能监控采集需在 collector 层接入，此处返回 nil。
func (p *provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// ——— ZStack REST 辅助 ———

// login 通过账户名/密码创建会话，返回 inventory.uuid 作为 X-Auth-Token。
func (p *provider) login(ctx context.Context, endpoint, user, pass string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"logInByAccount": map[string]string{"accountName": user, "password": pass},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/zstack/v1/accounts/sessions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("zstack login: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("zstack login failed: %s %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var r struct {
		Inventory struct {
			UUID string `json:"uuid"`
		} `json:"inventory"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("zstack login: bad response: %w", err)
	}
	if r.Inventory.UUID == "" {
		return "", fmt.Errorf("zstack login: empty session uuid")
	}
	return r.Inventory.UUID, nil
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
		return fmt.Errorf("zstack GET %s: %s %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ——— 云主机 ———

type zstackVM struct {
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	State      string `json:"state"`
	CPUNum     int    `json:"cpuNum"`
	MemorySize int    `json:"memorySize"` // 单位字节
	VmNics     []struct {
		IP string `json:"ip"`
	} `json:"vmNics"`
}

func (v zstackVM) toResource(region string) *cloud.Resource {
	ip := ""
	for _, n := range v.VmNics {
		if n.IP != "" {
			ip = n.IP
			break
		}
	}
	return &cloud.Resource{
		Provider:  "zstack",
		Type:      cloud.TypeVM,
		ID:        v.UUID,
		Name:      v.Name,
		Region:    region,
		Status:    strings.ToLower(v.State),
		PrivateIP: ip,
		Extra: map[string]any{
			"cpu_num":    v.CPUNum,
			"memory_MiB": v.MemorySize / (1024 * 1024),
		},
		DiscoveredAt: time.Now(),
	}
}

type zstackVMList struct {
	Inventories []zstackVM `json:"inventories"`
}

type zstackVMDetail struct {
	Inventory zstackVM `json:"inventory"`
}

func (p *provider) listVMs(ctx context.Context, endpoint, token, region string) ([]cloud.Resource, error) {
	var lst zstackVMList
	if err := p.get(ctx, endpoint+"/zstack/v1/vm-instances", token, &lst); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(lst.Inventories))
	now := time.Now()
	for _, v := range lst.Inventories {
		r := v.toResource(region)
		r.DiscoveredAt = now
		out = append(out, *r)
	}
	return out, nil
}

// ——— 三层网络 ———

type zstackL3 struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type zstackL3List struct {
	Inventories []zstackL3 `json:"inventories"`
}

func (p *provider) listL3Networks(ctx context.Context, endpoint, token, region string) ([]cloud.Resource, error) {
	var lst zstackL3List
	if err := p.get(ctx, endpoint+"/zstack/v1/l3-networks", token, &lst); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(lst.Inventories))
	now := time.Now()
	for _, n := range lst.Inventories {
		out = append(out, cloud.Resource{
			Provider:     "zstack",
			Type:         cloud.TypeVPC,
			ID:           n.UUID,
			Name:         n.Name,
			Region:       region,
			Status:       "available",
			Labels:       map[string]string{"network_type": n.Type},
			DiscoveredAt: now,
		})
	}
	return out, nil
}

// ——— 云盘 ———

type zstackVol struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type zstackVolList struct {
	Inventories []zstackVol `json:"inventories"`
}

func (p *provider) listVolumes(ctx context.Context, endpoint, token, region string) ([]cloud.Resource, error) {
	var lst zstackVolList
	if err := p.get(ctx, endpoint+"/zstack/v1/volumes", token, &lst); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(lst.Inventories))
	now := time.Now()
	for _, v := range lst.Inventories {
		out = append(out, cloud.Resource{
			Provider:     "zstack",
			Type:         cloud.TypeDisk,
			ID:           v.UUID,
			Name:         v.Name,
			Region:       region,
			Status:       "available",
			DiscoveredAt: now,
		})
	}
	return out, nil
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
