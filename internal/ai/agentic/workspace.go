// 会话工作区: 工具执行产生的文件类输出自动沉淀到当前会话的工作区(DB 文本存储),
// 前端对话区可查看/下载/删除。相当于轻量"伪沙箱"——AI 有可读写产物区, 无需容器隔离。
package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

// WorkspaceSink 写入会话工作区的回调(由 cmd/core 注入, nil=不收集)。
type WorkspaceSink func(ctx context.Context, conversationID, toolName, fileName string, content []byte) error

// SetWorkspaceSink 注入会话工作区写入回调。
func (e *Engine) SetWorkspaceSink(s WorkspaceSink) { e.workspaceSink = s }

// workspaceCollectTools 产物型工具: 执行结果自动收集进会话工作区。
var workspaceCollectTools = map[string]bool{
	"script_run":      true,
	"script_detail":   true,
	"cmdb_query":      true,
	"knowledge_query": true,
}

// collectToolArtifact 工具执行后, 若属产物型且会话工作区已接线, 把结果沉淀为工作区文件。
func (e *Engine) collectToolArtifact(ctx context.Context, conversationID, toolName string, args map[string]any, result any) {
	if conversationID == "" || e.workspaceSink == nil || result == nil {
		return
	}
	if !workspaceCollectTools[toolName] {
		return
	}
	// 结果序列化(限 256KB, 防大输出撑爆工作区)
	b, err := json.Marshal(result)
	if err != nil || len(b) == 0 {
		return
	}
	if len(b) > 256<<10 {
		b = b[:256<<10]
	}
	fileName := fmt.Sprintf("%s-%s.json", toolName, time.Now().Format("20060102-150405"))
	if err := e.workspaceSink(ctx, conversationID, toolName, fileName, b); err != nil {
		slog.Warn("workspace sink failed", "tool", toolName, "err", err)
	}
}

// SortedCollectTools 返回产物型工具名列表(排序, 供展示/文档)。
func SortedCollectTools() []string {
	out := make([]string, 0, len(workspaceCollectTools))
	for k := range workspaceCollectTools {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
