// Package sangfor 实现 OpsOrigin 云资源纳管中深信服超融合(aCloud/HCI)的 Provider。
// 采用 aCloud REST API，仅依赖标准库，与仓库内 VMware/OpenStack 的"纯标准库"风格一致。
//
// 纳管范围（MVP）：
//   - 计算：虚拟机 → TypeVM
//   - 网络：虚拟网络/端口组 → TypeVPC
//   - 存储：数据存储/卷 → TypeDisk
//
// 凭证约定（复用 cloud.Credential）：
//   AccessKey = 深信服管理员用户名
//   SecretKey = 密码
//   Extra["endpoint"] = aCloud 管理基址，如 https://172.16.0.1
//
// 注：aCloud REST 端点路径与字段以 MVP 形态给出，联调时按实际版本校正。
package sangfor

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
	cloud.RegisterKinds("sangfor", cloud.TypeVM, cloud.TypeVPC, cloud.TypeDisk)
}

// Name 返回提供商标识。
func (p *provider) Name() string { return "sangfor" }

// ListResources 列举指定地域、指定类型的深信服资源。
// 单类资源发现失败仅记录日志并跳过，不影响其他类（参照 VMware/OpenStack 容错范式）。
func (p *provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	endpoint := strings.TrimRight(cred.Extra["endpoint"], "/")
	if endpoint == "" {
		return nil, fmt.Errorf("sangfor: credential extra.endpoint is required")
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
			res, e = p.listNetworks(ctx, endpoint, token, region)
		case cloud.TypeDisk:
			res, e = p.listVolumes(ctx, endpoint, token, region)
		default:
			continue
		}
		if e != nil {
			slog.Error("sangfor list kind failed", "kind", k, "err", e)
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
		return nil, fmt.Errorf("sangfor: credential extra.endpoint is required")
	}
	token, err := p.login(ctx, endpoint, cred.AccessKey, cred.SecretKey)
	if err != nil {
		return nil, err
	}
	if kind != string(cloud.TypeVM) {
		return nil, &cloud.NoSuchResource{Provider: "sangfor", ID: id}
	}
	var vm sangforVM
	if err := p.get(ctx, endpoint+"/vapi/rest/v1/vms/"+id, token, &vm); err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, &cloud.NoSuchResource{Provider: "sangfor", ID: id}
		}
		return nil, err
	}
	return vm.toResource(firstNonEmpty(region, "default")), nil
}

// CollectMetrics 占位：深信服性能监控采集需在 collector 层接入，此处返回 nil。
func (p *provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// ——— aCloud REST 辅助 ———

// login 通过用户名密码获取 aCloud 会话令牌。
func (p *provider) login(ctx context.Context, endpoint, user, pass string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/vapi/rest/v1/session/login", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("sangfor login: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sangfor login failed: %s %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	return extractToken(raw), nil
}

// extractToken 从 aCloud 登录响应兼容多种结构提取 token。
func extractToken(raw []byte) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, key := range []string{"token", "sessionId", "session_id"} {
		if v, ok := m[key]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return s
			}
		}
	}
	if d, ok := m["data"]; ok {
		var dm map[string]json.RawMessage
		if json.Unmarshal(d, &dm) == nil {
			if v, ok := dm["token"]; ok {
				var s string
				if json.Unmarshal(v, &s) == nil {
					return s
				}
			}
		}
	}
	return ""
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
		return fmt.Errorf("sangfor GET %s: %s %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ——— 虚拟机 ———

// vmList 兼容 {list:[...]} / {data:{list:[...]}} / 直接数组 多种返回结构。
type vmList struct {
	List []sangforVM `json:"list"`
	Data struct {
		List []sangforVM `json:"list"`
	} `json:"data"`
}

type sangforVM struct {
	ID         string `json:"id"`
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	State      string `json:"state"`
	IPv4       string `json:"ip"`
	IP         string `json:"ipAddress"`
	CPU        int    `json:"cpu"`
	MemoryMiB  int    `json:"memorySizeMiB"`
	MemMB      int    `json:"memoryMb"`
}

func (v sangforVM) toResource(region string) *cloud.Resource {
	id := firstNonEmpty(v.ID, v.UUID)
	status := strings.ToLower(firstNonEmpty(v.Status, v.State))
	return &cloud.Resource{
		Provider:  "sangfor",
		Type:      cloud.TypeVM,
		ID:        id,
		Name:      v.Name,
		Region:    region,
		Status:    status,
		PrivateIP: firstNonEmpty(v.IPv4, v.IP),
		Extra: map[string]any{
			"cpu":        v.CPU,
			"memory_MiB": firstNonZero(v.MemoryMiB, v.MemMB),
		},
		DiscoveredAt: time.Now(),
	}
}

func (p *provider) listVMs(ctx context.Context, endpoint, token, region string) ([]cloud.Resource, error) {
	var lst vmList
	if err := p.get(ctx, endpoint+"/vapi/rest/v1/vms", token, &lst); err != nil {
		return nil, err
	}
	src := lst.List
	if len(src) == 0 {
		src = lst.Data.List
	}
	out := make([]cloud.Resource, 0, len(src))
	now := time.Now()
	for _, v := range src {
		r := v.toResource(region)
		r.DiscoveredAt = now
		out = append(out, *r)
	}
	return out, nil
}

// ——— 网络 ———

type sangforNet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func (p *provider) listNetworks(ctx context.Context, endpoint, token, region string) ([]cloud.Resource, error) {
	var nets []sangforNet
	if err := p.get(ctx, endpoint+"/vapi/rest/v1/networks", token, &nets); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(nets))
	now := time.Now()
	for _, n := range nets {
		out = append(out, cloud.Resource{
			Provider:     "sangfor",
			Type:         cloud.TypeVPC,
			ID:           n.ID,
			Name:         n.Name,
			Region:       region,
			Status:       "available",
			Labels:       map[string]string{"network_type": n.Type},
			DiscoveredAt: now,
		})
	}
	return out, nil
}

// ——— 存储 ———

type sangforVol struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (p *provider) listVolumes(ctx context.Context, endpoint, token, region string) ([]cloud.Resource, error) {
	var vols []sangforVol
	if err := p.get(ctx, endpoint+"/vapi/rest/v1/volumes", token, &vols); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(vols))
	now := time.Now()
	for _, v := range vols {
		out = append(out, cloud.Resource{
			Provider:     "sangfor",
			Type:         cloud.TypeDisk,
			ID:           v.ID,
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

// firstNonZero 返回第一个非零整数。
func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}
