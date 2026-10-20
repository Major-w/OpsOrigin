// Package collect 承载「外部指标采集源」抽象与实现。
//
// 设计原则: OpsOrigin 不重复造采集器, 而是兼容现成生态 ——
//   - Zabbix Agent 被动检查协议(TCP :10050): 直接吃社区/企业版 Zabbix Agent 的数据;
//   - Prometheus exporter HTTP 抓取(/metrics): 吃任意 exporter(node_exporter 等);
//   - 自有插件: 走 cmd/agent → tunnel → ingest → VMSink → VM(push 路径, 本包不涉及)。
//
// 每个 Source 是 pull 模式, 由 monitor.Service 的调度器按间隔触发 Collect,
// 产出 Sample 后统一落 VictoriaMetrics。
package collect

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 采集源类型。
const (
	TypeZabbix     = "zabbix"
	TypePrometheus = "prometheus"
)

// Sample 一次采集得到的单个指标点。
type Sample struct {
	Name      string
	Value     float64
	Unit      string
	Labels    map[string]string
	Timestamp time.Time
}

// Source 一个外部指标采集源(pull 模式)。
type Source interface {
	// Type 返回采集源类型(zabbix | prometheus)。
	Type() string
	// Collect 拉取当前指标。实现需保证并发安全。
	Collect(ctx context.Context) ([]Sample, error)
}

// Config 采集源配置(由前端/API 创建)。
type Config struct {
	ID       string
	TenantID string
	Name     string
	Type     string   // TypeZabbix | TypePrometheus
	Address  string   // host 或 host:port
	Port     int      // zabbix 默认 10050, prometheus 默认 9100
	Path     string   // prometheus /metrics 路径, 默认 /metrics
	Keys     []string // zabbix item keys(为空用默认集)
	Interval time.Duration
	CIID     string
	Labels   map[string]string // 附加静态标签
}

// NewSource 按类型构造采集源。
func NewSource(cfg Config) (Source, error) {
	switch cfg.Type {
	case TypeZabbix:
		return NewZabbixSource(cfg), nil
	case TypePrometheus:
		return NewPrometheusSource(cfg), nil
	default:
		return nil, errUnknownType(cfg.Type)
	}
}

func errUnknownType(t string) error {
	return fmt.Errorf("unknown source type: %s", t)
}

var metricNameRe = regexp.MustCompile(`[^a-zA-Z0-9_:]`)

// SanitizeMetricName 将任意 key 规整为 Prometheus 合法指标名(非法字符→下划线, 去首尾下划线)。
func SanitizeMetricName(s string) string {
	s = strings.TrimSpace(s)
	s = metricNameRe.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

// mergeLabels 合并 extra + 单样本 labels, 并保证 instance 来自 extra(避免覆盖)。
func mergeLabels(extra, sample map[string]string) map[string]string {
	merged := make(map[string]string, len(extra)+len(sample))
	for k, v := range extra {
		merged[k] = v
	}
	for k, v := range sample {
		merged[k] = v
	}
	if inst, ok := extra["instance"]; ok {
		merged["instance"] = inst
	}
	return merged
}
