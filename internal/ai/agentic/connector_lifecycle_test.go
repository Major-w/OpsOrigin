package agentic

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// makeCustomTool 构造一个测试用自定义工具(默认 http GET, 启用)。
func makeCustomTool(name, typ, method, url, cmd string) *CustomTool {
	return &CustomTool{
		ID:          "ct-" + name,
		TenantID:    "default",
		Name:        name,
		Type:        typ,
		Enabled:     true,
		HTTPMethod:  method,
		HTTPURL:     url,
		CLICommand:  cmd,
		Description: "test connector " + name,
	}
}

// TestConnectorLifecyclePolicySeeded 验证注册时按类型自动播种安全默认审批策略:
// 只读 http(GET) → PolicyAllow; 写 http(POST) → PolicyRBAC; 破坏性 http(DELETE) → PolicyApproval;
// cli → PolicyApproval。
func TestConnectorLifecyclePolicySeeded(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()

	getTool := makeCustomTool("c_get", "http", "GET", "https://api.example.com/x", "")
	if err := e.CreateCustomTool(ctx, getTool); err != nil {
		t.Fatalf("create GET tool failed: %v", err)
	}
	if pol, ok := e.policyOf("c_get"); !ok || pol != PolicyAllow {
		t.Fatalf("http GET should seed PolicyAllow, got %v/%v", pol, ok)
	}

	postTool := makeCustomTool("c_post", "http", "POST", "https://api.example.com/x", "")
	if err := e.CreateCustomTool(ctx, postTool); err != nil {
		t.Fatalf("create POST tool failed: %v", err)
	}
	if pol, ok := e.policyOf("c_post"); !ok || pol != PolicyRBAC {
		t.Fatalf("http POST should seed PolicyRBAC, got %v/%v", pol, ok)
	}

	delTool := makeCustomTool("c_del", "http", "DELETE", "https://api.example.com/x", "")
	if err := e.CreateCustomTool(ctx, delTool); err != nil {
		t.Fatalf("create DELETE tool failed: %v", err)
	}
	if pol, ok := e.policyOf("c_del"); !ok || pol != PolicyApproval {
		t.Fatalf("http DELETE should seed PolicyApproval, got %v/%v", pol, ok)
	}

	cliTool := makeCustomTool("c_cli", "cli", "", "", "echo hi")
	if err := e.CreateCustomTool(ctx, cliTool); err != nil {
		t.Fatalf("create CLI tool failed: %v", err)
	}
	if pol, ok := e.policyOf("c_cli"); !ok || pol != PolicyApproval {
		t.Fatalf("cli should seed PolicyApproval, got %v/%v", pol, ok)
	}
}

// TestConnectorLifecycleValidationRejects 验证能力校验钩子 fail-closed 拒绝非法连接器:
// http 缺 URL、cli 缺命令、非法方法、未知类型均被拒, 且不会残留到内存/registry。
func TestConnectorLifecycleValidationRejects(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()

	cases := []struct {
		name string
		tool *CustomTool
	}{
		{"http without url", makeCustomTool("bad1", "http", "GET", "", "")},
		{"cli without command", makeCustomTool("bad2", "cli", "", "", "")},
		{"http invalid method", makeCustomTool("bad3", "http", "FOO", "https://x.com", "")},
		{"unknown type", makeCustomTool("bad4", "rpc", "", "", "x")},
		{"http non-http url", makeCustomTool("bad5", "http", "GET", "ftp://x.com", "")},
	}
	for _, tc := range cases {
		if err := e.CreateCustomTool(ctx, tc.tool); err == nil {
			t.Fatalf("case %s: expected rejection, got nil", tc.name)
		}
		// 不应残留到内存 map / registry
		e.mu.RLock()
		_, inMem := e.customTools[tc.tool.ID]
		e.mu.RUnlock()
		if inMem {
			t.Fatalf("case %s: rejected tool must not remain in memory", tc.name)
		}
	}
}

