package deploy

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/httputil"
)

func NewService(db *sql.DB) *Service { return &Service{store: NewStore(db)} }

// AgentRunner 是部署脚本下发的执行通道契约（鸭子类型）。
// *agentmgr.Service 的 RunScriptOnAgent 方法签名天然满足本接口，
// 故 main.go 可直接 deploySvc.SetAgentRunner(agentSvc) 注入，无需 import agentmgr。
type AgentRunner interface {
	RunScriptOnAgent(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, runAs string) (string, int, error)
}

type Service struct {
	store  *Store
	runner AgentRunner // 真实部署通道（经 Tunnel 下发到 Agent 主机），可为 nil（降级为只读）
}

// SetAgentRunner 注入 Agent 执行通道，使部署真正在目标主机运行，而非 Core 本机模拟。
func (s *Service) SetAgentRunner(r AgentRunner) { s.runner = r }

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	// Apps
	mux.HandleFunc("GET /api/v1/deploy/apps", s.handleListApps)
	mux.HandleFunc("POST /api/v1/deploy/apps", s.handleCreateApp)
	mux.HandleFunc("PUT /api/v1/deploy/apps/{id}", s.handleUpdateApp)
	mux.HandleFunc("DELETE /api/v1/deploy/apps/{id}", s.handleDeleteApp)
	// Releases
	mux.HandleFunc("GET /api/v1/deploy/releases", s.handleListReleases)
	mux.HandleFunc("POST /api/v1/deploy/releases", s.handleCreateRelease)
	mux.HandleFunc("PUT /api/v1/deploy/releases/{id}", s.handleUpdateRelease)
	mux.HandleFunc("DELETE /api/v1/deploy/releases/{id}", s.handleDeleteRelease)
	mux.HandleFunc("POST /api/v1/deploy/releases/{id}/approve", s.handleApproveRelease)
	mux.HandleFunc("POST /api/v1/deploy/releases/{id}/reject", s.handleRejectRelease)
	mux.HandleFunc("POST /api/v1/deploy/releases/{id}/deploy", s.handleDeployRelease)
	mux.HandleFunc("POST /api/v1/deploy/releases/{id}/rollback", s.handleRollbackRelease)
	// Environments
	mux.HandleFunc("GET /api/v1/deploy/environments", s.handleListEnvs)
	mux.HandleFunc("POST /api/v1/deploy/environments", s.handleCreateEnv)
	mux.HandleFunc("PUT /api/v1/deploy/environments/{id}", s.handleUpdateEnv)
	mux.HandleFunc("DELETE /api/v1/deploy/environments/{id}", s.handleDeleteEnv)
	// History
	mux.HandleFunc("GET /api/v1/deploy/history", s.handleListHistory)
}

func (s *Service) handleListApps(w http.ResponseWriter, r *http.Request) {
	apps, _ := s.store.ListApps(r.Context(), tenant(r))
	httputil.WriteJSON(w, 200, apps)
}
func (s *Service) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var a DeployApp
	httputil.BindJSON(r, &a)
	a.TenantID = tenant(r)
	s.store.CreateApp(r.Context(), &a)
	httputil.WriteJSON(w, 201, a)
}
func (s *Service) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var a DeployApp
	httputil.BindJSON(r, &a)
	a.ID = id
	a.TenantID = tenant(r)
	s.store.UpdateApp(r.Context(), &a)
	httputil.WriteJSON(w, 200, a)
}
func (s *Service) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.store.DeleteApp(r.Context(), id, tenant(r))
	httputil.WriteJSON(w, 200, map[string]string{"ok": "deleted"})
}

func (s *Service) handleListReleases(w http.ResponseWriter, r *http.Request) {
	rel, _ := s.store.ListReleases(r.Context(), tenant(r))
	httputil.WriteJSON(w, 200, rel)
}
func (s *Service) handleCreateRelease(w http.ResponseWriter, r *http.Request) {
	var r2 ReleaseTicket
	httputil.BindJSON(r, &r2)
	r2.TenantID = tenant(r)
	if r2.AppName == "" {
		var a DeployApp
		apps, _ := s.store.ListApps(r.Context(), tenant(r))
		for _, app := range apps {
			if app.ID == r2.AppID {
				a = app
				break
			}
		}
		r2.AppName = a.Name
		r2.AppCode = a.Code
	}
	if r2.Operator == "" {
		r2.Operator = operatorOf(r)
	}
	s.store.CreateRelease(r.Context(), &r2)
	httputil.WriteJSON(w, 201, r2)
}
func (s *Service) handleUpdateRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var r2 ReleaseTicket
	httputil.BindJSON(r, &r2)
	r2.ID = id
	r2.TenantID = tenant(r)
	s.store.UpdateRelease(r.Context(), &r2)
	httputil.WriteJSON(w, 200, r2)
}
func (s *Service) handleDeleteRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.store.DeleteRelease(r.Context(), id, tenant(r))
	httputil.WriteJSON(w, 200, map[string]string{"ok": "deleted"})
}

