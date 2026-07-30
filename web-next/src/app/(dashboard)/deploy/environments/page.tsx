'use client'

import { useEffect, useState, useMemo } from 'react'
import { useDeployStore, type DeployEnvironment, type DeployMode } from '@/stores/deploy'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Cloud, Plus, Trash2, Pencil, Server, Globe, Shield, Clock, CheckCircle, AlertTriangle, History, Container, Boxes } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { toast } from '@/stores/toast'

const ENV_LABELS: Record<string, string> = { dev: '开发', test: '测试', staging: '预发', prod: '生产' }

const ENV_COLOR_MAP: Record<string, { bg: string; border: string; text: string; dot: string }> = {
  blue: { bg: 'bg-blue-500/5', border: 'border-blue-500/20', text: 'text-blue-600 dark:text-blue-400', dot: 'bg-blue-500' },
  amber: { bg: 'bg-amber-500/5', border: 'border-amber-500/20', text: 'text-amber-600 dark:text-amber-400', dot: 'bg-amber-500' },
  purple: { bg: 'bg-purple-500/5', border: 'border-purple-500/20', text: 'text-purple-600 dark:text-purple-400', dot: 'bg-purple-500' },
  red: { bg: 'bg-red-500/5', border: 'border-red-500/20', text: 'text-red-600 dark:text-red-400', dot: 'bg-red-500' },
}

const MODE_META: Record<DeployMode, { label: string; icon: any; color: string }> = {
  host: { label: '主机环境', icon: Server, color: 'bg-slate-500/10 text-slate-600 border-slate-500/20' },
  container: { label: '容器环境', icon: Container, color: 'bg-violet-500/10 text-violet-600 border-violet-500/20' },
}

