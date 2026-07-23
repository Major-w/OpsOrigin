// Package tencent 实现腾讯云（Tencent Cloud）资源纳管，遵循 cloud.Provider 契约。
//
// 鉴权采用腾讯云 TC3-HMAC-SHA256 签名（POST JSON + Authorization Header），
// 仅依赖标准库，无需引入庞大的官方 SDK。
//
// 覆盖资源：云服务器 CVM、云数据库 TencentDB（cdb）、负载均衡 CLB。
package tencent

import (
	"bytes"
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

// Provider 腾讯云纳管实现。
type Provider struct {
	http *http.Client
}

func init() {
	cloud.Register(&Provider{http: &http.Client{Timeout: 30 * time.Second}})
}

// Name 厂商标识。
func (p *Provider) Name() string { return "tencent" }

// ListResources 按类型列举资源。
func (p *Provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	if region == "" {
		region = "ap-guangzhou"
	}
	var out []cloud.Resource
	for _, k := range kinds {
		var (
			res []cloud.Resource
			err error
		)
		switch k {
		case cloud.TypeVM:
			res, err = p.listCVM(ctx, cred, region)
		case cloud.TypeDatabase:
			res, err = p.listCDB(ctx, cred, region)
		case cloud.TypeLoadBalancer:
			res, err = p.listCLB(ctx, cred, region)
		default:
			continue
		}
		if err != nil {
			return out, fmt.Errorf("tencent %s: %w", k, err)
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个云服务器详情（以 CVM 为例）。
func (p *Provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	if kind != string(cloud.TypeVM) {
		return nil, fmt.Errorf("tencent: GetResource only supports vm, got %q", kind)
	}
	res, err := p.listCVM(ctx, cred, region)
	if err != nil {
		return nil, err
	}
	for i := range res {
		if res[i].ID == id {
			return &res[i], nil
		}
	}
	return nil, &cloud.NoSuchResource{Provider: "tencent", ID: id}
}

// CollectMetrics 腾讯云指标需对接云监控 Barad，此处返回 nil（接口允许）。
func (p *Provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// ---------- CVM ----------

func (p *Provider) listCVM(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	body, err := p.call(ctx, cred, "cvm", "2017-03-12", "DescribeInstances", region, `{"Limit":100,"Offset":0}`)
	if err != nil {
		return nil, err
	}
	var r struct {
		Response struct {
			InstanceSet []struct {
				InstanceId       string   `json:"InstanceId"`
				InstanceName     string   `json:"InstanceName"`
				InstanceState    string   `json:"InstanceState"`
				PrivateIpAddresses []string `json:"PrivateIpAddresses"`
				PublicIpAddresses  []string `json:"PublicIpAddresses"`
				Placement        struct {
					Zone string `json:"Zone"`
				} `json:"Placement"`
			} `json:"InstanceSet"`
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse cvm: %w", err)
	}
	if r.Response.Error != nil {
		return nil, fmt.Errorf("cvm api: %s %s", r.Response.Error.Code, r.Response.Error.Message)
	}
	out := make([]cloud.Resource, 0, len(r.Response.InstanceSet))
	for _, it := range r.Response.InstanceSet {
		priv, pub := first(it.PrivateIpAddresses), first(it.PublicIpAddresses)
		name := it.InstanceName
		if name == "" {
			name = it.InstanceId
		}
		out = append(out, cloud.Resource{
			Provider: "tencent", Type: cloud.TypeVM, ID: it.InstanceId,
			Name: name, Region: region, Zone: it.Placement.Zone,
			Status: it.InstanceState, PublicIP: pub, PrivateIP: priv,
		})
	}
	return out, nil
}

// ---------- CDB (TencentDB MySQL) ----------

func (p *Provider) listCDB(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	body, err := p.call(ctx, cred, "cdb", "2017-03-20", "DescribeDBInstances", region, `{"Limit":100,"Offset":0}`)
	if err != nil {
		return nil, err
	}
	var r struct {
		Response struct {
			Items []struct {
				InstanceId   string `json:"InstanceId"`
				InstanceName string `json:"InstanceName"`
				Status       string `json:"Status"`
				Region       string `json:"Region"`
				Zone         string `json:"Zone"`
			} `json:"Items"`
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse cdb: %w", err)
	}
	if r.Response.Error != nil {
		return nil, fmt.Errorf("cdb api: %s %s", r.Response.Error.Code, r.Response.Error.Message)
	}
	out := make([]cloud.Resource, 0, len(r.Response.Items))
	for _, it := range r.Response.Items {
		name := it.InstanceName
		if name == "" {
			name = it.InstanceId
		}
		out = append(out, cloud.Resource{
			Provider: "tencent", Type: cloud.TypeDatabase, ID: it.InstanceId,
			Name: name, Region: it.Region, Zone: it.Zone, Status: it.Status,
		})
	}
	return out, nil
}

// ---------- CLB ----------

func (p *Provider) listCLB(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	body, err := p.call(ctx, cred, "clb", "2018-03-17", "DescribeLoadBalancers", region, `{"Limit":100,"Offset":0}`)
	if err != nil {
		return nil, err
	}
	var r struct {
		Response struct {
			LoadBalancerSet []struct {
				LoadBalancerId   string `json:"LoadBalancerId"`
				LoadBalancerName string `json:"LoadBalancerName"`
				Address           string `json:"Address"`
				Status            string `json:"Status"`
				Region            string `json:"Region"`
				Zone              string `json:"Zone"`
			} `json:"LoadBalancerSet"`
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse clb: %w", err)
	}
	if r.Response.Error != nil {
		return nil, fmt.Errorf("clb api: %s %s", r.Response.Error.Code, r.Response.Error.Message)
	}
	out := make([]cloud.Resource, 0, len(r.Response.LoadBalancerSet))
	for _, it := range r.Response.LoadBalancerSet {
		name := it.LoadBalancerName
		if name == "" {
			name = it.LoadBalancerId
		}
		out = append(out, cloud.Resource{
			Provider: "tencent", Type: cloud.TypeLoadBalancer, ID: it.LoadBalancerId,
			Name: name, Region: it.Region, Zone: it.Zone, Status: it.Status, PublicIP: it.Address,
		})
	}
	return out, nil
}

// call 执行一次腾讯云 TC3 签名调用。
func (p *Provider) call(ctx context.Context, cred cloud.Credential, svc, version, action, region, payload string) ([]byte, error) {
	host := svc + ".tencentcloudapi.com"
	ts := time.Now().Unix()
	date := time.Unix(ts, 0).UTC().Format("2006-01-02")

	hashed := sha256.Sum256([]byte(payload))
	payloadHash := hex.EncodeToString(hashed[:])

	headers := map[string]string{
		"content-type": "application/json",
		"host":         host,
		"x-tc-action":  action,
		"x-tc-region": region,
		"x-tc-version": version,
		"x-tc-timestamp": fmt.Sprintf("%d", ts),
	}

	// 规范请求（signedHeaders 按字典序）
	sh := sortedHeaderKeys(headers)
	var canonHead, signedHead string
	for i, k := range sh {
		canonHead += k + ":" + strings.TrimSpace(headers[k]) + "\n"
		if i > 0 {
			signedHead += ";"
		}
		signedHead += k
	}
	canonical := "POST\n/\n\n" + canonHead + "\n" + signedHead + "\n" + payloadHash

	scope := date + "/" + svc + "/tc3_request"
	h := sha256.Sum256([]byte(canonical))
	sts := "TC3-HMAC-SHA256\n" + fmt.Sprintf("%d", ts) + "\n" + scope + "\n" + hex.EncodeToString(h[:])

	kDate := hmacSHA256([]byte("TC3"+cred.SecretKey), []byte(date))
	kSvc := hmacSHA256(kDate, []byte(svc))
	kSign := hmacSHA256(kSvc, []byte("tc3_request"))
	sig := hex.EncodeToString(hmacSHA256(kSign, []byte(sts)))

	auth := fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		cred.AccessKey, scope, signedHead, sig)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/", bytes.NewReader([]byte(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", host)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", version)
	req.Header.Set("X-TC-Region", region)
	req.Header.Set("X-TC-Timestamp", fmt.Sprintf("%d", ts))
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

func sortedHeaderKeys(m map[string]string) []string {
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

func first(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}
