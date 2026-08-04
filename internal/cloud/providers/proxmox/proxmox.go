// Package proxmox 实现 OpsOrigin 云资源纳管中 Proxmox VE 的 Provider。
// 采用 Proxmox VE REST API(默认 JSON 输出)，仅依赖标准库，与仓库内
// 其余私有云 Provider(sangfor/zstack/vmware/openstack) 的"纯标准库"风格一致。
//
// 纳管范围（MVP）：
//   - 计算：集群虚拟机/容器(qemu/lxc) → TypeVM
//   - 存储：集群存储(SD/ceph/local/...) → TypeDisk
//   - 网络：SDN 虚拟网络(vnet) → TypeVPC（环境未启用 SDN 时该类自动跳过）
//
// 凭证约定（复用 cloud.Credential）：
//   AccessKey = Proxmox 登录用户名（含域，如 root@pam 或 user@pve）
//   SecretKey = 密码
//   Extra["endpoint"] = Proxmox 管理基址，须含端口，如 https://10.0.0.1:8006
//   Extra["insecure"] / Extra["skip_tls_verify"] = "true" 时跳过 TLS 证书校验
//     （Proxmox VE 8.x 默认 HTTPS 自签证书，真机纳管常需开启）
//
// 认证：POST /api2/json/access/ticket 取得 PVEAuthCookie，后续请求携带该 Cookie。
// 注：集群资源端点以 MVP 形态给出，联调时按实际版本校正；网络类依赖 SDN 插件。
package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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
	cloud.RegisterKinds("proxmox", cloud.TypeVM, cloud.TypeVPC, cloud.TypeDisk)
}

// Name 返回提供商标识。
func (p *provider) Name() string { return "proxmox" }

// ListResources 列举指定地域、指定类型的 Proxmox 资源。
// 单类资源发现失败仅记录日志并跳过，不影响其他类（参照私有云 Provider 容错范式）。
func (p *provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	endpoint := strings.TrimRight(cred.Extra["endpoint"], "/")
	if endpoint == "" {
		return nil, fmt.Errorf("proxmox: credential extra.endpoint is required (e.g. https://10.0.0.1:8006)")
	}
	ticket, err := p.login(ctx, cred, endpoint)
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
			res, e = p.listVMs(ctx, cred, endpoint, ticket)
		case cloud.TypeDisk:
			res, e = p.listStorages(ctx, cred, endpoint, ticket)
		case cloud.TypeVPC:
			res, e = p.listVNets(ctx, cred, endpoint, ticket)
		default:
			continue
		}
		if e != nil {
			slog.Error("proxmox list kind failed", "kind", k, "err", e)
			continue
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个资源详情（MVP 仅虚拟机支持详情，按 vmid 在集群资源中定位）。
func (p *provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	endpoint := strings.TrimRight(cred.Extra["endpoint"], "/")
	if endpoint == "" {
		return nil, fmt.Errorf("proxmox: credential extra.endpoint is required")
	}
	ticket, err := p.login(ctx, cred, endpoint)
	if err != nil {
		return nil, err
	}
	if kind != string(cloud.TypeVM) {
		return nil, &cloud.NoSuchResource{Provider: "proxmox", ID: id}
	}
	var cr clusterResources
	if err := p.get(ctx, cred, endpoint, ticket, "/api2/json/cluster/resources?type=vm", &cr); err != nil {
		return nil, err
	}
	for _, r := range cr.Data {
		if r.VMID == id {
			res := vmToResource(r, time.Now())
			return &res, nil
		}
	}
	return nil, &cloud.NoSuchResource{Provider: "proxmox", ID: id}
}

// CollectMetrics 占位：Proxmox 性能监控(RRD)采集需在 collector 层接入，此处返回 nil。
func (p *provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// ——— Proxmox REST 辅助 ———

// httpClient 按凭证返回 HTTP 客户端。
// Extra["insecure"] 或 Extra["skip_tls_verify"] 为 "true" 时跳过 TLS 证书校验，
// 用于 Proxmox VE 8.x 默认 HTTPS 自签证书的真机纳管场景。
func (p *provider) httpClient(cred cloud.Credential) *http.Client {
	if strings.EqualFold(cred.Extra["insecure"], "true") || strings.EqualFold(cred.Extra["skip_tls_verify"], "true") {
		return &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 允许自签证书
			},
		}
	}
	return p.http
}

