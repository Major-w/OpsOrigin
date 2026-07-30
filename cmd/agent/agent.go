package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/command"
	"github.com/opsorigin/core/internal/tunnel"
)

// AgentConfig holds runtime configuration.
type AgentConfig struct {
	ServerAddr string // e.g. "192.168.1.1:8080"
	GRPCServer string // e.g. "192.168.1.1:9091" (可选,启用 gRPC 直连)
	ProxyAddr  string // 非空时通过 Proxy 中转,不再直连 Core
	AgentName  string // optional, defaults to hostname
	Interval   time.Duration
}

// HeartbeatPayload is sent to the server periodically.
type HeartbeatPayload struct {
	AgentVersion string   `json:"agent_version"`
	OS           string   `json:"os"`
	OSVersion    string   `json:"os_version"`
	Hostname     string   `json:"hostname"`
	CPU          string   `json:"cpu"`
	Memory       string   `json:"memory"`
	Disk         string   `json:"disk"`
	IPs          []string `json:"ips"`
	Capabilities []string `json:"capabilities"`
}

// TaskRequest received from the server for script execution.
type TaskRequest struct {
	TaskID   string         `json:"task_id"`
	Action   string         `json:"action"`
	Script   string         `json:"script"`
	Params   map[string]any `json:"params,omitempty"`
	Timeout  int            `json:"timeout,omitempty"`

	// 沙箱执行:为 true 时不直接裸跑脚本,而是在本机创建真实隔离环境
	// (独立临时工作目录 + 净化环境变量 + 独立进程组;python 另建 venv)。
	// 见 sandbox.go。
	Sandbox      bool     `json:"sandbox,omitempty"`
	ScriptType   string   `json:"script_type,omitempty"`  // shell(默认) | python | bat
	Requirements []string `json:"requirements,omitempty"` // python 沙箱内 pip 安装的依赖
}

