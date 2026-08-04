// Package volcano 实现火山引擎（Volcano Engine / 字节跳动云）资源纳管，遵循 cloud.Provider 契约。
//
// 鉴权采用火山引擎「签名方法 V4」：HMAC-SHA256 头签名（X-Date + Authorization），
// 仅依赖标准库，无官方 SDK 依赖。
//
// 说明：本实现代码已按火山引擎 OpenAPI 结构编写，但尚未接入真实环境联调，
// SupportedVersions 标记为 untested（待测），待拿到 AK/SK 实测后转正为 verified。
//
// 覆盖资源：弹性云服务器 ECS、云数据库 RDS、负载均衡 CLB。
package volcano

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cloud"
)

// Provider 火山引擎纳管实现。
type Provider struct {
	http *http.Client
}

func init() {
	cloud.Register(&Provider{http: &http.Client{Timeout: 30 * time.Second}})
	cloud.RegisterKinds("volcano", cloud.TypeVM, cloud.TypeDatabase, cloud.TypeLoadBalancer)
}

// Name 厂商标识。
func (p *Provider) Name() string { return "volcano" }

// SupportedVersions 声明本 Provider 适配的火山引擎版本（代码已适配，待真实联调）。
func (p *Provider) SupportedVersions() []cloud.ProviderVersion {
	return []cloud.ProviderVersion{
		{Version: "OpenAPI（全区域）", Status: cloud.VersionUntested, Note: "AK/SK 签名已实现，待真实环境联调验证"},
	}
}

