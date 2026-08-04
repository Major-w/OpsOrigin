// Package huawei 实现华为云（Huawei Cloud）资源纳管，遵循 cloud.Provider 契约。
//
// 鉴权采用华为云 SDK-HMAC-SHA256 签名（X-Sdk-Date + Authorization Header），
// 仅依赖标准库，无需引入庞大的官方 SDK。
//
// 注意：华为云 API 为 project 级，需在凭证 Extra["project_id"] 中提供
// 项目 ID（可在“我的凭证 → 项目”中查询）。
//
// 覆盖资源：弹性云服务器 ECS、云数据库 RDS、弹性负载均衡 ELB。
package huawei

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cloud"
)

// Provider 华为云纳管实现。
type Provider struct {
	http *http.Client
}

func init() {
	cloud.Register(&Provider{http: &http.Client{Timeout: 30 * time.Second}})
}

// Name 厂商标识。
func (p *Provider) Name() string { return "huawei" }

// SupportedVersions 声明本 Provider 适配/验证过的华为云版本。
func (p *Provider) SupportedVersions() []cloud.ProviderVersion {
	return []cloud.ProviderVersion{
		{Version: "OpenAPI（全区域）", Status: cloud.VersionVerified, Note: "AK/SK 认证，接口向后兼容"},
	}
}

