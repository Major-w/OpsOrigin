package ai

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/opsorigin/core/internal/ai/model"
)

// team_stream_adapter 把团队编排的 TeamStreamEvent 映射为统一帧协议 v1(event: X + data: 双行帧),
// 与单员工主聊天(P1)同源, 使前端只需一套事件状态机。
//
// 设计约束(见 target-ai-assistant-c-convergence.md §3.3 / §3.4):
//   - 选择 P1 的 event 行语义为唯一基线, 团队侧只新增 adapter + 解析器分支, 不重写前端状态机。
//   - 团队增量(token)走独立 event: agent_token, 绝不塞进裸 data: —— 裸 data 是 JSON 字符串,
//     前端按文本拼接, 塞对象会丢 agent_id 并污染正文。
//   - 纯映射、无业务逻辑: 不调用引擎、不做权限判断, 避免引擎包反向依赖 HTTP 帧格式(分层单向)。
//
// 并行团队的多个 worker 会并发调用 emit, 故 writeTeamEventV1 内部加锁串行化写 ResponseWriter。

// writeTeamEventV1 把一个团队事件按 v1 帧协议写入 w, 调用方负责 flush。
func writeTeamEventV1(w io.Writer, mu *sync.Mutex, ev model.TeamStreamEvent) {
	name, payload := teamEventV1(ev)
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload)
}

// teamEventV1 返回 (v1 事件名, JSON payload), 完整实现 P2→v1 映射表(§3.4)。
func teamEventV1(ev model.TeamStreamEvent) (string, string) {
	switch ev.Type {
	case "agent_start":
		b, _ := json.Marshal(map[string]any{
			"phase":      "start",
			"agent_id":   ev.AgentID,
			"agent_name": ev.AgentName,
			"role":       ev.Role,
		})
		return "agent", string(b)
	case "agent_end":
		b, _ := json.Marshal(map[string]any{
			"phase":      "end",
			"agent_id":   ev.AgentID,
			"agent_name": ev.AgentName,
			"role":       ev.Role,
			"content":    ev.Content,
		})
		return "agent", string(b)
	case "token":
		b, _ := json.Marshal(map[string]any{
			"agent_id": ev.AgentID,
			"content":  ev.Content,
		})
		return "agent_token", string(b)
	case "tool_call":
		b, _ := json.Marshal(map[string]any{
			"name":     ev.ToolName,
			"args":     ev.ToolArgs,
			"status":   "running",
			"agent_id": ev.AgentID,
		})
		return "tool", string(b)
	case "tool_result":
		b, _ := json.Marshal(map[string]any{
			"name":     ev.ToolName,
			"result":   ev.ToolResult,
			"status":   "done",
			"agent_id": ev.AgentID,
		})
		return "tool", string(b)
	case "supervisor_review":
		b, _ := json.Marshal(map[string]any{
			"agent_id": ev.AgentID,
			"content":  ev.Content,
			"role":     "supervisor",
		})
		return "agent_token", string(b)
	case "final":
		b, _ := json.Marshal(map[string]any{
			"content": ev.Content,
			"done":    ev.Done,
		})
		return "final", string(b)
	case "error":
		// 复用 P1 error 语义: payload 为可读错误文本(前端 chatFriendlyError 同源)。
		b, _ := json.Marshal(ev.Error)
		return "error", string(b)
	default:
		b, _ := json.Marshal(ev.Error)
		return "error", string(b)
	}
}
