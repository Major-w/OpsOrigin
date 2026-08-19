package agentmgr

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/token"
	"github.com/opsorigin/core/internal/tunnel"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	var req TaskRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.store.CreateTask(r.Context(), agentID, &req); err != nil {
		slog.Warn("create task failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Proxy 路由:如果 Agent 通过 Proxy 连接,优先通过 tunnel 下发命令
	if s.tunnelServer != nil {
		if host, err := s.store.Get(r.Context(), agentID, auth.TenantFromContext(r.Context())); err == nil && host != nil && host.ProxyID != "" {
			payloadBytes, err := json.Marshal(req)
			if err != nil {
				slog.Warn("marshal task payload failed", "error", err)
			} else {
				cmd := &tunnel.CommandRequest{
					CommandID:     req.TaskID,
					TargetID:      agentID,
					Action:        req.Action,
					Payload:       payloadBytes,
					TimeoutSecond: int32(req.Timeout),
				}
				resp, err := s.tunnelServer.SendCommand(host.ProxyID, cmd)
				if err == nil && resp != nil && resp.Success {
					taskResp := &TaskResponse{
						TaskID:  req.TaskID,
						Success: true,
						Output:  string(resp.Output),
					}
					if err := s.store.CompleteTask(r.Context(), agentID, req.TaskID, taskResp); err != nil {
						slog.Warn("complete task via proxy failed", "error", err, "agent_id", agentID, "task_id", req.TaskID)
					}
					slog.Info("task dispatched via proxy", "agent_id", agentID, "task_id", req.TaskID, "proxy_id", host.ProxyID)
					httputil.WriteJSON(w, http.StatusOK, map[string]any{"task_id": req.TaskID, "status": "completed"})
					return
				}
				// 失败则降级到 WS/HTTP
				slog.Warn("proxy route failed, falling back to WS/HTTP", "error", err, "proxy_id", host.ProxyID)
			}
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, req)
}

func (s *Service) handleListTasks(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	tasks, err := s.store.ListTasks(r.Context(), agentID)
	if err != nil {
		slog.Warn("list tasks failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []*TaskInfo{}
	}
	httputil.WriteJSON(w, http.StatusOK, tasks)
}

func (s *Service) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	taskID := r.PathValue("task_id")
	var resp TaskResponse
	if err := httputil.BindJSON(r, &resp); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if resp.TaskID == "" {
		resp.TaskID = taskID
	}
	if err := s.store.CompleteTask(r.Context(), agentID, taskID, &resp); err != nil {
		slog.Warn("complete task failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 采集任务完成: 按类型解析输出入库(CMDB 更新 / 指标记录)
	s.handleCollectCompletion(r.Context(), agentID, taskID, &resp)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleDownloadTasks 返回该 agent 待执行的 pending 任务。
func (s *Service) handleDownloadTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	tasks, err := s.store.ListTasks(r.Context(), id)
	if err != nil {
		slog.Warn("download tasks failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pending := make([]*TaskInfo, 0)
	for _, t := range tasks {
		if t.Status == TaskStatusPending {
			pending = append(pending, t)
		}
	}
	httputil.WriteJSON(w, http.StatusOK, pending)
}

// handleCreateDownloadTask 创建文件分发任务(§5):
//  1. 校验目标 Agent 存在;
//  2. 依拓扑(host.ProxyID)选择直连 Server 或经 Proxy 的基址;
//  3. 签发 §6 短期下载令牌(file_id + agent_id 绑定);
//  4. 生成含令牌的下载 URL,落库为 pending 任务,Agent 轮询后直连/经代理拉取。
func (s *Service) handleCreateDownloadTask(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	var req downloadTaskRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.FileID == "" || req.FilePath == "" {
		httputil.WriteError(w, http.StatusBadRequest, "file_id and file_path are required")
		return
	}
	if s.fileTokenSecret == "" {
		httputil.WriteError(w, http.StatusInternalServerError, "file token secret not configured")
		return
	}

	host, err := s.store.Get(r.Context(), agentID, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}

	// 拓扑感知:经 Proxy 的 Agent 从 Proxy 公网地址拉取,否则直连 Server。
	base := s.serverBaseURL + "/api/v1"
	if host.ProxyID != "" {
		proxy, perr := s.store.GetProxy(r.Context(), host.ProxyID, tenantID)
		if perr != nil || proxy == nil {
			httputil.WriteError(w, http.StatusBadRequest, "agent's proxy not found")
			return
		}
		scheme := "http"
		if s.tlsEnabled {
			scheme = "https"
		}
		base = fmt.Sprintf("%s://%s:%d/api/v1", scheme, proxy.Address, proxy.Port)
	} else if s.serverBaseURL == "" {
		httputil.WriteError(w, http.StatusInternalServerError, "server base url not configured")
		return
	}

	ttl := time.Duration(req.TTLSec) * time.Second
	if ttl <= 0 {
		ttl = token.MaxTTL()
	}
	tok, err := token.Generate(s.fileTokenSecret, req.FileID, agentID, ttl)
	if err != nil {
		slog.Warn("mint download token failed", "error", err, "agent_id", agentID)
		httputil.WriteError(w, http.StatusInternalServerError, "mint token failed")
		return
	}

	task := &TaskRequest{
		TaskID:           uuid.New().String(),
		FileID:           req.FileID,
		FileURL:          buildFileURL(base, req.FileID, tok),
		FilePath:         req.FilePath,
		ExpectedChecksum: req.ExpectedChecksum,
		ChecksumAlgo:     req.ChecksumAlgo,
	}
	if err := s.store.CreateTask(r.Context(), agentID, task); err != nil {
		slog.Warn("create download task failed", "error", err, "agent_id", agentID)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("download task created",
		"agent_id", agentID, "file_id", req.FileID,
		"via_proxy", host.ProxyID != "", "ttl", ttl.String())
	httputil.WriteJSON(w, http.StatusCreated, task)
}
