package agentic

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/llm"
)

// FinetuneTask 一个反馈驱动微调任务(L3): 引用 /ai/dataset 导出的训练数据集,
// 对基座模型执行领域微调, 产物注册进模型池供 Agent 选用。
//
// 执行链: 训练集导出(P3-1) -> 微调任务 -> 产物模型注册进模型池(经 AddProvider,
// 与 P3-2 热更新闭环) -> Agent 选微调模型 -> 新反馈回流数据集。
//
// 存储为进程内 map(与 agents/plans 同策略); 多副本部署需落 PG 时按 outbox 模式扩展。
type FinetuneTask struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
	// DatasetRef 训练数据集引用(如 "latest" = 当前 /ai/dataset 全量导出)。
	DatasetRef string `json:"dataset_ref,omitempty"`
	// BaseModel 基座模型(如 deepseek-chat / qwen-max, 需在模型池内)。
	BaseModel string `json:"base_model"`
	// Method 微调方式: loRA(推荐) / full / api(平台托管微调 API)。
	Method string `json:"method"`
	// Params 训练参数(epochs / learning_rate / batch_size 等, 透传给微调后端)。
	Params map[string]any `json:"params,omitempty"`
	// Status pending(排队) -> training(执行中) -> succeeded / failed。
	Status string `json:"status"`
	// ResultModel 微调产物模型名(成功后注册进模型池的 Model)。
	ResultModel string `json:"result_model,omitempty"`
	// ResultProviderName 产物注册进模型池的 provider 名(如 finetuned-<id>), 前端可据此
	// 在模型池/Agent 表单选择。
	ResultProviderName string     `json:"result_provider_name,omitempty"`
	Error              string     `json:"error,omitempty"`
	CreatedBy          string     `json:"created_by,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
}

// FinetuneRunnerFunc 微调执行回调(由 cmd/core 注入真实微调后端; nil=未配置微调后端)。
// 返回微调产物模型名; 错误时任务置 failed。
// 未注入时任务创建后停留在 pending, 并在 Error 中提示"未配置微调后端"。
type FinetuneRunnerFunc func(ctx context.Context, t *FinetuneTask) (resultModel string, err error)

// finetuneStatusPending/Training/Succeeded/Failed 微调任务状态常量。
const (
	FinetuneStatusPending   = "pending"
	FinetuneStatusTraining  = "training"
	FinetuneStatusSucceeded = "succeeded"
	FinetuneStatusFailed    = "failed"
)

// finetuneMock 微调后端 mock 开关(测试/演示用, 非真实训练):
// 开启时即使未注入 FinetuneRunnerFunc, 创建任务也会走 mock 执行(1s 后成功, 产物为
// "<base>-ft")。真实环境应注入 FinetuneRunnerFunc, mock 仅用于闭环验证。
var finetuneMock bool

// SetFinetuneMock 启用/关闭微调 mock 执行器(仅测试与演示)。
func SetFinetuneMock(on bool) { finetuneMock = on }

// CreateFinetuneTask 创建微调任务并(若配置了执行器)异步启动训练。
// 任务创建后立即返回; 执行结果经任务状态 + 模型池可见。
func (e *Engine) CreateFinetuneTask(tenantID, name, datasetRef, baseModel, method string, params map[string]any, createdBy string) (*FinetuneTask, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("task name required")
	}
	if strings.TrimSpace(baseModel) == "" {
		return nil, fmt.Errorf("base model required")
	}
	t := &FinetuneTask{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		Name:       name,
		DatasetRef: orDefault(datasetRef, "latest"),
		BaseModel:  baseModel,
		Method:     orDefault(method, "loRA"),
		Params:     params,
		Status:     FinetuneStatusPending,
		CreatedBy:  createdBy,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	e.mu.Lock()
	if e.finetuneTasks == nil {
		e.finetuneTasks = map[string]*FinetuneTask{}
	}
	e.finetuneTasks[t.ID] = t
	runner := e.FinetuneRunnerFunc
	mock := finetuneMock
	e.mu.Unlock()

	if runner != nil {
		go e.executeFinetune(t.ID, runner)
	} else if mock {
		go e.executeFinetuneMock(t.ID)
	} else {
		t.Error = "未配置微调后端 (FinetuneRunnerFunc 未注入); 任务停留 pending"
		slog.Warn("finetune task created without runner", "task", t.ID)
	}
	return t, nil
}

// executeFinetune 异步执行真实微调: pending -> training -> succeeded/failed,
// 成功后把产物模型注册进模型池(AddProvider), 与 P3-2 热更新闭环。
func (e *Engine) executeFinetune(id string, runner FinetuneRunnerFunc) {
	t := e.updateFinetuneStatus(id, FinetuneStatusTraining, "", "", "")
	if t == nil {
		return
	}
	result, err := runner(context.Background(), t)
	if err != nil {
		e.updateFinetuneStatus(id, FinetuneStatusFailed, "", "", err.Error())
		slog.Warn("finetune failed", "task", id, "err", err)
		return
	}
	e.completeFinetune(id, result)
}

// executeFinetuneMock 演示/测试用 mock 微调(1s 后成功, 产物 "<base>-ft")。
func (e *Engine) executeFinetuneMock(id string) {
	time.Sleep(time.Second)
	e.mu.RLock()
	t := e.finetuneTasks[id]
	base := ""
	if t != nil {
		base = t.BaseModel
	}
	e.mu.RUnlock()
	e.completeFinetune(id, base+"-ft")
}

// completeFinetune 任务置 succeeded 并把产物模型注册进模型池。
// provider 名为 finetuned-<id前8>, 基座 URL/Key 继承自模型池中匹配 BaseModel 的 provider。
func (e *Engine) completeFinetune(id, resultModel string) {
	e.mu.RLock()
	t := e.finetuneTasks[id]
	e.mu.RUnlock()
	if t == nil {
		return
	}
	providerName := fmt.Sprintf("finetuned-%s", id[:8])
	if e.router != nil {
		baseCfg := e.findProviderConfigByModel(t.BaseModel)
		baseCfg.Model = resultModel
		e.router.AddProvider(providerName, baseCfg, 90, []string{"text"}, "public", "", false)
	} else {
		slog.Warn("finetune completed but router not set; model not registered", "task", id)
	}
	e.updateFinetuneStatus(id, FinetuneStatusSucceeded, resultModel, providerName, "")
	slog.Info("finetune succeeded", "task", id, "model", resultModel, "provider", providerName)
}

// findProviderConfigByModel 从模型池中按 Model 名找 provider 配置(继承其 BaseURL/APIKey/Timeout)。
// 找不到时返回空配置(AddProvider 内部会补默认模型名, 但 URL 为空时 llm.NewClient 用默认端点)。
func (e *Engine) findProviderConfigByModel(model string) llm.Config {
	if e.router == nil {
		return llm.Config{Model: model}
	}
	for _, p := range e.router.providers {
		if p.cfg.Model == model {
			return p.cfg
		}
	}
	// 兜底: 用第一个健康 provider 的端点
	for _, p := range e.router.providers {
		if p.cfg.BaseURL != "" {
			cfg := p.cfg
			cfg.Model = model
			return cfg
		}
	}
	return llm.Config{Model: model}
}

// updateFinetuneStatus 更新任务状态字段并返回最新任务(不存在返回 nil)。
func (e *Engine) updateFinetuneStatus(id, status, resultModel, providerName, errMsg string) *FinetuneTask {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.finetuneTasks[id]
	if t == nil {
		return nil
	}
	t.Status = status
	t.ResultModel = resultModel
	t.ResultProviderName = providerName
	t.Error = errMsg
	t.UpdatedAt = time.Now()
	if status == FinetuneStatusSucceeded || status == FinetuneStatusFailed {
		now := time.Now()
		t.FinishedAt = &now
	}
	return t
}

// ListFinetuneTasks 返回该租户的微调任务(按创建时间倒序)。
func (e *Engine) ListFinetuneTasks(tenantID string) []*FinetuneTask {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []*FinetuneTask
	for _, t := range e.finetuneTasks {
		if t.TenantID != tenantID {
			continue
		}
		cp := *t
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// GetFinetuneTask 按 ID 查询微调任务。
func (e *Engine) GetFinetuneTask(id string) (*FinetuneTask, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.finetuneTasks[id]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}
