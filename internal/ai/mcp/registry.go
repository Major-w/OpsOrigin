package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

type ParamSchema struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required"`
	Default     any      `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]ParamSchema `json:"parameters"`
	Handler     ToolHandler            `json:"-"`
}

type ToolHandler func(ctx context.Context, input map[string]any) (any, error)

type Registry struct {
	mu    sync.RWMutex
	tools map[string]ToolDefinition
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]ToolDefinition),
	}
}

func (r *Registry) Register(tool ToolDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = tool
	slog.Info("mcp tool registered", "name", tool.Name)
}

// Unregister 从注册表移除指定工具(自定义工具禁用/删除时调用)。
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; ok {
		delete(r.tools, name)
		slog.Info("mcp tool unregistered", "name", name)
	}
}

func (r *Registry) List() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		handlerless := t
		handlerless.Handler = nil
		tools = append(tools, handlerless)
	}
	return tools
}

func (r *Registry) Get(name string) (ToolDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Execute(ctx context.Context, name string, input map[string]any) (any, error) {
	tool, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool %s not found", name)
	}
	if tool.Handler == nil {
		return nil, fmt.Errorf("tool %s has no handler", name)
	}

	inputJSON, _ := json.Marshal(input)
	slog.Info("executing mcp tool", "name", name, "input", string(inputJSON))

	result, err := tool.Handler(ctx, input)
	if err != nil {
		slog.Error("mcp tool execution failed", "name", name, "error", err)
		return nil, fmt.Errorf("tool %s failed: %w", name, err)
	}

	return result, nil
}
