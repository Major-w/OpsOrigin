package agentmgr

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/remoteexec"
	"github.com/opsorigin/core/pkg/validate"
	"golang.org/x/crypto/ssh"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterAgentRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.HostName("主机名称", req.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 勾选了 as_proxy 时,注册为 Proxy 节点
	if req.AsProxy {
		proxy := &ProxyInfo{
			TenantID: auth.TenantFromContext(r.Context()),
			Name:     req.Name,
			Address:  req.Address,
			Port:     req.Port,
			Status:   string(AgentStatusOnline),
		}
		if proxy.Port == 0 {
			proxy.Port = 8080
		}
		if err := s.store.RegisterProxy(r.Context(), proxy); err != nil {
			slog.Warn("register proxy failed", "error", err)
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, proxy)
		return
	}
	host := s.buildHost(&req, r)
	if err := s.store.Register(r.Context(), host); err != nil {
		slog.Warn("register agent failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 方案 X 闭环：主机注册即同步为 CMDB host CI（agent_hosts → CMDB 最后一跳）。
	s.syncHostCI(r.Context(), auth.TenantFromContext(r.Context()), host)
	// 方案 X:主机纳管即触发 CMDB 采集任务创建(统一调度)。
	if s.onAgentOnboard != nil {
		if e := s.onAgentOnboard(auth.TenantFromContext(r.Context()), host.ID); e != nil {
			slog.Warn("agent onboard → cmdb failed", "agent", host.ID, "err", e)
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, host)
}

func (s *Service) buildHost(req *RegisterAgentRequest, r *http.Request) *AgentHost {
	return &AgentHost{
		TenantID:      auth.TenantFromContext(r.Context()),
		Name:          req.Name,
		Type:          req.Type,
		Address:       req.Address,
		Port:          req.Port,
		OSType:        req.OSType,
		AuthType:      req.AuthType,
		SSHUser:       req.SSHUser,
		SSHPassword:   req.SSHPassword,
		SSHKey:        req.SSHKey,
		InstallMethod: req.InstallMethod,
		Labels:        req.Labels,
		ProxyID:       req.ProxyID,
	}
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	agentType := r.URL.Query().Get("type")
	hosts, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		slog.Warn("list agents failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Redis 模式下批量查在线状态，避免逐条 EXISTS N+1
	var onlineSet map[string]bool
	if s.hbWriter != nil && s.hbWriter.rdb != nil {
		ids := make([]string, 0, len(hosts))
		for _, h := range hosts {
			ids = append(ids, h.ID)
		}
		onlineSet = s.hbWriter.BulkOnlineStatus(r.Context(), ids)
	}
	now := time.Now()
	result := make([]*AgentHost, 0, len(hosts))
	for _, h := range hosts {
		if agentType != "" && string(h.Type) != agentType {
			continue
		}
		copyHost := *h
		// 优先 Redis 判定在线状态; 回退到 last_seen 时间戳
		if onlineSet != nil {
			if !onlineSet[h.ID] {
				copyHost.Status = AgentStatusOffline
			} else {
				copyHost.Status = AgentStatusOnline
			}
		} else if copyHost.LastHeartbeat != nil && now.Sub(*copyHost.LastHeartbeat) > OfflineThreshold {
			copyHost.Status = AgentStatusOffline
		}
		result = append(result, &copyHost)
	}
	// 动态标注疑似重复记录,供前端提示用户合并
	s.decorateDuplicates(result)
	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, host)
}

// decorateDuplicates 动态标注"疑似重复"关系:同一结果集内,若两条记录主机名相同
// 或 IP 有交集且 ID 不同,则将非规范记录标记为 duplicate_of 规范记录 ID,供前端提示合并。
// 规范记录优先取在线者,其次取最近心跳者。该字段仅运行时计算,不落库。
func (s *Service) decorateDuplicates(hosts []*AgentHost) {
	n := len(hosts)
	if n < 2 {
		return
	}
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	linked := func(a, b *AgentHost) bool {
		if a.Hostname != "" && a.Hostname == b.Hostname {
			return true
		}
		for _, ip := range a.IPs {
			for _, ip2 := range b.IPs {
				if ip != "" && ip == ip2 {
					return true
				}
			}
		}
		return false
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if hosts[i].ID != hosts[j].ID && linked(hosts[i], hosts[j]) {
				ra, rb := find(i), find(j)
				if ra != rb {
					parent[ra] = rb
				}
			}
		}
	}
	groups := map[int][]*AgentHost{}
	for i := 0; i < n; i++ {
		groups[find(i)] = append(groups[find(i)], hosts[i])
	}
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		canonical := g[0]
		for _, h := range g[1:] {
			if h.Status == AgentStatusOnline && canonical.Status != AgentStatusOnline {
				canonical = h
				continue
			}
			if h.Status == canonical.Status && h.LastHeartbeat != nil &&
				(canonical.LastHeartbeat == nil || h.LastHeartbeat.After(*canonical.LastHeartbeat)) {
				canonical = h
			}
		}
		for _, m := range g {
			if m.ID != canonical.ID {
				m.DuplicateOf = canonical.ID
			}
		}
	}
}

// MergeHosts 将 sourceID 合并进 targetID:target 作为规范记录保留,source 的标签(含沙箱)、
// 环境等字段合并进 target(仅补 target 缺失项),随后删除 source。在线记录不可作为 source 删除,
// 以免误删正在运行的 Agent 主机。
func (s *Service) MergeHosts(ctx context.Context, sourceID, targetID, tenantID string) error {
	if sourceID == targetID {
		return fmt.Errorf("不能将记录合并到自身")
	}
	src, err := s.store.Get(ctx, sourceID, tenantID)
	if err != nil {
		return err
	}
	tgt, err := s.store.Get(ctx, targetID, tenantID)
	if err != nil {
		return err
	}
	if src == nil || tgt == nil {
		return fmt.Errorf("记录不存在")
	}
	// 以真实在线状态(Redis 心跳)判断,而非可能陈旧的 DB status 列:
	// Redis 模式下离线检测器不回写 DB,旧记录的 status 列可能仍停留在 online。
	if s.isHostOnline(ctx, src) {
		return fmt.Errorf("源记录当前在线,无法合并删除;请改为将另一条记录合并到本条")
	}
	if src.Labels != nil {
		if tgt.Labels == nil {
			tgt.Labels = map[string]string{}
		}
		for k, v := range src.Labels {
			if _, ok := tgt.Labels[k]; !ok {
				tgt.Labels[k] = v
			}
		}
	}
	if tgt.Environment == "" && src.Environment != "" {
		tgt.Environment = src.Environment
	}
	tgt.UpdatedAt = time.Now()
	if err := s.store.Update(ctx, tgt); err != nil {
		return err
	}
	return s.store.Delete(ctx, sourceID, tenantID)
}

// isHostOnline 返回主机真实在线状态:优先 Redis 心跳(权威),回退到 DB status 列或
// last_heartbeat 时间戳。避免依赖可能陈旧的 agent_hosts.status 列(离线检测器在 Redis
// 模式下不回写该列)。
func (s *Service) isHostOnline(ctx context.Context, h *AgentHost) bool {
	if s.hbWriter != nil && s.hbWriter.rdb != nil {
		return s.hbWriter.BulkOnlineStatus(ctx, []string{h.ID})[h.ID]
	}
	if h.LastHeartbeat != nil && time.Since(*h.LastHeartbeat) <= OfflineThreshold {
		return true
	}
	return h.Status == AgentStatusOnline
}

// handleMerge 合并接口:POST /api/v1/agents/{id}/merge  body {"target_id": "..."}
// 将 {id}(疑似重复/旧记录)合并进 target_id(规范记录)。
func (s *Service) handleMerge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var body struct {
		TargetID string `json:"target_id"`
	}
	if err := httputil.BindJSON(r, &body); err != nil || body.TargetID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request: target_id required")
		return
	}
	if err := s.MergeHosts(r.Context(), id, body.TargetID, tenantID); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "merged", "source": id, "target": body.TargetID})
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	var req UpdateAgentRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.HostNameOptional("主机名称", req.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name != "" {
		host.Name = req.Name
	}
	if req.Address != "" {
		host.Address = req.Address
	}
	if req.Port != 0 {
		host.Port = req.Port
	}
	if req.SSHUser != "" {
		host.SSHUser = req.SSHUser
	}
	if req.AuthType != "" {
		host.AuthType = req.AuthType
	}
	if req.Labels != nil {
		host.Labels = req.Labels
	}
	if err := s.store.Update(r.Context(), host); err != nil {
		slog.Warn("update agent failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, host)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.Delete(r.Context(), id, tenantID); err != nil {
		slog.Warn("delete agent failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req HeartbeatRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.hbWriter.Record(r.Context(), id, tenantID, &req); err != nil {
		slog.Warn("heartbeat failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := map[string]any{
		"status":            "ok",
		"breaker_open":      false,
		"restart_requested": false,
	}

	// 心跳响应注入 ResourceGuard 配置,使 agent 本地缓存资源自保护阈值。
	rg := s.getOrCreateRG(id)
	resp["resource_guard"] = rg

	// T12: 心跳响应中注入升级指令
	if s.upgradeMgr != nil {
		if upgrade := s.upgradeMgr.CheckEligibility(id); upgrade != nil {
			resp["upgrade"] = upgrade
		}
	}

	httputil.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleBatchInstall(w http.ResponseWriter, r *http.Request) {
	var req BatchInstallRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}

	addresses, err := parseAddressList(strings.Join(req.Addresses, ","))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	serverBase := fmt.Sprintf("%s://%s", scheme, r.Host)
	tenantID := auth.TenantFromContext(r.Context())

	// 方案 X:主机纳管即触发 CMDB 采集任务创建(统一调度)。
	onboard := func(agentID string) {
		if s.onAgentOnboard != nil {
			if e := s.onAgentOnboard(tenantID, agentID); e != nil {
				slog.Warn("batch-install onboard → cmdb failed", "agent", agentID, "err", e)
			}
		}
	}

	results := make([]map[string]any, 0, len(addresses))
	for _, addr := range addresses {
		authType := "ssh"
		if req.CredentialType == "ssh_key" || req.SSHKey != "" {
			authType = "ssh_key"
		}

		// 勾选 as_proxy 时,直接注册为 Proxy 节点(不需要 SSH 连接,不做 Agent 安装)
		if req.AsProxy {
			proxy := &ProxyInfo{
				TenantID: tenantID,
				Name:     addr,
				Address:  addr,
				Port:     req.Port,
				Status:   string(AgentStatusOnline),
			}
			if proxy.Port == 0 {
				proxy.Port = 8080
			}
			if err := s.store.RegisterProxy(r.Context(), proxy); err != nil {
				results = append(results, map[string]any{"address": addr, "status": "failed", "error": err.Error()})
				continue
			}
			results = append(results, map[string]any{"address": addr, "status": "proxy_registered", "proxy_id": proxy.ID})
			continue
		}

		reachable, detail := s.testSSHConnectivity(addr, req.Port, req.Username, req.Password, req.SSHKey)
		if !reachable {
			slog.Warn("batch install SSH unreachable, register as agentless", "address", addr, "detail", detail)
			host := &AgentHost{
				TenantID:      tenantID,
				Name:          addr,
				Type:          AgentTypeAgentless,
				Address:       addr,
				Port:          req.Port,
				AuthType:      authType,
				SSHUser:       req.Username,
				SSHPassword:   req.Password,
				SSHKey:        req.SSHKey,
				InstallMethod: InstallManual,
				ProxyID:       req.ProxyID,
			}
			if err := s.store.Register(r.Context(), host); err != nil {
				results = append(results, map[string]any{"address": addr, "status": "failed", "error": err.Error()})
				continue
			}
			results = append(results, map[string]any{"address": addr, "status": "failed", "error": detail, "agent_id": host.ID})
			onboard(host.ID)
			continue
		}

		installCmd := fmt.Sprintf("curl -sSL %s/api/v1/agents/install-script?os=linux | bash", serverBase)
		output, runErr := s.runSSHCommand(addr, req.Port, req.Username, req.Password, req.SSHKey, installCmd)
		if runErr != nil {
			slog.Warn("batch install command failed, register as agentless", "address", addr, "error", runErr)
			host := &AgentHost{
				TenantID:      tenantID,
				Name:          addr,
				Type:          AgentTypeAgentless,
				Address:       addr,
				Port:          req.Port,
				AuthType:      authType,
				SSHUser:       req.Username,
				SSHPassword:   req.Password,
				SSHKey:        req.SSHKey,
				InstallMethod: InstallManual,
				ProxyID:       req.ProxyID,
			}
			s.store.Register(r.Context(), host)
			onboard(host.ID)
			results = append(results, map[string]any{"address": addr, "status": "failed", "error": runErr.Error(), "agent_id": host.ID, "output": output})
			continue
		}

		host := &AgentHost{
			TenantID:      tenantID,
			Name:          addr,
			Type:          AgentTypeAgent,
			Address:       addr,
			Port:          req.Port,
			AuthType:      authType,
			SSHUser:       req.Username,
			SSHPassword:   req.Password,
			SSHKey:        req.SSHKey,
			InstallMethod: InstallAuto,
			ProxyID:       req.ProxyID,
		}
		if err := s.store.Register(r.Context(), host); err != nil {
			results = append(results, map[string]any{"address": addr, "status": "failed", "error": err.Error()})
			continue
		}
		onboard(host.ID)
		results = append(results, map[string]any{"address": addr, "status": "installed", "agent_id": host.ID, "output": output})
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Service) handleTestSSH(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address  string `json:"address"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		SSHKey   string `json:"ssh_key,omitempty"`
		UseKey   bool   `json:"use_key,omitempty"`
		ProxyID  string `json:"proxy_id,omitempty"` // 指定则经 Proxy 探测
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	authType := "password"
	if req.UseKey || req.SSHKey != "" {
		authType = "ssh_key"
	}

	// 通过 Proxy 探测：目标在隔离网段，由 Proxy 代为 SSH 连通性检测
	if req.ProxyID != "" && s.tunnelServer != nil {
		_, _, execErr := s.execViaProxy(req.ProxyID, "ssh_test", map[string]any{
			"host":     req.Address,
			"port":     req.Port,
			"username": req.Username,
			"password": req.Password,
			"ssh_key":  req.SSHKey,
		}, 5)
		reachable := execErr == nil
		detail := "connected via proxy"
		if !reachable {
			detail = execErr.Error()
			slog.Warn("SSH test via proxy failed", "address", req.Address, "proxy_id", req.ProxyID, "detail", detail)
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"reachable": reachable,
			"address":   fmt.Sprintf("%s:%d", req.Address, req.Port),
			"auth_type": authType,
			"via":       "proxy",
			"detail":    detail,
		})
		return
	}

	reachable, detail := s.testSSHConnectivity(req.Address, req.Port, req.Username, req.Password, req.SSHKey)
	if !reachable {
		slog.Warn("SSH test failed", "address", req.Address, "detail", detail)
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"reachable": reachable,
		"address":   fmt.Sprintf("%s:%d", req.Address, req.Port),
		"auth_type": authType,
		"detail":    detail,
	})
}

// testSSHConnectivity 使用真实 SSH 握手检测目标主机连通性。
func (s *Service) testSSHConnectivity(addr string, port int, username, password, sshKey string) (bool, string) {
	config := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: remoteexec.DefaultHostKeyCallback(),
		Timeout:         5 * time.Second,
	}
	if sshKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(sshKey))
		if err != nil {
			return false, "parse key failed: " + err.Error()
		}
		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	}
	if password != "" {
		config.Auth = append(config.Auth, ssh.Password(password))
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(addr, fmt.Sprintf("%d", port)), config)
	if err != nil {
		return false, err.Error()
	}
	client.Close()
	return true, "connected"
}

// runSSHCommand 通过 SSH 在远程主机执行命令并返回合并输出。
func (s *Service) runSSHCommand(addr string, port int, username, password, sshKey, cmd string) (string, error) {
	config := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: remoteexec.DefaultHostKeyCallback(),
		Timeout:         10 * time.Second,
	}
	if sshKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(sshKey))
		if err != nil {
			return "", fmt.Errorf("parse key: %w", err)
		}
		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	}
	if password != "" {
		config.Auth = append(config.Auth, ssh.Password(password))
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(addr, fmt.Sprintf("%d", port)), config)
	if err != nil {
		return "", err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	out, err := session.CombinedOutput(cmd)
	return string(out), err
}

func (s *Service) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	osType := r.URL.Query().Get("os")
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = "amd64"
	}
	serverAddr := r.Host
	if serverAddr == "" {
		serverAddr = "localhost:8080"
	}
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		serverAddr = h
	}
	// 确定 scheme:优先 X-Forwarded-Proto,其次 TLS 配置,再次 r.TLS,默认 http
	scheme := s.determineScheme(r)
	// proxy 参数值为 Proxy 的地址(host:port),非空时在启动命令中追加 --proxy
	proxyAddr := r.URL.Query().Get("proxy")
	proxyFlag := ""
	if proxyAddr != "" {
		proxyFlag = " --proxy " + proxyAddr
	}

	var script string
	var filename string
	switch osType {
	case "windows":
		filename = "install-agent.ps1"
		script = fmt.Sprintf(`# OpsOrigin Agent Installer (Windows, arch=%s)
$ErrorActionPreference = "Stop"
$server = "%s"
$scheme = "%s"
$api = "$($scheme)://$($server)/api/v1/agents/download?os=windows&arch=%s"
$installPath = "C:\Program Files\OpsOrigin"
New-Item -ItemType Directory -Force -Path $installPath | Out-Null
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -Uri $api -OutFile "$installPath\agent.exe"
New-Service -Name "OpsOriginAgent" -BinaryPathName "$installPath\agent.exe --server $server%s" -DisplayName "OpsOrigin Agent" -StartupType Automatic
Start-Service -Name "OpsOriginAgent"
Write-Host "OpsOrigin Agent installed successfully (arch=%s)"`, arch, serverAddr, scheme, arch, proxyFlag, arch)
	case "linux":
		filename = "install-agent.sh"
		script = fmt.Sprintf(`#!/bin/bash
# OpsOrigin Agent Installer (Linux, arch=%s)
set -e
SERVER="%s"
SCHEME="%s"
API="$SCHEME://$SERVER/api/v1/agents/download?os=linux&arch=%s"
INSTALL_DIR="/opt/opsorigin"
sudo mkdir -p $INSTALL_DIR
sudo curl -sSL $API -o $INSTALL_DIR/agent
sudo chmod +x $INSTALL_DIR/agent
sudo tee /etc/systemd/system/opsorigin-agent.service > /dev/null <<EOF
[Unit]
Description=OpsOrigin Agent
After=network.target
[Service]
Type=simple
ExecStart=$INSTALL_DIR/agent --server $SERVER%s
Restart=always
RestartSec=10s
ExecStartPre=/bin/bash -c 'sleep $((RANDOM %% 30))'
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable opsorigin-agent
sudo systemctl start opsorigin-agent
echo "OpsOrigin Agent installed successfully (arch=%s)"`, arch, serverAddr, scheme, arch, proxyFlag, arch)
	case "container":
		filename = "install-agent-container.sh"
		// 镜像名可配置:支持 ?image=<registry>/opsorigin-agent:tag,默认 opsorigin/agent:latest。
		// 注意:--server 与 OPSORIGIN_SERVER 均**不带 scheme**,agent 内部自行拼接 http://。
		image := r.URL.Query().Get("image")
		if image == "" {
			image = "opsorigin/agent:latest"
		}
		script = fmt.Sprintf(`#!/bin/bash
# OpsOrigin Agent Installer (Container / Docker)
set -e
SERVER="%s"
IMAGE="%s"

# 检查 Docker 是否可用
if ! command -v docker >/dev/null 2>&1; then
  echo "[ERROR] docker not found. Please install Docker first."
  exit 1
fi

# 拉取并运行 Agent 容器(自动注册到 Server)
# 挂载宿主 /proc、/sys、docker.sock 只读视图以采集宿主机指标;
# 如需采集容器集群自身,可额外挂载 /var/lib/docker 或改为 DaemonSet 部署。
docker run -d \
  --name opsorigin-agent \
  --restart always \
  -e OPSORIGIN_SERVER="%s" \
  -v /etc/hostname:/etc/hostname:ro \
  -v /etc/os-release:/etc/os-release:ro \
  -v /proc:/host/proc:ro \
  -v /sys:/host/sys:ro \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  "$IMAGE" \
  --server "%s"%s

echo "OpsOrigin Agent container started successfully"
echo "View logs: docker logs -f opsorigin-agent"`, serverAddr, image, serverAddr, serverAddr, proxyFlag)
	case "proxy":
		proxyID := r.URL.Query().Get("proxy_id")
		if proxyID == "" {
			proxyID = fmt.Sprintf("proxy-%s", uuid.New().String()[:8])
		}
		listenPort := r.URL.Query().Get("port")
		if listenPort == "" {
			listenPort = "9090"
		}
		filename = "install-proxy.sh"
		script = fmt.Sprintf(`#!/bin/bash
# OpsOrigin Proxy Installer
set -e
SERVER="%s"
SCHEME="%s"
PROXY_ID="%s"
LISTEN_PORT="%s"
API="$SCHEME://$SERVER/api/v1/agents/download?os=linux&arch=amd64&type=proxy"
INSTALL_DIR="/opt/opsorigin/proxy"
sudo mkdir -p $INSTALL_DIR
sudo curl -sSL $API -o $INSTALL_DIR/opsproxy
sudo chmod +x $INSTALL_DIR/opsproxy
sudo tee $INSTALL_DIR/config.yaml > /dev/null <<EOF
proxy:
  id: "$PROXY_ID"
  core_addr: "$SERVER"
  listen: ":$LISTEN_PORT"
EOF
sudo tee /etc/systemd/system/opsorigin-proxy.service > /dev/null <<EOF
[Unit]
Description=OpsOrigin Proxy
After=network.target
[Service]
Type=simple
ExecStart=$INSTALL_DIR/opsproxy -config $INSTALL_DIR/config.yaml
Restart=always
RestartSec=10s
ExecStartPre=/bin/bash -c 'sleep $((RANDOM %% 30))'
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable opsorigin-proxy
sudo systemctl start opsorigin-proxy
echo "OpsOrigin Proxy installed successfully (id=$PROXY_ID, listen=:$LISTEN_PORT)"`, serverAddr, scheme, proxyID, listenPort)
	default:
		httputil.WriteError(w, http.StatusBadRequest, "os parameter required: linux, windows, container, or proxy")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

// determineScheme 判断当前请求的 scheme(用于生成安装脚本中的下载链接)。
// 优先级: X-Forwarded-Proto > tlsEnabled 配置 > r.TLS > 默认 http
func (s *Service) determineScheme(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto == "https" {
		return "https"
	}
	if s.tlsEnabled {
		return "https"
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