// TestConnectorLifecycleUnregisterCleansPolicy 验证反注册时仅清理自动播种策略、不误删人工覆盖。
func TestConnectorLifecycleUnregisterCleansPolicy(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()

	// 注册一个 http GET(自动播种 PolicyAllow)
	getTool := makeCustomTool("auto_get", "http", "GET", "https://api.example.com/x", "")
	if err := e.CreateCustomTool(ctx, getTool); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, ok := e.policyOf("auto_get"); !ok {
		t.Fatalf("expected auto-seeded policy present")
	}
	// 人工覆盖同名为 PolicyApproval
	e.SetToolPolicy("auto_get", PolicyApproval)

	// 反注册: 自动播种标记已被人工覆盖清除(因 policyOf 命中人工值), 策略应保留人工值。
	if err := e.DeleteCustomTool(ctx, getTool.ID, "default"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if pol, ok := e.policyOf("auto_get"); !ok || pol != PolicyApproval {
		t.Fatalf("manual override must survive unregister, got %v/%v", pol, ok)
	}

	// 全新自动播种的连接器: 反注册后策略被清理。
	fresh := makeCustomTool("auto_fresh", "http", "GET", "https://api.example.com/x", "")
	if err := e.CreateCustomTool(ctx, fresh); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if _, ok := e.policyOf("auto_fresh"); !ok {
		t.Fatalf("expected auto-seeded policy present before delete")
	}
	if err := e.DeleteCustomTool(ctx, fresh.ID, "default"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, ok := e.policyOf("auto_fresh"); ok {
		t.Fatalf("auto-seeded policy must be cleaned on unregister")
	}
}

// TestConnectorLifecycleCustomHookOrder 验证自定义生命周期钩子按注册顺序触发,
// 且 OnRegister 在创建时、OnUnregister 在删除时各触发一次。
func TestConnectorLifecycleCustomHookOrder(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()

	var events []string
	e.RegisterConnectorLifecycleHook(LifecycleHook{
		OnRegister: func(_ context.Context, c ConnectorInfo) error {
			events = append(events, "reg:"+c.Name)
			return nil
		},
		OnUnregister: func(_ context.Context, c ConnectorInfo) error {
			events = append(events, "unreg:"+c.Name)
			return nil
		},
	})

	tool := makeCustomTool("hooked", "http", "GET", "https://api.example.com/x", "")
	if err := e.CreateCustomTool(ctx, tool); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if len(events) != 1 || events[0] != "reg:hooked" {
		t.Fatalf("expected single OnRegister, got %#v", events)
	}
	if err := e.DeleteCustomTool(ctx, tool.ID, "default"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if len(events) != 2 || events[1] != "unreg:hooked" {
		t.Fatalf("expected OnUnregister after delete, got %#v", events)
	}
}

// TestConnectorLifecycleRegisterHookBlocks 验证注册钩子返回 error 时连接器被拒绝(fail-closed),
// 并给出可读错误; 与内置校验/播种链路互不误伤(不残留播种策略)。
func TestConnectorLifecycleRegisterHookBlocks(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	e.RegisterConnectorLifecycleHook(LifecycleHook{
		OnRegister: func(_ context.Context, c ConnectorInfo) error {
			if strings.Contains(c.Name, "blocked") {
				return fmt.Errorf("connector %q is blocklisted", c.Name)
			}
			return nil
		},
	})

	tool := makeCustomTool("blocked_tool", "http", "GET", "https://api.example.com/x", "")
	err := e.CreateCustomTool(ctx, tool)
	if err == nil || !strings.Contains(err.Error(), "blocklisted") {
		t.Fatalf("expected blocklist rejection, got %v", err)
	}
	if _, ok := e.policyOf("blocked_tool"); ok {
		t.Fatalf("rejected connector must not leave a seeded policy")
	}
	// 正常连接器仍可通过
	ok := makeCustomTool("allow_tool", "http", "GET", "https://api.example.com/x", "")
	if err := e.CreateCustomTool(ctx, ok); err != nil {
		t.Fatalf("allowed tool should pass, got %v", err)
	}
}
