package agentic

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/ai/mcp"
)

// registerCodingTools 注册代码编写类工具(coding_*), 仅在 sandboxRoot 已配置时由 Register 调用。
// 只读类(read)免交互式审批, 写/执行类(write/critical)须经 codingApproval 交互式确认(fail-closed)。
func (ts *MCPToolSet) registerCodingTools(reg *mcp.Registry) {
	reg.Register(mcp.ToolDefinition{
		Name: "coding_read_file",
		Description: "Read a file inside the coding sandbox (repo root). Args: path(string) REQUIRED relative or absolute path within the sandbox; " +
			"start_line(number) optional 1-based; end_line(number) optional 1-based. Returns file content (truncated to 2000 lines). " +
			"Use this to inspect current code before editing.",
		Parameters: map[string]mcp.ParamSchema{
			"path":       {Type: "string", Description: "文件路径, 沙箱内的相对或绝对路径(REQUIRED)", Required: true},
			"start_line": {Type: "number", Description: "起始行(1-based), 可选", Required: false},
			"end_line":   {Type: "number", Description: "结束行(1-based), 可选", Required: false},
		},
		Handler: ts.codingReadFile,
		Risk:    "read",
	})
	reg.Register(mcp.ToolDefinition{
		Name: "coding_list_files",
		Description: "List files/dirs under a directory inside the coding sandbox. Args: path(string) optional dir (default sandbox root); " +
			"recursive(bool) optional default false; max_depth(number) optional default 2; pattern(string) optional glob like *.go. Returns matched relative paths.",
		Parameters: map[string]mcp.ParamSchema{
			"path":      {Type: "string", Description: "目录路径, 默认沙箱根", Required: false},
			"recursive": {Type: "boolean", Description: "是否递归, 默认 false", Required: false},
			"max_depth": {Type: "number", Description: "递归最大深度, 默认 2", Required: false},
			"pattern":   {Type: "string", Description: "glob 匹配, 如 *.go", Required: false},
		},
		Handler: ts.codingListFiles,
		Risk:    "read",
	})
	reg.Register(mcp.ToolDefinition{
		Name: "coding_grep_files",
		Description: "Search file contents inside the coding sandbox (like grep -rn). Args: pattern(string) REQUIRED regex; " +
			"path(string) optional dir (default root); glob(string) optional file filter like *.go; max_matches(number) optional default 50; ignore_case(bool) optional. " +
			"Skips .git/node_modules/vendor. Returns file:line:match lines.",
		Parameters: map[string]mcp.ParamSchema{
			"pattern":     {Type: "string", Description: "正则模式(REQUIRED)", Required: true},
			"path":        {Type: "string", Description: "搜索目录, 默认沙箱根", Required: false},
			"glob":        {Type: "string", Description: "文件过滤 glob, 如 *.go", Required: false},
			"max_matches": {Type: "number", Description: "最大匹配数, 默认 50", Required: false},
			"ignore_case": {Type: "boolean", Description: "忽略大小写, 默认 false", Required: false},
		},
		Handler: ts.codingGrepFiles,
		Risk:    "read",
	})
	reg.Register(mcp.ToolDefinition{
		Name: "coding_edit_file",
		Description: "Edit a file inside the coding sandbox via exact string replacement (precise, like a targeted sed). Args: " +
			"path(string) REQUIRED; old_string(string) REQUIRED exact text to find; new_string(string) REQUIRED replacement; " +
			"expected_replacements(number) optional how many occurrences expected (default 1; mismatch aborts). Requires human approval before applying. " +
			"Prefer a small, unique old_string to avoid ambiguity.",
		Parameters: map[string]mcp.ParamSchema{
			"path":                  {Type: "string", Description: "文件路径(REQUIRED)", Required: true},
			"old_string":            {Type: "string", Description: "待替换的精确原文(REQUIRED)", Required: true},
			"new_string":            {Type: "string", Description: "替换后的文本(REQUIRED)", Required: true},
			"expected_replacements": {Type: "number", Description: "期望替换次数, 默认 1; 与实际不符则报错不修改", Required: false, Default: 1},
		},
		Handler: ts.codingEditFile,
		Risk:    "write",
	})
	reg.Register(mcp.ToolDefinition{
		Name: "coding_write_file",
		Description: "Create or overwrite a file inside the coding sandbox. Args: path(string) REQUIRED; content(string) REQUIRED full file content. " +
			"Requires human approval. Use for new files or full rewrites; prefer coding_edit_file for small changes.",
		Parameters: map[string]mcp.ParamSchema{
			"path":    {Type: "string", Description: "文件路径(REQUIRED)", Required: true},
			"content": {Type: "string", Description: "完整文件内容(REQUIRED)", Required: true},
		},
		Handler: ts.codingWriteFile,
		Risk:    "write",
	})
	reg.Register(mcp.ToolDefinition{
		Name: "coding_apply_patch",
		Description: "Apply a unified diff patch inside the coding sandbox (uses `git apply`). Args: patch(string) REQUIRED unified diff text. " +
			"Requires human approval. Patch paths resolve relative to sandbox root and must not escape it.",
		Parameters: map[string]mcp.ParamSchema{
			"patch": {Type: "string", Description: "unified diff 文本(REQUIRED)", Required: true},
		},
		Handler: ts.codingApplyPatch,
		Risk:    "write",
	})
	reg.Register(mcp.ToolDefinition{
		Name: "coding_run_shell",
		Description: "Run a shell command inside the coding sandbox (cwd = sandbox root). Args: command(string) REQUIRED; timeout(number) optional seconds default 60 (max 600). " +
			"Requires human approval. Dangerous commands (rm -rf /, mkfs, dd, shutdown, reboot, curl|bash, wget|sh, fork bomb) are rejected. Output truncated to 20KB.",
		Parameters: map[string]mcp.ParamSchema{
			"command": {Type: "string", Description: "shell 命令(REQUIRED)", Required: true},
			"timeout": {Type: "number", Description: "超时秒数, 默认 60, 上限 600", Required: false, Default: 60},
		},
		Handler: ts.codingRunShell,
		Risk:    "critical",
	})
}

