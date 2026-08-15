package agentic

import (
	"context"
	"sync"
)

// 本文件实现 agent 执行钩子链, 参考 DeepSeek Harness 的扩展点设计:
//   agent/pre-step → step/start → tool/call → tool/result → step/end
// 把"审批闸门 / 护栏 / 观测 / 计费上限"等横切关注点从主循环剥离为可组合钩子,
// 新增能力时挂一个函数即可, 无需改动 chatWithToolsConv 主循环。

// PreStepHook 在每轮 LLM 请求组装前触发。返回 skip=true 可短路本轮(不调用 LLM)。
type PreStepHook func(ctx context.Context, agentID, prompt string) (skip bool, err error)

// PreToolCallHook 在工具执行前触发。可拒绝(allow=false, 返回 errMsg)或改写入参(overriddenArgs 非空时覆盖)。
type PreToolCallHook func(ctx context.Context, agentID, toolName string, args map[string]any) (allow bool, overriddenArgs map[string]any, errMsg string)

// PostToolResultHook 在工具执行后触发, 仅观测(不影响结果/错误)。
type PostToolResultHook func(ctx context.Context, agentID, toolName string, result any, execErr bool)

// PostStepHook 在每轮 LLM 请求结束后触发(无论是否产生工具调用)。
type PostStepHook func(ctx context.Context, agentID string, stepIndex int, finished bool)

// Hook 一组可选钩子; 未设置(nil)的字段在链中自动跳过。
type Hook struct {
	PreStep     PreStepHook
	PreToolCall PreToolCallHook
	PostTool    PostToolResultHook
	PostStep    PostStepHook
}

// HookChain 钩子链: 按注册顺序执行各类型钩子。并发安全。
type HookChain struct {
	mu       sync.Mutex
	preStep  []PreStepHook
	preTool  []PreToolCallHook
	postTool []PostToolResultHook
	postStep []PostStepHook
}

// NewHookChain 构造空钩子链。
func NewHookChain() *HookChain {
	return &HookChain{}
}

// Add 注册一组钩子(按类型追加到链尾, 保持注册顺序)。
func (c *HookChain) Add(h Hook) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h.PreStep != nil {
		c.preStep = append(c.preStep, h.PreStep)
	}
	if h.PreToolCall != nil {
		c.preTool = append(c.preTool, h.PreToolCall)
	}
	if h.PostTool != nil {
		c.postTool = append(c.postTool, h.PostTool)
	}
	if h.PostStep != nil {
		c.postStep = append(c.postStep, h.PostStep)
	}
}

// RunPreStep 执行 PreStep 链; 任一返回 skip=true 或 err 即短路返回。
func (c *HookChain) RunPreStep(ctx context.Context, agentID, prompt string) (bool, error) {
	c.mu.Lock()
	hs := append([]PreStepHook(nil), c.preStep...)
	c.mu.Unlock()
	for _, h := range hs {
		if skip, err := h(ctx, agentID, prompt); skip || err != nil {
			return skip, err
		}
	}
	return false, nil
}

// RunPreToolCall 执行 PreToolCall 链; 任一拒绝即短路(返回 allow=false + errMsg); 否则返回(可能被前序钩子改写的)args。
func (c *HookChain) RunPreToolCall(ctx context.Context, agentID, name string, args map[string]any) (bool, map[string]any, string) {
	c.mu.Lock()
	hs := append([]PreToolCallHook(nil), c.preTool...)
	c.mu.Unlock()
	for _, h := range hs {
		allow, ov, msg := h(ctx, agentID, name, args)
		if !allow {
			return false, args, msg
		}
		if ov != nil {
			args = ov
		}
	}
	return true, args, ""
}

// RunPostTool 执行 PostTool 链(仅观测)。
func (c *HookChain) RunPostTool(ctx context.Context, agentID, name string, result any, execErr bool) {
	c.mu.Lock()
	hs := append([]PostToolResultHook(nil), c.postTool...)
	c.mu.Unlock()
	for _, h := range hs {
		h(ctx, agentID, name, result, execErr)
	}
}

// RunPostStep 执行 PostStep 链。
func (c *HookChain) RunPostStep(ctx context.Context, agentID string, idx int, finished bool) {
	c.mu.Lock()
	hs := append([]PostStepHook(nil), c.postStep...)
	c.mu.Unlock()
	for _, h := range hs {
		h(ctx, agentID, idx, finished)
	}
}
