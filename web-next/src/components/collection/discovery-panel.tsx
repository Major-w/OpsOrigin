'use client'

/**
 * 自动发现面板：探针 Agent / SNMP 主动扫描，把网络中"未知"资产捞进来。
 *
 * 与采集任务的分工：采集任务是对「已纳管资源」的周期性刷新；自动发现是对
 * 「尚未入库资产」的探测，发现结果需人工/批量确认后才同步为配置项。
 */

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch, apiList, cn } from '@/lib/utils'
import {
  Box as AppIcon, CheckCircle, Clock, Database, GitBranch, Layers,
  Loader2, Play, Plus, RefreshCw, Server, ShieldAlert, Wifi,
} from 'lucide-react'

interface DiscoveryTask {
  id: string
  name: string
  type: string
  target: string
  status: 'idle' | 'running' | 'success' | 'failed'
  config: Record<string, any>
  found_count: number
  last_run_at?: string
  created_at: string
}

interface DiscoveryResult {
  id: string
  task_id: string
  ci_name: string
  ci_type: string
  ip_address: string
  attributes: Record<string, any>
  source: string
  synced: boolean
  synced_ci_id?: string
  discovered_at: string
}

/** 后端 GET /cmdb/discovery/types 返回的插件类型信息。 */
interface DiscoveryTypeInfo {
  type: string
  label: string
  ci_type: string
  channel: string
  note: string
}

interface QualityIssue {
  ci_id?: string
  ci_name?: string
  type_id?: string
  dimension: string
  severity: string
  message: string
}

interface QualityReport {
  tenant_id: string
  scanned_at: string
  score: number
  by_type: { type_id: string; name: string; total: number; completeness: number; issue_count: number }[]
  issues: QualityIssue[]
}

const TYPE_ICON: Record<string, any> = {
  server: Server, host: Server, snmp: Wifi, network: Wifi,
  database: Database, middleware: Layers, application: AppIcon, app: AppIcon,
  relation: GitBranch,
}

const TYPE_CONFIG_HINT: Record<string, string> = {
  snmp: '{"agent_id":"探针AgentID","community":"public","snmp_ver":"2c","role":"core"}',
  network: '{"agent_id":"探针AgentID","community":"public"}',
  database: '{"agent_id":"探针AgentID","engine":"mysql","port":"3306","db_user":"root","db_pass":"***"}',
  middleware: '{"agent_id":"探针AgentID","mtype":"redis","port":"6379"}',
  application: '{"agent_id":"探针AgentID","ports":"8080,443","mtype":"tomcat"}',
  server: '{"agent_id":"探针AgentID"}',
  host: '{"agent_id":"探针AgentID"}',
  relation: '{}',
}

const FALLBACK_TYPES: DiscoveryTypeInfo[] = [
  { type: 'server', label: '物理服务器', ci_type: 'server', channel: 'agent', note: '探针 Agent 本地采集' },
  { type: 'snmp', label: '网络设备(SNMP)', ci_type: 'switch', channel: 'snmp', note: '交换机/路由器/防火墙' },
  { type: 'database', label: '数据库实例', ci_type: 'database', channel: 'agent', note: 'MySQL/PG/Oracle' },
  { type: 'middleware', label: '中间件实例', ci_type: 'middleware', channel: 'agent', note: 'Redis/Nginx/ES/Kafka' },
  { type: 'application', label: '应用程序', ci_type: 'app', channel: 'agent', note: '端口+HTTP 指纹' },
]

