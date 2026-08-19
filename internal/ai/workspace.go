package ai

import (
	"context"
	"fmt"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/aiworkspace"
	"github.com/opsorigin/core/internal/report"
)

// AI 定义工作空间：用自然语言描述想看什么，模型在统一报表模块已注册的
// 数据集白名单内填出一个看板草稿。
//
// 边界（重要）：
//   - 本端点只产"草稿"，不落库。用户在前端确认后走既有 POST /report/dashboards，
//     归属与权限校验跟人工搭建看板完全一致，AI 不构成越权旁路。
//   - 模型看不到任何 SQL、表名与租户数据，只看到脱敏的数据集目录（字段名 + 展示名）。
//   - 生成结果会被 report.Dashboard.Validate 终审，非法字段一律进不来。

// generateWorkspaceTimeout 结构化生成通常比对话慢(要吐一大坨 JSON)，给足预算。
const generateWorkspaceTimeout = 90 * time.Second

type generateWorkspaceRequest struct {
	Prompt string `json:"prompt"`
}

func (s *Service) handleGenerateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req generateWorkspaceRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		httputil.WriteError(w, http.StatusBadRequest, "请描述你想看的数据")
		return
	}
	if len([]rune(req.Prompt)) > 1000 {
		httputil.WriteError(w, http.StatusBadRequest, "需求描述过长，请精简到 1000 字以内")
		return
	}
	client := s.LLMClient()
	if client == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "尚未配置 AI 模型，请先在系统设置中完成配置")
		return
	}

	model := ""
	if s.cfg != nil {
		model = s.cfg.LLM.Model
	}
	ctx, cancel := context.WithTimeout(r.Context(), generateWorkspaceTimeout)
	defer cancel()

	draft, err := aiworkspace.New(client, model).Generate(ctx, report.Default(), req.Prompt)
	if err != nil {
		// 生成失败多半是"模型没听懂/数据集覆盖不到"，属于可重试的业务态，不是 5xx。
		httputil.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, draft)
}

// workspaceSuggestion 是提示词建议 chip，按已注册数据集动态生成，
// 新模块注册数据集后建议自动出现，无需改前端硬编码。
type workspaceSuggestion struct {
	Dataset string `json:"dataset"`
	Label   string `json:"label"`
	Prompt  string `json:"prompt"`
}

func (s *Service) handleWorkspaceSuggestions(w http.ResponseWriter, r *http.Request) {
	datasets := report.Default().List()
	out := make([]workspaceSuggestion, 0, len(datasets))
	for _, ds := range datasets {
		label := ds.Label
		if label == "" {
			label = ds.Name
		}
		dim := firstGroupableLabel(ds)
		var prompt string
		switch {
		case ds.TimeField != "" && dim != "":
			prompt = fmt.Sprintf("看近 30 天%s的变化趋势，并按%s拆分分布", label, dim)
		case ds.TimeField != "":
			prompt = fmt.Sprintf("看近 30 天%s的变化趋势", label)
		case dim != "":
			prompt = fmt.Sprintf("按%s统计%s的数量分布", dim, label)
		default:
			prompt = fmt.Sprintf("看一下%s的总体情况", label)
		}
		out = append(out, workspaceSuggestion{Dataset: ds.Name, Label: label, Prompt: prompt})
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

// firstGroupableLabel 取数据集第一个可分组的非时间维度展示名，用于拼建议语。
func firstGroupableLabel(ds *report.Dataset) string {
	for _, d := range ds.Dimensions {
		if !d.Groupable || d.Type == report.FieldTime {
			continue
		}
		if d.Label != "" {
			return d.Label
		}
		return d.Name
	}
	return ""
}
