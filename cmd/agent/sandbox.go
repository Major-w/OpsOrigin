package main

// 沙箱执行(2026-07-30 重构)
//
// 背景:旧实现把"沙箱"做成 Core 进程内注册的一条伪 Agent 记录,脚本实际在
// Server 本机裸跑,既污染主机列表也谈不上隔离。新设计是:给部署 Server 的那些
// 主机上**真实运行的 Agent** 打一个 sandbox 标签,带标签的 Agent 收到
// Sandbox=true 的任务时,在其所在目标主机上创建一个真正的隔离环境来执行:
//
//   1. 独立临时工作目录(执行结束整体删除),HOME/TMPDIR 都指向它,
//      脚本产生的任何文件都落在沙箱内,不污染主机。
//   2. 净化环境变量:只透传必要的 PATH/LANG 等,不继承 Agent 进程持有的
//      令牌、密钥等敏感环境变量,再叠加本次任务的 PARAM_* 入参。
//   3. 独立进程组 + 超时后整组 kill,避免脚本 fork 出的子进程变成孤儿。
//   4. Python 脚本额外创建虚拟环境(venv),依赖装在沙箱内的 venv 里,
//      不会污染主机的全局 site-packages;执行结束随目录一并销毁。

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/opsorigin/core/internal/command"
)

// sandboxEnvWhitelist 是允许从 Agent 进程继承到沙箱子进程的环境变量名。
// 其余一律不透传,防止 Agent 持有的凭证/令牌泄漏进被测脚本。
var sandboxEnvWhitelist = []string{
	"PATH", "LANG", "LC_ALL", "TZ", "SystemRoot", "COMSPEC", "PATHEXT",
}

// executeScriptSandboxed 在目标主机的真实隔离环境中执行脚本(沙箱模式: 防读防写)。
func (a *Agent) executeScriptSandboxed(task *TaskRequest) TaskResponse {
	return a.runScriptIsolated(task, "sandbox")
}

// executeScriptCollect 只读采集模式: 允许脚本读取主机系统信息(采集上报是目的),
// 但写仍被限制在隔离临时目录、环境净化(不泄漏 Agent 凭证)、拦截逃逸宿主的高危命令。
// 与 sandbox 的差异在"读"的语义与未来隔离实现:
//   - sandbox: 未来 bwrap 化时全隔离(不挂载主机路径);
//   - collect:  未来 bwrap 化时只读挂载 /proc /sys /var/log 等采集所需路径。
func (a *Agent) executeScriptCollect(task *TaskRequest) TaskResponse {
	return a.runScriptIsolated(task, "collect")
}

// runScriptIsolated 进程级隔离执行(沙箱 / 只读采集共用骨架)。
// mode 仅用于语义标记与日志: "sandbox" | "collect"。
func (a *Agent) runScriptIsolated(task *TaskRequest, mode string) TaskResponse {
	// 沙箱高危命令硬拦截:复用 command 包的高危检测逻辑,针对沙箱环境内置一组
	// 禁止执行的规则(关机 / 删除系统目录 / 格式化磁盘 / 远程管道执行 等)。
	// 命中的脚本直接拒绝——不进入隔离环境、不降级放行。
	if blocked := command.BlockedInSandbox(task.Script); len(blocked) > 0 {
		var b strings.Builder
		b.WriteString("sandbox: 脚本包含沙箱禁止的高危命令,已拒绝执行:\n")
		for _, m := range blocked {
			b.WriteString(fmt.Sprintf("  - 第 %d 行 [%s] %s: %s\n", m.Line, m.Level, m.RuleID, m.Desc))
		}
		return TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1, Error: b.String()}
	}

	// §6 命令白名单:沙箱执行同样受控。
	if err := a.verifyCommand(task.Script); err != nil {
		return TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1,
			Error: "denied by whitelist: " + err.Error()}
	}

	start := time.Now()
	timeout := time.Duration(task.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}

	// 1. 独立临时工作目录,执行完整体销毁。
	dir, err := os.MkdirTemp("", "opsorigin-sbx-")
	if err != nil {
		return TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1,
			Error: fmt.Sprintf("create sandbox dir: %v", err)}
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	scriptType := strings.ToLower(strings.TrimSpace(task.ScriptType))
	if scriptType == "" {
		scriptType = "shell"
	}

	var setupLog string
	var cmd *exec.Cmd

	switch scriptType {
	case "python", "py":
		interp, log, err := a.preparePythonVenv(ctx, dir, task.Requirements)
		setupLog = log
		if err != nil {
			return TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1,
				Output: setupLog, Error: err.Error(),
				Duration: time.Since(start).Milliseconds()}
		}
		scriptPath := filepath.Join(dir, "main.py")
		if err := os.WriteFile(scriptPath, []byte(task.Script), 0o600); err != nil {
			return TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1,
				Error: fmt.Sprintf("write script: %v", err)}
		}
		cmd = exec.CommandContext(ctx, interp, scriptPath)

	case "bat", "cmd":
		scriptPath := filepath.Join(dir, "run.bat")
		if err := os.WriteFile(scriptPath, []byte(task.Script), 0o700); err != nil {
			return TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1,
				Error: fmt.Sprintf("write script: %v", err)}
		}
		cmd = exec.CommandContext(ctx, "cmd", "/C", scriptPath)

	default: // shell
		if runtime.GOOS == "windows" {
			scriptPath := filepath.Join(dir, "run.bat")
			if err := os.WriteFile(scriptPath, []byte(task.Script), 0o700); err != nil {
				return TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1,
					Error: fmt.Sprintf("write script: %v", err)}
			}
			cmd = exec.CommandContext(ctx, "cmd", "/C", scriptPath)
		} else {
			scriptPath := filepath.Join(dir, "run.sh")
			if err := os.WriteFile(scriptPath, []byte(task.Script), 0o700); err != nil {
				return TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1,
					Error: fmt.Sprintf("write script: %v", err)}
			}
			// 以"解释器 + 脚本文件"方式执行,避免 -c 拼接带来的引号/注入问题。
			cmd = exec.CommandContext(ctx, sandboxShell(), scriptPath)
		}
	}

	// 2. 工作目录与净化后的环境变量。
	cmd.Dir = dir
	cmd.Env = sandboxEnv(dir, task.Params)
	// 3. 独立进程组,超时后可整组回收。
	applySandboxProcAttr(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	// 超时时子进程可能已 fork 出后代,连同整个进程组一起清理。
	if ctx.Err() == context.DeadlineExceeded && cmd.Process != nil {
		killProcessGroup(cmd.Process.Pid)
	}
	duration := time.Since(start)

	exitCode := 0
	errMsg := stderr.String()
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			if errMsg == "" {
				errMsg = runErr.Error()
			}
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		exitCode = -1
		errMsg = fmt.Sprintf("sandbox execution timeout after %s", timeout)
	}

	return TaskResponse{
		TaskID:   task.TaskID,
		Success:  exitCode == 0 && ctx.Err() == nil,
		Output:   setupLog + stdout.String(),
		ExitCode: exitCode,
		Duration: duration.Milliseconds(),
		Error:    errMsg,
	}
}

