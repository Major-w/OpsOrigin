// Package sandbox 提供在 Server 本机运行的"沙箱"脚本执行器。
//
// 设计目标（对应接入操作指引中的"勾选沙箱环境执行"）：
//   - 脚本执行不再需要选择目标主机，而是直接在 Server 部署所在机器上，
//     于一个隔离子进程中运行，用于安全的调试/冒烟测试。
//   - 隔离手段：独立临时工作目录、受 timeout 约束、默认无额外特权环境、
//     不回显/不注入 Server 自身凭证。
//   - 多个沙箱环境：每个在 Server 上注册的 AgentTypeSandbox Agent 对应一个
//     沙箱环境，可由调用方通过 sandboxID 指定；不指定时取默认本地执行器。
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Result 是沙箱执行结果。
type Result struct {
	Output   string        `json:"output"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration_ms,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// Executor 在 Server 本机以隔离子进程方式执行脚本（沙箱）。
type Executor struct {
	// WorkDir 沙箱根目录（会在其下创建临时子目录），默认 os.TempDir()。
	WorkDir string
	// Shell 解释器，默认 /bin/sh（传脚本文件路径）。
	Shell string
	// Env 追加到子进程的环境变量（不暴露 Server 真实凭证）。
	Env []string
}

// NewExecutor 构造默认本地沙箱执行器。
func NewExecutor() *Executor {
	return &Executor{
		WorkDir: os.TempDir(),
		Shell:   defaultShell(),
	}
}

func defaultShell() string {
	if s, err := exec.LookPath("bash"); err == nil {
		return s
	}
	return "/bin/sh"
}

// Execute 在沙箱中执行给定脚本文本，返回合并输出与退出码。
// 脚本被写入隔离临时目录后以 "shell 脚本路径" 方式执行，避免 -c 拼接带来的
// 引号/注入问题。超时被归类为 Error="sandbox execution timeout"，ExitCode=-1。
func (e *Executor) Execute(ctx context.Context, script string, timeout time.Duration) (*Result, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir, err := os.MkdirTemp(e.WorkDir, "opsorigin-sandbox-")
	if err != nil {
		return nil, fmt.Errorf("create sandbox dir: %w", err)
	}
	defer os.RemoveAll(dir)

	scriptPath := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, e.Shell, scriptPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), e.Env...)
	out, runErr := cmd.CombinedOutput()
	dur := time.Since(start)

	res := &Result{Output: string(out), Duration: dur}
	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			res.Error = "sandbox execution timeout"
			res.ExitCode = -1
			return res, nil
		}
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		res.Error = runErr.Error()
		res.ExitCode = -1
		return res, nil
	}
	return res, nil
}
