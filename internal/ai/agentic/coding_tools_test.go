package agentic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
)

func approveAll(ctx context.Context, tool, agentID string, args map[string]any, summary string) (bool, string) {
	return true, ""
}

func TestResolveSafePathEscapes(t *testing.T) {
	ts := NewMCPToolSet(nil)
	dir := t.TempDir()
	ts.SetCodingSandbox(dir)

	// ../../ 穿趆必须被拒绝
	if _, err := ts.resolveSafePath(filepath.Join(dir, "..", "..", "etc", "passwd")); err == nil {
		t.Fatal("expected escape error for ../../ path")
	}
	// 指向沙箱外的绝对路径必须被拒绝
	if _, err := ts.resolveSafePath("/etc/passwd"); err == nil {
		t.Fatal("expected escape error for absolute path outside sandbox")
	}
	// 沙箱内的相对路径应当通过
	p, err := ts.resolveSafePath("sub/file.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(p, dir) {
		t.Fatalf("resolved path not inside sandbox: %s", p)
	}
}

func TestCodingReadFileInSandbox(t *testing.T) {
	ts := NewMCPToolSet(nil)
	dir := t.TempDir()
	ts.SetCodingSandbox(dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ts.codingReadFile(context.Background(), map[string]any{"path": "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["status"] != "ok" {
		t.Fatalf("status=%v", m["status"])
	}
	if m["content"] != "hello\nworld" {
		t.Fatalf("content=%v", m["content"])
	}
}

func TestCodingReadFileEscapes(t *testing.T) {
	ts := NewMCPToolSet(nil)
	ts.SetCodingSandbox(t.TempDir())
	res, _ := ts.codingReadFile(context.Background(), map[string]any{"path": "../../etc/passwd"})
	m := res.(map[string]any)
	if m["status"] != "error" {
		t.Fatalf("expected error for escape, got %v", m["status"])
	}
}

func TestCodingEditFileRequiresApproval(t *testing.T) {
	ts := NewMCPToolSet(nil)
	dir := t.TempDir()
	ts.SetCodingSandbox(dir)
	fp := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(fp, []byte("foo bar baz"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 未注入审批器: 写操作必须 fail-closed 拒绝
	res, _ := ts.codingEditFile(context.Background(), map[string]any{"path": "f.txt", "old_string": "bar", "new_string": "X"})
	m := res.(map[string]any)
	if m["status"] != "rejected" {
		t.Fatalf("expected rejected (fail-closed), got %v", m["status"])
	}

	// 注入审批器后: 写操作生效
	ts.SetCodingApproval(approveAll)
	res, _ = ts.codingEditFile(context.Background(), map[string]any{"path": "f.txt", "old_string": "bar", "new_string": "X"})
	m = res.(map[string]any)
	if m["status"] != "ok" {
		t.Fatalf("expected ok, got %v", m["status"])
	}
	if m["replacements"] != 1 {
		t.Fatalf("replacements=%v", m["replacements"])
	}
	data, _ := os.ReadFile(fp)
	if string(data) != "foo X baz" {
		t.Fatalf("file content=%q", string(data))
	}
}

func TestCodingEditFileAmbiguous(t *testing.T) {
	ts := NewMCPToolSet(nil)
	dir := t.TempDir()
	ts.SetCodingSandbox(dir)
	fp := filepath.Join(dir, "f.txt")
	os.WriteFile(fp, []byte("bar bar bar"), 0o644)
	ts.SetCodingApproval(approveAll)
	// expected_replacements=1 但实际 3 次 -> 应报错不修改
	res, _ := ts.codingEditFile(context.Background(), map[string]any{"path": "f.txt", "old_string": "bar", "new_string": "X", "expected_replacements": 1})
	m := res.(map[string]any)
	if m["status"] != "error" {
		t.Fatalf("expected error for ambiguous edit, got %v", m["status"])
	}
	data, _ := os.ReadFile(fp)
	if string(data) != "bar bar bar" {
		t.Fatalf("file should be unchanged, got %q", string(data))
	}
}

func TestCodingRunShellDangerousBlocked(t *testing.T) {
	ts := NewMCPToolSet(nil)
	ts.SetCodingSandbox(t.TempDir())
	res, _ := ts.codingRunShell(context.Background(), map[string]any{"command": "rm -rf /"})
	m := res.(map[string]any)
	if m["status"] != "error" {
		t.Fatalf("expected error, got %v", m["status"])
	}
	if !strings.Contains(fmt.Sprint(m["error"]), "dangerous") {
		t.Fatalf("error should mention dangerous: %v", m["error"])
	}
}

func TestCodingRunShellApproval(t *testing.T) {
	ts := NewMCPToolSet(nil)
	ts.SetCodingSandbox(t.TempDir())
	ts.SetCodingApproval(approveAll)
	res, _ := ts.codingRunShell(context.Background(), map[string]any{"command": "echo hi"})
	m := res.(map[string]any)
	if m["status"] != "ok" {
		t.Fatalf("status=%v", m["status"])
	}
	if !strings.Contains(fmt.Sprint(m["output"]), "hi") {
		t.Fatalf("output=%v", m["output"])
	}
}

func TestCodingApplyPatchEscapes(t *testing.T) {
	ts := NewMCPToolSet(nil)
	ts.SetCodingSandbox(t.TempDir())
	patch := "--- a/x\n+++ b/../../etc/passwd\n@@ -1 +1 @@\n- a\n+ b\n"
	res, _ := ts.codingApplyPatch(context.Background(), map[string]any{"patch": patch})
	m := res.(map[string]any)
	if m["status"] != "error" {
		t.Fatalf("expected error for escape patch, got %v", m["status"])
	}
}

func TestRegisterCodingTools(t *testing.T) {
	ts := NewMCPToolSet(nil)
	ts.SetCodingSandbox(t.TempDir())
	reg := mcp.NewRegistry()
	ts.Register(reg)

	byName := map[string]mcp.ToolDefinition{}
	for _, td := range reg.List() {
		byName[td.Name] = td
	}
	for _, want := range []string{
		"coding_read_file", "coding_list_files", "coding_grep_files",
		"coding_edit_file", "coding_write_file", "coding_apply_patch", "coding_run_shell",
	} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("coding tool not registered: %s", want)
		}
	}
	// 风险分级: 读类=read, 写类=write, 执行类=critical
	if byName["coding_read_file"].Risk != "read" {
		t.Fatalf("coding_read_file risk=%q", byName["coding_read_file"].Risk)
	}
	if byName["coding_edit_file"].Risk != "write" {
		t.Fatalf("coding_edit_file risk=%q", byName["coding_edit_file"].Risk)
	}
	if byName["coding_run_shell"].Risk != "critical" {
		t.Fatalf("coding_run_shell risk=%q", byName["coding_run_shell"].Risk)
	}
}

// #3: 即使已批准, 写/编辑受保护路径(.env/密钥)也必须被拒绝。
func TestCodingEditProtectedFileBlocked(t *testing.T) {
	ts := NewMCPToolSet(nil)
	dir := t.TempDir()
	ts.SetCodingSandbox(dir)
	ts.SetCodingApproval(approveAll)
	fp := filepath.Join(dir, ".env")
	if err := os.WriteFile(fp, []byte("SECRET=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := ts.codingEditFile(context.Background(), map[string]any{"path": ".env", "old_string": "SECRET=1", "new_string": "SECRET=2"})
	m := res.(map[string]any)
	if m["status"] != "error" {
		t.Fatalf("expected error for protected .env edit, got %v", m["status"])
	}
	if !strings.Contains(fmt.Sprint(m["error"]), "protected") {
		t.Fatalf("error should mention protected: %v", m["error"])
	}
	data, _ := os.ReadFile(fp)
	if string(data) != "SECRET=1\n" {
		t.Fatalf("protected file must be unchanged, got %q", string(data))
	}
}

// #3: 写操作(.env)也必须被保护拦截。
func TestCodingWriteProtectedFileBlocked(t *testing.T) {
	ts := NewMCPToolSet(nil)
	dir := t.TempDir()
	ts.SetCodingSandbox(dir)
	ts.SetCodingApproval(approveAll)
	res, _ := ts.codingWriteFile(context.Background(), map[string]any{"path": ".env.prod", "content": "TOKEN=x"})
	m := res.(map[string]any)
	if m["status"] != "error" {
		t.Fatalf("expected error for protected .env.prod write, got %v", m["status"])
	}
}

// #4: 补丁 rename to 指向沙箱外必须被拒绝(此前只校验 +++ b/ 行)。
func TestCodingApplyPatchRenameEscapes(t *testing.T) {
	ts := NewMCPToolSet(nil)
	ts.SetCodingSandbox(t.TempDir())
	patch := "diff --git a/inside.txt b/outside\nrename from inside.txt\nrename to ../../etc/passwd\n"
	res, _ := ts.codingApplyPatch(context.Background(), map[string]any{"patch": patch})
	m := res.(map[string]any)
	if m["status"] != "error" {
		t.Fatalf("expected error for rename escape patch, got %v", m["status"])
	}
}

// #4: 补丁无法干净应用(conflict)时, 审批不应被触发, 直接返回错误。
func TestCodingApplyPatchDryRunConflictNoApproval(t *testing.T) {
	ts := NewMCPToolSet(nil)
	dir := t.TempDir()
	ts.SetCodingSandbox(dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	ts.SetCodingApproval(func(ctx context.Context, tool, agentID string, args map[string]any, summary string) (bool, string) {
		called = true
		return true, ""
	})
	// 上下文不匹配, git apply --check 必然失败
	patch := "--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-lineX\n+lineY\n"
	res, _ := ts.codingApplyPatch(context.Background(), map[string]any{"patch": patch})
	m := res.(map[string]any)
	if m["status"] != "error" {
		t.Fatalf("expected error for conflicting patch, got %v", m["status"])
	}
	if called {
		t.Fatal("approval must NOT be requested for a patch that cannot apply")
	}
}

// #4: 合法补丁在批准后应成功应用。
func TestCodingApplyPatchValid(t *testing.T) {
	ts := NewMCPToolSet(nil)
	dir := t.TempDir()
	ts.SetCodingSandbox(dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("foo bar baz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts.SetCodingApproval(approveAll)
	patch := "--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-foo bar baz\n+foo X baz\n"
	res, _ := ts.codingApplyPatch(context.Background(), map[string]any{"patch": patch})
	m := res.(map[string]any)
	if m["status"] != "ok" {
		t.Fatalf("expected ok, got %v", m["status"])
	}
	data, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(data) != "foo X baz\n" {
		t.Fatalf("patched content=%q", string(data))
	}
}

// #4: extractPatchPaths 正确提取 +++/---/rename 路径并剥离时间戳。
func TestExtractPatchPaths(t *testing.T) {
	patch := "diff --git a/old.go b/new.go\n--- a/old.go\t2024-01-01 00:00:00\n+++ b/new.go\t2024-01-01 00:00:00\n" +
		"rename from a/legacy.go\nrename to b/modern.go\n"
	paths, err := extractPatchPaths(patch)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(paths, ",")
	for _, want := range []string{"old.go", "new.go", "legacy.go", "modern.go"} {
		if !strings.Contains(got, want) {
			t.Fatalf("extractPatchPaths=%q missing %q", got, want)
		}
	}
	// 时间戳不应混入路径
	if strings.Contains(got, "2024") {
		t.Fatalf("timestamp leaked into path: %q", got)
	}
}
