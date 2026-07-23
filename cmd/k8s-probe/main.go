// Command k8s-probe 是 OpsOrigin 的 K8s 集群探针。
// 以 DaemonSet 形态部署到集群每个节点，采集节点/Pod/工作负载/事件/指标，
// 通过 HTTP 上报 Core（参考主机 Agent 的上报范式）。
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/opsorigin/core/internal/probe"
)

// ready 在首次采集成功后置位，供 /readyz 探针判断。
var ready atomic.Bool

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	server := flag.String("server", "", "Core 地址 (http(s)://host:8080)")
	token := flag.String("token", "", "探针注册令牌 (Core 分配)")
	cluster := flag.String("cluster", "", "集群标识 (Core 侧登记名)")
	kubeconfig := flag.String("kubeconfig", "", "kubeconfig 路径；空=in-cluster")
	inCluster := flag.Bool("in-cluster", false, "强制使用 in-cluster 配置（忽略 --kubeconfig）")
	interval := flag.Duration("interval", 60*time.Second, "采集间隔")
	healthAddr := flag.String("health-addr", ":8081", "health/readiness HTTP 监听地址（DaemonSet 探针用）")
	flag.Parse()

	if *server == "" {
		slog.Error("missing --server")
		os.Exit(1)
	}
	if *cluster == "" {
		*cluster = os.Getenv("CLUSTER_NAME")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 健康服务：DaemonSet 的 liveness/readiness 探针打这里。
	// /healthz 进程存活即 200；/readyz 首次采集成功后才 200。
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
			if ready.Load() {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ready"))
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		})
		if err := http.ListenAndServe(*healthAddr, mux); err != nil {
			slog.Error("health server stopped", "error", err)
		}
	}()

	// --in-cluster 时忽略 --kubeconfig，强制走 ServiceAccount（DaemonSet 默认形态）。
	if *inCluster {
		*kubeconfig = ""
	}

	collector, err := probe.NewCollector(*kubeconfig)
	if err != nil {
		slog.Error("init collector failed", "error", err)
		os.Exit(1)
	}
	reporter := probe.NewReporter(*server, *token)

	agentID := ""
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	slog.Info("k8s probe started", "server", *server, "cluster", *cluster, "interval", interval.String())

	// 立即采集一次，之后按间隔
	runOnce(ctx, collector, reporter, *cluster, &agentID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-sigCh:
			slog.Info("k8s probe shutting down")
			return
		case <-ticker.C:
			runOnce(ctx, collector, reporter, *cluster, &agentID)
		}
	}
}

func runOnce(ctx context.Context, c *probe.Collector, r *probe.Reporter, cluster string, agentID *string) {
	rep, err := c.Collect(ctx)
	if err != nil {
		slog.Warn("collect failed", "error", err)
		return
	}
	rep.Cluster = cluster
	ready.Store(true) // 首次成功采集即可判定就绪

	id, err := r.Report(ctx, *agentID, rep)
	if err != nil {
		slog.Warn("report failed", "error", err)
		return
	}
	if *agentID == "" && id != "" {
		*agentID = id
		slog.Info("probe registered", "agent_id", id)
	}
	slog.Info("collect+report ok",
		"nodes", len(rep.Nodes), "pods", len(rep.Pods),
		"workloads", len(rep.Workloads), "events", len(rep.Events), "metrics", len(rep.Metrics))
}
