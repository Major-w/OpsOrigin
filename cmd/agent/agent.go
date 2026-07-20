package main

import (
	"bytes"
	"context"
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
	"github.com/opsorigin/core/internal/tunnel"
)

// AgentConfig holds runtime configuration.
type AgentConfig struct {
	ServerAddr string // e.g. "192.168.1.1:8080"
	WSServer   string // e.g. "ws://192.168.1.1:5588/agent-ws" (可选,启用长连接)
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
type FileDownloadRequest struct {
	FileURL  string `json:"file_url"`
	FilePath string `json:"file_path"`
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

func (a *Agent) DownloadFile(fileURL, filePath string) error {
	resp, err := a.client.Get(fileURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}
	
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	
	_, err = io.Copy(file, resp.Body)
	return err
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
		if err := a.DownloadFile(task.FileURL, task.FilePath); err != nil {
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

	if err := a.Register(ctx); err != nil {
		return err
	}

	go a.HeartbeatLoop(ctx)
	go a.PollTasks(ctx)
	go a.DownloadFileLoop(ctx)

	// 如果配置了 WebSocket 服务器,启动长连接 goroutine
	// 长连接启用后,任务下发和结果上报优先走 WS;HTTP 轮询作为 fallback
	if a.cfg.WSServer != "" {
		go a.WebSocketLoop(ctx)
	}

	// Report CMDB data on startup
	if err := a.ReportCMDBData(); err != nil {
		fmt.Printf("[agent] CMDB report error: %v\n", err)
	}

	info := a.collectSystemInfo()
	metrics := []MetricReport{
		{AgentID: a.agentID, Name: "agent.cpu.cores", Value: float64(runtime.NumCPU()), Unit: "cores", Timestamp: time.Now().UnixMilli()},
		{AgentID: a.agentID, Name: "agent.os", Value: 1, Unit: "count", Timestamp: time.Now().UnixMilli()},
	}
	_ = a.ReportMetrics(metrics)

	data, _ := json.MarshalIndent(info, "", "  ")
	fmt.Printf("[agent] system info collected:\n%s\n", string(data))
	fmt.Printf("[agent] running, heartbeat interval=%s\n", a.cfg.Interval)

	metricTicker := time.NewTicker(60 * time.Second)
	cmdbTicker := time.NewTicker(5 * time.Minute)
	defer metricTicker.Stop()
	defer cmdbTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-a.stopCh:
			return nil
		case t := <-metricTicker.C:
			_ = a.ReportMetrics([]MetricReport{{AgentID: a.agentID, Name: "agent.heartbeat", Value: 1, Unit: "count", Timestamp: t.UnixMilli()}})
		case <-cmdbTicker.C:
			if err := a.ReportCMDBData(); err != nil {
				fmt.Printf("[agent] CMDB report error: %v\n", err)
			}
		}
	}
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

// --- WebSocket Long Connection ---

// WebSocketLoop 维护与 Server 的 WebSocket 长连接,自动重连。
// 通过长连接接收任务下发,执行后通过长连接上报结果。
func (a *Agent) WebSocketLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		default:
		}

		err := a.connectAndServeWS(ctx)
		if err != nil {
			fmt.Printf("[agent] websocket error: %v, reconnecting in 5s...\n", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// connectAndServeWS 建立一次 WebSocket 连接并处理消息,直到连接断开。
func (a *Agent) connectAndServeWS(ctx context.Context) error {
	// 构建 WS URL,附加 agent_id 参数
	wsURL := a.cfg.WSServer
	if a.agentID != "" {
		sep := "?"
		if strings.Contains(wsURL, "?") {
			sep = "&"
		}
		wsURL = wsURL + sep + "agent_id=" + a.agentID
	}

	fmt.Printf("[agent] connecting to websocket: %s\n", wsURL)

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}

	a.wsMu.Lock()
	a.wsConn = conn
	a.wsMu.Unlock()

	defer func() {
		conn.Close(websocket.StatusNormalClosure, "")
		a.wsMu.Lock()
		a.wsConn = nil
		a.wsMu.Unlock()
	}()

	fmt.Printf("[agent] websocket connected to server\n")

	// 启动 ping goroutine
	go a.wsPingLoop(ctx, conn)

	// 读循环:接收 Server 下发的任务
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-a.stopCh:
			return nil
		default:
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("ws read: %w", err)
		}

		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			fmt.Printf("[agent] ws unmarshal error: %v\n", err)
			continue
		}

		msgType, _ := msg["type"].(string)
		switch msgType {
		case "task":
			// 解析为 TaskRequest 并执行
			taskData, _ := json.Marshal(msg)
			var task TaskRequest
			if err := json.Unmarshal(taskData, &task); err != nil {
				fmt.Printf("[agent] ws parse task error: %v\n", err)
				continue
			}
			fmt.Printf("[agent] task received via ws: %s\n", task.TaskID)
			go a.executeTaskAndReportWS(ctx, task, conn)
		case "ping":
			// 响应 ping
			pong, _ := json.Marshal(map[string]string{"type": "pong"})
			conn.Write(ctx, websocket.MessageText, pong)
		default:
			// 忽略未知消息
		}
	}
}

// wsPingLoop 定期发送 ping 保持连接活跃。
func (a *Agent) wsPingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			ping, _ := json.Marshal(map[string]string{"type": "ping"})
			if err := conn.Write(ctx, websocket.MessageText, ping); err != nil {
				return
			}
		}
	}
}

// executeTaskAndReportWS 执行任务并通过 WebSocket 上报结果。
func (a *Agent) executeTaskAndReportWS(ctx context.Context, task TaskRequest, conn *websocket.Conn) {
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

	// 构建 WS 结果消息
	result := map[string]any{
		"type":        "task_result",
		"task_id":     task.TaskID,
		"success":     exitCode == 0,
		"output":      stdout.String(),
		"exit_code":   exitCode,
		"duration_ms": duration.Milliseconds(),
		"error":       stderr.String(),
		"time":        time.Now().UnixMilli(),
	}

	data, _ := json.Marshal(result)
	writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer writeCancel()
	if err := conn.Write(writeCtx, websocket.MessageText, data); err != nil {
		// WS 写失败,回退到 HTTP 上报
		fmt.Printf("[agent] ws report failed, falling back to http: %v\n", err)
		a.reportTaskResultHTTP(ctx, task, stdout.String(), stderr.String(), exitCode, duration)
		return
	}
	fmt.Printf("[agent] task %s completed via ws in %dms, exit=%d\n",
		task.TaskID, duration.Milliseconds(), exitCode)
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