// resolveSafePath 将用户传入路径解析为沙箱内的真实绝对路径, 并阻止越界:
//   - 相对路径 join 沙箱根, 绝对路径直接采纳;
//   - filepath.Clean 后必须仍以 sandboxRoot 为前缀(防 ../../ 穿趆);
//   - 对真实存在的符号链接做 EvalSymlinks 二次校验, 防 symlink 逃逸。
func (ts *MCPToolSet) resolveSafePath(p string) (string, error) {
	if ts.sandboxRoot == "" {
		return "", fmt.Errorf("coding sandbox not configured")
	}
	rootAbs, err := filepath.Abs(ts.sandboxRoot)
	if err != nil {
		return "", fmt.Errorf("coding sandbox root invalid: %w", err)
	}
	rootAbs = filepath.Clean(rootAbs)

	var joined string
	if filepath.IsAbs(p) {
		joined = filepath.Clean(p)
	} else {
		joined = filepath.Clean(filepath.Join(rootAbs, p))
	}
	// 防 ../ 路径穿趆: 解析后的路径必须仍位于沙箱根内。
	if joined != rootAbs && !strings.HasPrefix(joined, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes sandbox root %q", p, rootAbs)
	}
	// 符号链接逃逸防护: 解析已存在目标的真实路径再次校验。
	if fi, lerr := os.Lstat(joined); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		if real, e2 := filepath.EvalSymlinks(joined); e2 == nil {
			if real != rootAbs && !strings.HasPrefix(real, rootAbs+string(os.PathSeparator)) {
				return "", fmt.Errorf("symlink %q escapes sandbox root", p)
			}
		}
	}
	return joined, nil
}

// requireCodingApproval 代码编写类写/执行操作的统一交互式审批闸门。
// 未注入 codingApproval 时直接拒绝(fail-closed), 确保缺配置也不会静默放行写/执行。
func (ts *MCPToolSet) requireCodingApproval(ctx context.Context, tool string, args map[string]any, summary string) (bool, string) {
	if ts.codingApproval == nil {
		return false, "coding approval not configured: write/exec operations are disabled (fail-closed)"
	}
	return ts.codingApproval(ctx, tool, agentIDFrom(ctx), args, summary)
}

func (ts *MCPToolSet) codingReadFile(ctx context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path required")
	}
	fp, err := ts.resolveSafePath(path)
	if err != nil {
		return map[string]any{"status": "error", "error": err.Error()}, nil
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		return map[string]any{"status": "error", "error": err.Error()}, nil
	}
	lines := strings.Split(string(data), "\n")
	total := len(lines)
	start, _ := toInt(args["start_line"])
	end, _ := toInt(args["end_line"])
	if start > 0 || end > 0 {
		if start < 1 {
			start = 1
		}
		e := end
		if e < 1 || e > total {
			e = total
		}
		if start > e {
			start = e
		}
		lines = lines[start-1 : e]
	}
	if len(lines) > 2000 {
		lines = lines[:2000]
	}
	return map[string]any{
		"status":      "ok",
		"path":        fp,
		"total_lines": total,
		"shown_lines": len(lines),
		"content":     strings.Join(lines, "\n"),
	}, nil
}