// ListResources 按类型列举资源。
func (p *Provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	if region == "" {
		region = "cn-beijing"
	}
	var out []cloud.Resource
	for _, k := range kinds {
		var (
			res []cloud.Resource
			err error
		)
		switch k {
		case cloud.TypeVM:
			res, err = p.listECS(ctx, cred, region)
		case cloud.TypeDatabase:
			res, err = p.listRDS(ctx, cred, region)
		case cloud.TypeLoadBalancer:
			res, err = p.listCLB(ctx, cred, region)
		default:
			continue
		}
		if err != nil {
			return out, fmt.Errorf("volcano %s: %w", k, err)
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个 ECS 实例详情。
func (p *Provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	if kind != string(cloud.TypeVM) {
		return nil, fmt.Errorf("volcano: GetResource only supports vm, got %q", kind)
	}
	res, err := p.listECS(ctx, cred, region)
	if err != nil {
		return nil, err
	}
	for i := range res {
		if res[i].ID == id {
			return &res[i], nil
		}
	}
	return nil, &cloud.NoSuchResource{Provider: "volcano", ID: id}
}

// CollectMetrics 火山引擎指标需对接云监控，此处返回 nil（接口允许）。
func (p *Provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// ECS（计算）
// ---------------------------------------------------------------------------

func (p *Provider) listECS(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	body, err := p.call(ctx, cred, "ecs", region, "DescribeInstances", "2020-04-01", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Instances []struct {
			InstanceId      string `json:"InstanceId"`
			InstanceName    string `json:"InstanceName"`
			Status          string `json:"Status"`
			RegionId        string `json:"RegionId"`
			ZoneId          string `json:"ZoneId"`
			InstanceType    string `json:"InstanceType"`
			PublicIpAddress struct {
				IpAddress []string `json:"IpAddress"`
			} `json:"PublicIpAddress"`
			PrivateIpAddress struct {
				IpAddress []string `json:"IpAddress"`
			} `json:"PrivateIpAddress"`
		} `json:"Instances"`
		Result struct {
			Instances []struct {
				InstanceId      string `json:"InstanceId"`
				InstanceName    string `json:"InstanceName"`
				Status          string `json:"Status"`
				RegionId        string `json:"RegionId"`
				ZoneId          string `json:"ZoneId"`
				InstanceType    string `json:"InstanceType"`
				PublicIpAddress struct {
					IpAddress []string `json:"IpAddress"`
				} `json:"PublicIpAddress"`
				PrivateIpAddress struct {
					IpAddress []string `json:"IpAddress"`
				} `json:"PrivateIpAddress"`
			} `json:"Instances"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("volcano: parse ECS: %w", err)
	}
	items := r.Instances
	if len(items) == 0 {
		items = r.Result.Instances
	}
	out := make([]cloud.Resource, 0, len(items))
	for _, it := range items {
		priv, pub := firstOrEmpty(it.PrivateIpAddress.IpAddress), firstOrEmpty(it.PublicIpAddress.IpAddress)
		name := it.InstanceName
		if name == "" {
			name = it.InstanceId
		}
		out = append(out, cloud.Resource{
			Provider: "volcano", Type: cloud.TypeVM, ID: it.InstanceId,
			Name: name, Region: it.RegionId, Zone: it.ZoneId, Status: it.Status,
			PublicIP: pub, PrivateIP: priv,
			Extra: map[string]any{"instance_type": it.InstanceType},
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// RDS（数据库）
// ---------------------------------------------------------------------------

func (p *Provider) listRDS(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	body, err := p.call(ctx, cred, "rds", region, "DescribeDBInstances", "2018-01-01", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Instances []struct {
			InstanceId   string `json:"InstanceId"`
			InstanceName string `json:"InstanceName"`
			DBEngine     string `json:"DBEngine"`
			Status       string `json:"Status"`
			Region       string `json:"Region"`
			Zone         string `json:"Zone"`
		} `json:"Instances"`
		Result struct {
			Instances []struct {
				InstanceId   string `json:"InstanceId"`
				InstanceName string `json:"InstanceName"`
				DBEngine     string `json:"DBEngine"`
				Status       string `json:"Status"`
				Region       string `json:"Region"`
				Zone         string `json:"Zone"`
			} `json:"Instances"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("volcano: parse RDS: %w", err)
	}
	items := r.Instances
	if len(items) == 0 {
		items = r.Result.Instances
	}
	out := make([]cloud.Resource, 0, len(items))
	for _, it := range items {
		name := it.InstanceName
		if name == "" {
			name = it.InstanceId
		}
		out = append(out, cloud.Resource{
			Provider: "volcano", Type: cloud.TypeDatabase, ID: it.InstanceId,
			Name: name, Region: it.Region, Zone: it.Zone, Status: it.Status,
			Extra: map[string]any{"engine": it.DBEngine},
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// CLB（负载均衡）
// ---------------------------------------------------------------------------

func (p *Provider) listCLB(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	body, err := p.call(ctx, cred, "clb", region, "DescribeLoadBalancers", "2020-04-01", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		LoadBalancers []struct {
			LoadBalancerId   string `json:"LoadBalancerId"`
			LoadBalancerName string `json:"LoadBalancerName"`
			Status           string `json:"Status"`
			Region           string `json:"Region"`
			Address          string `json:"Address"`
		} `json:"LoadBalancers"`
		Result struct {
			LoadBalancers []struct {
				LoadBalancerId   string `json:"LoadBalancerId"`
				LoadBalancerName string `json:"LoadBalancerName"`
				Status           string `json:"Status"`
				Region           string `json:"Region"`
				Address          string `json:"Address"`
			} `json:"LoadBalancers"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("volcano: parse CLB: %w", err)
	}
	items := r.LoadBalancers
	if len(items) == 0 {
		items = r.Result.LoadBalancers
	}
	out := make([]cloud.Resource, 0, len(items))
	for _, it := range items {
		name := it.LoadBalancerName
		if name == "" {
			name = it.LoadBalancerId
		}
		out = append(out, cloud.Resource{
			Provider: "volcano", Type: cloud.TypeLoadBalancer, ID: it.LoadBalancerId,
			Name: name, Region: it.Region, Status: it.Status, PublicIP: it.Address,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 火山引擎「签名方法 V4」（HMAC-SHA256 头签名）
// ---------------------------------------------------------------------------

// call 执行一次火山引擎 OpenAPI 调用（GET + Action/Version 查询参数 + V4 签名）。
func (p *Provider) call(ctx context.Context, cred cloud.Credential, service, region, action, version string, extra url.Values) ([]byte, error) {
	params := url.Values{}
	params.Set("Action", action)
	params.Set("Version", version)
	params.Set("Region", region)
	for k, vs := range extra {
		for _, v := range vs {
			params.Add(k, v)
		}
	}
	host := "open.volcengineapi.com"
	qs, xDate, signature := p.sign(cred, service, region, params)
	payloadHash := sha256hex([]byte(""))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"?"+qs, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Host", host)
	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", payloadHash)
	req.Header.Set("Authorization", fmt.Sprintf(
		"HMAC-SHA256 Credential=%s/%s/%s/%s/request, SignedHeaders=host;x-content-sha256;x-date, Signature=%s",
		cred.AccessKey, xDate[:8], region, service, signature))

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("volcano: %s %s returned %d: %s", service, action, resp.StatusCode, string(data))
	}
	if e := volcanoErr(data); e != "" {
		return nil, fmt.Errorf("volcano: %s", e)
	}
	return data, nil
}

// sign 计算规范查询串、X-Date 与 HMAC-SHA256 签名（火山引擎「签名方法 V4」）。
func (p *Provider) sign(cred cloud.Credential, service, region string, params url.Values) (canonicalQuery, xDate, signature string) {
	now := time.Now().UTC()
	xDate = now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := params[k]
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, pctEncode(k)+"="+pctEncode(v))
		}
	}
	canonicalQuery = strings.Join(parts, "&")

	payloadHash := sha256hex([]byte(""))
	canonicalHeaders := "host:open.volcengineapi.com\n" + "x-content-sha256:" + payloadHash + "\n" + "x-date:" + xDate + "\n"
	signedHeaders := "host;x-content-sha256;x-date"
	canonicalRequest := strings.Join([]string{
		"GET", "/", canonicalQuery, canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	stringToSign := "HMAC-SHA256\n" + xDate + "\n" + sha256hex([]byte(canonicalRequest))

	kDate := hmacSHA256([]byte(dateStamp), []byte(cred.SecretKey))
	kRegion := hmacSHA256([]byte(region), kDate)
	kService := hmacSHA256([]byte(service), kRegion)
	kSigning := hmacSHA256([]byte("request"), kService)
	signature = hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))
	return canonicalQuery, xDate, signature
}

// volcanoErr 解析火山引擎错误响应（{"Error":{"Code":..,"Message":..}}）。
func volcanoErr(b []byte) string {
	var e struct {
		ResponseMetadata struct {
			Error struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
	}
	if json.Unmarshal(b, &e) != nil {
		return ""
	}
	if e.ResponseMetadata.Error.Code == "" {
		return ""
	}
	return e.ResponseMetadata.Error.Code + ": " + e.ResponseMetadata.Error.Message
}

// firstOrEmpty 返回切片首个元素，空切片返回空串。
func firstOrEmpty(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

// sha256hex 计算 SHA256 的十六进制摘要。
func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hmacSHA256 返回 keyed-HMAC-SHA256。
func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// pctEncode 按 RFC 3986 严格 URL 编码（空格→%20，*→%2A；-_.~ 本就无需编码）。
func pctEncode(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	return e
}
