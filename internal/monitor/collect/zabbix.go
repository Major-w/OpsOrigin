package collect

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

// ZabbixSource 通过 Zabbix Agent 被动检查协议(TCP, 默认 10050)拉取指标。
//
// 协议: Server 发送 item key + "\n", Agent 返回一行 value(文本)。
// 与社区版/企业版 Zabbix Agent 100% 协议兼容 —— 客户机房已有的 Zabbix Agent
// 无需改动即可被 OpsOrigin 直接纳管, 复用其全部 item key。
type ZabbixSource struct {
	cfg     Config
	keys    []string
	port    int
	timeout time.Duration
}

// DefaultZabbixKeys 一组常用基础监控 item keys(主机/CPU/内存/磁盘/网络/进程/交换)。
// 用户不填 keys 时使用, 覆盖中小客户最常见的主机监控诉求。
var DefaultZabbixKeys = []string{
	"system.cpu.load[all,avg1]",
	"system.cpu.load[all,avg5]",
	"system.cpu.util[,idle]",
	"vm.memory.size[total]",
	"vm.memory.size[used]",
	"vm.memory.size[available]",
	"vfs.fs.size[/,total]",
	"vfs.fs.size[/,used]",
	"vfs.fs.size[/,pused]",
	"net.if.in[eth0]",
	"net.if.out[eth0]",
	"system.uptime",
	"proc.num",
	"system.swap.size[,pfree]",
}

// NewZabbixSource 构造 Zabbix 采集源(端口默认 10050, keys 空用默认集)。
func NewZabbixSource(cfg Config) *ZabbixSource {
	keys := cfg.Keys
	if len(keys) == 0 {
		keys = DefaultZabbixKeys
	}
	port := cfg.Port
	if port == 0 {
		port = 10050
	}
	return &ZabbixSource{cfg: cfg, keys: keys, port: port, timeout: 5 * time.Second}
}

// Type 实现 Source。
func (s *ZabbixSource) Type() string { return TypeZabbix }

// Collect 逐 key 拉取(每 key 一条 TCP 连接, 被动协议最稳)。
// 单 key 失败(不支持/非数值)跳过, 不影响其余。
func (s *ZabbixSource) Collect(ctx context.Context) ([]Sample, error) {
	host := s.cfg.Address
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, strconv.Itoa(s.port))
	}
	out := make([]Sample, 0, len(s.keys))
	now := time.Now()
	for _, key := range s.keys {
		val, err := s.query(ctx, host, strings.TrimSpace(key))
		if err != nil {
			slog.Warn("zabbix key skipped", "source", s.cfg.ID, "key", key, "err", err)
			continue
		}
		out = append(out, Sample{
			Name:      SanitizeMetricName(key),
			Value:     val,
			Labels:    s.baseLabels(),
			Timestamp: now,
		})
	}
	return out, nil
}

// query 发起一次被动检查: 建连 → 发送 key → 读一行 value。
func (s *ZabbixSource) query(ctx context.Context, host, key string) (float64, error) {
	d := net.Dialer{Timeout: s.timeout}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(s.timeout))
	if _, err := conn.Write([]byte(key + "\n")); err != nil {
		return 0, err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && line == "" {
		return 0, err
	}
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "ZBX_NOTSUPPORTED") {
		return 0, fmt.Errorf("not supported: %s", key)
	}
	v, err := strconv.ParseFloat(line, 64)
	if err != nil {
		return 0, fmt.Errorf("non-numeric value %q", line)
	}
	return v, nil
}

func (s *ZabbixSource) baseLabels() map[string]string {
	labels := map[string]string{
		"instance": s.cfg.Address,
		"source":   "zabbix",
	}
	if s.cfg.CIID != "" {
		labels["ci_id"] = s.cfg.CIID
	}
	for k, v := range s.cfg.Labels {
		labels[k] = v
	}
	return labels
}
