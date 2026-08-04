package discovery_test

import (
	"context"
	"testing"

	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/discovery/plugins"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// fakeRunner 是一个测试用 ScriptRunner：忽略脚本，直接回吐预设的 JSON 行输出。
// 用于验证各插件的解析路径（parseLines + CIType 映射），不依赖真实 Agent。
type fakeRunner struct {
	out  string
	code int
	err  error
}

func (f *fakeRunner) RunScriptOnAgent(_ context.Context, _, _, _ string, _ map[string]string, _ int, _ string) (string, int, error) {
	return f.out, f.code, f.err
}

func newTask(taskType, target, agentID string, cfg map[string]any) *model.DiscoveryTask {
	return &model.DiscoveryTask{
		TenantID: "default",
		Type:     taskType,
		Target:   target,
		Config:   cfg,
	}
}

func TestAgentIDOf(t *testing.T) {
	task := newTask("server", "1.2.3.4", "agent-9", map[string]any{"agent_id": "agent-9"})
	if got := discovery.AgentIDOf(task); got != "agent-9" {
		t.Fatalf("AgentIDOf = %q, want agent-9", got)
	}
	if got := discovery.CredOf(task, "community"); got != "" {
		t.Fatalf("CredOf(community) = %q, want empty", got)
	}
}

func TestRegistry(t *testing.T) {
	reg := plugins.NewRegistry()
	if len(reg.List()) == 0 {
		t.Fatal("registry should contain builtin discoverers")
	}
	if _, ok := reg.Lookup("server"); !ok {
		t.Fatal("server discoverer should be registered")
	}
	// 未知类型应查不到
	if _, ok := reg.Lookup("nope"); ok {
		t.Fatal("unknown type should not be registered")
	}
}

// 各插件解析路径验证：fakeRunner 回吐一行 JSON，断言 Item 字段映射正确。
func TestPluginParse(t *testing.T) {
	cases := []struct {
		name    string
		task    *model.DiscoveryTask
		out     string
		wantCI  string
		wantIP  string
		wantKey string
	}{
		{
			name:   "server",
			task:   newTask("server", "10.0.0.5", "a1", map[string]any{"agent_id": "a1"}),
			out:    `{"name":"node-1","ip":"10.0.0.5","attrs":{"os":"linux","vendor":"dell"}}`,
			wantCI: "server", wantIP: "10.0.0.5", wantKey: "vendor",
		},
		{
			name:   "network",
			task:   newTask("snmp", "10.0.0.1", "a1", map[string]any{"agent_id": "a1", "community": "public"}),
			out:    `{"name":"core-sw","ip":"10.0.0.1","attrs":{"vendor":"cisco","ports":48}}`,
			wantCI: "switch", wantIP: "10.0.0.1", wantKey: "vendor",
		},
		{
			name:   "database",
			task:   newTask("database", "10.0.0.2", "a1", map[string]any{"agent_id": "a1", "engine": "mysql"}),
			out:    `{"name":"mysql-10.0.0.2:3306","ip":"10.0.0.2","attrs":{"engine":"mysql","version":"8.0.36"}}`,
			wantCI: "database", wantIP: "10.0.0.2", wantKey: "version",
		},
		{
			name:   "middleware",
			task:   newTask("middleware", "10.0.0.3", "a1", map[string]any{"agent_id": "a1", "mtype": "redis"}),
			out:    `{"name":"redis-10.0.0.3:6379","ip":"10.0.0.3","attrs":{"middleware_type":"redis","version":"7.2"}}`,
			wantCI: "middleware", wantIP: "10.0.0.3", wantKey: "middleware_type",
		},
		{
			name:   "application",
			task:   newTask("application", "10.0.0.4", "a1", map[string]any{"agent_id": "a1", "ports": "8080"}),
			out:    `{"name":"unknown-10.0.0.4:8080","ip":"10.0.0.4","attrs":{"app_type":"unknown","port":8080,"http_code":"200"}}`,
			wantCI: "app", wantIP: "10.0.0.4", wantKey: "http_code",
		},
	}
	reg := plugins.NewRegistry()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, ok := reg.Lookup(c.task.Type)
			if !ok {
				t.Fatalf("discoverer for %q not registered", c.task.Type)
			}
			items, err := d.Discover(context.Background(), c.task, &fakeRunner{out: c.out, code: 0})
			if err != nil {
				t.Fatalf("Discover err: %v", err)
			}
			if len(items) != 1 {
				t.Fatalf("got %d items, want 1", len(items))
			}
			it := items[0]
			if it.CIType != c.wantCI {
				t.Errorf("CIType = %q, want %q", it.CIType, c.wantCI)
			}
			if it.IPAddress != c.wantIP {
				t.Errorf("IPAddress = %q, want %q", it.IPAddress, c.wantIP)
			}
			if _, ok := it.Attributes[c.wantKey]; !ok {
				t.Errorf("attributes missing key %q: %v", c.wantKey, it.Attributes)
			}
		})
	}
}

// 容错：单行解析失败不应阻断其余资源。
func TestParseLinesTolerance(t *testing.T) {
	reg := plugins.NewRegistry()
	d, _ := reg.Lookup("server")
	task := newTask("server", "h", "a1", map[string]any{"agent_id": "a1"})
	out := "not-json\n{\"name\":\"ok\",\"ip\":\"1.1.1.1\",\"attrs\":{}}\n\n"
	items, err := d.Discover(context.Background(), task, &fakeRunner{out: out, code: 0})
	if err != nil {
		t.Fatalf("Discover err: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("tolerant parse got %d items, want 1", len(items))
	}
}
