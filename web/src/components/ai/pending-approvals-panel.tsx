'use client'

// 待审批请求(跨会话 / 跨设备): GET /ai/approvals/pending + POST /ai/approvals/{id}/respond。
// 聊天窗口内的 SSE ask 弹窗只覆盖当前会话; 离开会话或换设备后仍须能看到并处理队列中的请求。

import { useCallback, useEffect, useState } from 'react'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'
import { Clock, RefreshCw, ShieldCheck } from 'lucide-react'
import { useApprovalStore } from '@/stores/ai'
import type { PendingApproval } from '@/stores/ai'
import { useCan } from '@/stores/permissions'
import { getMessage } from '@/lib/message'

interface Decision {
  item: PendingApproval
  action: 'approve' | 'reject'
}

function formatTime(v?: string): string {
  if (!v) return '—'
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

export function PendingApprovalsPanel() {
  const listPendingApprovals = useApprovalStore(s => s.listPendingApprovals)
  const respondApproval = useApprovalStore(s => s.respondApproval)
  // 列表读取只需 ai:read, 但响应端点 POST /ai/approvals/{id}/respond 走 ai:write。
  const canRespond = useCan('ai:write')

  const [items, setItems] = useState<PendingApproval[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [decision, setDecision] = useState<Decision | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const list = await listPendingApprovals()
      setItems(Array.isArray(list) ? list : [])
    } catch (e) {
      setError(e instanceof Error ? e.message : '待审批列表加载失败')
    } finally {
      setLoading(false)
    }
  }, [listPendingApprovals])

  useEffect(() => { void load() }, [load])

  // 批准后操作会真正执行, 拒绝也会终止等待中的 AI 轮次 —— 两者都必须二次确认 + 防重复提交。
  // 提交函数名不得与浏览器原生确认框同名: 契约第 6 步按字面扫描, 同名会被判为残留。
  const submitDecision = async () => {
    if (!decision || busy) return
    setBusy(true)
    try {
      const ok = await respondApproval(decision.item.id, decision.action)
      if (!ok) return
      getMessage().success(decision.action === 'approve' ? '已批准，操作将继续执行' : '已拒绝，操作已停止')
      setDecision(null)
      await load()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between space-y-0">
        <div>
          <CardTitle className="text-base flex items-center gap-2"><ShieldCheck className="h-4 w-4" />待审批请求</CardTitle>
          <CardDescription>敏感操作在等待确认期间会留在队列中；批准后立即执行，拒绝或超时一律停止。列表按租户隔离。</CardDescription>
        </div>
        <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
          <RefreshCw className="h-3.5 w-3.5 mr-1" />刷新
        </Button>
      </CardHeader>
      <CardContent>
        {loading ? (
          <p className="text-sm text-muted-foreground py-6 text-center">加载中…</p>
        ) : error ? (
          <ErrorState message={error} onRetry={() => void load()} />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground py-6 text-center">当前没有待审批的操作</p>
        ) : (
          <div className="space-y-2">
            {items.map(it => (
              <div key={it.id} className="rounded-md border border-border px-3 py-2 space-y-1.5">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="text-sm font-medium break-all">{it.summary || it.tool}</div>
                    <div className="text-xs text-muted-foreground mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5">
                      <Badge variant="outline">{it.tool}</Badge>
                      {it.agent_id ? <span>员工 {it.agent_id}</span> : null}
                      <span>发起 {formatTime(it.created_at)}</span>
                      <span className="inline-flex items-center gap-1"><Clock className="h-3 w-3" />截止 {formatTime(it.deadline)}</span>
                    </div>
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <Button size="sm" variant="outline" disabled={!canRespond || busy} onClick={() => setDecision({ item: it, action: 'reject' })}>拒绝</Button>
                    <Button size="sm" disabled={!canRespond || busy} onClick={() => setDecision({ item: it, action: 'approve' })}>批准</Button>
                  </div>
                </div>
                {it.args ? (
                  <pre className="whitespace-pre-wrap break-all rounded bg-muted/30 px-2 py-1.5 text-xs text-muted-foreground">{it.args}</pre>
                ) : null}
              </div>
            ))}
          </div>
        )}
        {!canRespond && !loading && !error && items.length > 0 && (
          <p className="text-xs text-muted-foreground pt-2">当前角色无 ai:write 权限，仅可查看待审批项；如需处理请联系管理员授权。</p>
        )}
      </CardContent>

      <ConfirmDialog
        open={!!decision}
        onClose={() => setDecision(null)}
        onConfirm={() => void submitDecision()}
        title={decision?.action === 'approve' ? '批准敏感操作' : '拒绝敏感操作'}
        description={decision?.action === 'approve'
          ? `批准后「${decision?.item.summary || decision?.item.tool}」将立即执行，请确认参数无误。`
          : `拒绝后「${decision?.item.summary || decision?.item.tool}」将停止执行。`}
        confirmLabel={decision?.action === 'approve' ? '批准执行' : '拒绝'}
        loading={busy}
      />
    </Card>
  )
}