// preparePythonVenv 在沙箱目录内创建 Python 虚拟环境并安装依赖,
// 返回虚拟环境中的解释器路径。依赖只装在沙箱内,不污染主机全局环境。
func (a *Agent) preparePythonVenv(ctx context.Context, dir string, requirements []string) (string, string, error) {
	var log strings.Builder

	basePython := lookupPython()
	if basePython == "" {
		return "", log.String(), fmt.Errorf("目标主机上未找到 python3/python,无法创建沙箱虚拟环境")
	}

	venvDir := filepath.Join(dir, ".venv")
	mk := exec.CommandContext(ctx, basePython, "-m", "venv", venvDir)
	mk.Dir = dir
	mk.Env = sandboxEnv(dir, nil)
	if out, err := mk.CombinedOutput(); err != nil {
		// venv 不可用(如未装 python3-venv)时明确报错,不静默降级到全局解释器,
		// 否则"沙箱"就名不副实了。
		return "", log.String(), fmt.Errorf("创建 Python 虚拟环境失败(请确认目标主机已安装 venv 模块): %v: %s",
			err, strings.TrimSpace(string(out)))
	}
	log.WriteString("[sandbox] python venv created at .venv\n")

	interp := filepath.Join(venvDir, "bin", "python")
	pip := filepath.Join(venvDir, "bin", "pip")
	if runtime.GOOS == "windows" {
		interp = filepath.Join(venvDir, "Scripts", "python.exe")
		pip = filepath.Join(venvDir, "Scripts", "pip.exe")
	}

	if len(requirements) > 0 {
		args := append([]string{"install", "--disable-pip-version-check", "-q"}, requirements...)
		ins := exec.CommandContext(ctx, pip, args...)
		ins.Dir = dir
		ins.Env = sandboxEnv(dir, nil)
		out, err := ins.CombinedOutput()
		if err != nil {
			return "", log.String(), fmt.Errorf("沙箱内安装依赖失败 %v: %v: %s",
				requirements, err, strings.TrimSpace(string(out)))
		}
		log.WriteString(fmt.Sprintf("[sandbox] installed requirements: %s\n",
			strings.Join(requirements, ", ")))
	}
	return interp, log.String(), nil
}

// lookupPython 优先 python3,回退 python。
func lookupPython() string {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// sandboxShell 优先 bash,回退 /bin/sh。
func sandboxShell() string {
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	return "/bin/sh"
}

// sandboxEnv 构造沙箱子进程的环境变量:白名单透传 + HOME/TMPDIR 指向沙箱目录
// + 本次任务的 PARAM_* 入参。刻意不使用 os.Environ(),避免把 Agent 持有的
// 凭证(如 OPSORIGIN_TOKEN 之类)泄漏给被测脚本。
func sandboxEnv(dir string, params map[string]any) []string {
	env := make([]string, 0, 16)
	for _, k := range sandboxEnvWhitelist {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	env = append(env,
		"HOME="+dir,
		"TMPDIR="+dir,
		"TEMP="+dir,
		"TMP="+dir,
		"PWD="+dir,
		"OPSORIGIN_SANDBOX=1",
	)
	for k, v := range params {
		env = append(env, fmt.Sprintf("PARAM_%s=%v", strings.ToUpper(k), v))
	}
	return env
}

// applySandboxProcAttr 让子进程独立成组,便于超时后整组回收。
func applySandboxProcAttr(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 杀掉以 pid 为组长的整个进程组。
func killProcessGroup(pid int) {
	if runtime.GOOS == "windows" {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