export function DiscoveryPanel() {
  const [tasks, setTasks] = useState<DiscoveryTask[]>([])
  const [results, setResults] = useState<DiscoveryResult[]>([])
  const [typeInfos, setTypeInfos] = useState<DiscoveryTypeInfo[]>([])
  const [quality, setQuality] = useState<QualityReport | null>(null)
  const [selectedTask, setSelectedTask] = useState<string | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ name: '', type: 'server', target: '', config: '{"agent_id":"探针AgentID"}' })
  const [loading, setLoading] = useState(true)

  const fetchTasks = async () => {
    setLoading(true)
    try { setTasks(await apiList<DiscoveryTask>('/cmdb/discovery')) } catch { setTasks([]) }
    setLoading(false)
  }

  const fetchResults = async (taskId: string) => {
    try { setResults(await apiList<DiscoveryResult>(`/cmdb/discovery/${taskId}/results`)) } catch { setResults([]) }
  }

  const fetchTypeInfos = async () => {
    try {
      const data = await apiFetch<{ types: DiscoveryTypeInfo[] }>('/cmdb/discovery/types')
      setTypeInfos(data?.types || [])
    } catch { setTypeInfos([]) }
  }

  const fetchQuality = async () => {
    try { setQuality(await apiFetch<QualityReport>('/cmdb/quality')) } catch { setQuality(null) }
  }

  useEffect(() => { fetchTasks(); fetchTypeInfos(); fetchQuality() }, [])
  useEffect(() => { if (selectedTask) fetchResults(selectedTask) }, [selectedTask])

  const handleCreateTask = async () => {
    try {
      await apiFetch('/cmdb/discovery', { method: 'POST', body: JSON.stringify({ ...form, config: JSON.parse(form.config) }) })
      fetchTasks()
    } catch (e) { console.error(e) }
    setDialogOpen(false)
    setForm({ name: '', type: 'server', target: '', config: '{"agent_id":"探针AgentID"}' })
  }

  const runTask = async (id: string) => {
    setTasks(prev => prev.map(t => t.id === id ? { ...t, status: 'running' } : t))
    try {
      await apiFetch(`/cmdb/discovery/${id}/run`, { method: 'POST' })
      setTimeout(() => { fetchTasks(); if (selectedTask === id) fetchResults(id) }, 3000)
    } catch { setTasks(prev => prev.map(t => t.id === id ? { ...t, status: 'failed' } : t)) }
  }

  const syncResult = async (resultId: string) => {
    try {
      await apiFetch(`/cmdb/discovery/${resultId}/sync`, { method: 'POST' })
      setResults(prev => prev.map(r => r.id === resultId ? { ...r, synced: true } : r))
    } catch (e) { console.error(e) }
  }

  const syncAll = async (taskId: string) => {
    try {
      await apiFetch(`/cmdb/discovery/${taskId}/sync-all`, { method: 'POST' })
      setResults(prev => prev.map(r => r.task_id === taskId ? { ...r, synced: true } : r))
    } catch (e) { console.error(e) }
  }

  const typeIcon = (type: string) => {
    const Icon = TYPE_ICON[type] || Server
    return <Icon className="h-4 w-4" />
  }

  const unsyncedCount = results.filter(r => !r.synced).length
  const syncedCount = results.filter(r => r.synced).length
  const runningCount = tasks.filter(t => t.status === 'running').length
  const typeList = typeInfos.length > 0 ? typeInfos : FALLBACK_TYPES

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-end gap-2">
        {selectedTask && unsyncedCount > 0 && (
          <Button variant="outline" size="sm" onClick={() => syncAll(selectedTask)}>
            <RefreshCw className="h-4 w-4" />全部同步 ({unsyncedCount})
          </Button>
        )}
        <Button size="sm" onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4" />新建发现任务</Button>
      </div>

      {/* 数据质量中心 */}
      <Card className="border-emerald-200 dark:border-emerald-800">
        <CardHeader className="py-3 flex flex-row items-center justify-between">
          <CardTitle className="text-sm flex items-center gap-2"><ShieldAlert className="h-4 w-4 text-emerald-600" />数据质量中心</CardTitle>
          <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={fetchQuality}><RefreshCw className="h-3.5 w-3.5" />重新扫描</Button>
        </CardHeader>
        <CardContent className="p-4">
          {quality ? (
            <div className="space-y-3">
              <div className="flex items-center gap-4">
                <div className="h-14 w-14 rounded-full flex items-center justify-center text-lg font-bold"
                  style={{
                    background: quality.score >= 80 ? 'rgba(16,185,129,.15)' : quality.score >= 60 ? 'rgba(245,158,11,.15)' : 'rgba(239,68,68,.15)',
                    color: quality.score >= 80 ? '#059669' : quality.score >= 60 ? '#d97706' : '#dc2626',
                  }}>
                  {quality.score.toFixed(0)}
                </div>
                <div className="text-xs text-muted-foreground">
                  <p>综合质量分（完整性均值 − 问题惩罚）</p>
                  <p className="mt-1">模型类型 {quality.by_type.length} 个 · 发现问题 {quality.issues.length} 条</p>
                </div>
              </div>
              {quality.issues.length > 0 && (
                <div className="space-y-1 max-h-40 overflow-y-auto">
                  {quality.issues.slice(0, 8).map((iss, i) => (
                    <div key={i} className="flex items-start gap-2 text-xs">
                      <Badge variant={iss.severity === 'high' ? 'destructive' : iss.severity === 'medium' ? 'default' : 'outline'} className="text-[10px] shrink-0">{iss.severity}</Badge>
                      <span className="text-muted-foreground">{iss.message}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ) : (
            <div className="text-center py-4 text-muted-foreground text-xs">点击「重新扫描」执行数据质量检查</div>
          )}
        </CardContent>
      </Card>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <Card><CardContent className="p-4 flex items-center gap-3">
          <div className="h-10 w-10 rounded-lg bg-emerald-100 dark:bg-emerald-500/20 flex items-center justify-center"><Server className="h-5 w-5 text-emerald-600 dark:text-emerald-400" /></div>
          <div><p className="text-2xl font-bold">{tasks.reduce((sum, t) => sum + t.found_count, 0)}</p><p className="text-xs text-muted-foreground">已发现资产</p></div>
        </CardContent></Card>
        <Card><CardContent className="p-4 flex items-center gap-3">
          <div className="h-10 w-10 rounded-lg bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center"><CheckCircle className="h-5 w-5 text-blue-600 dark:text-blue-400" /></div>
          <div><p className="text-2xl font-bold">{syncedCount}</p><p className="text-xs text-muted-foreground">已同步</p></div>
        </CardContent></Card>
        <Card><CardContent className="p-4 flex items-center gap-3">
          <div className="h-10 w-10 rounded-lg bg-orange-100 dark:bg-orange-500/20 flex items-center justify-center"><Clock className="h-5 w-5 text-orange-600 dark:text-orange-400" /></div>
          <div><p className="text-2xl font-bold">{runningCount}</p><p className="text-xs text-muted-foreground">运行中</p></div>
        </CardContent></Card>
        <Card><CardContent className="p-4 flex items-center gap-3">
          <div className="h-10 w-10 rounded-lg bg-purple-100 dark:bg-purple-500/20 flex items-center justify-center"><Plus className="h-5 w-5 text-purple-600 dark:text-purple-400" /></div>
          <div><p className="text-2xl font-bold">{unsyncedCount}</p><p className="text-xs text-muted-foreground">待同步</p></div>
        </CardContent></Card>
      </div>

      <div className="flex gap-4">
        <div className="w-80 space-y-2 shrink-0">
          <h3 className="text-sm font-medium text-muted-foreground px-1">发现任务</h3>
          {loading ? (
            <div className="text-center py-4 text-muted-foreground text-xs">加载中...</div>
          ) : tasks.length === 0 ? (
            <div className="text-center py-8 text-muted-foreground text-xs">暂无任务</div>
          ) : tasks.map(task => (
            <Card key={task.id} className={cn('cursor-pointer transition-all hover:shadow-md', selectedTask === task.id && 'border-emerald-500')} onClick={() => setSelectedTask(task.id)}>
              <CardContent className="p-3">
                <div className="flex items-center gap-3">
                  <div className="h-8 w-8 rounded-lg bg-gray-100 dark:bg-gray-800 flex items-center justify-center shrink-0">{typeIcon(task.type)}</div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium truncate">{task.name}</span>
                      <Badge variant={task.status === 'success' ? 'secondary' : task.status === 'running' ? 'default' : 'outline'} className="text-[10px] shrink-0">
                        {task.status === 'running' && <Loader2 className="h-3 w-3 mr-1 animate-spin" />}
                        {task.status === 'idle' ? '待执行' : task.status === 'running' ? '运行中' : task.status === 'success' ? '成功' : '失败'}
                      </Badge>
                    </div>
                    <p className="text-xs text-muted-foreground mt-0.5 truncate">目标: {task.target} · {task.found_count} 个资产</p>
                  </div>
                  <Button variant="ghost" size="sm" className="h-7 w-7 p-0 shrink-0"
                    onClick={e => { e.stopPropagation(); runTask(task.id) }} disabled={task.status === 'running'}>
                    {task.status === 'running' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>

        <div className="flex-1">
          {selectedTask ? (
            <Card>
              <CardHeader className="py-3"><CardTitle className="text-sm">已发现资产 ({results.length})</CardTitle></CardHeader>
              <CardContent className="p-0">
                {results.length === 0 ? (
                  <div className="text-center py-8 text-muted-foreground text-xs">该任务暂无发现结果</div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b text-left text-muted-foreground">
                          <th className="pb-2 font-medium px-4 py-2">名称</th>
                          <th className="pb-2 font-medium px-4 py-2">类型</th>
                          <th className="pb-2 font-medium px-4 py-2">IP</th>
                          <th className="pb-2 font-medium px-4 py-2">属性</th>
                          <th className="pb-2 font-medium px-4 py-2">发现时间</th>
                          <th className="pb-2 font-medium px-4 py-2">状态</th>
                          <th className="pb-2 font-medium px-4 py-2">操作</th>
                        </tr>
                      </thead>
                      <tbody>
                        {results.map(d => (
                          <tr key={d.id} className="border-b last:border-0 hover:bg-gray-50 dark:hover:bg-gray-800/50">
                            <td className="px-4 py-3 font-medium">{d.ci_name}</td>
                            <td className="px-4 py-3"><Badge variant="outline" className="text-[10px]">{d.ci_type}</Badge></td>
                            <td className="px-4 py-3 text-xs font-mono">{d.ip_address}</td>
                            <td className="px-4 py-3 text-xs text-muted-foreground max-w-[200px] truncate">
                              {d.attributes ? Object.entries(d.attributes).map(([k, v]) => `${k}=${v}`).join(', ') : '-'}
                            </td>
                            <td className="px-4 py-3 text-xs text-muted-foreground">{d.discovered_at?.slice(0, 19).replace('T', ' ')}</td>
                            <td className="px-4 py-3">
                              {d.synced
                                ? <Badge variant="secondary" className="text-[10px]"><CheckCircle className="h-3 w-3 mr-1" />已同步</Badge>
                                : <Badge variant="outline" className="text-[10px]">待同步</Badge>}
                            </td>
                            <td className="px-4 py-3">
                              {!d.synced && <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => syncResult(d.id)}>同步</Button>}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </CardContent>
            </Card>
          ) : (
            <Card className="h-[300px] flex items-center justify-center">
              <div className="text-center text-muted-foreground">
                <Server className="h-12 w-12 mx-auto mb-3 opacity-30" />
                <p>请在左侧选择一个发现任务查看结果</p>
              </div>
            </Card>
          )}
        </div>
      </div>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建发现任务">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">任务名称 *</label>
            <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="内网服务器扫描" /></div>
          <div>
            <label className="text-sm font-medium">发现方式 *</label>
            <div className="grid grid-cols-2 gap-2 mt-1">
              {typeList.map(dt => {
                const Icon = TYPE_ICON[dt.type] || Server
                return (
                  <button key={dt.type}
                    className={cn('p-3 rounded-lg border text-left transition-all',
                      form.type === dt.type ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10' : 'border-gray-200 dark:border-gray-700 hover:border-gray-300')}
                    onClick={() => setForm(f => ({ ...f, type: dt.type, config: TYPE_CONFIG_HINT[dt.type] || '{}' }))}>
                    <div className="flex items-center gap-2"><Icon className="h-4 w-4" /><span className="text-sm font-medium">{dt.label}</span></div>
                    <p className="text-xs text-muted-foreground mt-1">{dt.note}</p>
                    {dt.ci_type && <p className="text-[10px] text-muted-foreground mt-0.5">落库类型: {dt.ci_type}</p>}
                  </button>
                )
              })}
            </div>
          </div>
          <div><label className="text-sm font-medium">扫描目标 *</label>
            <Input value={form.target} onChange={e => setForm(f => ({ ...f, target: e.target.value }))} placeholder="设备IP / 主机IP / 域名" /></div>
          <div><label className="text-sm font-medium">配置 (JSON)</label>
            <textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono"
              value={form.config} onChange={e => setForm(f => ({ ...f, config: e.target.value }))} placeholder='{"agent_id":"探针AgentID"}' /></div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
          <Button onClick={handleCreateTask} disabled={!form.name || !form.target}>创建</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