func (ts *MCPToolSet) codingListFiles(ctx context.Context, args map[string]any) (any, error) {
	dir, _ := args["dir"].(string)
	base := ts.sandboxRoot
	if dir != "" {
		b, err := ts.resolveSafePath(dir)
		if err != nil {
			return map[string]any{"status": "error", "error": err.Error()}, nil
		}
		base = b
	}
	recursive, _ := args["recursive"].(bool)
	maxDepth := toIntDefault(args["max_depth"], 2)
	if maxDepth < 1 {
		maxDepth = 1
	}
	pattern, _ := args["pattern"].(string)
	var out []string
	walkErr := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != base {
				rel, _ := filepath.Rel(base, p)
				depth := strings.Count(rel, string(os.PathSeparator)) + 1
				if !recursive && depth > 0 {
					return filepath.SkipDir
				}
				if recursive && depth >= maxDepth {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if pattern != "" {
			if m, e := filepath.Match(pattern, d.Name()); e != nil || !m {
				return nil
			}
		}
		out = append(out, relPath(base, p))
		return nil
	})
	if walkErr != nil {
		return map[string]any{"status": "error", "error": walkErr.Error()}, nil
	}
	truncated := false
	if len(out) > 500 {
		out = out[:500]
		truncated = true
	}
	res := map[string]any{"status": "ok", "count": len(out), "paths": out}
	if truncated {
		res["note"] = "truncated to 500 entries"
	}
	return res, nil
}

func (ts *MCPToolSet) codingGrepFiles(ctx context.Context, args map[string]any) (any, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("pattern required")
	}
	dir, _ := args["path"].(string)
	base := ts.sandboxRoot
	if dir != "" {
		b, err := ts.resolveSafePath(dir)
		if err != nil {
			return map[string]any{"status": "error", "error": err.Error()}, nil
		}
		base = b
	}
	glob, _ := args["glob"].(string)
	ignoreCase, _ := args["ignore_case"].(bool)
	maxMatches := toIntDefault(args["max_matches"], 50)
	if maxMatches < 1 {
		maxMatches = 50
	}
	expr := pattern
	if ignoreCase {
		expr = "(?i)" + pattern
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return map[string]any{"status": "error", "error": "invalid regex: " + err.Error()}, nil
	}
	var matches []string
	walkErr := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == "vendor" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if glob != "" {
			if m, e := filepath.Match(glob, d.Name()); e != nil || !m {
				return nil
			}
		}
		data, e := os.ReadFile(p)
		if e != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for i, ln := range lines {
			if re.MatchString(ln) {
				matches = append(matches, relPath(base, p)+":"+strconv.Itoa(i+1)+":"+ln)
				if len(matches) >= maxMatches {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != filepath.SkipAll {
		return map[string]any{"status": "error", "error": walkErr.Error()}, nil
	}
	return map[string]any{"status": "ok", "count": len(matches), "matches": matches}, nil
}

func (ts *MCPToolSet) codingEditFile(ctx context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)
	if path == "" || oldStr == "" {
		return nil, fmt.Errorf("path and old_string and new_string required")
	}
	fp, err := ts.resolveSafePath(path)
	if err != nil {
		return map[string]any{"status": "error", "error": err.Error()}, nil
	}
	if rel, rerr := filepath.Rel(ts.sandboxRoot, fp); rerr == nil && isSensitiveWriteTarget(rel) {
		slog.Warn("coding tool blocked on protected path", "tool", "coding_edit_file", "path", rel)
		return map[string]any{"status": "error", "error": "refusing to edit protected file: " + rel}, nil
	}
	ok, reason := ts.requireCodingApproval(ctx, "coding_edit_file", args, fmt.Sprintf("编辑文件 %s", fp))
	if !ok {
		return map[string]any{"status": "rejected", "message": reason}, nil
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		return map[string]any{"status": "error", "error": err.Error()}, nil
	}
	content := string(data)
	count := strings.Count(content, oldStr)
	expected := toIntDefault(args["expected_replacements"], 1)
	if expected > 0 && count != expected {
		return map[string]any{"status": "error", "error": fmt.Sprintf("old_string found %d times, expected %d; abort to avoid ambiguous edit", count, expected)}, nil
	}
	if count == 0 {
		return map[string]any{"status": "error", "error": "old_string not found in file"}, nil
	}
	if err := os.WriteFile(fp, []byte(strings.Replace(content, oldStr, newStr, -1)), 0o644); err != nil {
		return map[string]any{"status": "error", "error": err.Error()}, nil
	}
	return map[string]any{"status": "ok", "path": fp, "replacements": count}, nil
}

func (ts *MCPToolSet) codingWriteFile(ctx context.Context, args map[string]any) (any, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return nil, fmt.Errorf("path required")
	}
	fp, err := ts.resolveSafePath(path)
	if err != nil {
		return map[string]any{"status": "error", "error": err.Error()}, nil
	}
	if rel, rerr := filepath.Rel(ts.sandboxRoot, fp); rerr == nil && isSensitiveWriteTarget(rel) {
		slog.Warn("coding tool blocked on protected path", "tool", "coding_write_file", "path", rel)
		return map[string]any{"status": "error", "error": "refusing to write protected file: " + rel}, nil
	}
	ok, reason := ts.requireCodingApproval(ctx, "coding_write_file", args, fmt.Sprintf("写入文件 %s", fp))
	if !ok {
		return map[string]any{"status": "rejected", "message": reason}, nil
	}
	if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
		return map[string]any{"status": "error", "error": err.Error()}, nil
	}
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		return map[string]any{"status": "error", "error": err.Error()}, nil
	}
	return map[string]any{"status": "ok", "path": fp, "bytes": len(content)}, nil
}