// TaskResponse sent back to the server after execution.
type TaskResponse struct {
	TaskID   string `json:"task_id"`
	Success  bool   `json:"success"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Duration int64  `json:"duration_ms"`
	Error    string `json:"error,omitempty"`
}

// MetricReport sent for monitoring data.
type MetricReport struct {
	AgentID   string  `json:"agent_id"`
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Timestamp int64   `json:"timestamp"`
}

// CMDBReport for CMDB data reporting.
type CMDBReport struct {
	AgentID    string            `json:"agent_id"`
	Hostname   string            `json:"hostname"`
	OS         string            `json:"os"`
	OSVersion  string            `json:"os_version"`
	CPU        string            `json:"cpu"`
	Memory     string            `json:"memory"`
	Disk       string            `json:"disk"`
	IPs        []string          `json:"ips"`
	Labels     map[string]string `json:"labels,omitempty"`
	Timestamp  int64             `json:"timestamp"`
}

// FileDownloadRequest for downloading files from server.
// ExpectedChecksum / ChecksumAlgo: 非空时下载完成后强制校验(§5.3 MD5/SHA256)。
// 目前校验算法固定为 sha256(与 Server 端 download_tokens 约定一致)。
type FileDownloadRequest struct {
	FileURL         string `json:"file_url"`
	FilePath        string `json:"file_path"`
	ExpectedChecksum string `json:"expected_checksum,omitempty"`
	ChecksumAlgo    string `json:"checksum_algo,omitempty"`
}

// Agent is the main agent process.
type Agent struct {
	cfg          AgentConfig
	agentID      string
	token        string
	baseURL      string
	pollURL      string
	client       *http.Client
	tunnelClient *tunnel.AgentClient // Proxy 中转客户端(可选)
	grpcClient   *GRPCAgentClient    // gRPC 直连客户端(可选)
	stopCh       chan struct{}
	stopOnce     sync.Once
	wsConn       *websocket.Conn // WebSocket 长连接(可选)
	wsMu         sync.Mutex

	// 命令白名单(§6):非 nil 时每次执行前强制校验,未命中则拒绝。
	// 由 Server 的 GET /api/v1/agents/:id/whitelist 下发,nil 表示该租户未启用。
	whitelist       *command.Whitelist
	whitelistMu     sync.RWMutex
	whitelistVer    int64
}

func NewAgent(cfg AgentConfig) *Agent {
	baseURL := fmt.Sprintf("http://%s/api/v1", cfg.ServerAddr)
	a := &Agent{
		cfg:     cfg,
		baseURL: baseURL,
		pollURL: baseURL + "/agents/tasks",
		client:  &http.Client{Timeout: 30 * time.Second},
		stopCh:  make(chan struct{}),
	}

	// 配置了 Proxy 地址时,创建 tunnel 客户端,通过 Proxy 中转所有通信
	if cfg.ProxyAddr != "" {
		a.agentID = uuid.NewString()
		a.tunnelClient = tunnel.NewAgentClient(cfg.ProxyAddr, a.agentID)
		a.tunnelClient.OnAction(a.handleTunnelAction)
	}

	// 配置了 gRPC 服务器地址时,创建 gRPC 客户端,直连 Core
	if cfg.GRPCServer != "" {
		if a.agentID == "" {
			a.agentID = uuid.NewString()
		}
		a.grpcClient = NewGRPCAgentClient(cfg.GRPCServer, a.agentID)
		a.grpcClient.OnAction(a.handleTunnelAction)
		// 采集一次系统信息,注册时随 gRPC RegisterRequest 上报(OS/IP/CPU/Memory),
		// 让 Core 端 agent_hosts 不再是无资源信息的空壳。
		a.grpcClient.sysInfo = a.collectSystemInfo()
	}

	return a
}

// --- Registration ---

func (a *Agent) Register(ctx context.Context) error {
	hostname, _ := os.Hostname()
	if a.cfg.AgentName != "" {
		hostname = a.cfg.AgentName
	}

	payload := map[string]any{
		"name":       hostname,
		"type":       "agent",
		"address":    getPrimaryIP(),
		"port":       8080,
		"os_type":    runtime.GOOS,
		"auth_type":  "token",
	}

	data, _ := json.Marshal(payload)
	resp, err := a.client.Post(a.baseURL+"/agents", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register failed %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	a.agentID = result.ID

	fmt.Printf("[agent] registered with server, agent_id=%s\n", a.agentID)
	return nil
}

// --- Command Whitelist (§6) ---

// verifyCommand 命令白名单校验。whitelist 为 nil 表示该租户未启用,直接放行。
func (a *Agent) verifyCommand(script string) error {
	a.whitelistMu.RLock()
	wl := a.whitelist
	a.whitelistMu.RUnlock()
	if wl == nil {
		return nil
	}
	return wl.Verify(script)
}

// fetchWhitelist 从 Server 拉取本 Agent 所属租户的命令白名单。
// 失败不阻塞启动(后续执行仍受控于已加载的或空白名单)。
func (a *Agent) fetchWhitelist(ctx context.Context) {
	if a.agentID == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, "GET",
		a.baseURL+"/agents/"+a.agentID+"/whitelist", nil)
	if err != nil {
		return
	}
	resp, err := a.client.Do(req)
	if err != nil {
		slog.Warn("fetch whitelist failed", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		slog.Warn("fetch whitelist rejected", "status", resp.StatusCode)
		return
	}
	var body struct {
		Enabled bool              `json:"enabled"`
		Version int64            `json:"version"`
		Rules   []json.RawMessage `json:"rules"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		slog.Warn("decode whitelist failed", "error", err)
		return
	}
	if !body.Enabled {
		// 租户未启用白名单:放行一切(由管理员在 Server 端开启 enforce)。
		a.whitelistMu.Lock()
		a.whitelist = nil
		a.whitelistMu.Unlock()
		slog.Info("command whitelist disabled for tenant", "agent_id", a.agentID)
		return
	}
	rulesJSON, _ := json.Marshal(body.Rules)
	wl, err := command.FromJSON("enforce", string(rulesJSON))
	if err != nil {
		slog.Warn("build whitelist failed", "error", err)
		return
	}
	a.whitelistMu.Lock()
	a.whitelist = wl
	a.whitelistVer = body.Version
	a.whitelistMu.Unlock()
	slog.Info("command whitelist loaded", "agent_id", a.agentID, "version", body.Version, "rules", len(body.Rules))
}

// startWhitelistRefresh 周期性刷新白名单(管理员在 Server 端变更规则后生效)。
func (a *Agent) startWhitelistRefresh(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.fetchWhitelist(ctx)
		}
	}
}

// --- Heartbeat ---