export default function EnvironmentsPage() {
  const { environments, fetchEnvs, createEnv, updateEnv, deleteEnv } = useDeployStore()
  const [editTarget, setEditTarget] = useState<DeployEnvironment | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<DeployEnvironment | null>(null)
  const [form, setForm] = useState<Partial<DeployEnvironment>>({
    name: '', type: 'dev', mode: 'host', description: '', hosts: [], agent_ids: [],
  })
  const [hostInput, setHostInput] = useState('')
  const [agentInput, setAgentInput] = useState('')

  useEffect(() => { fetchEnvs() }, [])

  const stats = useMemo(() => ({
    total: environments.length,
    host: environments.filter(e => (e.mode || 'host') === 'host').length,
    container: environments.filter(e => e.mode === 'container').length,
    agents: environments.reduce((s, e) => s + (e.agent_ids?.length || 0), 0),
  }), [environments])

  const openEdit = (env: DeployEnvironment) => {
    setEditTarget(env)
    setForm({ ...env })
    setHostInput((env.hosts || []).join(', '))
    setAgentInput((env.agent_ids || []).join(', '))
  }

  const openCreate = () => {
    setCreateOpen(true)
    setEditTarget(null)
    setForm({ name: '', type: 'dev', mode: 'host', description: '', hosts: [], agent_ids: [] })
    setHostInput('')
    setAgentInput('')
  }

  const handleSave = async () => {
    if (!form.name?.trim()) { toast.error('环境名称不能为空'); return }
    const hosts = hostInput.split(',').map(s => s.trim()).filter(Boolean)
    const agent_ids = agentInput.split(',').map(s => s.trim()).filter(Boolean)
    const data = { ...form, hosts, agent_ids }
    if (editTarget) {
      await updateEnv(editTarget.id, data)
      setEditTarget(null)
    } else {
      await createEnv(data)
      setCreateOpen(false)
    }
  }

  const handleConfirmDelete = async () => {
    if (!confirmDelete) return
    const ok = await deleteEnv(confirmDelete.id)
    if (ok) setConfirmDelete(null)
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">环境管理</h1>
          <p className="text-sm text-muted-foreground">管理发布环境的部署模式（主机/K8s 容器）、目标主机与执行 Agent</p>
        </div>
        <Button size="sm" onClick={openCreate}><Plus className="h-3.5 w-3.5 mr-1" />新建环境</Button>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Cloud className="h-3 w-3 inline mr-1" />环境总数</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Server className="h-3 w-3 inline mr-1" />主机环境</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-slate-500">{stats.host}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Container className="h-3 w-3 inline mr-1" />容器环境</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-violet-500">{stats.container}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Boxes className="h-3 w-3 inline mr-1" />绑定 Agent</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{stats.agents}</span></CardContent></Card>
      </div>

      {/* 环境卡片网格 */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {environments.map(env => {
          const cm = ENV_COLOR_MAP[env.type === 'prod' ? 'red' : env.type === 'staging' ? 'purple' : env.type === 'test' ? 'amber' : 'blue'] || ENV_COLOR_MAP.blue
          const mode = (env.mode || 'host') as DeployMode
          const mm = MODE_META[mode]
          const MIcon = mm.icon
          return (
            <Card key={env.id} className={cn("border", cm.border, cm.bg)}>
              <CardHeader className="py-3 pb-2">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <div className={cn("w-2.5 h-2.5 rounded-full", cm.dot)} />
                    <span className="text-sm font-bold">{env.name}</span>
                    <Badge variant="outline" className={cn("text-[10px]", cm.text, cm.border)}>{ENV_LABELS[env.type] || env.type}</Badge>
                    <span className={cn("text-[10px] px-1.5 py-0.5 rounded border inline-flex items-center gap-1", mm.color)}><MIcon className="h-2.5 w-2.5" />{mm.label}</span>
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
                  <div className="flex items-center gap-1.5"><Server className="h-3 w-3 text-muted-foreground" /><span className="text-muted-foreground">主机:</span><span className="font-mono">{(env.hosts || []).length} 台</span></div>
                  <div className="flex items-center gap-1.5"><Globe className="h-3 w-3 text-muted-foreground" /><span className="text-muted-foreground">Agent:</span><span className="font-mono">{(env.agent_ids || []).length} 个</span></div>
                </div>
                <div className="flex items-center gap-2 pt-1 border-t text-[10px] text-muted-foreground">
                  {env.status === 'active' ? <CheckCircle className="h-3 w-3 text-emerald-500" /> : <AlertTriangle className="h-3 w-3 text-amber-500" />}
                  <span>{env.status === 'active' ? '启用中' : env.status}</span>
                  {env.updated_at && <><Clock className="h-3 w-3" /><span>{formatTime(env.updated_at)}</span></>}
                </div>
                {(env.hosts || []).length > 0 && (
                  <div className="flex flex-wrap gap-1 pt-1">
                    {(env.hosts || []).map(h => <span key={h} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-muted/50">{h}</span>)}
                  </div>
                )}
                {(env.agent_ids || []).length > 0 && (
                  <div className="flex flex-wrap gap-1">
                    {(env.agent_ids || []).map(a => <span key={a} className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-600 border border-emerald-500/20">{a}</span>)}
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
                <label className="text-xs font-medium">环境类型</label>
                <select className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.type} onChange={e => setForm(f => ({ ...f, type: e.target.value as any }))} disabled={!!editTarget}>
                  {Object.entries(ENV_LABELS).map(([v, l]) => <option key={v} value={v}>{l}</option>)}
                </select>
              </div>
              <div className="col-span-2">
                <label className="text-xs font-medium">部署模式</label>
                <div className="mt-1 flex gap-2">
                  {(Object.keys(MODE_META) as DeployMode[]).map(m => {
                    const on = (form.mode || 'host') === m
                    const Meta = MODE_META[m]
                    const Icon = Meta.icon
                    return (
                      <button key={m} type="button" onClick={() => setForm(f => ({ ...f, mode: m }))}
                        className={cn("flex-1 flex items-center justify-center gap-1.5 h-9 rounded-md border text-xs", on ? cn(Meta.color, 'font-medium') : 'border-input text-muted-foreground hover:bg-muted/30')}>
                        <Icon className="h-3.5 w-3.5" />{Meta.label}
                      </button>
                    )
                  })}
                </div>
              </div>
              <div className="col-span-2"><label className="text-xs font-medium">描述</label><input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.description || ''} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>
              <div className="col-span-2"><label className="text-xs font-medium">目标主机（逗号分隔，仅展示/记录）</label><input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm font-mono" value={hostInput} onChange={e => setHostInput(e.target.value)} placeholder="host-01, host-02" /></div>
              <div className="col-span-2">
                <label className="text-xs font-medium">执行 Agent ID（逗号分隔，决定脚本/指令发往哪些 Agent）</label>
                <input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm font-mono" value={agentInput} onChange={e => setAgentInput(e.target.value)} placeholder="agent-xxxx, agent-yyyy（容器模式填 K8s 网关 Agent）" />
                <p className="text-[10px] text-muted-foreground mt-1">主机模式：填被管主机 Agent；容器模式：填集群内的 K8s 网关 Agent（带 kubectl）。</p>
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
            <p className="text-sm text-muted-foreground">确认删除 <span className="font-medium text-foreground">{confirmDelete.name}</span>？</p>
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setConfirmDelete(null)}>取消</Button>
              <Button variant="destructive" size="sm" onClick={handleConfirmDelete}><Trash2 className="h-3.5 w-3.5 mr-1" />确认删除</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
