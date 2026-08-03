// Package aws 实现 OpsOrigin 云资源纳管中 AWS 的 Provider。
// 采用 AWS Signature Version 4（Query 协议）直接调用官方 API，
// 仅依赖标准库，无任何第三方 SDK 依赖，便于审阅与离线编译。
//
// 纳管范围（与主机/Agentless/Proxy 接入同源的“纳管”语义）：
//   - 计算：EC2 实例（DescribeInstances）
//   - 数据库：RDS 实例（DescribeDBInstances）
//   - 负载均衡：ELBv2 负载均衡器（DescribeLoadBalancers）
package aws

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cloud"
)

// Provider 实现 cloud.Provider 接口。
type Provider struct {
	http *http.Client
}

func init() {
	cloud.Register(&Provider{http: &http.Client{Timeout: 30 * time.Second}})
}

// Name 返回提供商标识。
func (p *Provider) Name() string { return "aws" }

// ListResources 列举指定地域、指定类型的资源。
func (p *Provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	if region == "" {
		region = "us-east-1"
	}
	var out []cloud.Resource
	for _, k := range kinds {
		var (
			res []cloud.Resource
			err error
		)
		switch k {
		case cloud.TypeVM:
			res, err = p.listEC2(ctx, cred, region)
		case cloud.TypeDatabase:
			res, err = p.listRDS(ctx, cred, region)
		case cloud.TypeLoadBalancer:
			res, err = p.listELB(ctx, cred, region)
		default:
			continue // VPC/Bucket/K8sCluster 由对应子产品扩展
		}
		if err != nil {
			return out, fmt.Errorf("aws %s: %w", k, err)
		}
		out = append(out, res...)
	}
	return out, nil
}

// GetResource 获取单个资源详情（列表过滤实现）。
func (p *Provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	kt, err := parseKind(kind)
	if err != nil {
		return nil, err
	}
	all, err := p.ListResources(ctx, cred, region, []cloud.ResourceType{kt})
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, &cloud.NoSuchResource{Provider: "aws", ID: id}
}

// CollectMetrics 占位：AWS CloudWatch 指标采集需在 collector 层接入，此处返回 nil。
func (p *Provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}

// parseKind 将字符串类型的 kind 转回 ResourceType（兼容 UI 传入字符串）。
func parseKind(kind string) (cloud.ResourceType, error) {
	switch cloud.ResourceType(kind) {
	case cloud.TypeVM, cloud.TypeDatabase, cloud.TypeLoadBalancer:
		return cloud.ResourceType(kind), nil
	default:
		return "", fmt.Errorf("aws: unsupported kind %q", kind)
	}
}

// ---------------------------------------------------------------------------
// SigV4 签名（Query 协议）
// ---------------------------------------------------------------------------