func (ts *MCPToolSet) codingApplyPatch(ctx context.Context, args map[string]any) (any, error) {
	patch, _ := args["patch"].(string)
	if patch == "" {
		return nil, fmt.Errorf("patch required")
	}
	// 沙箱校验: 解析补丁中所有路径行(+++ / --- / rename / copy), 任一越界即拒绝。
	paths, perr := extractPatchPaths(patch)
	if perr != nil {
		return map[string]any{"status": "error", "error": perr.Error()}, nil
	}
	for _, p := range paths {
		if p == "/dev/null" || p == "" {
			continue
		}
		fp, err := ts.resolveSafePath(p)
		if err != nil {
			return map[string]any{"status": "error", "error": "patch path escapes sandbox: " + err.Error()}, nil
		}
		if rel, rerr := filepath.Rel(ts.sandboxRoot, fp); rerr == nil && isSensitiveWriteTarget(rel) {
			slog.Warn("coding tool blocked on protected path", "tool", "coding_apply_patch", "path", rel)
			return map[string]any{"status": "error", "error": "patch touches protected file: " + rel}, nil
		}
	}
	// 干跑校验: 审批前先 git apply --check, 冲突直接返回, 避免让用户盲批无法应用的补丁。
	if conflict, skip := gitApplyDryRun(ts.sandboxRoot, patch); !skip && conflict != "" {
		return map[string]any{"status": "error", "error": "patch will not apply cleanly:\n" + conflict}, nil
	}
	ok, reason := ts.requireCodingApproval(ctx, "coding_apply_patch", args, "应用补丁到代码沙箱")
	if !ok {
		return map[string]any{"status": "rejected", "message": reason}, nil
	}
	cmd := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn")
	cmd.Dir = ts.sandboxRoot
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return map[string]any{"status": "error", "error": fmt.Sprintf("git apply failed: %v\n%s", err, out)}, nil
	}
	res := map[string]any{"status": "ok", "applied": true}
	// 应用成功后回显变更概览(git diff --stat), 让用户/模型直接看到补丁落地效果; 非 git 环境为空。
	if st := gitDiffStat(ts.sandboxRoot); st != "" {
		res["diff_stat"] = st
	}
	return res, nil
}

// gitDiffStat 返回沙箱内工作区 diff --stat 概览(截断到 2KB); 非 git 环境或无变更返回空。
func gitDiffStat(root string) string {
	cmd := exec.Command("git", "diff", "--stat")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return ""
	}
	if len(s) > 2000 {
		s = s[:2000] + "\n...[truncated]"
	}
	return s
}