// handleApproveRelease 审批通过，校验审批人是否在 approvers 名单内（名单非空时）。
func (s *Service) handleApproveRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t := tenant(r)
	rel, err := s.store.GetRelease(r.Context(), id, t)
	if err != nil || rel == nil {
		httputil.WriteJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	operator := operatorOf(r)
	if len(rel.Approvers) > 0 && !contains(rel.Approvers, operator) {
		httputil.WriteJSON(w, 403, map[string]string{"error": "审批人不在 approvers 名单内", "operator": operator})
		return
	}
	rel.Status = "approved"
	rel.ApprovedBy = operator
	now := time.Now()
	rel.ApprovedAt = &now
	s.store.UpdateRelease(r.Context(), rel)
	httputil.WriteJSON(w, 200, rel)
}
func (s *Service) handleRejectRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t := tenant(r)
	var body struct {
		Reason string `json:"reason"`
	}
	httputil.BindJSON(r, &body)
	rel, err := s.store.GetRelease(r.Context(), id, t)
	if err != nil || rel == nil {
		httputil.WriteJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	rel.Status = "rejected"
	rel.RejectedBy = operatorOf(r)
	rel.RejectReason = body.Reason
	s.store.UpdateRelease(r.Context(), rel)
	httputil.WriteJSON(w, 200, rel)
}

// handleDeployRelease 触发真实部署：先置 deploying，再异步经 Agent 通道执行。
func (s *Service) handleDeployRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t := tenant(r)
	rel, err := s.store.GetRelease(r.Context(), id, t)
	if err != nil || rel == nil {
		httputil.WriteJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	if rel.Status == "deploying" {
		httputil.WriteJSON(w, 409, map[string]string{"error": "deploy already in progress"})
		return
	}
	rel.Operator = operatorOf(r)
	rel.Status = "deploying"
	now := time.Now()
	rel.DeployedAt = &now
	if err := s.store.UpdateRelease(r.Context(), rel); err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	// 脱离请求上下文，避免客户端断开导致部署中断。
	go s.executeDeploy(rel)
	httputil.WriteJSON(w, 202, rel)
}

// handleRollbackRelease 回滚到该应用+环境的上一成功版本。
// 容器模式走 kubectl rollout undo；主机模式重新下发上一成功版本的部署脚本。
func (s *Service) handleRollbackRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t := tenant(r)
	rel, err := s.store.GetRelease(r.Context(), id, t)
	if err != nil || rel == nil {
		httputil.WriteJSON(w, 404, map[string]string{"error": "not found"})
		return
	}

	apps, _ := s.store.ListApps(r.Context(), t)
	var app DeployApp
	for _, a := range apps {
		if a.ID == rel.AppID {
			app = a
			break
		}
	}
	env := s.findEnv(r.Context(), t, rel.Env)
	operator := operatorOf(r)

	// 容器模式：kubectl rollout undo 回到上一 ReplicaSet。
	if env != nil && env.Mode == ModeContainer {
		out, ok := s.runContainerRollback(r.Context(), t, app, env)
		rel.Status = "rolled_back"
		s.store.UpdateRelease(r.Context(), rel)
		note := operator + " 触发回滚（容器模式）"
		if !ok {
			note += " 失败:\n" + out
		}
		s.historyFor(rel, "rolled_back", note+"\n"+out)
		httputil.WriteJSON(w, 200, rel)
		return
	}

	// 主机模式：重新下发上一成功版本的部署脚本。
	prev, _ := s.findPrevSucceeded(r.Context(), t, rel.AppID, rel.Env, rel.ID)
	out := ""
	if app.DeployScript != "" && env != nil && len(env.AgentIDs) > 0 && prev != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		params := map[string]string{
			"APP": app.Code, "VERSION": prev.Version, "ENV": rel.Env,
			"PORT": fmt.Sprintf("%d", app.TargetPort), "ROLLBACK": "true",
		}
		var b strings.Builder
		ok := true
		for _, agentID := range env.AgentIDs {
			o, code, e := s.runner.RunScriptOnAgent(ctx, agentID, t, app.DeployScript, params, 600, "")
			fmt.Fprintf(&b, "[agent %s] exit=%d err=%v\n%s\n", agentID, code, e, o)
			if e != nil || code != 0 {
				ok = false
			}
		}
		out = b.String()
		if !ok {
			rel.Status = "rolled_back"
			s.store.UpdateRelease(r.Context(), rel)
			s.historyFor(rel, "rolled_back", "回滚脚本下发失败:\n"+out)
			httputil.WriteJSON(w, 200, rel)
			return
		}
	}
	rel.Status = "rolled_back"
	s.store.UpdateRelease(r.Context(), rel)
	note := operator + " 触发回滚"
	if prev != nil {
		note += " 至版本 " + prev.Version
	}
	s.historyFor(rel, "rolled_back", note)
	httputil.WriteJSON(w, 200, rel)
}

