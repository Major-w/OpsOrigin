package agentic

import (
	"context"
	"testing"
)

// TestDryRunGateInjectsDryRun 验证 dry-run 模式下安全预览工具被注入 dry_run=true。
func TestDryRunGateInjectsDryRun(t *testing.T) {
	e := &Engine{runMode: RunModeDryRun}
	args := map[string]any{"host": "h1"}
	ok, got, msg := e.dryRunGate(context.Background(), "", "script_run", args)
	if !ok {
		t.Fatalf("script_run should be allowed in dry-run, got deny: %s", msg)
	}
	if got["dry_run"] != true {
		t.Fatalf("expected dry_run=true injected, got %v", got["dry_run"])
	}
}

// TestDryRunGateRejectsUnsafeTool 验证 dry-run 模式下无法安全预览的写操作被拒绝(不碰生产)。
func TestDryRunGateRejectsUnsafeTool(t *testing.T) {
	e := &Engine{runMode: RunModeDryRun}
	ok, _, msg := e.dryRunGate(context.Background(), "", "ticket_create", map[string]any{})
	if ok {
		t.Fatalf("ticket_create must be rejected in dry-run mode")
	}
	if msg == "" {
		t.Fatalf("rejection must carry a descriptive message")
	}
}

// TestDryRunGatePassthroughStandard 验证 standard 模式下 dryRunGate 不改行为(放行且不注入)。
func TestDryRunGatePassthroughStandard(t *testing.T) {
	e := &Engine{runMode: RunModeStandard}
	args := map[string]any{}
	ok, got, _ := e.dryRunGate(context.Background(), "", "ticket_create", args)
	if !ok {
		t.Fatalf("standard mode must allow ticket_create")
	}
	if _, exists := got["dry_run"]; exists {
		t.Fatalf("standard mode must not inject dry_run")
	}
}

// TestAutonomousBypassesControlledGate 验证 autonomous 模式下自造工具免二次审批(自治优先)。
func TestAutonomousBypassesControlledGate(t *testing.T) {
	e := &Engine{
		runMode:        RunModeAutonomous,
		controlledMode: true,
		customTools:    map[string]*CustomTool{"my_tool": {Name: "my_tool"}},
	}
	// 无角色上下文也直接放行(自治优先于受控闸门, 但仍需 RBAC 特权校验在 rbacGate 处)。
	ok, _, msg := e.controlledGate(context.Background(), "", "my_tool", nil)
	if !ok {
		t.Fatalf("autonomous mode must bypass controlled gate for custom tool, got: %s", msg)
	}
}

// TestControlledGateStillEnforcedInStandard 验证 standard 模式下受控闸门仍生效(回归)。
func TestControlledGateStillEnforcedInStandard(t *testing.T) {
	e := &Engine{
		runMode:        RunModeStandard,
		controlledMode: true,
		customTools:    map[string]*CustomTool{"my_tool": {Name: "my_tool"}},
	}
	// standard + 受控开启: 自造工具需审批(无角色被拒)。
	ok, _, _ := e.controlledGate(context.Background(), "", "my_tool", nil)
	if ok {
		t.Fatalf("standard+controlled must require approval for custom tool")
	}
}

// TestDefaultRunModeIsStandard 验证引擎默认运行模式为 standard(零值即 Standard, 不改变既有行为)。
func TestDefaultRunModeIsStandard(t *testing.T) {
	e := &Engine{}
	if e.runMode != RunModeStandard {
		t.Fatalf("expected default RunModeStandard, got %v", e.runMode)
	}
}
