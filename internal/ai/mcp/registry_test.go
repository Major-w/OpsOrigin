package mcp

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolDefinition{
		Name:        "echo",
		Description: "echo tool",
		Handler: func(ctx context.Context, in map[string]any) (any, error) {
			return in["msg"], nil
		},
	})

	def, ok := r.Get("echo")
	if !ok {
		t.Fatal("expected echo tool to be registered")
	}
	if def.Handler == nil {
		t.Fatal("handler should not be nil via Get")
	}
	// List 必须剥离 Handler(避免暴露内部函数)。
	list := r.List()
	if len(list) != 1 || list[0].Handler != nil {
		t.Fatalf("List must strip Handler: %#v", list)
	}
}

func TestRegistryExecute(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolDefinition{
		Name: "add",
		Handler: func(ctx context.Context, in map[string]any) (any, error) {
			a, _ := in["a"].(float64)
			b, _ := in["b"].(float64)
			return a + b, nil
		},
	})

	got, err := r.Execute(context.Background(), "add", map[string]any{"a": 2.0, "b": 3.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(float64) != 5.0 {
		t.Fatalf("expected 5, got %v", got)
	}

	// 不存在的工具返回错误。
	if _, err := r.Execute(context.Background(), "nope", nil); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestRegistryExecuteHandlerError(t *testing.T) {
	r := NewRegistry()
	boom := errors.New("boom")
	r.Register(ToolDefinition{
		Name:    "fail",
		Handler: func(ctx context.Context, in map[string]any) (any, error) { return nil, boom },
	})
	if _, err := r.Execute(context.Background(), "fail", nil); !errors.Is(err, boom) {
		t.Fatalf("expected wrapped boom error, got %v", err)
	}
}
