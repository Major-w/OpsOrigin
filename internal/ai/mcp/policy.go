// 任务级 Agent 的工具审批闸门(T3): Runner 与引擎之间的执行前判定通道。
//
// 链路: ai.Service.runTaskStep 在驱动 ChatStream 前, 把 enforceTaskPolicy 闭包经 ctx 注入
// (WithTaskToolGate); agentic.Engine.execToolChecked 在真实执行工具前取出闸门询问放行与否。
// 普通对话 ctx 无闸门 → 引擎行为完全不变, 任务级权限判定对既有链路零侵入。
//
// 闸门返回值语义:
//   - (true, "")          放行
//   - (false, reason)     拒绝执行, reason 会作为工具结果回传给模型(其据此改道)
//
// 需要人工审批时, 闸门内部阻塞等待用户批复(任务转 waiting_approval), 批复通过后返回放行。
package mcp

import "context"

// TaskToolGate 工具执行前闸门: 参数为工具名与解析后的调用参数。
type TaskToolGate func(ctx context.Context, tool string, args map[string]any) (allowed bool, reason string)

type taskGateCtxKey struct{}

// WithTaskToolGate 把闸门注入 ctx(仅任务 Runner 调用)。
func WithTaskToolGate(ctx context.Context, gate TaskToolGate) context.Context {
	if gate == nil {
		return ctx
	}
	return context.WithValue(ctx, taskGateCtxKey{}, gate)
}

// TaskToolGateFrom 提取 ctx 中的闸门; 无任务上下文时返回 nil(调用方跳过判定)。
func TaskToolGateFrom(ctx context.Context) TaskToolGate {
	g, _ := ctx.Value(taskGateCtxKey{}).(TaskToolGate)
	return g
}
