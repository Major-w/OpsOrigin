// Package aliyun 实现阿里云（Aliyun）资源纳管，遵循 cloud.Provider 契约。
//
// 鉴权采用阿里云 RPC 风格 API + HMAC-SHA1 签名（SignatureMethod=HMAC-SHA1,
// SignatureVersion=1.0），仅依赖标准库，无需引入庞大的官方 SDK。
//
// 覆盖资源：云主机 ECS、云数据库 RDS、负载均衡 SLB。
package aliyun

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
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

// Provider 阿里云纳管实现。
type Provider struct {
	http *http.Client
}

func init() {
	cloud.Register(&Provider{http: &http.Client{Timeout: 30 * time.Second}})
}

// Name 厂商标识。
func (p *Provider) Name() string { return "aliyun" }

// ListResources 按类型列举资源。
func (p *Provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	if region == "" {
		region = "cn-hangzhou"
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
			res, err = p.listSLB(ctx, cred, region)
		default:
			continue // VPC/Bucket/K8sCluster 由对应子产品扩展
		}
		if err != nil {
			return out, fmt.Errorf("aliyun %s: %w", k, err)
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个云主机详情（以 ECS 为例）。
func (p *Provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	if kind != string(cloud.TypeVM) {
		return nil, fmt.Errorf("aliyun: GetResource only supports vm, got %q", kind)
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
	return nil, &cloud.NoSuchResource{Provider: "aliyun", ID: id}
}

// CollectMetrics 阿里云指标需对接云监控，此处返回 nil（接口允许）。
func (p *Provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// ---------- ECS ----------

func (p *Provider) listECS(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	body, err := p.rpc(ctx, cred, "ecs.aliyuncs.com", "2014-05-26", "DescribeInstances", region, map[string]string{
		"PageSize": "100",
	})
	if err != nil {
		return nil, err
	}
	var r struct {
		Instances struct {
			Instance []struct {
				InstanceId      string `json:"InstanceId"`
				InstanceName    string `json:"InstanceName"`
				Status          string `json:"Status"`
				RegionId        string `json:"RegionId"`
				ZoneId          string `json:"ZoneId"`
				PublicIpAddress struct {
					IpAddress []string `json:"IpAddress"`
				} `json:"PublicIpAddress"`
				InnerIpAddress struct {
					IpAddress []string `json:"IpAddress"`
				} `json:"InnerIpAddress"`
			} `json:"Instance"`
		} `json:"Instances"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse ecs: %w", err)
	}
	out := make([]cloud.Resource, 0, len(r.Instances.Instance))
	for _, it := range r.Instances.Instance {
		priv := ""
		if len(it.InnerIpAddress.IpAddress) > 0 {
			priv = it.InnerIpAddress.IpAddress[0]
		}
		pub := ""
		if len(it.PublicIpAddress.IpAddress) > 0 {
			pub = it.PublicIpAddress.IpAddress[0]
		}
		out = append(out, cloud.Resource{
			Provider: "aliyun", Type: cloud.TypeVM, ID: it.InstanceId,
			Name: it.InstanceName, Region: it.RegionId, Zone: it.ZoneId,
			Status: it.Status, PublicIP: pub, PrivateIP: priv,
		})
	}
	return out, nil
}

// ---------- RDS ----------

func (p *Provider) listRDS(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	body, err := p.rpc(ctx, cred, "rds.aliyuncs.com", "2014-08-15", "DescribeDBInstances", region, map[string]string{
		"PageSize": "100",
	})
	if err != nil {
		return nil, err
	}
	var r struct {
		Items struct {
			DBInstance []struct {
				DBInstanceId          string `json:"DBInstanceId"`
				DBInstanceDescription string `json:"DBInstanceDescription"`
				DBInstanceStatus      string `json:"DBInstanceStatus"`
				RegionId              string `json:"RegionId"`
				ZoneId                string `json:"ZoneId"`
			} `json:"DBInstance"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse rds: %w", err)
	}
	out := make([]cloud.Resource, 0, len(r.Items.DBInstance))
	for _, it := range r.Items.DBInstance {
		name := it.DBInstanceDescription
		if name == "" {
			name = it.DBInstanceId
		}
		out = append(out, cloud.Resource{
			Provider: "aliyun", Type: cloud.TypeDatabase, ID: it.DBInstanceId,
			Name: name, Region: it.RegionId, Zone: it.ZoneId, Status: it.DBInstanceStatus,
		})
	}
	return out, nil
}

// ---------- SLB ----------

func (p *Provider) listSLB(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	body, err := p.rpc(ctx, cred, "slb.aliyuncs.com", "2014-05-15", "DescribeLoadBalancers", region, map[string]string{
		"PageSize": "100",
	})
	if err != nil {
		return nil, err
	}
	var r struct {
		LoadBalancers struct {
			LoadBalancer []struct {
				LoadBalancerId     string `json:"LoadBalancerId"`
				LoadBalancerName   string `json:"LoadBalancerName"`
				Address            string `json:"Address"`
				RegionId           string `json:"RegionId"`
				LoadBalancerStatus string `json:"LoadBalancerStatus"`
			} `json:"LoadBalancer"`
		} `json:"LoadBalancers"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse slb: %w", err)
	}
	out := make([]cloud.Resource, 0, len(r.LoadBalancers.LoadBalancer))
	for _, it := range r.LoadBalancers.LoadBalancer {
		name := it.LoadBalancerName
		if name == "" {
			name = it.LoadBalancerId
		}
		out = append(out, cloud.Resource{
			Provider: "aliyun", Type: cloud.TypeLoadBalancer, ID: it.LoadBalancerId,
			Name: name, Region: it.RegionId, Status: it.LoadBalancerStatus, PublicIP: it.Address,
		})
	}
	return out, nil
}

// rpc 执行一次阿里云 RPC 调用（GET + 签名）。
func (p *Provider) rpc(ctx context.Context, cred cloud.Credential, host, version, action, region string, extra map[string]string) ([]byte, error) {
	params := map[string]string{
		"Format":           "JSON",
		"Version":          version,
		"AccessKeyId":      cred.AccessKey,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   fmt.Sprintf("%d", time.Now().UnixNano()),
		"SignatureVersion": "1.0",
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Action":           action,
		"RegionId":         region,
	}
	for k, v := range extra {
		params[k] = v
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var qparts []string
	for _, k := range keys {
		qparts = append(qparts, percentEncode(k)+"="+percentEncode(params[k]))
	}
	query := strings.Join(qparts, "&")
	canonical := "GET&" + percentEncode("/") + "&" + percentEncode(query)

	mac := hmac.New(sha1.New, []byte(cred.SecretKey+"&"))
	mac.Write([]byte(canonical))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	u := "https://" + host + "/?" + query + "&Signature=" + percentEncode(sign)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(data))
	}
	if e := codeErr(data); e != "" {
		return nil, fmt.Errorf("api error: %s", e)
	}
	return data, nil
}

// codeErr 解析阿里云公共错误结构。
func codeErr(b []byte) string {
	var e struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if json.Unmarshal(b, &e) != nil || e.Code == "" {
		return ""
	}
	return e.Code + ": " + e.Message
}

// percentEncode 按阿里云规范的 URL 编码（空格→%20，*→%2A）。
func percentEncode(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	return e
}
