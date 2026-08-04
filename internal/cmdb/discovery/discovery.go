// Package discovery 定义 CMDB 自动发现的插件框架。
//
// 设计要点：
//   - 自动发现与「资源纳管」层（云/主机/容器，经 internal/cloud）职责分离：
//     纳管层是单一数据源，负责拉取基础 CI；本包只负责在探针 Agent 上跑脚本，
//     扫描网络设备 / 中间件 / 数据库 / 应用 / 物理服务器等「纳管层不覆盖」的资源。
//   - 传输复用 Agent 通道（agentmgr.RunScriptOnAgent），不引入新依赖、不形成 import 环：
//     ScriptRunner 仅声明与 *agentmgr.Service.RunScriptOnAgent 完全一致的签名，
//     由 cmd/core 把 agentSvc 注入即可满足（鸭子类型）。
//   - 每个插件 = 一段在探针 Agent 上执行的脚本模板 + 一个纯函数解析器（便于单测）。
package discovery

import (
	"context"
	"fmt"
	"sort"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// ScriptRunner 在探针 Agent 上执行脚本（签名与 *agentmgr.Service.RunScriptOnAgent 完全一致）。
// 刻意定义在 discovery 包内，避免 cmdb ↔ agentmgr 循环依赖；cmd/core 注入 agentSvc 即满足。
type ScriptRunner interface {
	RunScriptOnAgent(ctx context.Context, agentID, tenantID, content string,
		params map[string]string, timeout int, runAs string) (stdout string, exitCode int, err error)
}

// Item 是一条被发现资源的归一化表示，由插件解析脚本输出得到。
type Item struct {
	Name       string         // CI 名称（如主机名、设备 sysName）
	CIType     string         // 映射到的 CMDB 模型类型 ID（server/switch/database/middleware/app）
	IPAddress  string         // 主 IP（可选）
	Attributes map[string]any // 其余采集属性（vendor/version/port/engine...）
}

// Discoverer 是一个自动发现插件：针对某类资源，经探针 Agent 执行脚本并解析出 Item 列表。
type Discoverer interface {
	// Types 返回该插件注册到的发现任务 Type（一个插件可覆盖多个别名，如 snmp/network）。
	Types() []string
	// Discover 执行一次发现。scriptRunner 为探针 Agent 通道；凭证从 task.Config 透传为 params。
	Discover(ctx context.Context, task *model.DiscoveryTask, runner ScriptRunner) ([]*Item, error)
}

// Registry 是 Type → Discoverer 的注册表。
type Registry struct {
	discoverers map[string]Discoverer
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{discoverers: make(map[string]Discoverer)}
}

// Register 注册一个插件（其 Types() 返回的每一个别名都会映射到该插件）。
func (r *Registry) Register(d Discoverer) {
	for _, t := range d.Types() {
		r.discoverers[t] = d
	}
}

// Lookup 按 Type 查找插件。
func (r *Registry) Lookup(typeName string) (Discoverer, bool) {
	d, ok := r.discoverers[typeName]
	return d, ok
}

// List 返回所有已注册 Type（升序，供前端下拉）。
func (r *Registry) List() []string {
	out := make([]string, 0, len(r.discoverers))
	for t := range r.discoverers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// AgentIDOf 从发现任务 Config 读取探针 Agent ID（脚本将在此 Agent 上执行）。
func AgentIDOf(task *model.DiscoveryTask) string {
	if task == nil || task.Config == nil {
		return ""
	}
	if v, ok := task.Config["agent_id"].(string); ok {
		return v
	}
	return ""
}

// CredOf 从发现任务 Config 读取凭证字段（如 community / user / password）。
func CredOf(task *model.DiscoveryTask, key string) string {
	if task == nil || task.Config == nil {
		return ""
	}
	if v, ok := task.Config[key].(string); ok {
		return v
	}
	return ""
}

// ToParams 把发现任务里的凭证字段收集为脚本参数（避免把密钥写进脚本体）。
// keys 为需要透传的 Config 键；TARGET 始终注入为 task.Target。
func ToParams(task *model.DiscoveryTask, keys ...string) map[string]string {
	p := map[string]string{"TARGET": task.Target}
	for _, k := range keys {
		if v := CredOf(task, k); v != "" {
			p[k] = v
		}
	}
	return p
}

// requireAgent 校验探针 Agent 已配置，否则返回可读错误。
func requireAgent(task *model.DiscoveryTask) (string, error) {
	id := AgentIDOf(task)
	if id == "" {
		return "", fmt.Errorf("discovery task %q 未配置探针 Agent（Config.agent_id 必填）", task.ID)
	}
	return id, nil
}