// login 通过用户名密码获取 PVEAuthCookie(ticket)。
func (p *provider) login(ctx context.Context, cred cloud.Credential, endpoint string) (string, error) {
	form := url.Values{}
	form.Set("username", cred.AccessKey)
	form.Set("password", cred.SecretKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/api2/json/access/ticket", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.httpClient(cred).Do(req)
	if err != nil {
		return "", fmt.Errorf("proxmox login: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("proxmox login failed: %s %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var r struct {
		Data struct {
			Ticket string `json:"ticket"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("proxmox login decode: %w", err)
	}
	if r.Data.Ticket == "" {
		return "", fmt.Errorf("proxmox login: empty ticket in response")
	}
	return r.Data.Ticket, nil
}

// get 携带 PVEAuthCookie 发起 GET 并解码 JSON（api2/json 返回 {data:...}）。
func (p *provider) get(ctx context.Context, cred cloud.Credential, endpoint, ticket, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return err
	}
	req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: ticket, Path: "/"})
	resp, err := p.httpClient(cred).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("proxmox GET %s: %s %s", path, resp.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ——— 虚拟机/容器 ———

// clusterResources 为 GET /cluster/resources 的返回结构（data 数组）。
type clusterResources struct {
	Data []pveResource `json:"data"`
}

// pveResource 描述单条集群资源（qemu/lxc 虚拟机或容器）。
type pveResource struct {
	Type    string `json:"type"`     // qemu(虚拟)/lxc(容器)
	VMID    string `json:"vmid"`     // 集群内唯一数字 ID
	Name    string `json:"name"`     // 配置名（可能为空）
	Node    string `json:"node"`     // 所在物理节点
	Status  string `json:"status"`   // running/stopped
	CPUs    int    `json:"cpus"`     // 分配的 vCPU 数
	Mem     int64  `json:"mem"`      // 当前内存使用(bytes)
	MaxMem  int64  `json:"maxmem"`   // 内存上限(bytes)
	Disk    int64  `json:"disk"`     // 当前磁盘使用(bytes)
	MaxDisk int64  `json:"maxdisk"`  // 磁盘上限(bytes)
}

// vmToResource 将集群资源条目映射为统一 Resource（VM/容器均归 TypeVM，以 Extra.hypervisor 区分）。
func vmToResource(r pveResource, now time.Time) cloud.Resource {
	name := r.Name
	if name == "" {
		name = "vm-" + r.VMID
	}
	return cloud.Resource{
		Provider: "proxmox",
		Type:     cloud.TypeVM,
		ID:       r.VMID,
		Name:     name,
		Region:   firstNonEmpty(r.Node, "cluster"),
		Status:   strings.ToLower(r.Status),
		Extra: map[string]any{
			"hypervisor":     r.Type, // qemu / lxc
			"node":           r.Node,
			"cpus":           r.CPUs,
			"mem_bytes":      r.Mem,
			"max_mem_bytes":  r.MaxMem,
			"disk_bytes":     r.Disk,
			"max_disk_bytes": r.MaxDisk,
		},
		DiscoveredAt: now,
	}
}

func (p *provider) listVMs(ctx context.Context, cred cloud.Credential, endpoint, ticket string) ([]cloud.Resource, error) {
	var cr clusterResources
	if err := p.get(ctx, cred, endpoint, ticket, "/api2/json/cluster/resources?type=vm", &cr); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(cr.Data))
	now := time.Now()
	for _, r := range cr.Data {
		out = append(out, vmToResource(r, now))
	}
	return out, nil
}

// ——— 存储 ———

type pveStorage struct {
	Storage string `json:"storage"` // 存储标识
	Type    string `json:"type"`    // dir/lvmthin/nfs/cephfs/...
	Node    string `json:"node"`    // 归属节点（集群存储可为空）
	Content string `json:"content"` // 可存内容（vm/iso/backup/...）
	Active  int    `json:"active"`  // 1=可用
}

type storageList struct {
	Data []pveStorage `json:"data"`
}

func (p *provider) listStorages(ctx context.Context, cred cloud.Credential, endpoint, ticket string) ([]cloud.Resource, error) {
	var sl storageList
	if err := p.get(ctx, cred, endpoint, ticket, "/api2/json/storage", &sl); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(sl.Data))
	now := time.Now()
	for _, s := range sl.Data {
		status := "available"
		if s.Active != 1 {
			status = "inactive"
		}
		out = append(out, cloud.Resource{
			Provider: "proxmox",
			Type:     cloud.TypeDisk,
			ID:       s.Storage,
			Name:     s.Storage,
			Region:   firstNonEmpty(s.Node, "cluster"),
			Status:   status,
			Labels:   map[string]string{"storage_type": s.Type},
			Extra:    map[string]any{"content": s.Content, "node": s.Node},
			DiscoveredAt: now,
		})
	}
	return out, nil
}

// ——— 网络(SDN vnet) ———

type pveVNet struct {
	VNet string `json:"vnet"`
	Zone string `json:"zone"`
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

type vnetList struct {
	Data []pveVNet `json:"data"`
}

func (p *provider) listVNets(ctx context.Context, cred cloud.Credential, endpoint, ticket string) ([]cloud.Resource, error) {
	var vl vnetList
	if err := p.get(ctx, cred, endpoint, ticket, "/api2/json/sdn/vnets", &vl); err != nil {
		return nil, err
	}
	out := make([]cloud.Resource, 0, len(vl.Data))
	now := time.Now()
	for _, v := range vl.Data {
		if v.VNet == "" {
			continue
		}
		out = append(out, cloud.Resource{
			Provider: "proxmox",
			Type:     cloud.TypeVPC,
			ID:       v.VNet,
			Name:     v.VNet,
			Region:   firstNonEmpty(v.Zone, "cluster"),
			Status:   "available",
			Labels:   map[string]string{"sdn_zone": v.Zone, "sdn_type": v.Type},
			Extra:    map[string]any{"tag": v.Tag},
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
