'use client'

import { useEffect, useState, useMemo } from 'react'
import { useDeployStore, type DeployEnvironment } from '@/stores/deploy'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Cloud, Plus, Trash2, Pencil, Server, Globe, Shield, Clock, GitBranch, CheckCircle, AlertTriangle, History } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { toast } from '@/stores/toast'

const ENV_LABELS: Record<string, string> = { dev: '开发', test: '测试', staging: '预发', prod: '生产' }

const ENV_COLOR_MAP: Record<string, { bg: string; border: string; text: string; dot: string }> = {
  blue: { bg: 'bg-blue-500/5', border: 'border-blue-500/20', text: 'text-blue-600 dark:text-blue-400', dot: 'bg-blue-500' },
  amber: { bg: 'bg-amber-500/5', border: 'border-amber-500/20', text: 'text-amber-600 dark:text-amber-400', dot: 'bg-amber-500' },
  purple: { bg: 'bg-purple-500/5', border: 'border-purple-500/20', text: 'text-purple-600 dark:text-purple-400', dot: 'bg-purple-500' },
  red: { bg: 'bg-red-500/5', border: 'border-red-500/20', text: 'text-red-600 dark:text-red-400', dot: 'bg-red-500' },
}

export default function EnvironmentsPage() {
  const { environments, fetchEnvironments, createEnvironment, updateEnvironment, deleteEnvironment } = useDeployStore()
  const [editTarget, setEditTarget] = useState<DeployEnvironment | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<DeployEnvironment | null>(null)
  const [form, setForm] = useState<Partial<DeployEnvironment>>({
    name: '', code: 'dev', description: '', color: 'blue', deploy_path: '/opt/apps/dev',
    domain: '', port_range: '8000-8999', requires_approval: false, auto_rollback: false, max_releases: 10, target_hosts: [],
  })
  const [hostInput, setHostInput] = useState('')

  useEffect(() => { fetchEnvironments() }, [])

  const stats = useMemo(() => ({
    total: environments.length,
    apps: environments.reduce((s, e) => s + e.app_count, 0),
    approval: environments.filter(e => e.requires_approval).length,
    autoRollback: environments.filter(e => e.auto_rollback).length,
  }), [environments])

  const openEdit = (env: DeployEnvironment) => {
    setEditTarget(env)
    setForm({ ...env })
    setHostInput(env.target_hosts.join(', '))
  }

  const openCreate = () => {
    setCreateOpen(true)
    setEditTarget(null)
    setForm({ name: '', code: 'dev', description: '', color: 'blue', deploy_path: '/opt/apps/dev', domain: '', port_range: '8000-8999', requires_approval: false, auto_rollback: false, max_releases: 10, target_hosts: [] })
    setHostInput('')
  }

  const handleSave = async () => {
    if (!form.name?.trim()) { toast.error('环境名称不能为空'); return }
    const hosts = hostInput.split(',').map(s => s.trim()).filter(Boolean)
    const data = { ...form, target_hosts: hosts }
    if (editTarget) {
      await updateEnvironment(editTarget.id, data)
      setEditTarget(null)
    } else {
      await createEnvironment(data)
      setCreateOpen(false)
    }
  }

  const handleConfirmDelete = async () => {
    if (!confirmDelete) return
    if (confirmDelete.app_count > 0) { toast.error(`该环境还有 ${confirmDelete.app_count} 个应用,无法删除`); return }
    const ok = await deleteEnvironment(confirmDelete.id)
    if (ok) setConfirmDelete(null)
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">环境管理</h1>
          <p className="text-sm text-muted-foreground">管理发布环境的目标主机、审批策略与回滚配置</p>
        </div>
        <Button size="sm" onClick={openCreate}><Plus className="h-3.5 w-3.5 mr-1" />新建环境</Button>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Cloud className="h-3 w-3 inline mr-1" />环境总数</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Server className="h-3 w-3 inline mr-1" />应用总数</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{stats.apps}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Shield className="h-3 w-3 inline mr-1" />需审批环境</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-amber-500">{stats.approval}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><History className="h-3 w-3 inline mr-1" />自动回滚</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-cyan-500">{stats.autoRollback}</span></CardContent></Card>
      </div>

      {/* 环境卡片网格 */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {environments.map(env => {
          const cm = ENV_COLOR_MAP[env.color] || ENV_COLOR_MAP.blue
          return (
            <Card key={env.id} className={cn("border", cm.border, cm.bg)}>
              <CardHeader className="py-3 pb-2">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <div className={cn("w-2.5 h-2.5 rounded-full", cm.dot)} />
                    <span className="text-sm font-bold">{env.name}</span>
                    <Badge variant="outline" className={cn("text-[10px]", cm.text, cm.border)}>{ENV_LABELS[env.code] || env.code}</Badge>
                  </div>
                  <div className="flex gap-0.5">
                    <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => openEdit(env)} title="编辑"><Pencil className="h-3 w-3" /></Button>
                    <Button variant="ghost" size="icon" className="h-6 w-6 text-destructive" onClick={() => setConfirmDelete(env)} title="删除"><Trash2 className="h-3 w-3" /></Button>
                  </div>
                </div>
              </CardHeader>
              <CardContent className="space-y-2.5 pt-0">
                {env.description && <p className="text-xs text-muted-foreground">{env.description}</p>}
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div className="flex items-center gap-1.5"><Server className="h-3 w-3 text-muted-foreground" /><span className="text-muted-foreground">主机:</span><span className="font-mono">{env.target_hosts.length} 台</span></div>
                  <div className="flex items-center gap-1.5"><Globe className="h-3 w-3 text-muted-foreground" /><span className="text-muted-foreground">域名:</span><span className="font-mono truncate">{env.domain || '-'}</span></div>
                  <div className="flex items-center gap-1.5"><GitBranch className="h-3 w-3 text-muted-foreground" /><span className="text-muted-foreground">路径:</span><span className="font-mono truncate">{env.deploy_path}</span></div>
                  <div className="flex items-center gap-1.5"><Cloud className="h-3 w-3 text-muted-foreground" /><span className="text-muted-foreground">端口:</span><span className="font-mono">{env.port_range}</span></div>
                </div>
                <div className="flex flex-wrap items-center gap-2 pt-1 border-t">
                  <div className="flex items-center gap-1">
                    {env.requires_approval ? <Shield className="h-3 w-3 text-amber-500" /> : <CheckCircle className="h-3 w-3 text-emerald-500" />}
                    <span className="text-[10px] text-muted-foreground">{env.requires_approval ? '需审批' : '免审批'}</span>
                  </div>
                  <div className="flex items-center gap-1">
                    {env.auto_rollback ? <CheckCircle className="h-3 w-3 text-cyan-500" /> : <AlertTriangle className="h-3 w-3 text-muted-foreground" />}
                    <span className="text-[10px] text-muted-foreground">{env.auto_rollback ? '自动回滚' : '手动回滚'}</span>
                  </div>
                  <div className="flex items-center gap-1"><Server className="h-3 w-3 text-muted-foreground" /><span className="text-[10px] text-muted-foreground">{env.app_count} 个应用</span></div>
                  {env.last_release_at && <div className="flex items-center gap-1"><Clock className="h-3 w-3 text-muted-foreground" /><span className="text-[10px] text-muted-foreground">{formatTime(env.last_release_at)}</span></div>}
                </div>
                {/* 主机列表 */}
                {env.target_hosts.length > 0 && (
                  <div className="flex flex-wrap gap-1 pt-1">
                    {env.target_hosts.map(h => <span key={h} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-muted/50">{h}</span>)}
                  </div>
                )}
              </CardContent>
            </Card>
          )
        })}
      </div>

      {/* 编辑/新建 Dialog */}
      {(editTarget || createOpen) && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => { setEditTarget(null); setCreateOpen(false) }}>
          <div className="bg-background rounded-lg border shadow-lg w-full max-w-lg mx-4 p-5 space-y-3 max-h-[80vh] overflow-y-auto" onClick={e => e.stopPropagation()}>
            <h3 className="text-sm font-bold">{editTarget ? '编辑环境' : '新建环境'}</h3>
            <div className="grid grid-cols-2 gap-3">
              <div><label className="text-xs font-medium">名称 *</label><input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.name || ''} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="开发环境" /></div>
              <div>
                <label className="text-xs font-medium">环境代码</label>
                <select className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.code} onChange={e => setForm(f => ({ ...f, code: e.target.value as any }))} disabled={!!editTarget}>
                  {Object.entries(ENV_LABELS).map(([v, l]) => <option key={v} value={v}>{l}</option>)}
                </select>
              </div>
              <div className="col-span-2"><label className="text-xs font-medium">描述</label><input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.description || ''} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>
              <div>
                <label className="text-xs font-medium">颜色标识</label>
                <div className="mt-1 flex gap-2">
                  {['blue', 'amber', 'purple', 'red'].map(c => (
                    <button key={c} type="button" onClick={() => setForm(f => ({ ...f, color: c }))}
                      className={cn("w-6 h-6 rounded-full border-2", ENV_COLOR_MAP[c].dot, form.color === c ? 'border-foreground' : 'border-transparent')} />
                  ))}
                </div>
              </div>
              <div><label className="text-xs font-medium">部署路径</label><input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm font-mono" value={form.deploy_path || ''} onChange={e => setForm(f => ({ ...f, deploy_path: e.target.value }))} /></div>
              <div><label className="text-xs font-medium">域名</label><input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm font-mono" value={form.domain || ''} onChange={e => setForm(f => ({ ...f, domain: e.target.value }))} placeholder="dev.example.com" /></div>
              <div><label className="text-xs font-medium">端口范围</label><input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm font-mono" value={form.port_range || ''} onChange={e => setForm(f => ({ ...f, port_range: e.target.value }))} placeholder="8000-8999" /></div>
              <div className="col-span-2"><label className="text-xs font-medium">目标主机（逗号分隔）</label><input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm font-mono" value={hostInput} onChange={e => setHostInput(e.target.value)} placeholder="host-01, host-02" /></div>
              <div><label className="text-xs font-medium">最大保留版本数</label><input type="number" className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.max_releases || 0} onChange={e => setForm(f => ({ ...f, max_releases: parseInt(e.target.value) || 5 }))} /></div>
              <div className="flex items-end gap-4 pb-1">
                <label className="flex items-center gap-1.5 cursor-pointer">
                  <input type="checkbox" checked={!!form.requires_approval} onChange={e => setForm(f => ({ ...f, requires_approval: e.target.checked }))} className="h-4 w-4 rounded border-input" />
                  <span className="text-xs">需要审批</span>
                </label>
                <label className="flex items-center gap-1.5 cursor-pointer">
                  <input type="checkbox" checked={!!form.auto_rollback} onChange={e => setForm(f => ({ ...f, auto_rollback: e.target.checked }))} className="h-4 w-4 rounded border-input" />
                  <span className="text-xs">失败自动回滚</span>
                </label>
              </div>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button variant="outline" size="sm" onClick={() => { setEditTarget(null); setCreateOpen(false) }}>取消</Button>
              <Button size="sm" onClick={handleSave} disabled={!form.name?.trim()}>{editTarget ? '保存' : '创建'}</Button>
            </div>
          </div>
        </div>
      )}

      {/* 确认删除 */}
      {confirmDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setConfirmDelete(null)}>
          <div className="bg-background rounded-lg border shadow-lg w-full max-w-sm mx-4 p-5 space-y-3" onClick={e => e.stopPropagation()}>
            <h3 className="text-sm font-bold text-destructive">确认删除环境</h3>
            <p className="text-sm text-muted-foreground">确认删除 <span className="font-medium text-foreground">{confirmDelete.name}</span>?</p>
            {confirmDelete.app_count > 0 && <p className="text-xs text-destructive">该环境还有 {confirmDelete.app_count} 个应用,无法删除</p>}
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setConfirmDelete(null)}>取消</Button>
              <Button variant="destructive" size="sm" onClick={handleConfirmDelete} disabled={confirmDelete.app_count > 0}><Trash2 className="h-3.5 w-3.5 mr-1" />确认删除</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
