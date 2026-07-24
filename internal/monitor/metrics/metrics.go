package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry 是全局共享的 Prometheus Registry。
var Registry = prometheus.NewRegistry()

func init() {
	// 注册 Go 运行时指标
	Registry.MustRegister(collectors.NewGoCollector())
}

// 自定义指标定义
var (
	// AgentmgrGrpcConnections 当前 gRPC 连接数
	AgentmgrGrpcConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agentmgr_grpc_connections",
			Help: "Current number of gRPC connections",
		},
		[]string{"instance", "tenant_id"},
	)

	// AgentmgrHeartbeatTotal 心跳总次数
	AgentmgrHeartbeatTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentmgr_heartbeat_total",
			Help: "Total number of heartbeats received",
		},
		[]string{"instance", "tenant_id"},
	)

	// HttpRequestDuration HTTP request duration histogram
	HttpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		// 必须无标签: promhttp.InstrumentHandlerDuration 会自行加入 code/method 标签,
		// 传入带标签的 ObserverVec 会在 server 启动时 panic(见 cmd/core/main.go:138)。
		[]string{},
	)

	// MonitorProbeResults 当前内存中缓冲的探测结果条数(F4-R3)
	MonitorProbeResults = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "monitor_probe_results",
			Help: "Current number of probe results buffered in memory",
		},
	)
)

func init() {
	Registry.MustRegister(AgentmgrGrpcConnections)
	Registry.MustRegister(AgentmgrHeartbeatTotal)
	Registry.MustRegister(HttpRequestDuration)
	Registry.MustRegister(MonitorProbeResults)
}

// NewMetricsHandler 返回 Prometheus /metrics 端点的 http.Handler。
func NewMetricsHandler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
}
