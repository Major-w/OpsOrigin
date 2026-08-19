'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch, apiList } from '@/lib/utils'
import { Plus, Trash2, Bell, Send, RefreshCw, Play } from 'lucide-react'
import { cn } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'

interface Subscription {
  id: string; name: string; model_type_id?: string; event_type: string
  channel: 'webhook' | 'notification'; target: string; enabled: boolean; created_at?: string
}
interface Delivery { id: string; sub_id: string; event_type: string; ci_id: string; status: string; error?: string; created_at: string }
interface CIModel { id: string; name: string }

const EVENT_LABELS: Record<string, string> = {
  '*': '全部事件', ci_created: '创建', ci_updated: '更新', ci_deleted: '删除', ci_archived: '归档',
}
const CHANNEL_LABELS: Record<string, string> = { webhook: 'Webhook', notification: '通知中心' }

export default function SubscriptionPage() {
  const [subs, setSubs] = useState<Subscription[]>([])
  const [deliveries, setDeliveries] = useState<Delivery[]>([])
  const [models, setModels] = useState<CIModel[]>([])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<{ name: string; model_type_id: string; event_type: string; channel: 'webhook' | 'notification'; target: string; enabled: boolean }>({
    name: '', model_type_id: '', event_type: '*', channel: 'webhook', target: '', enabled: true,
  })

  const load = async () => {
    try { setSubs(await apiList<Subscription>('/cmdb/subscriptions')) } catch { setSubs([]) }
    try { setDeliveries(await apiList<Delivery>('/cmdb/subscription-deliveries?limit=30')) } catch { setDeliveries([]) }
    try { setModels(await apiList<CIModel>('/cmdb/model-type?limit=1000')) } catch { setModels([]) }
  }

  useEffect(() => { load() }, [])

  const create = async () => {
    if (!form.name.trim()) { toast.error('请输入订阅名称'); return }
    if (form.channel === 'webhook' && !form.target.trim()) { toast.error('webhook 需要目标 URL'); return }
    try {
      await apiFetch('/cmdb/subscriptions', { method: 'POST', body: JSON.stringify({ ...form, model_type_id: form.model_type_id || undefined, target: form.target.trim() }) })
      toast.success('订阅已创建'); setDialogOpen(false); setForm({ name: '', model_type_id: '', event_type: '*', channel: 'webhook', target: '', enabled: true }); load()
    } catch (e: any) { toast.error(e.message) }
  }

  const toggle = async (sub: Subscription) => {
    try { await apiFetch(`/cmdb/subscriptions/${sub.id}`, { method: 'PUT', body: JSON.stringify({ enabled: !sub.enabled }) }); load() }
    catch (e: any) { toast.error(e.message) }
  }

  const [delTarget, setDelTarget] = useState<Subscription | null>(null)
  const [delSubmitting, setDelSubmitting] = useState(false)

  const confirmRemove = async () => {
    if (!delTarget) return
    setDelSubmitting(true)
    try { await apiFetch(`/cmdb/subscriptions/${delTarget.id}`, { method: 'DELETE' }); toast.success('已删除'); load(); setDelTarget(null) }
    catch (e: any) { toast.error(e.message) } finally { setDelSubmitting(false) }
  }

  const test = async (sub: Subscription) => {
    try {
      const d = await apiFetch<Delivery>(`/cmdb/subscriptions/${sub.id}/test`, { method: 'POST' })
      if (d.status === 'success') toast.success('测试投递成功')
      else toast.error('测试投递失败: ' + (d.error || '未知错误'))
      load()
    } catch (e: any) { toast.error(e.message) }
  }

  const subName = (id: string) => subs.find(s => s.id === id)?.name || id.slice(0, 8)
  const modelName = (id?: string) => id ? (models.find(m => m.id === id)?.name || id.slice(0, 8)) : '全部模型'

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">消息订阅</h1>
          <p className="text-sm text-muted-foreground">订阅 CI 变更（创建/更新/删除/归档），推送至 Webhook 或平台通知中心</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={load}><RefreshCw className="h-4 w-4" />刷新</Button>
          <Button onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4" />新建订阅</Button>
        </div>
      </div>

      {/* 订阅列表 */}
      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Bell className="h-4 w-4" />订阅（{subs.length}）</CardTitle></CardHeader>
        <CardContent className="p-0">
          {subs.length === 0 ? <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无订阅，点击右上角新建</p> : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead><tr className="border-b text-left text-muted-foreground">
                  <th className="font-medium px-4 py-2">名称</th><th className="font-medium px-4 py-2">模型</th><th className="font-medium px-4 py-2">事件</th>
                  <th className="font-medium px-4 py-2">渠道</th><th className="font-medium px-4 py-2">目标</th><th className="font-medium px-4 py-2">启用</th><th className="font-medium px-4 py-2 text-right">操作</th>
                </tr></thead>
                <tbody>
                  {subs.map(s => (
                    <tr key={s.id} className="border-b last:border-0 hover:bg-gray-50 dark:hover:bg-gray-800/50">
                      <td className="px-4 py-3 font-medium">{s.name}</td>
                      <td className="px-4 py-3 text-xs">{modelName(s.model_type_id)}</td>
                      <td className="px-4 py-3"><Badge variant="secondary">{EVENT_LABELS[s.event_type] || s.event_type}</Badge></td>
                      <td className="px-4 py-3"><Badge variant={s.channel === 'webhook' ? 'info' : 'warning'}>{CHANNEL_LABELS[s.channel]}</Badge></td>
                      <td className="px-4 py-3 text-xs font-mono text-muted-foreground max-w-[240px] truncate">{s.target || '—'}</td>
                      <td className="px-4 py-3">
                        <button onClick={() => toggle(s)} className={cn('relative inline-flex h-5 w-9 items-center rounded-full transition-colors', s.enabled ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600')}>
                          <span className={cn('inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform', s.enabled ? 'translate-x-[18px]' : 'translate-x-[3px]')} />
                        </button>
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center justify-end gap-1">
                          <Button variant="ghost" size="sm" className="h-7 w-7 p-0" title="测试投递" onClick={() => test(s)}><Play className="h-3.5 w-3.5" /></Button>
                          <Button variant="ghost" size="sm" className="h-7 w-7 p-0 text-red-500" onClick={() => setDelTarget(s)}><Trash2 className="h-3.5 w-3.5" /></Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 投递日志 */}
      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Send className="h-4 w-4" />投递日志（最近 30 条）</CardTitle></CardHeader>
        <CardContent className="p-0">
          {deliveries.length === 0 ? <p className="px-4 py-6 text-sm text-muted-foreground text-center">暂无投递记录</p> : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead><tr className="border-b text-left text-muted-foreground">
                  <th className="font-medium px-4 py-2">订阅</th><th className="font-medium px-4 py-2">事件</th><th className="font-medium px-4 py-2">CI</th><th className="font-medium px-4 py-2">状态</th><th className="font-medium px-4 py-2">时间</th>
                </tr></thead>
                <tbody>
                  {deliveries.map(d => (
                    <tr key={d.id} className="border-b last:border-0">
                      <td className="px-4 py-2 text-xs">{subName(d.sub_id)}</td>
                      <td className="px-4 py-2"><Badge variant="secondary" className="text-[10px]">{EVENT_LABELS[d.event_type] || d.event_type}</Badge></td>
                      <td className="px-4 py-2 text-xs font-mono">{d.ci_id.slice(0, 12)}</td>
                      <td className="px-4 py-2"><Badge variant={d.status === 'success' ? 'success' : 'destructive'} className="text-[10px]" title={d.error}>{d.status === 'success' ? '成功' : '失败'}</Badge></td>
                      <td className="px-4 py-2 text-xs text-muted-foreground">{d.created_at?.slice(0, 19).replace('T', ' ')}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 新建订阅 */}
      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建订阅">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">订阅名称 *</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="如：生产环境变更通知" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">模型（可选）</label>
              <select value={form.model_type_id} onChange={e => setForm(f => ({ ...f, model_type_id: e.target.value }))} className="w-full mt-1 rounded-md border border-input bg-background px-2 py-1.5 text-sm">
                <option value="">全部模型</option>
                {models.map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
              </select>
            </div>
            <div>
              <label className="text-sm font-medium">事件类型</label>
              <select value={form.event_type} onChange={e => setForm(f => ({ ...f, event_type: e.target.value }))} className="w-full mt-1 rounded-md border border-input bg-background px-2 py-1.5 text-sm">
                {Object.entries(EVENT_LABELS).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </div>
          </div>
          <div>
            <label className="text-sm font-medium">投递渠道</label>
            <div className="flex gap-2 mt-1">
              {(['webhook', 'notification'] as const).map(ch => (
                <button key={ch} onClick={() => setForm(f => ({ ...f, channel: ch }))} className={cn('flex-1 px-3 py-2 text-xs rounded-md border transition-colors', form.channel === ch ? 'border-emerald-500 bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400' : 'border-gray-200 dark:border-gray-700 text-muted-foreground')}>
                  {CHANNEL_LABELS[ch]}{ch === 'notification' && '（需在通知中心配置 cmdb.ci_changed 策略）'}
                </button>
              ))}
            </div>
          </div>
          {form.channel === 'webhook' && (
            <div><label className="text-sm font-medium">Webhook URL *</label><Input value={form.target} onChange={e => setForm(f => ({ ...f, target: e.target.value }))} placeholder="https://example.com/hook" className="font-mono" /></div>
          )}
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={create} disabled={!form.name.trim()}>创建</Button></DialogFooter>
      </Dialog>

      <ConfirmDialog
        open={!!delTarget}
        onClose={() => setDelTarget(null)}
        onConfirm={confirmRemove}
        title="确认删除订阅"
        description={delTarget ? `确认删除订阅「${delTarget.name}」？` : ''}
        confirmLabel="删除"
        loading={delSubmitting}
      />
    </div>
  )
}
