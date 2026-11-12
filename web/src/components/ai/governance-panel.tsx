'use client'

import { useCallback, useEffect, useState } from 'react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'
import { apiFetch } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { useCan } from '@/stores/permissions'
import { ShieldCheck, Users, Activity, RefreshCw, Trash2, Eye, FileJson } from 'lucide-react'

// 数字员工治理面板(P0): 独立身份 / 可见性与分享 / 运行轨迹只读复盘。
// 审批策略配置在「审批与安全」Tab, 此处复用同一后端策略接口展示当前生效值。

interface AgentRow {
  id: string
  name: string
  owner_id?: string
  visibility: 'private' | 'shared' | 'public'
  permission?: 'owner' | 'edit' | 'view'
}

interface ShareRow {
  id: string
  target_type: 'user' | 'group'
  target_id: string
  permission: 'view' | 'edit'
  shared_by?: string
}

interface Turn {
  id: string
  agent_id: string
  conversation_id: string
  trace_id?: string
  started_at: string
  prompt?: string
  final_content?: string
  error?: string
  steps?: { index: number; model?: string; tool_calls?: { name: string }[] }[]
}

const VISIBILITY_LABEL: Record<string, string> = {
  private: '私有', shared: '按分享', public: '同租户公开',
}
const PERMISSION_LABEL: Record<string, string> = {
  owner: '属主', edit: '可编辑', view: '可使用',
}