// executeDeploy 是真实部署引擎：依据环境 Mode 在目标落地下发指令。
//   - host 模式：经主机 Agent 下发 DeployScript（shell）。
//   - container 模式：经 K8s 网关 Agent 下发 kubectl（set image / apply）。
//
// 每一步都如实写入 running→succeeded/failed/skipped 与输出；任一失败则整体 failed。
func (s *Service) executeDeploy(rel *ReleaseTicket) {
	if s.runner == nil {
		rel.Status = "failed"
		s.persist(context.Background(), rel)
		s.historyFor(rel, "failed", "部署通道未配置（AgentRunner 为空），无法下发部署脚本")
		slog.Warn("deploy skipped: no agent runner", "release", rel.ID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	t := rel.TenantID

	apps, _ := s.store.ListApps(ctx, t)
	var app DeployApp
	for _, a := range apps {
		if a.ID == rel.AppID {
			app = a
			break
		}
	}
	env := s.findEnv(ctx, t, rel.Env)

	rel.Steps = nil
	failed := false

	// 步骤一：部署（按环境模式分支）
	step := DeployStep{ID: "deploy", Name: "部署", Status: "running"}
	rel.Steps = append(rel.Steps, step)
	s.persist(ctx, rel)

	var deployOut strings.Builder
	if env == nil {
		step.Status = "failed"
		step.Output = "找不到该环境类型的部署环境配置"
	} else if len(env.AgentIDs) == 0 {
		step.Status = "failed"
		step.Output = "目标环境未绑定执行 Agent（agent_ids 为空），无法下发部署指令"
	} else if env.Mode == ModeContainer {
		ok, out := s.runContainerDeploy(ctx, t, app, env, rel.Version)
		step.Output = out
		if ok {
			step.Status = "succeeded"
		} else {
			step.Status = "failed"
			failed = true
		}
	} else {
		// 主机模式：下发 DeployScript
		if app.DeployScript == "" {
			step.Status = "failed"
			step.Output = "应用未配置部署脚本（deploy_script），无法执行部署"
		} else {
			params := map[string]string{
				"APP": app.Code, "VERSION": rel.Version, "ENV": rel.Env,
				"PORT": fmt.Sprintf("%d", app.TargetPort),
			}
			ok := true
			for _, agentID := range env.AgentIDs {
				o, code, e := s.runner.RunScriptOnAgent(ctx, agentID, t, app.DeployScript, params, 600, "")
				fmt.Fprintf(&deployOut, "[agent %s] exit=%d err=%v\n%s\n", agentID, code, e, o)
				if e != nil || code != 0 {
					ok = false
				}
			}
			step.Output = deployOut.String()
			if ok {
				step.Status = "succeeded"
			} else {
				step.Status = "failed"
				failed = true
			}
		}
	}
	s.persist(ctx, rel)

	// 步骤二：健康检查（按环境模式分支）
	hc := DeployStep{ID: "health_check", Name: "健康检查", Status: "running"}
	rel.Steps = append(rel.Steps, hc)
	s.persist(ctx, rel)
	if env != nil && env.Mode == ModeContainer {
		ok, out := s.runContainerHealth(ctx, t, app, env)
		hc.Output = out
		if ok {
			hc.Status = "succeeded"
		} else {
			hc.Status = "failed"
			failed = true
		}
	} else {
		if app.HealthCheckURL == "" {
			hc.Status = "skipped"
			hc.Output = "应用未配置健康检查 URL（health_check_url），跳过"
		} else {
			code, body, e := httpProbe(app.HealthCheckURL, 8*time.Second)
			if e != nil {
				hc.Status = "failed"
				hc.Output = fmt.Sprintf("健康检查请求失败: %v", e)
				failed = true
			} else if code < 200 || code >= 300 {
				hc.Status = "failed"
				hc.Output = fmt.Sprintf("健康检查返回非 2xx: HTTP %d\n%s", code, truncate(body, 512))
				failed = true
			} else {
				hc.Status = "succeeded"
				hc.Output = fmt.Sprintf("健康检查通过: HTTP %d", code)
			}
		}
	}
	s.persist(ctx, rel)

	// 汇总结果
	if failed {
		rel.Status = "failed"
		s.persist(ctx, rel)
		s.historyFor(rel, "failed", deployOut.String())
		slog.Warn("deploy failed", "release", rel.ID, "app", rel.AppName, "env", rel.Env)
		return
	}
	rel.Status = "succeeded"
	if app.ID != "" {
		if app.CurrentVersions == nil {
			app.CurrentVersions = map[DeployEnv]string{}
		}
		app.CurrentVersions[rel.Env] = rel.Version
		s.store.UpdateApp(ctx, &app)
	}
	s.persist(ctx, rel)
	s.historyFor(rel, "succeeded", deployOut.String())
	slog.Info("deploy completed", "release", rel.ID, "app", rel.AppName, "env", rel.Env, "mode", string(env.Mode), "version", rel.Version)
}

// runContainerDeploy 容器模式部署：经 K8s 网关 Agent 下发 kubectl 指令。
func (s *Service) runContainerDeploy(ctx context.Context, t string, app DeployApp, env *DeployEnvironment, version string) (bool, string) {
	if app.ImageURL == "" || app.Workload == "" {
		return false, "容器模式部署需要配置 image_url 与 workload"
	}
	script := buildContainerDeployScript(app, version)
	var b strings.Builder
	ok := true
	for _, agentID := range env.AgentIDs {
		o, code, e := s.runner.RunScriptOnAgent(ctx, agentID, t, script, nil, 600, "")
		fmt.Fprintf(&b, "[agent %s] exit=%d err=%v\n%s\n", agentID, code, e, o)
		if e != nil || code != 0 {
			ok = false
		}
	}
	return ok, b.String()
}

// runContainerHealth 容器模式探活：kubectl rollout status；
// 若配置了 HTTP 健康检查 URL，再叠加一次 HTTP 探活。
func (s *Service) runContainerHealth(ctx context.Context, t string, app DeployApp, env *DeployEnvironment) (bool, string) {
	if app.Namespace == "" || app.Workload == "" {
		return true, "容器模式未配置 namespace/workload，跳过集群探活"
	}
	cmd := fmt.Sprintf("kubectl -n %s rollout status %s --timeout=120s", app.Namespace, app.Workload)
	var b strings.Builder
	agentID := env.AgentIDs[0]
	o, code, e := s.runner.RunScriptOnAgent(ctx, agentID, t, cmd, nil, 600, "")
	fmt.Fprintf(&b, "[agent %s] exit=%d err=%v\n%s\n", agentID, code, e, o)
	if e != nil || code != 0 {
		return false, b.String()
	}
	if app.HealthCheckURL != "" {
		hc, body, e2 := httpProbe(app.HealthCheckURL, 8*time.Second)
		fmt.Fprintf(&b, "HTTP probe: code=%d err=%v body=%s\n", hc, e2, truncate(body, 256))
		if e2 != nil || hc < 200 || hc >= 300 {
			return false, b.String()
		}
	}
	return true, b.String()
}

// runContainerRollback 容器模式回滚：kubectl rollout undo 回到上一 ReplicaSet。
func (s *Service) runContainerRollback(ctx context.Context, t string, app DeployApp, env *DeployEnvironment) (string, bool) {
	if app.Namespace == "" || app.Workload == "" {
		return "容器模式回滚需要配置 namespace/workload", false
	}
	cmd := fmt.Sprintf("kubectl -n %s rollout undo %s", app.Namespace, app.Workload)
	var b strings.Builder
	ok := true
	for _, agentID := range env.AgentIDs {
		o, code, e := s.runner.RunScriptOnAgent(ctx, agentID, t, cmd, nil, 600, "")
		fmt.Fprintf(&b, "[agent %s] exit=%d err=%v\n%s\n", agentID, code, e, o)
		if e != nil || code != 0 {
			ok = false
		}
	}
	return b.String(), ok
}

// buildContainerDeployScript 依据应用容器配置生成 kubectl 部署脚本。
// 若配置了 manifest 则优先 kubectl apply -f -；否则 kubectl set image。
func buildContainerDeployScript(app DeployApp, version string) string {
	ns := app.Namespace
	if ns == "" {
		ns = "default"
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	if app.Manifest != "" {
		b.WriteString("cat <<'OO_MANIFEST' | kubectl -n " + ns + " apply -f -\n")
		b.WriteString(app.Manifest)
		if !strings.HasSuffix(app.Manifest, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("OO_MANIFEST\n")
	} else {
		cnt := app.ContainerName
		if cnt == "" {
			cnt = app.Code
		}
		if cnt == "" {
			cnt = app.Name
		}
		img := imageWithTag(app.ImageURL, version)
		b.WriteString(fmt.Sprintf("kubectl -n %s set image %s %s=%s --record\n", ns, app.Workload, cnt, img))
	}
	return b.String()
}

// imageWithTag 将 version 作为 tag 拼到镜像地址上（若已有 tag 则替换）。
func imageWithTag(base, version string) string {
	if version == "" {
		return base
	}
	if i := strings.LastIndex(base, ":"); i > strings.LastIndex(base, "/") {
		base = base[:i]
	}
	return base + ":" + version
}

// persist 写回发布单（忽略瞬时错误，避免部署中断）。
func (s *Service) persist(ctx context.Context, rel *ReleaseTicket) {
	if err := s.store.UpdateRelease(ctx, rel); err != nil {
		slog.Warn("deploy persist failed", "release", rel.ID, "err", err)
	}
}

// findEnv 按环境类型（dev/test/staging/prod）查找绑定的环境。
func (s *Service) findEnv(ctx context.Context, t, env string) *DeployEnvironment {
	envs, err := s.store.ListEnvs(ctx, t)
	if err != nil {
		return nil
	}
	for i := range envs {
		if envs[i].Type == env {
			return &envs[i]
		}
	}
	return nil
}

// findPrevSucceeded 查找该应用+环境（排除当前发布单）最近一条 succeeded 的发布历史。
func (s *Service) findPrevSucceeded(ctx context.Context, t, appID, env, excludeID string) (*ReleaseHistory, error) {
	hist, err := s.store.ListHistory(ctx, t)
	if err != nil {
		return nil, err
	}
	for _, h := range hist {
		if h.AppID == appID && h.Env == env && h.Status == "succeeded" && h.ReleaseID != excludeID {
			return &h, nil
		}
	}
	return nil, nil
}

func (s *Service) historyFor(rel *ReleaseTicket, status, output string) {
	s.store.CreateHistory(context.Background(), &ReleaseHistory{
		ReleaseID: rel.ID, AppID: rel.AppID, AppName: rel.AppName,
		Env: rel.Env, Version: rel.Version, Status: status,
		Operator: rel.Operator, Output: output, TenantID: rel.TenantID,
	})
}

func (s *Service) handleListEnvs(w http.ResponseWriter, r *http.Request) {
	envs, _ := s.store.ListEnvs(r.Context(), tenant(r))
	if len(envs) == 0 {
		for _, t := range []DeployEnv{"dev", "test", "staging", "prod"} {
			e := DeployEnvironment{Name: t, Type: t, Status: "active", TenantID: tenant(r)}
			s.store.CreateEnv(r.Context(), &e)
			envs = append(envs, e)
		}
	}
	httputil.WriteJSON(w, 200, envs)
}
func (s *Service) handleCreateEnv(w http.ResponseWriter, r *http.Request) {
	var e DeployEnvironment
	httputil.BindJSON(r, &e)
	e.TenantID = tenant(r)
	s.store.CreateEnv(r.Context(), &e)
	httputil.WriteJSON(w, 201, e)
}
func (s *Service) handleUpdateEnv(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var e DeployEnvironment
	httputil.BindJSON(r, &e)
	e.ID = id
	e.TenantID = tenant(r)
	s.store.UpdateEnv(r.Context(), &e)
	httputil.WriteJSON(w, 200, e)
}
func (s *Service) handleDeleteEnv(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.store.DeleteEnv(r.Context(), id, tenant(r))
	httputil.WriteJSON(w, 200, map[string]string{"ok": "deleted"})
}

func (s *Service) handleListHistory(w http.ResponseWriter, r *http.Request) {
	h, _ := s.store.ListHistory(r.Context(), tenant(r))
	httputil.WriteJSON(w, 200, h)
}

func tenant(r *http.Request) string {
	t := r.Header.Get("X-Tenant-ID")
	if t == "" {
		t = "default"
	}
	return t
}

func operatorOf(r *http.Request) string {
	if v := r.Header.Get("X-User-ID"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Principal-ID"); v != "" {
		return v
	}
	return "system"
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// httpProbe 对给定 URL 发起 GET 探活，返回状态码与截断响应体。
func httpProbe(url string, timeout time.Duration) (int, string, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, string(b), nil
}