// ListResources 按类型列举资源。
func (p *Provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	if region == "" {
		region = "cn-north-4"
	}
	pid := cred.Extra["project_id"]
	if pid == "" {
		return nil, fmt.Errorf("huawei: Extra[\"project_id\"] required")
	}
	var out []cloud.Resource
	for _, k := range kinds {
		var (
			res []cloud.Resource
			err error
		)
		switch k {
		case cloud.TypeVM:
			res, err = p.listECS(ctx, cred, region, pid)
		case cloud.TypeDatabase:
			res, err = p.listRDS(ctx, cred, region, pid)
		case cloud.TypeLoadBalancer:
			res, err = p.listELB(ctx, cred, region, pid)
		default:
			continue
		}
		if err != nil {
			return out, fmt.Errorf("huawei %s: %w", k, err)
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个弹性云服务器详情（以 ECS 为例）。
func (p *Provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	if kind != string(cloud.TypeVM) {
		return nil, fmt.Errorf("huawei: GetResource only supports vm, got %q", kind)
	}
	pid := cred.Extra["project_id"]
	res, err := p.listECS(ctx, cred, region, pid)
	if err != nil {
		return nil, err
	}
	for i := range res {
		if res[i].ID == id {
			return &res[i], nil
		}
	}
	return nil, &cloud.NoSuchResource{Provider: "huawei", ID: id}
}

// CollectMetrics 华为云指标需对接云监控 CES，此处返回 nil（接口允许）。
func (p *Provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// ---------- ECS ----------

func (p *Provider) listECS(ctx context.Context, cred cloud.Credential, region, pid string) ([]cloud.Resource, error) {
	path := fmt.Sprintf("/v1/%s/cloudservers/detail", pid)
	body, err := p.call(ctx, cred, "ecs", region, "GET", path, "")
	if err != nil {
		return nil, err
	}
	var r struct {
		Servers []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			Addresses map[string][]struct {
				Version string `json:"version"`
				Addr    string `json:"addr"`
			} `json:"addresses"`
			Metadata struct {
				Region           string `json:"region"`
				AvailabilityZone string `json:"availability_zone"`
			} `json:"metadata"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse ecs: %w", err)
	}
	out := make([]cloud.Resource, 0, len(r.Servers))
	for _, s := range r.Servers {
		var pub, priv string
		for _, addrs := range s.Addresses {
			for _, a := range addrs {
				if a.Version == "4" || a.Version == "6" {
					if strings.HasPrefix(a.Addr, "192.168") || strings.HasPrefix(a.Addr, "10.") || strings.HasPrefix(a.Addr, "172.") {
						priv = a.Addr
					} else {
						pub = a.Addr
					}
				}
			}
		}
		name := s.Name
		if name == "" {
			name = s.ID
		}
		out = append(out, cloud.Resource{
			Provider: "huawei", Type: cloud.TypeVM, ID: s.ID,
			Name: name, Region: s.Metadata.Region, Zone: s.Metadata.AvailabilityZone,
			Status: s.Status, PublicIP: pub, PrivateIP: priv,
		})
	}
	return out, nil
}

// ---------- RDS ----------

func (p *Provider) listRDS(ctx context.Context, cred cloud.Credential, region, pid string) ([]cloud.Resource, error) {
	path := fmt.Sprintf("/v3/%s/instances", pid)
	body, err := p.call(ctx, cred, "rds", region, "GET", path, "")
	if err != nil {
		return nil, err
	}
	var r struct {
		Instances []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Region string `json:"region"`
			AzMode string `json:"az_mode"`
		} `json:"instances"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse rds: %w", err)
	}
	out := make([]cloud.Resource, 0, len(r.Instances))
	for _, it := range r.Instances {
		name := it.Name
		if name == "" {
			name = it.ID
		}
		out = append(out, cloud.Resource{
			Provider: "huawei", Type: cloud.TypeDatabase, ID: it.ID,
			Name: name, Region: it.Region, Status: it.Status,
		})
	}
	return out, nil
}

// ---------- ELB ----------

func (p *Provider) listELB(ctx context.Context, cred cloud.Credential, region, pid string) ([]cloud.Resource, error) {
	path := fmt.Sprintf("/v2/%s/elb/loadbalancers", pid)
	body, err := p.call(ctx, cred, "elb", region, "GET", path, "")
	if err != nil {
		return nil, err
	}
	var r struct {
		Loadbalancers []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			VipAddress string `json:"vip_address"`
			ProvStatus string `json:"provisioning_status"`
			OpStatus   string `json:"operating_status"`
		} `json:"loadbalancers"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse elb: %w", err)
	}
	out := make([]cloud.Resource, 0, len(r.Loadbalancers))
	for _, it := range r.Loadbalancers {
		name := it.Name
		if name == "" {
			name = it.ID
		}
		out = append(out, cloud.Resource{
			Provider: "huawei", Type: cloud.TypeLoadBalancer, ID: it.ID,
			Name: name, Region: region, Status: it.ProvStatus, PublicIP: it.VipAddress,
		})
	}
	return out, nil
}

// call 执行一次华为云 SDK-HMAC-SHA256 签名调用。
func (p *Provider) call(ctx context.Context, cred cloud.Credential, svc, region, method, path, payload string) ([]byte, error) {
	host := fmt.Sprintf("%s.%s.myhuaweicloud.com", svc, region)
	pid := cred.Extra["project_id"]
	now := time.Now().UTC()
	sdkDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")

	if payload == "" {
		payload = "" // GET 无 body
	}
	ph := sha256.Sum256([]byte(payload))
	payloadHash := hex.EncodeToString(ph[:])

	headers := map[string]string{
		"host":         host,
		"x-sdk-date":   sdkDate,
		"x-project-id": pid,
	}
	sh := sortedKeys(headers)
	var canonHead, signedHead string
	for i, k := range sh {
		canonHead += k + ":" + strings.TrimSpace(headers[k]) + "\n"
		if i > 0 {
			signedHead += ";"
		}
		signedHead += k
	}

	canonical := method + "\n" + path + "\n" + "" + "\n" + canonHead + "\n" + signedHead + "\n" + payloadHash
	ch := sha256.Sum256([]byte(canonical))
	sts := "SDK-HMAC-SHA256\n" + sdkDate + "\n" + hex.EncodeToString(ch[:])

	kDate := hmacSHA256([]byte(cred.SecretKey), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kSvc := hmacSHA256(kRegion, []byte(svc))
	kSign := hmacSHA256(kSvc, []byte("sdk_request"))
	sig := hex.EncodeToString(hmacSHA256(kSign, []byte(sts)))

	auth := fmt.Sprintf("SDK-HMAC-SHA256 Access=%s, SignedHeaders=%s, Signature=%s",
		cred.AccessKey, signedHead, sig)

	req, err := http.NewRequestWithContext(ctx, method, "https://"+host+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Host", host)
	req.Header.Set("X-Sdk-Date", sdkDate)
	req.Header.Set("X-Project-Id", pid)
	req.Header.Set("Authorization", auth)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}