export function GovernancePanel() {
  // F2.4: 轨迹含工具入参与结果, 后端显式收敛到 ai:trace:read(不回落 ai:read)。
  // 只门控轨迹卡片, 不锁整个治理面板 —— /ai/agents 走 ai:read, viewer 仍可查看数字员工。
  // useCan 在权限加载完成前乐观放行(项目既有约定), 加载完成后无权限即切到无权限态。
  const canTrace = useCan('ai:trace:read')

  const [agents, setAgents] = useState<AgentRow[]>([])
  const [agentsLoading, setAgentsLoading] = useState(true)
  const [agentsError, setAgentsError] = useState('')
  const [turns, setTurns] = useState<Turn[]>([])
  const [turnsLoading, setTurnsLoading] = useState(true)
  const [turnsError, setTurnsError] = useState('')
  const [conversationId, setConversationId] = useState('')
  const [selected, setSelected] = useState<Turn | null>(null)
  const [otel, setOtel] = useState<unknown>(null)
  const [shareTarget, setShareTarget] = useState<AgentRow | null>(null)
  const [shares, setShares] = useState<ShareRow[]>([])
  const [shareForm, setShareForm] = useState({ target_type: 'user', target_id: '', permission: 'view' })
  const [pendingDelete, setPendingDelete] = useState<ShareRow | null>(null)
  const [busy, setBusy] = useState(false)

  const loadAgents = useCallback(async () => {
    setAgentsLoading(true)
    setAgentsError('')
    try {
      const data = await apiFetch<any>('/ai/agents')
      setAgents(Array.isArray(data) ? data : [])
    } catch (e) {
      setAgentsError(e instanceof Error ? e.message : '数字员工列表加载失败')
    } finally {
      setAgentsLoading(false)
    }
  }, [])

  // cid 由调用方显式传入: 挂载时传空串拉全量, 查询/刷新传当前筛选值。
  // 不闭包依赖 conversationId, 保证 useCallback 稳定 —— 挂载 effect 只跑一次, 筛选输入不会逐键发请求。
  const loadTurns = useCallback(async (cid: string = '') => {
    setTurnsLoading(true)
    setTurnsError('')
    try {
      const id = cid.trim()
      const qs = id ? `?conversation_id=${encodeURIComponent(id)}` : ''
      const data = await apiFetch<any>(`/ai/trajectories${qs}`)
      setTurns(Array.isArray(data) ? data : [])
    } catch (e) {
      setTurnsError(e instanceof Error ? e.message : '运行轨迹加载失败')
    } finally {
      setTurnsLoading(false)
    }
  }, [])

  // 挂载时同时拉取员工列表与全量轨迹: turnsLoading 初始为 true, 少了 loadTurns 会一直卡在「加载中…」。
  // 两者拆开: 轨迹请求受 ai:trace:read 守卫, 无权限时不发请求, 也就不会撞 403 错误态。
  useEffect(() => { void loadAgents() }, [loadAgents])
  useEffect(() => { if (canTrace) void loadTurns('') }, [loadTurns, canTrace])

  const changeVisibility = async (a: AgentRow, visibility: string) => {
    if (busy) return
    setBusy(true)
    try {
      await apiFetch(`/ai/agents/${a.id}`, {
        method: 'PUT',
        body: JSON.stringify({ visibility }),
      })
      getMessage().success('可见范围已更新')
      await loadAgents()
    } catch (e) {
      getMessage().error(e instanceof Error ? e.message : '更新失败')
    } finally {
      setBusy(false)
    }
  }

  const openShares = async (a: AgentRow) => {
    setShareTarget(a)
    setShares([])
    try {
      const data = await apiFetch<any>(`/ai/agents/${a.id}/shares`)
      setShares(Array.isArray(data) ? data : [])
    } catch (e) {
      getMessage().error(e instanceof Error ? e.message : '分享列表加载失败')
    }
  }

  const addShare = async () => {
    if (!shareTarget || busy || !shareForm.target_id.trim()) return
    setBusy(true)
    try {
      await apiFetch(`/ai/agents/${shareTarget.id}/shares`, {
        method: 'POST',
        body: JSON.stringify({ ...shareForm, target_id: shareForm.target_id.trim() }),
      })
      getMessage().success('已添加分享')
      setShareForm({ target_type: 'user', target_id: '', permission: 'view' })
      await openShares(shareTarget)
      await loadAgents()
    } catch (e) {
      getMessage().error(e instanceof Error ? e.message : '添加分享失败')
    } finally {
      setBusy(false)
    }
  }

  const removeShare = async () => {
    if (!shareTarget || !pendingDelete || busy) return
    setBusy(true)
    try {
      await apiFetch(`/ai/agents/${shareTarget.id}/shares/${pendingDelete.id}`, { method: 'DELETE' })
      getMessage().success('已取消分享')
      setPendingDelete(null)
      await openShares(shareTarget)
    } catch (e) {
      getMessage().error(e instanceof Error ? e.message : '取消分享失败')
    } finally {
      setBusy(false)
    }
  }

  const openTurn = async (t: Turn) => {
    setSelected(t)
    setOtel(null)
    try {
      const data = await apiFetch<any>(`/ai/trajectories/${t.id}`)
      setSelected(data)
    } catch (e) {
      getMessage().error(e instanceof Error ? e.message : '轨迹详情加载失败')
    }
  }

  const exportOTel = async (t: Turn) => {
    try {
      const data = await apiFetch<any>(`/ai/trajectories/${t.id}/otel`)
      setOtel(data)
      getMessage().success('已生成 OTel GenAI 轨迹')
    } catch (e) {
      getMessage().error(e instanceof Error ? e.message : '导出失败')
    }
  }

  const canModify = (a: AgentRow) => a.permission === 'owner' || a.permission === 'edit'

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex flex-row items-start justify-between space-y-0">
          <div>
            <CardTitle className="text-base flex items-center gap-2"><Users className="h-4 w-4" />数字员工身份与可见范围</CardTitle>
            <CardDescription>新建员工默认私有；同租户公开或使用分享名单后，其他人才能使用。属主与管理员可调整。</CardDescription>
          </div>
          <Button variant="outline" size="sm" onClick={() => void loadAgents()} disabled={agentsLoading}>
            <RefreshCw className="h-3.5 w-3.5 mr-1" />刷新
          </Button>
        </CardHeader>
        <CardContent>
          {agentsLoading ? (
            <p className="text-sm text-muted-foreground py-6 text-center">加载中…</p>
          ) : agentsError ? (
            <ErrorState message={agentsError} onRetry={() => void loadAgents()} />
          ) : agents.length === 0 ? (
            <p className="text-sm text-muted-foreground py-6 text-center">暂无可见的数字员工</p>
          ) : (
            <div className="space-y-2">
              {agents.map(a => (
                <div key={a.id} className="flex items-center justify-between gap-3 rounded-md border border-border px-3 py-2">
                  <div className="min-w-0">
                    <div className="text-sm font-medium truncate">{a.name}</div>
                    <div className="text-xs text-muted-foreground mt-0.5">
                      属主 {a.owner_id || '系统'} · {PERMISSION_LABEL[a.permission || ''] || '—'}
                    </div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <Badge variant="outline">{VISIBILITY_LABEL[a.visibility] || a.visibility}</Badge>
                    <Select
                      className="h-8 text-xs"
                      value={a.visibility}
                      disabled={!canModify(a) || busy}
                      onChange={e => void changeVisibility(a, e.target.value)}
                      options={[
                        { value: 'private', label: '私有' },
                        { value: 'shared', label: '按分享' },
                        { value: 'public', label: '同租户公开' },
                      ]}
                    />
                    <Button variant="ghost" size="sm" disabled={a.permission !== 'owner'} onClick={() => void openShares(a)} title="管理分享">
                      <Users className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-start justify-between space-y-0">
          <div>
            <CardTitle className="text-base flex items-center gap-2"><Activity className="h-4 w-4" />运行轨迹复盘</CardTitle>
            <CardDescription>每次请求的 LLM 轮次与工具调用均按租户留痕，可导出 OTel GenAI 语义约定格式。默认保留 30 天。</CardDescription>
          </div>
          {canTrace && (
            <Button variant="outline" size="sm" onClick={() => void loadTurns(conversationId)} disabled={turnsLoading}>
              <RefreshCw className="h-3.5 w-3.5 mr-1" />刷新
            </Button>
          )}
        </CardHeader>
        <CardContent className="space-y-3">
          {canTrace ? (
            <>
              <div className="flex items-center gap-2">
                <Input
                  className="max-w-xs"
                  placeholder="按会话 ID 筛选（留空查全部）"
                  value={conversationId}
                  onChange={e => setConversationId(e.target.value)}
                  onKeyDown={e => { if (e.key === 'Enter') void loadTurns(conversationId) }}
                />
                <Button variant="outline" size="sm" onClick={() => void loadTurns(conversationId)}>查询</Button>
              </div>
              {turnsLoading ? (
                <p className="text-sm text-muted-foreground py-6 text-center">加载中…</p>
              ) : turnsError ? (
                <ErrorState message={turnsError} onRetry={() => void loadTurns(conversationId)} />
              ) : turns.length === 0 ? (
                <p className="text-sm text-muted-foreground py-6 text-center">暂无运行轨迹（发起一次对话后即可看到）</p>
              ) : (
                <div className="space-y-2">
                  {turns.map(t => (
                    <div key={t.id} className="flex items-center justify-between gap-3 rounded-md border border-border px-3 py-2">
                      <div className="min-w-0">
                        <div className="text-sm font-medium truncate">{t.prompt || '(无提问文本)'}</div>
                        <div className="text-xs text-muted-foreground mt-0.5">
                          {new Date(t.started_at).toLocaleString()} · {t.steps?.length || 0} 轮 · 员工 {t.agent_id || '默认'}
                          {t.error ? ' · 含错误' : ''}
                        </div>
                      </div>
                      <div className="flex items-center gap-1 shrink-0">
                        <Button variant="ghost" size="sm" onClick={() => void openTurn(t)} title="查看详情"><Eye className="h-3.5 w-3.5" /></Button>
                        <Button variant="ghost" size="sm" onClick={() => void exportOTel(t)} title="导出 OTel"><FileJson className="h-3.5 w-3.5" /></Button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </>
          ) : (
            <div className="flex items-start gap-2 rounded-md border border-border px-3 py-4 text-sm text-muted-foreground">
              <ShieldCheck className="h-4 w-4 mt-0.5 shrink-0" />
              <span>
                当前角色无 ai:trace:read 权限，运行轨迹包含工具入参与结果，仅对获授权角色开放。
                如需查看或导出，请联系管理员在角色中授予该权限；上方数字员工的可见范围与分享仍可管理。
              </span>
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base flex items-center gap-2"><ShieldCheck className="h-4 w-4" />边界说明</CardTitle>
          <CardDescription>审批策略已落库并在平台层统一裁决，配置入口在「审批与安全」Tab。</CardDescription>
        </CardHeader>
        <CardContent className="text-sm text-muted-foreground space-y-1">
          <p>· 策略按「数字员工级覆盖 &gt; 租户级默认」生效，重启与多副本共享同一份配置。</p>
          <p>· 所有工具调用先过网关策略判定，再执行；超时或拒绝一律拒绝执行。</p>
          <p>· 工具入参与结果中的凭据类字段在落盘前已脱敏，仅支持只读复盘，不支持重放执行。</p>
        </CardContent>
      </Card>

      <Dialog open={!!shareTarget} onClose={() => setShareTarget(null)} title={shareTarget ? `分享「${shareTarget.name}」` : ''}>
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">仅属主可增删分享。view = 可使用，edit = 可改配置。</p>
          <div className="grid grid-cols-[1fr_2fr_1fr_auto] gap-2 items-end">
            <div>
              <label className="text-xs text-muted-foreground">类型</label>
              <Select
                className="mt-1"
                value={shareForm.target_type}
                onChange={e => setShareForm(f => ({ ...f, target_type: e.target.value }))}
                options={[{ value: 'user', label: '用户' }, { value: 'group', label: '用户组' }]}
              />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">用户 / 组 ID</label>
              <Input className="mt-1" value={shareForm.target_id} onChange={e => setShareForm(f => ({ ...f, target_id: e.target.value }))} placeholder="如 u_1001" />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">权限</label>
              <Select
                className="mt-1"
                value={shareForm.permission}
                onChange={e => setShareForm(f => ({ ...f, permission: e.target.value }))}
                options={[{ value: 'view', label: '可使用' }, { value: 'edit', label: '可编辑' }]}
              />
            </div>
            <Button size="sm" disabled={busy || !shareForm.target_id.trim()} onClick={() => void addShare()}>添加</Button>
          </div>
          {shares.length === 0 ? (
            <p className="text-sm text-muted-foreground py-3 text-center">还没有分享给任何人</p>
          ) : (
            <div className="space-y-1">
              {shares.map(sh => (
                <div key={sh.id} className="flex items-center justify-between rounded border border-border px-2 py-1.5 text-sm">
                  <span>{sh.target_type === 'user' ? '用户' : '用户组'} · {sh.target_id} · {PERMISSION_LABEL[sh.permission] || sh.permission}</span>
                  <Button variant="ghost" size="sm" onClick={() => setPendingDelete(sh)} title="取消分享"><Trash2 className="h-3.5 w-3.5" /></Button>
                </div>
              ))}
            </div>
          )}
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setShareTarget(null)}>关闭</Button></DialogFooter>
      </Dialog>

      <ConfirmDialog
        open={!!pendingDelete}
        onClose={() => setPendingDelete(null)}
        onConfirm={() => void removeShare()}
        title="取消分享"
        description={`确定移除 ${pendingDelete?.target_id || ''} 的访问权限吗？`}
        confirmLabel="移除"
        loading={busy}
      />

      <Dialog open={!!selected} onClose={() => setSelected(null)} title="轨迹详情">
        {selected && (
          <div className="space-y-2 text-sm">
            <p className="text-muted-foreground">Trace ID: {selected.trace_id || '—'}</p>
            <p className="text-muted-foreground">会话: {selected.conversation_id}</p>
            <div className="max-h-72 overflow-auto rounded border border-border bg-muted/30 p-2 text-xs">
              <pre className="whitespace-pre-wrap break-all">{JSON.stringify(selected.steps || [], null, 2)}</pre>
            </div>
            <Button variant="outline" size="sm" onClick={() => void exportOTel(selected)}><FileJson className="h-3.5 w-3.5 mr-1" />导出 OTel</Button>
          </div>
        )}
        <DialogFooter><Button variant="outline" onClick={() => setSelected(null)}>关闭</Button></DialogFooter>
      </Dialog>

      <Dialog open={otel !== null} onClose={() => setOtel(null)} title="OTel GenAI 轨迹导出">
        <div className="max-h-96 overflow-auto rounded border border-border bg-muted/30 p-2 text-xs">
          <pre className="whitespace-pre-wrap break-all">{JSON.stringify(otel, null, 2)}</pre>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setOtel(null)}>关闭</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