func (a *Agent) Heartbeat(ctx context.Context) error {
	payload := a.collectSystemInfo()
	data, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST",
		a.baseURL+"/agents/"+a.agentID+"/heartbeat", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("heartbeat rejected %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status          string `json:"status"`
		BreakerOpen     bool   `json:"breaker_open"`
		RestartRequested bool  `json:"restart_requested"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.RestartRequested {
		fmt.Printf("[agent] restart requested by server, exiting so the host service can restart the agent\n")
		a.Stop()
	}
	return nil
}

func (a *Agent) HeartbeatLoop(ctx context.Context) {
	interval := a.cfg.Interval
	if interval == 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if err := a.Heartbeat(ctx); err != nil {
		fmt.Printf("[agent] initial heartbeat error: %v\n", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			if err := a.Heartbeat(ctx); err != nil {
				fmt.Printf("[agent] heartbeat error: %v\n", err)
			}
		}
	}
}

// --- Task Polling ---

func (a *Agent) PollTasks(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			tasks, err := a.fetchPendingTasks(ctx)
			if err != nil {
				fmt.Printf("[agent] poll error: %v\n", err)
				continue
			}
			for _, task := range tasks {
				go a.executeTask(ctx, task)
			}
		}
	}
}

func (a *Agent) fetchPendingTasks(ctx context.Context) ([]TaskRequest, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET",
		a.baseURL+"/agents/"+a.agentID+"/tasks", nil)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tasks []TaskRequest
	json.NewDecoder(resp.Body).Decode(&tasks)
	return tasks, nil
}

func (a *Agent) executeTask(ctx context.Context, task TaskRequest) {
	// §6 命令白名单:执行前强制校验,未命中则拒绝并上报。
	if err := a.verifyCommand(task.Script); err != nil {
		fmt.Printf("[agent] task %s denied by whitelist: %v\n", task.TaskID, err)
		result := TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1, Error: err.Error(), Duration: 0}
		data, _ := json.Marshal(result)
		if resp, rerr := a.client.Post(
			a.baseURL+"/agents/"+a.agentID+"/tasks/"+task.TaskID,
			"application/json", bytes.NewReader(data)); rerr == nil {
			resp.Body.Close()
		}
		return
	}

	start := time.Now()
	timeout := time.Duration(task.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "windows":
		cmd = exec.CommandContext(execCtx, "cmd", "/C", task.Script)
	default:
		cmd = exec.CommandContext(execCtx, "/bin/sh", "-c", task.Script)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	result := TaskResponse{
		TaskID:   task.TaskID,
		Success:  exitCode == 0,
		Output:   stdout.String(),
		ExitCode: exitCode,
		Duration: duration.Milliseconds(),
		Error:    stderr.String(),
	}
	data, _ := json.Marshal(result)

	resp, err := a.client.Post(
		a.baseURL+"/agents/"+a.agentID+"/tasks/"+task.TaskID,
		"application/json", bytes.NewReader(data))
	if err != nil {
		fmt.Printf("[agent] report task result error: %v\n", err)
		return
	}
	resp.Body.Close()
	fmt.Printf("[agent] task %s completed in %dms, exit=%d\n",
		task.TaskID, duration.Milliseconds(), exitCode)
}

// executeScript 执行脚本任务并返回结果,不进行 HTTP 结果上报。
// 供 tunnel 模式(handleTunnelAction)复用。
func (a *Agent) executeScript(task *TaskRequest) TaskResponse {
	// 沙箱任务走真实隔离执行路径(见 sandbox.go)。
	if task.Sandbox {
		return a.executeScriptSandboxed(task)
	}

	// §6 命令白名单:执行前强制校验。
	if err := a.verifyCommand(task.Script); err != nil {
		fmt.Printf("[agent] script task %s denied by whitelist: %v\n", task.TaskID, err)
		return TaskResponse{TaskID: task.TaskID, Success: false, ExitCode: -1, Error: err.Error(), Duration: 0}
	}

	start := time.Now()
	timeout := time.Duration(task.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	execCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "windows":
		cmd = exec.CommandContext(execCtx, "cmd", "/C", task.Script)
	default:
		cmd = exec.CommandContext(execCtx, "/bin/sh", "-c", task.Script)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return TaskResponse{
		TaskID:   task.TaskID,
		Success:  exitCode == 0,
		Output:   stdout.String(),
		ExitCode: exitCode,
		Duration: duration.Milliseconds(),
		Error:    stderr.String(),
	}
}

// handleTunnelAction 处理通过 Proxy 下发的命令。
// payload 格式与 HTTP TaskRequest 兼容。
func (a *Agent) handleTunnelAction(action string, payload []byte) ([]byte, error) {
	switch action {
	case "execute_script", "run_script", "script":
		var task TaskRequest
		if err := json.Unmarshal(payload, &task); err != nil {
			return nil, fmt.Errorf("unmarshal task: %w", err)
		}
		resp := a.executeScript(&task)
		out, _ := json.Marshal(resp)
		return out, nil
	case "restart":
		fmt.Printf("[agent] restart requested via tunnel, exiting so the host service can restart the agent\n")
		a.Stop()
		return []byte(`{"status":"restarting"}`), nil
	case "upgrade":
		// 重新下载 agent 二进制并重启
		var req struct {
			TargetVersion string `json:"target_version,omitempty"`
		}
		json.Unmarshal(payload, &req)
		output, err := a.doUpgrade(req.TargetVersion)
		if err != nil {
			return nil, fmt.Errorf("upgrade failed: %w", err)
		}
		return []byte(output), nil
	case "uninstall":
		// 卸载:停止服务、删除文件、退出进程
		go func() {
			time.Sleep(2 * time.Second)
			a.doUninstall()
		}()
		return []byte("uninstalling"), nil
	default:
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

// doUpgrade 重新下载 agent 二进制并重启。
// targetVersion 为空时升级到最新版本。
func (a *Agent) doUpgrade(targetVersion string) (string, error) {
	// 获取当前可执行文件路径
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}

	// 备份当前二进制
	backupPath := exePath + ".bak"
	if err := os.Rename(exePath, backupPath); err != nil {
		return "", fmt.Errorf("backup current binary: %w", err)
	}

	// 从 Server 下载新二进制
	// URL: http://SERVER/api/v1/agents/download?os=linux&arch=amd64
	serverBase := fmt.Sprintf("http://%s", a.cfg.ServerAddr)
	downloadURL := fmt.Sprintf("%s/api/v1/agents/download?os=linux&arch=amd64", serverBase)

	resp, err := http.Get(downloadURL)
	if err != nil {
		// 下载失败,回滚
		os.Rename(backupPath, exePath)
		return "", fmt.Errorf("download new binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		os.Rename(backupPath, exePath)
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// 写入新二进制
	out, err := os.Create(exePath)
	if err != nil {
		os.Rename(backupPath, exePath)
		return "", fmt.Errorf("create new binary: %w", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Rename(backupPath, exePath)
		return "", fmt.Errorf("write new binary: %w", err)
	}
	out.Close()

	if err := os.Chmod(exePath, 0755); err != nil {
		return "", fmt.Errorf("chmod: %w", err)
	}

	// 删除备份
	os.Remove(backupPath)

	// 重启:通过 syscall.Exec 替换当前进程
	slog.Info("agent upgraded, restarting", "path", exePath)
	go func() {
		time.Sleep(1 * time.Second)
		a.Stop()
	}()

	return fmt.Sprintf("upgraded successfully, restarting"), nil
}

// doUninstall 停止服务、删除二进制和配置文件,然后退出进程。
func (a *Agent) doUninstall() {
	slog.Info("agent uninstalling")

	exePath, err := os.Executable()
	if err != nil {
		slog.Error("get executable path failed", "error", err)
		os.Exit(1)
	}

	// 停止自己
	a.Stop()

	// 删除二进制
	os.Remove(exePath)

	// 删除配置文件(同目录下的 config.yaml)
	configPath := filepath.Join(filepath.Dir(exePath), "config.yaml")
	os.Remove(configPath)

	// 尝试停止 systemd 服务(如果有)
	// 注意:agent 进程本身可能就是 systemd 管理的,直接 exit 即可
	// systemd 会尝试重启,但二进制已删除,重启会失败

	slog.Info("agent uninstalled, exiting")
	os.Exit(0)
}

// --- Metrics Reporting ---

func (a *Agent) ReportMetrics(metrics []MetricReport) error {
	if len(metrics) == 0 {
		return nil
	}
	data, _ := json.Marshal(metrics)
	resp, err := a.client.Post(
		a.baseURL+"/monitor/metrics",
		"application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// --- CMDB Data Reporting ---

func (a *Agent) ReportCMDBData() error {
	info := a.collectSystemInfo()
	report := CMDBReport{
		AgentID:   a.agentID,
		Hostname:  info.Hostname,
		OS:        info.OS,
		OSVersion: info.OSVersion,
		CPU:       info.CPU,
		Memory:    info.Memory,
		Disk:      info.Disk,
		IPs:       info.IPs,
		Timestamp: time.Now().UnixMilli(),
	}
	
	data, _ := json.Marshal(report)
	resp, err := a.client.Post(
		a.baseURL+"/agents/"+a.agentID+"/cmdb",
		"application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// --- File Download ---

// DownloadFile 下载文件,带断点续传(Range)、SHA256 校验与指数退避重试(§5.3)。
// expectedChecksum 为空时跳过校验。下载失败自动重试最多 3 次(1s/2s 退避)。
func (a *Agent) DownloadFile(fileURL, filePath, expectedChecksum, algo string) error {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			slog.Warn("file download retry", "url", fileURL, "attempt", attempt+1, "backoff", backoff.String())
			time.Sleep(backoff)
		}
		if err := a.downloadFileOnce(fileURL, filePath, expectedChecksum, algo); err != nil {
			lastErr = err
			slog.Warn("file download attempt failed", "url", fileURL, "attempt", attempt+1, "error", err)
			continue
		}
		return nil
	}
	return fmt.Errorf("download %s failed after %d attempts: %w", fileURL, maxRetries, lastErr)
}

// downloadFileOnce 单次下载(支持从已存在文件尾部续传)。
func (a *Agent) downloadFileOnce(fileURL, filePath, expectedChecksum, algo string) error {
	// 断点续传:若目标文件已存在,从文件末尾继续。
	var startOffset int64
	if fi, err := os.Stat(filePath); err == nil && fi.Size() > 0 {
		startOffset = fi.Size()
	}

	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	if startOffset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startOffset))
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download status %d", resp.StatusCode)
	}

	flag := os.O_CREATE | os.O_WRONLY
	if startOffset > 0 {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(filePath, flag, 0644)
	if err != nil {
		return fmt.Errorf("open destination: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	// 确保落盘后再校验。
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync file: %w", err)
	}

	if expectedChecksum != "" {
		got, err := fileChecksum(filePath, algo)
		if err != nil {
			return fmt.Errorf("checksum compute: %w", err)
		}
		if !strings.EqualFold(got, strings.TrimSpace(expectedChecksum)) {
			return fmt.Errorf("checksum mismatch: got %s want %s", got, expectedChecksum)
		}
		slog.Info("file download verified", "path", filePath, "algo", algo, "checksum", got)
	}
	return nil
}

// fileChecksum 计算文件校验和,algo 为空或 "sha256" 使用 SHA256,"md5" 使用 MD5。
func fileChecksum(path, algo string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	switch strings.ToLower(algo) {
	case "md5":
		h := md5.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	default:
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
}

func (a *Agent) DownloadFileLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.pollDownloadTasks(ctx)
		}
	}
}

func (a *Agent) pollDownloadTasks(ctx context.Context) {
	req, _ := http.NewRequestWithContext(ctx, "GET",
		a.baseURL+"/agents/"+a.agentID+"/download-tasks", nil)
	resp, err := a.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	
	var tasks []FileDownloadRequest
	json.NewDecoder(resp.Body).Decode(&tasks)
	
	for _, task := range tasks {
		if err := a.DownloadFile(task.FileURL, task.FilePath, task.ExpectedChecksum, task.ChecksumAlgo); err != nil {
			fmt.Printf("[agent] download error for %s: %v\n", task.FileURL, err)
		} else {
			fmt.Printf("[agent] downloaded %s to %s\n", task.FileURL, task.FilePath)
		}
	}
}

// --- System Info ---

func (a *Agent) collectSystemInfo() HeartbeatPayload {
	hostname, _ := os.Hostname()
	payload := HeartbeatPayload{
		AgentVersion: "1.0.0",
		OS:           runtime.GOOS,
		Hostname:     hostname,
		Capabilities: []string{"shell", "script", "file_transfer", "monitor"},
	}
	payload.CPU = collectCPUInfo()
	payload.Memory = collectMemoryInfo()
	payload.Disk = collectDiskInfo()
	payload.IPs = collectIPs()
	payload.OSVersion = collectOSVersion()
	return payload
}

func collectCPUInfo() string {
	cpuCount := runtime.NumCPU()
	switch runtime.GOOS {
	case "linux":
		data, err := os.ReadFile("/proc/cpuinfo")
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "model name") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						return fmt.Sprintf("%d Cores (%s)", cpuCount, strings.TrimSpace(parts[1]))
					}
				}
			}
		}
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output()
		if err == nil {
			return fmt.Sprintf("%d Cores (%s)", cpuCount, strings.TrimSpace(string(out)))
		}
	}
	return fmt.Sprintf("%d Cores", cpuCount)
}

func collectMemoryInfo() string {
	switch runtime.GOOS {
	case "linux":
		data, err := os.ReadFile("/proc/meminfo")
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "MemTotal") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						kb := 0
						fmt.Sscanf(fields[1], "%d", &kb)
						return fmt.Sprintf("%.1f GB", float64(kb)/(1024*1024))
					}
				}
			}
		}
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			var bytes int64
			fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &bytes)
			return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
		}
	}
	return "unknown"
}

func collectDiskInfo() string {
	switch runtime.GOOS {
	case "linux":
		out, err := exec.Command("df", "-h", "/").Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) >= 2 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 4 {
					return fmt.Sprintf("%s total / %s used", fields[1], fields[2])
				}
			}
		}
	case "darwin":
		out, err := exec.Command("df", "-h", "/").Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) >= 2 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 3 {
					return fmt.Sprintf("%s total / %s used", fields[1], fields[2])
				}
			}
		}
	}
	return "unknown"
}

func collectOSVersion() string {
	switch runtime.GOOS {
	case "linux":
		data, err := os.ReadFile("/etc/os-release")
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "PRETTY_NAME=") {
					return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				}
			}
		}
	case "darwin":
		out, err := exec.Command("sw_vers", "-productVersion").Output()
		if err == nil {
			return "macOS " + strings.TrimSpace(string(out))
		}
	}
	return runtime.GOOS
}

func collectIPs() []string {
	ips := make([]string, 0)
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}
	return ips
}

func getPrimaryIP() string {
	ips := collectIPs()
	if len(ips) > 0 {
		return ips[0]
	}
	return "127.0.0.1"
}

// --- Main ---

func (a *Agent) Run(ctx context.Context) error {
	// 启动前拉取命令白名单(§6),并周期刷新;失败不阻塞启动。
	a.fetchWhitelist(ctx)
	go a.startWhitelistRefresh(ctx)

	// gRPC 直连模式:直接通过 gRPC 与 Core 通信
	if a.grpcClient != nil {
		slog.Info("agent starting in gRPC mode", "core", a.cfg.GRPCServer, "agent_id", a.agentID)
		return a.grpcClient.Start(ctx)
	}

	// Proxy 中转模式:通过 Proxy 连接 Core,不走直连
	if a.tunnelClient != nil {
		slog.Info("agent starting in proxy mode", "proxy", a.cfg.ProxyAddr, "agent_id", a.agentID)
		return a.tunnelClient.Start(ctx)
	}

	// 仅保留 gRPC 接入方式:Agent 直连 Core,或经 Proxy 中转(二者均为 gRPC 隧道)。
	// HTTP 轮询与 WebSocket 两种连接模式已禁用(先禁用,后续如需可重新启用)。
	return fmt.Errorf("agent 仅支持 gRPC 接入: 请通过 --grpc-server 直连 Core,或通过 --proxy 经代理中转;HTTP 轮询与 WebSocket 模式已禁用")
}

func (a *Agent) Stop() {
	a.stopOnce.Do(func() {
		close(a.stopCh)
		a.wsMu.Lock()
		if a.wsConn != nil {
			a.wsConn.Close(websocket.StatusNormalClosure, "agent stopping")
			a.wsConn = nil
		}
		a.wsMu.Unlock()
	})
}


// reportTaskResultHTTP 通过 HTTP 上报任务结果(WS 不可用时的 fallback)。
func (a *Agent) reportTaskResultHTTP(ctx context.Context, task TaskRequest, stdout, stderr string, exitCode int, duration time.Duration) {
	result := TaskResponse{
		TaskID:   task.TaskID,
		Success:  exitCode == 0,
		Output:   stdout,
		ExitCode: exitCode,
		Duration: duration.Milliseconds(),
		Error:    stderr,
	}
	data, _ := json.Marshal(result)
	resp, err := a.client.Post(
		a.baseURL+"/agents/"+a.agentID+"/tasks/"+task.TaskID,
		"application/json", bytes.NewReader(data))
	if err != nil {
		fmt.Printf("[agent] http report task result error: %v\n", err)
		return
	}
	resp.Body.Close()
}