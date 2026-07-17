'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch } from '@/lib/utils'
import { Plus, Play, RefreshCw, Server, Wifi, Cloud, Terminal, CheckCircle, Clock, Loader2 } from 'lucide-react'
import { cn } from '@/lib/utils'

interface DiscoveryTask {
  id: string
  name: string
  type: 'agent' | 'snmp' | 'api' | 'cloud'
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

const DISCOVERY_TYPES = [
  { value: 'agent', label: 'Agent 发现', icon: Terminal, desc: '通过已部署的 Agent 扫描网络资产' },
  { value: 'snmp', label: 'SNMP 发现', icon: Wifi, desc: '通过 SNMP 协议发现网络设备' },
  { value: 'api', label: 'API 接入', icon: Cloud, desc: '通过第三方 API 接入资产数据' },
  { value: 'cloud', label: '云厂商同步', icon: Cloud, desc: '从 AWS/阿里云/腾讯云同步资产' },
]

export default function CMDBDiscoveryPage() {
  const [tasks, setTasks] = useState<DiscoveryTask[]>([])
  const [results, setResults] = useState<DiscoveryResult[]>([])
  const [selectedTask, setSelectedTask] = useState<string | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ name: '', type: 'agent' as const, target: '', config: '{}' })
  const [loading, setLoading] = useState(true)

  const fetchTasks = async () => {
    setLoading(true)
    try {
      const data = await apiFetch<DiscoveryTask[]>('/cmdb/discovery')
      setTasks(data || [])
    } catch { setTasks([]) }
    setLoading(false)
  }

  const fetchResults = async (taskId: string) => {
    try {
      const data = await apiFetch<DiscoveryResult[]>(`/cmdb/discovery/${taskId}/results`)
      setResults(data || [])
    } catch { setResults([]) }
  }

  useEffect(() => { fetchTasks() }, [])

  useEffect(() => {
    if (selectedTask) fetchResults(selectedTask)
  }, [selectedTask])

  const handleCreateTask = async () => {
    try {
      await apiFetch('/cmdb/discovery', {
        method: 'POST',
        body: JSON.stringify({ ...form, config: JSON.parse(form.config) })
      })
      fetchTasks()
    } catch (e) { console.error(e) }
    setDialogOpen(false)
    setForm({ name: '', type: 'agent', target: '', config: '{}' })
  }

  const runTask = async (id: string) => {
    setTasks(prev => prev.map(t => t.id === id ? { ...t, status: 'running' } : t))
    try {
      await apiFetch(`/cmdb/discovery/${id}/run`, { method: 'POST' })
      // 轮询等待完成
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
    const t = DISCOVERY_TYPES.find(d => d.value === type)
    return t ? <t.icon className="h-4 w-4" /> : <Server className="h-4 w-4" />
  }

  const unsyncedCount = results.filter(r => !r.synced).length
  const syncedCount = results.filter(r => r.synced).length
  const runningCount = tasks.filter(t => t.status === 'running').length

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">自动发现与采集</h1>
          <p className="text-sm text-muted-foreground">通过 Agent、SNMP、API、云厂商接口自动发现资产</p>
        </div>
        <div className="flex gap-2">
          {selectedTask && unsyncedCount > 0 && (
            <Button variant="outline" onClick={() => syncAll(selectedTask)}><RefreshCw className="h-4 w-4" />全部同步 ({unsyncedCount})</Button>
          )}
          <Button onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4" />新建任务</Button>
        </div>
      </div>

      {/* 统计卡片 */}
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
        {/* 左侧：任务列表 */}
        <div className="w-80 space-y-2 shrink-0">
          <h3 className="text-sm font-medium text-muted-foreground px-1">发现任务</h3>
          {loading ? (
            <div className="text-center py-4 text-muted-foreground text-xs">加载中...</div>
          ) : tasks.length === 0 ? (
            <div className="text-center py-8 text-muted-foreground text-xs">暂无任务</div>
          ) : tasks.map(task => (
            <Card key={task.id} className={cn("cursor-pointer transition-all hover:shadow-md", selectedTask === task.id && "border-emerald-500")} onClick={() => setSelectedTask(task.id)}>
              <CardContent className="p-3">
                <div className="flex items-center gap-3">
                  <div className="h-8 w-8 rounded-lg bg-gray-100 dark:bg-gray-800 flex items-center justify-center shrink-0">
                    {typeIcon(task.type)}
                  </div>
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
                  <Button variant="ghost" size="sm" className="h-7 w-7 p-0 shrink-0" onClick={e => { e.stopPropagation(); runTask(task.id) }} disabled={task.status === 'running'}>
                    {task.status === 'running' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>

        {/* 右侧：发现结果 */}
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
          <div><label className="text-sm font-medium">任务名称 *</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="内网服务器扫描" /></div>
          <div>
            <label className="text-sm font-medium">发现方式 *</label>
            <div className="grid grid-cols-2 gap-2 mt-1">
              {DISCOVERY_TYPES.map(dt => {
                const Icon = dt.icon
                return (
                  <button key={dt.value} className={cn("p-3 rounded-lg border text-left transition-all", form.type === dt.value ? "border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10" : "border-gray-200 dark:border-gray-700 hover:border-gray-300")} onClick={() => setForm(f => ({ ...f, type: dt.value as any }))}>
                    <div className="flex items-center gap-2"><Icon className="h-4 w-4" /><span className="text-sm font-medium">{dt.label}</span></div>
                    <p className="text-xs text-muted-foreground mt-1">{dt.desc}</p>
                  </button>
                )
              })}
            </div>
          </div>
          <div><label className="text-sm font-medium">扫描目标 *</label><Input value={form.target} onChange={e => setForm(f => ({ ...f, target: e.target.value }))} placeholder="192.168.1.0/24 或 region" /></div>
          <div><label className="text-sm font-medium">配置 (JSON)</label><textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono" value={form.config} onChange={e => setForm(f => ({ ...f, config: e.target.value }))} placeholder='{"timeout": 30}' /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleCreateTask} disabled={!form.name || !form.target}>创建</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