// sigV4 对 AWS Query 请求参数进行 SigV4 签名，返回可拼接到 URL 的已签名查询串。
func sigV4(service, region, endpoint, accessKey, secretKey string, params url.Values) (string, error) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	// 加入签名所需公共参数
	params.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	params.Set("X-Amz-Credential", fmt.Sprintf("%s/%s/%s/%s/aws4_request", accessKey, dateStamp, region, service))
	params.Set("X-Amz-Date", amzDate)
	params.Set("X-Amz-SignedHeaders", "host")

	// 规范查询串：按 key 排序，value 做标准 URL 编码
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonParts []string
	for _, k := range keys {
		vs := params[k]
		sort.Strings(vs)
		for _, v := range vs {
			canonParts = append(canonParts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	canonicalQuery := strings.Join(canonParts, "&")

	host := strings.TrimPrefix(endpoint, "https://")
	host = strings.TrimPrefix(host, "http://")
	canonicalHeaders := "host:" + host + "\n"
	signedHeaders := "host"

	payloadHash := sha256hex([]byte(""))
	canonicalRequest := strings.Join([]string{
		"GET",
		"/",
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := getSignatureKey(secretKey, dateStamp, region, service)
	signature := hexhmac(signingKey, stringToSign)

	params.Set("X-Amz-Signature", signature)
	return params.Encode(), nil
}

// awsCall 执行一次带 SigV4 签名的 AWS Query API 调用，返回响应体字节。
func (p *Provider) awsCall(ctx context.Context, service, region, endpoint, accessKey, secretKey, action, apiVersion string, extra url.Values) ([]byte, error) {
	params := url.Values{}
	params.Set("Action", action)
	params.Set("Version", apiVersion)
	for k, vs := range extra {
		for _, v := range vs {
			params.Add(k, v)
		}
	}
	qs, err := sigV4(service, region, endpoint, accessKey, secretKey, params)
	if err != nil {
		return nil, err
	}
	reqURL := endpoint + "?" + qs
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Host", strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"))

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aws: %s %s returned %d: %s", service, action, resp.StatusCode, string(body))
	}
	return body, nil
}

// ---------------------------------------------------------------------------
// EC2（计算）
// ---------------------------------------------------------------------------

type ec2DescribeInstancesResponse struct {
	XMLName      xml.Name `xml:"DescribeInstancesResponse"`
	Reservations []struct {
		Instances []struct {
			InstanceID   string `xml:"instanceId"`
			InstanceType string `xml:"instanceType"`
			State        struct {
				Name string `xml:"name"`
			} `xml:"instanceState"`
			PrivateIP  string `xml:"privateIpAddress"`
			PublicIP   string `xml:"ipAddress"`
			AZ         string `xml:"placement>availabilityZone"`
			LaunchTime string `xml:"launchTime"`
		} `xml:"instancesSet>item"`
	} `xml:"reservationSet>item"`
}

func (p *Provider) listEC2(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	endpoint := fmt.Sprintf("https://ec2.%s.amazonaws.com/", region)
	body, err := p.awsCall(ctx, "ec2", region, endpoint, cred.AccessKey, cred.SecretKey, "DescribeInstances", "2016-11-15", nil)
	if err != nil {
		return nil, err
	}
	var resp ec2DescribeInstancesResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("aws: parse EC2 response: %w", err)
	}
	out := make([]cloud.Resource, 0)
	for _, r := range resp.Reservations {
		for _, inst := range r.Instances {
			ip := inst.PublicIP
			if ip == "" {
				ip = inst.PrivateIP
			}
			out = append(out, cloud.Resource{
				Provider: "aws", Type: cloud.TypeVM, ID: inst.InstanceID,
				Name: inst.InstanceID, Region: region, Zone: inst.AZ, Status: inst.State.Name,
				PublicIP: inst.PublicIP, PrivateIP: inst.PrivateIP,
				Extra: map[string]any{
					"instance_type":     inst.InstanceType,
					"availability_zone": inst.AZ,
					"launch_time":       inst.LaunchTime,
					"ip":                ip,
				},
			})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// RDS（数据库）
// ---------------------------------------------------------------------------

type rdsDescribeResponse struct {
	XMLName     xml.Name `xml:"DescribeDBInstancesResponse"`
	DBInstances []struct {
		DBInstanceIdentifier string `xml:"DBInstanceIdentifier"`
		Engine               string `xml:"Engine"`
		EngineVersion        string `xml:"EngineVersion"`
		DBInstanceStatus     string `xml:"DBInstanceStatus"`
		Endpoint             struct {
			Address string `xml:"Address"`
			Port    int    `xml:"Port"`
		} `xml:"Endpoint"`
		AllocatedStorage int `xml:"AllocatedStorage"`
	} `xml:"DescribeDBInstancesResult>DBInstances>DBInstance"`
}

func (p *Provider) listRDS(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	endpoint := fmt.Sprintf("https://rds.%s.amazonaws.com/", region)
	body, err := p.awsCall(ctx, "rds", region, endpoint, cred.AccessKey, cred.SecretKey, "DescribeDBInstances", "2014-10-31", nil)
	if err != nil {
		return nil, err
	}
	var resp rdsDescribeResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("aws: parse RDS response: %w", err)
	}
	out := make([]cloud.Resource, 0)
	for _, db := range resp.DBInstances {
		out = append(out, cloud.Resource{
			Provider: "aws", Type: cloud.TypeDatabase, ID: db.DBInstanceIdentifier,
			Name: db.DBInstanceIdentifier, Region: region, Status: db.DBInstanceStatus,
			Extra: map[string]any{
				"engine":         db.Engine,
				"engine_version": db.EngineVersion,
				"address":        db.Endpoint.Address,
				"port":           db.Endpoint.Port,
				"allocated_gb":   db.AllocatedStorage,
			},
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// ELBv2（负载均衡）
// ---------------------------------------------------------------------------

type elbv2DescribeResponse struct {
	XMLName   xml.Name `xml:"DescribeLoadBalancersResponse"`
	Balancers []struct {
		LoadBalancerARN string `xml:"LoadBalancerArn"`
		DNSName         string `xml:"DNSName"`
		Scheme          string `xml:"Scheme"`
		State           struct {
			Code string `xml:"Code"`
		} `xml:"State"`
		Type string `xml:"Type"`
	} `xml:"DescribeLoadBalancersResult>LoadBalancers>member"`
}

func (p *Provider) listELB(ctx context.Context, cred cloud.Credential, region string) ([]cloud.Resource, error) {
	endpoint := fmt.Sprintf("https://elasticloadbalancing.%s.amazonaws.com/", region)
	body, err := p.awsCall(ctx, "elasticloadbalancing", region, endpoint, cred.AccessKey, cred.SecretKey, "DescribeLoadBalancers", "2015-12-01", nil)
	if err != nil {
		return nil, err
	}
	var resp elbv2DescribeResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("aws: parse ELB response: %w", err)
	}
	out := make([]cloud.Resource, 0)
	for _, lb := range resp.Balancers {
		out = append(out, cloud.Resource{
			Provider: "aws", Type: cloud.TypeLoadBalancer, ID: lb.LoadBalancerARN,
			Name: lb.DNSName, Region: region, Status: lb.State.Code,
			Extra: map[string]any{
				"scheme": lb.Scheme,
				"type":   lb.Type,
				"dns":    lb.DNSName,
			},
		})
	}
	return out, nil
}
