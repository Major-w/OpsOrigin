package cmdb

import (
	"net/http"

	"github.com/opsorigin/core/pkg/httputil"
)

// CollectorInfo 描述一个采集器插槽（kind × resourceType）及其运行时注册状态。
// 采集器可插拔：具体实现由 cmd/core 在启动时按 (kind, resourceType) 注册进注册表；
// 此处暴露「平台规划的采集矩阵 + 当前是否已注册」，供前端展示采集能力覆盖情况。
type CollectorInfo struct {
	Kind         string `json:"kind"`          // cmdb | monitor
	ResourceType string `json:"resource_type"` // cloud | host | container
	Registered   bool   `json:"registered"`    // 运行时是否已有采集器实现注册
	Description  string `json:"description"`
}

// knownCollectorMatrix 平台规划的采集器矩阵。实际注册由 cmd/core 在启动时完成
// （当前已注册 cmdb:cloud / monitor:cloud；host / container 待接入 Agent 采集器后注册）。
var knownCollectorMatrix = []CollectorInfo{
	{Kind: "cmdb", ResourceType: "cloud", Description: "云资源 CMDB 采集（server 拉模式，复用云厂商 API）"},
	{Kind: "monitor", ResourceType: "cloud", Description: "云监控指标采集（server 拉模式，经 onMetric 落 ingest 管道）"},
	{Kind: "cmdb", ResourceType: "host", Description: "主机 CMDB 采集（经 Agent 通道，由 Agent 本地执行后 push 上报）"},
	{Kind: "monitor", ResourceType: "host", Description: "主机监控指标采集（经 Agent 通道 push 上报）"},
	{Kind: "cmdb", ResourceType: "container", Description: "容器 CMDB 采集（经 Agent 通道，由 Agent 本地执行后 push 上报）"},
	{Kind: "monitor", ResourceType: "container", Description: "容器监控指标采集（经 Agent 通道 push 上报）"},
}

// ListCollectors 返回采集器矩阵与当前注册状态（实时查注册表）。
func (s *Service) ListCollectors() []CollectorInfo {
	out := make([]CollectorInfo, 0, len(knownCollectorMatrix))
	for _, c := range knownCollectorMatrix {
		info := c
		_, info.Registered = s.registry.Lookup(c.Kind, c.ResourceType)
		out = append(out, info)
	}
	return out
}

func (s *Service) handleListCollectors(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, httputil.EmptySliceIfNil(s.ListCollectors()))
}