// extractPatchPaths 从 unified diff 提取所有涉及的文件路径(去掉 a/ b/ 前缀),
// 覆盖 +++ / --- / rename / copy 四类路径行, 供沙箱越界校验。返回去重后的相对路径。
// 路径取到首个制表符为止(标准 diff 的 ---/+++ 行在路径后跟 \t 时间戳)。
func extractPatchPaths(patch string) ([]string, error) {
	re := regexp.MustCompile(`^(?:(?:---|\+\+\+)\s+(?:a/|b/)?([^\t\n]+)|(?:rename|copy)\s+(?:from|to)\s+([^\t\n]+))`)
	seen := make(map[string]bool)
	var out []string
	for _, line := range strings.Split(patch, "\n") {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		p := strings.TrimSpace(m[1])
		if p == "" && m[2] != "" {
			p = strings.TrimSpace(m[2])
		}
		if p == "" || p == "/dev/null" {
			continue
		}
		p = strings.Trim(p, `"`)
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// gitApplyDryRun 在沙箱内干跑 git apply --check。返回冲突信息; 非 git 环境(skip=true)则跳过。
func gitApplyDryRun(root, patch string) (conflict string, skip bool) {
	cmd := exec.Command("git", "apply", "--check", "--whitespace=nowarn")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := string(out)
		if strings.Contains(s, "not a git repository") || strings.Contains(s, "outside repository") {
			return "", true
		}
		return s, false
	}
	return "", false
}

// isSensitiveWriteTarget 命中受保护的敏感路径(相对沙箱)时返回 true, 拒绝写/补丁操作,
// 防止 AI 在默认沙箱(=仓库根)下误改 .git / .env / 密钥等文件。
func isSensitiveWriteTarget(rel string) bool {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == ".git" {
			return true
		}
	}
	base := parts[len(parts)-1]
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env."):
		return true
	case strings.HasSuffix(base, ".pem"), strings.HasSuffix(base, ".key"),
		strings.HasSuffix(base, ".pfx"), strings.HasSuffix(base, ".p12"):
		return true
	case base == "id_rsa", base == "id_ed25519", base == "id_dsa", base == "id_ecdsa":
		return true
	case strings.HasSuffix(base, ".sqlite"), strings.HasSuffix(base, ".db"):
		return true
	case base == "service-account.json", base == "credentials.json":
		return true
	}
	return false
}

func (ts *MCPToolSet) codingRunShell(ctx context.Context, args map[string]any) (any, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("command required")
	}
	if blocked := isDangerousShell(command); blocked != "" {
		return map[string]any{"status": "error", "error": "dangerous command blocked: " + blocked}, nil
	}
	ok, reason := ts.requireCodingApproval(ctx, "coding_run_shell", args, fmt.Sprintf("在沙箱执行命令: %s", command))
	if !ok {
		return map[string]any{"status": "rejected", "message": reason}, nil
	}
	timeout := toIntDefault(args["timeout"], 60)
	if timeout < 1 || timeout > 600 {
		timeout = 60
	}
	ctx2, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx2, "sh", "-c", command)
	cmd.Dir = ts.sandboxRoot
	out, err := cmd.CombinedOutput()
	output := string(out)
	if len(output) > 20000 {
		output = output[:20000] + "\n...[truncated]"
	}
	res := map[string]any{"status": "ok", "command": command, "output": output}
	if err != nil {
		res["error"] = err.Error()
		res["exit_code"] = exitCodeOf(cmd, err)
	}
	return res, nil
}

// isDangerousShell 命中已知破坏性命令模式时返回其标签(用于拒绝执行)。
func isDangerousShell(cmd string) string {
	checks := []struct {
		re    *regexp.Regexp
		label string
	}{
		{regexp.MustCompile(`rm\s+-rf\s+/`), "rm -rf /"},
		{regexp.MustCompile(`rm\s+-r[ f]*\s+/`), "rm -r /"},
		{regexp.MustCompile(`mkfs`), "mkfs"},
		{regexp.MustCompile(`\bdd\s+if=`), "dd if="},
		{regexp.MustCompile(`\bshutdown\b`), "shutdown"},
		{regexp.MustCompile(`\breboot\b`), "reboot"},
		{regexp.MustCompile(`curl\s+[^|]*\|\s*(bash|sh)`), "curl|bash"},
		{regexp.MustCompile(`wget\s+[^|]*\|\s*(bash|sh)`), "wget|sh"},
		{regexp.MustCompile(`:\(\)\s*\{`), "fork bomb"},
	}
	for _, c := range checks {
		if c.re.MatchString(cmd) {
			return c.label
		}
	}
	return ""
}

func exitCodeOf(cmd *exec.Cmd, err error) int {
	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	return -1
}

// toInt 从 JSON 常见数值形态(float64/int/int64/string)解析整数。
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i, true
		}
	}
	return 0, false
}

func toIntDefault(v any, def int) int {
	if i, ok := toInt(v); ok {
		return i
	}
	return def
}

// relPath 返回 p 相对 root 的路径(用于工具结果展示)。
func relPath(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}
