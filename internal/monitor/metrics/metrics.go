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
		[]string{"handler", "method", "code"},
	)
)

func init() {
	Registry.MustRegister(AgentmgrGrpcConnections)
	Registry.MustRegister(AgentmgrHeartbeatTotal)
	Registry.MustRegister(HttpRequestDuration)
}

// NewMetricsHandler 返回 Prometheus /metrics 端点的 http.Handler。
func NewMetricsHandler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{})
}

// InstrumentHandler 包装一个 http.Handler，自动记录 HTTP 请求持续时间。
func InstrumentHandler(handlerName string, next http.Handler) http.Handler {
	return promhttp.InstrumentHandlerDuration(HttpRequestDuration.MustCurryWith(prometheus.Labels{"handler": handlerName}), next)
}
