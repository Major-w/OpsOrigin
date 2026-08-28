
import { useEffect, useState } from 'react'
import { useMonitorStore } from '@/stores/monitor'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Badge, Input, Modal, Tabs, Select, Tag, Space, Popconfirm } from 'antd'
import { formatTime } from '@/lib/utils'
import { Plus, Trash2, Play, CheckCircle, XCircle, Settings2, Loader2 } from 'lucide-react'

const severityColor: Record<string, string> = { critical: 'red', high: 'red', warning: 'orange', info: 'default' }
const statusColor: Record<string, string> = { up: 'green', down: 'red', unknown: 'default' }
const statusLabels: Record<string, string> = { up: '正常', down: '宕机', unknown: '未知' }
const PROTOCOL_OPTIONS = ['tcp', 'http', 'icmp', 'dns', 'ssl', 'udp']

function targetStatusColor(s: string): string {
  if (s === 'up') return 'green'
  if (s === 'down') return 'red'
  return 'default'
}

export default function MonitorConfigPage() {
  const store = useMonitorStore()
  const { targets, rules, probeResult, probeResults, loading } = store
  const [tab, setTab] = useState('targets')
  const [targetDialogOpen, setTargetDialogOpen] = useState(false)
  const [targetForm, setTargetForm] = useState({ name: '', address: '', port: 80, probes: ['tcp'], ci_id: '' })
  const [ruleDialogOpen, setRuleDialogOpen] = useState(false)
  const [ruleForm, setRuleForm] = useState({ name: '', metric_name: 'cpu_usage_percent', operator: '>', threshold: 80, duration: 60, severity: 'warning' })
  const [probeForm, setProbeForm] = useState({ address: '', port: 80, protocol: 'tcp' })
  const [collectMetrics, setCollectMetrics] = useState<any[]>([])
  const [metricsLoading, setMetricsLoading] = useState(false)
  const [ingestStats, setIngestStats] = useState<{ received?: number; points?: number; dropped?: number; flushed?: number; flush_error?: number } | null>(null)

  useEffect(() => {
    if (tab === 'metrics') {
      loadCollectMetrics()
      loadIngestStats()
    }
  }, [tab])

  const loadIngestStats = async () => {
    try {
      const r = await fetch('/api/v1/monitor/ingest/stats')
      if (r.ok) setIngestStats(await r.json())
    } catch { /* 后端不可用时静默 */ }
  }

  const loadCollectMetrics = async () => {
    setMetricsLoading(true)
    try {
      const r = await fetch('/api/v1/collect-metrics?limit=500')
      if (r.ok) setCollectMetrics(await r.json())
    } catch { /* 后端不可用时静默 */ }
    setMetricsLoading(false)
  }

  useEffect(() => {
    store.fetchTargets()
    store.fetchRules()
    store.fetchMetrics()
  }, [])

  const selectedProbes = targetForm.probes.filter(Boolean)

  const handleAddTarget = async () => {
    await store.addTarget(targetForm)
    setTargetDialogOpen(false)
    setTargetForm({ name: '', address: '', port: 80, probes: ['tcp'], ci_id: '' })
  }

  const handleCreateRule = async () => {
    await store.createRule(ruleForm)
    setRuleDialogOpen(false)
    setRuleForm({ name: '', metric_name: 'cpu_usage_percent', operator: '>', threshold: 80, duration: 60, severity: 'warning' })
  }

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2"><Settings2 className="h-6 w-6 text-blue-500" />监控配置</h1>
        <p className="text-sm text-muted-foreground">采集目标、手动探测、指标与告警规则管理</p>
      </div>

      <Tabs activeKey={tab} onChange={setTab} items={[
        { key: 'targets', label: `采集目标 (${targets.length})` },
        { key: 'rules', label: `告警规则 (${rules.length})` },
        { key: 'probe', label: '手动探测' },
        { key: 'metrics', label: '指标历史' },
      ]} />

      {/* 采集目标 */}
      {tab === 'targets' && (
        <div>
          <div className="flex justify-end mb-2">
            <Button type="primary" size="middle" onClick={() => setTargetDialogOpen(true)}><Plus className="h-3 w-3 mr-1" />添加目标</Button>
          </div>
          <Card>
            <CardContent className="p-0">
              {loading ? (
                <p className="py-8 text-sm text-muted-foreground text-center flex items-center justify-center gap-2"><Loader2 className="h-4 w-4 animate-spin" />加载中...</p>
              ) : targets.length === 0 ? (
                <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无监控目标。从 CMDB 创建 CI 后自动发现，或手动添加。</p>
              ) : (
                <table className="w-full text-sm">
                  <thead><tr className="border-b text-left text-muted-foreground">
                    <th className="pb-2 px-4 font-medium">名称</th>
                    <th className="pb-2 px-4 font-medium">地址</th>
                    <th className="pb-2 px-4 font-medium hidden md:table-cell">探测</th>
                    <th className="pb-2 px-4 font-medium">状态</th>
                    <th className="pb-2 px-4 font-medium hidden md:table-cell">最后检查</th>
                    <th className="pb-2 px-4 font-medium w-20"></th>
                  </tr></thead>
                  <tbody>
                    {targets.map(t => (
                      <tr key={t.id} className="border-b last:border-0">
                        <td className="py-2 px-4 font-medium">{t.name}</td>
                        <td className="py-2 px-4 text-muted-foreground">{t.address}{t.port ? `:${t.port}` : ''}</td>
                        <td className="py-2 px-4 hidden md:table-cell"><Space size={4}>{t.probes?.map(p => <Tag key={p}>{p}</Tag>)}</Space></td>
                        <td className="py-2 px-4"><Tag color={targetStatusColor(t.status)}>{statusLabels[t.status] || t.status}</Tag></td>
                        <td className="py-2 px-4 text-muted-foreground text-xs hidden md:table-cell">{t.last_checked ? formatTime(t.last_checked) : '未检查'}</td>
                        <td className="py-2 px-4">
                          <Popconfirm title="确认删除该监控目标？" onConfirm={() => store.deleteTarget(t.id)}>
                            <Button type="text" danger size="middle" className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300"><Trash2 className="h-3 w-3" /></Button>
                          </Popconfirm>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {/* 告警规则 */}
      {tab === 'rules' && (
        <div>
          <div className="flex justify-end mb-2">
            <Button type="primary" size="middle" onClick={() => setRuleDialogOpen(true)}><Plus className="h-3 w-3 mr-1" />新建规则</Button>
          </div>
          <Card>
            <CardContent className="p-0">
              {rules.length === 0 ? (
                <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无告警规则。</p>
              ) : (
                <table className="w-full text-sm">
                  <thead><tr className="border-b text-left text-muted-foreground">
                    <th className="pb-2 px-4 font-medium">名称</th>
                    <th className="pb-2 px-4 font-medium">指标</th>
                    <th className="pb-2 px-4 font-medium">条件</th>
                    <th className="pb-2 px-4 font-medium">严重度</th>
                    <th className="pb-2 px-4 font-medium">状态</th>
                    <th className="pb-2 px-4 font-medium w-16"></th>
                  </tr></thead>
                  <tbody>
                    {rules.map(r => (
                      <tr key={r.id} className="border-b last:border-0">
                        <td className="py-2 px-4 font-medium">{r.name}</td>
                        <td className="py-2 px-4 text-muted-foreground">{r.metric_name}</td>
                        <td className="py-2 px-4 text-muted-foreground">{r.operator} {r.threshold} ({r.duration}s)</td>
                        <td className="py-2 px-4"><Tag color={severityColor[r.severity] || 'default'} className="text-xs">{r.severity}</Tag></td>
                        <td className="py-2 px-4">{r.enabled ? <Tag color="green" className="text-xs">启用</Tag> : <Tag className="text-xs">禁用</Tag>}</td>
                        <td className="py-2 px-4">
                          <Popconfirm title="确认删除该告警规则？" onConfirm={() => store.deleteRule(r.id)}>
                            <Button type="text" danger size="middle" className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300"><Trash2 className="h-3 w-3" /></Button>
                          </Popconfirm>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {/* 手动探测 */}
      {tab === 'probe' && (
        <Card>
          <CardHeader><CardTitle className="text-sm">手动探测</CardTitle></CardHeader>
          <CardContent className="space-y-3">
            <div className="grid grid-cols-3 gap-3">
              <div><label className="text-sm font-medium">地址</label><Input value={probeForm.address} onChange={e => setProbeForm(f => ({ ...f, address: e.target.value }))} placeholder="example.com" /></div>
              <div><label className="text-sm font-medium">端口</label><Input type="number" value={probeForm.port} onChange={e => setProbeForm(f => ({ ...f, port: parseInt(e.target.value) || 0 }))} /></div>
              <div><label className="text-sm font-medium">协议</label>
                <Select value={probeForm.protocol} onChange={v => setProbeForm(f => ({ ...f, protocol: v }))} options={PROTOCOL_OPTIONS.map(p => ({ label: p.toUpperCase(), value: p }))} className="w-full" />
              </div>
            </div>
            <Button type="primary" onClick={() => store.probe(probeForm)} disabled={!probeForm.address}><Play className="h-3 w-3 mr-1" />开始探测</Button>
            {probeResult && (
              <div className="border rounded-md p-3 mt-2">
                {probeResult.success ? (
                  <div className="flex items-center gap-2 text-blue-500"><CheckCircle className="h-4 w-4" /><span className="font-medium">探测成功</span><span className="text-muted-foreground text-sm">延迟 {probeResult.latency_ms}ms</span></div>
                ) : (
                  <div className="text-destructive space-y-1">
                    <div className="flex items-center gap-2"><XCircle className="h-4 w-4" /><span className="font-medium">探测失败</span></div>
                    <p className="text-sm">{probeResult.error_message}</p>
                  </div>
                )}
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {/* 指标历史 */}
      {tab === 'metrics' && (
        <div>
          <Card className="mb-4">
            <CardHeader className="pb-2 flex-row items-center justify-between">
              <CardTitle className="text-sm flex items-center gap-2">
                <span className={`h-2 w-2 rounded-full ${ingestStats ? (ingestStats.dropped ? 'bg-amber-500' : 'bg-blue-500') : 'bg-gray-400'}`} />
                上报管道状态
              </CardTitle>
              <Button size="middle" className="text-xs" onClick={loadIngestStats}>刷新</Button>
            </CardHeader>
            <CardContent>
              {ingestStats === null ? (
                <p className="text-xs text-muted-foreground py-2 text-center">管道未启用或服务不可达(需配置 OPSORIGIN_CORE_VM_ADDR 并接入 Agent)。</p>
              ) : (
                <div className="grid grid-cols-2 md:grid-cols-5 gap-3">
                  <div className="rounded-lg border border-gray-200 dark:border-gray-800 p-3">
                    <div className="text-lg font-bold">{ingestStats.received ?? 0}</div>
                    <div className="text-[10px] text-muted-foreground">收到批次</div>
                  </div>
                  <div className="rounded-lg border border-gray-200 dark:border-gray-800 p-3">
                    <div className="text-lg font-bold">{ingestStats.points ?? 0}</div>
                    <div className="text-[10px] text-muted-foreground">指标点数</div>
                  </div>
                  <div className="rounded-lg border border-gray-200 dark:border-gray-800 p-3">
                    <div className="text-lg font-bold">{ingestStats.flushed ?? 0}</div>
                    <div className="text-[10px] text-muted-foreground">落库批次</div>
                  </div>
                  <div className={`rounded-lg border p-3 ${(ingestStats.dropped || 0) > 0 ? 'border-amber-500/50 bg-amber-500/5' : 'border-gray-200 dark:border-gray-800'}`}>
                    <div className="text-lg font-bold text-amber-600 dark:text-amber-400">{ingestStats.dropped ?? 0}</div>
                    <div className="text-[10px] text-muted-foreground">背压丢弃</div>
                  </div>
                  <div className={`rounded-lg border p-3 ${(ingestStats.flush_error || 0) > 0 ? 'border-red-500/50 bg-red-500/5' : 'border-gray-200 dark:border-gray-800'}`}>
                    <div className="text-lg font-bold text-red-600 dark:text-red-400">{ingestStats.flush_error ?? 0}</div>
                    <div className="text-[10px] text-muted-foreground">落库失败</div>
                  </div>
                </div>
              )}
            </CardContent>
          </Card>

          <Card className="mb-4">
            <CardHeader className="pb-2 flex-row items-center justify-between">
              <CardTitle className="text-sm">采集指标趋势</CardTitle>
              <Button size="middle" className="text-xs" onClick={loadCollectMetrics} disabled={metricsLoading}>
                {metricsLoading ? <Loader2 className="h-3 w-3 animate-spin" /> : '刷新'}
              </Button>
            </CardHeader>
            <CardContent>
              {collectMetrics.length === 0 ? (
                <p className="text-xs text-muted-foreground py-4 text-center">
                  暂无采集指标数据。可在「自动化运维 → 数据采集」创建 metric 类型采集任务，指标会持久化到这里并绘制趋势。
                </p>
              ) : (() => {
                const groups = new Map<string, { agent: string; name: string; unit: string; points: { t: number; v: number }[] }>()
                for (const m of collectMetrics) {
                  const key = `${m.agent_id || ''}::${m.name || ''}`
                  if (!groups.has(key)) groups.set(key, { agent: m.agent_id || 'unknown', name: m.name || 'metric', unit: m.unit || '', points: [] })
                  const g = groups.get(key)!
                  const ts = m.collected_at ? new Date(m.collected_at).getTime() : 0
                  g.points.push({ t: ts, v: Number(m.value) || 0 })
                }
                const list = Array.from(groups.values()).map(g => ({ ...g, points: g.points.sort((a, b) => a.t - b.t) }))
                return (
                  <div className="grid gap-4 md:grid-cols-2">
                    {list.map(g => (
                      <div key={`${g.agent}::${g.name}`} className="rounded-lg border border-gray-200 dark:border-gray-800 p-3">
                        <div className="flex items-center justify-between mb-1">
                          <span className="text-xs font-medium truncate">{g.name}</span>
                          <span className="text-[10px] text-muted-foreground">{g.agent} · {g.unit}</span>
                        </div>
                        <CollectMetricChart points={g.points} />
                      </div>
                    ))}
                  </div>
                )
              })()}
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle className="text-sm">探测指标历史 (最近 {probeResults.length} 条)</CardTitle></CardHeader>
            <CardContent className="p-0">
              {probeResults.length === 0 ? (
                <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无探测数据。</p>
              ) : (
                <div className="max-h-80 overflow-auto">
                  <table className="w-full text-sm">
                    <thead><tr className="border-b text-left text-muted-foreground sticky top-0 bg-background">
                      <th className="pb-2 px-4 font-medium">指标</th>
                      <th className="pb-2 px-4 font-medium">值</th>
                      <th className="pb-2 px-4 font-medium">单位</th>
                      <th className="pb-2 px-4 font-medium">状态</th>
                      <th className="pb-2 px-4 font-medium">时间</th>
                    </tr></thead>
                    <tbody>
                      {probeResults.slice(-50).reverse().map((r, i) => (
                        <tr key={i} className="border-b last:border-0">
                          <td className="py-1.5 px-4">{r.name}</td>
                          <td className="py-1.5 px-4">{r.value?.toFixed?.(1) || r.value}</td>
                          <td className="py-1.5 px-4 text-muted-foreground">{r.unit || ''}</td>
                          <td className="py-1.5 px-4"><Tag color={r.success ? 'green' : 'red'} className="text-xs">{r.success ? '成功' : '失败'}</Tag></td>
                          <td className="py-1.5 px-4 text-muted-foreground text-xs">{formatTime(r.timestamp)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {/* 添加目标弹窗 */}
      <Modal title="添加监控目标" open={targetDialogOpen} onCancel={() => setTargetDialogOpen(false)} footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">名称 *</label><Input value={targetForm.name} onChange={e => setTargetForm(f => ({ ...f, name: e.target.value }))} placeholder="生产数据库" /></div>
          <div><label className="text-sm font-medium">地址 *</label><Input value={targetForm.address} onChange={e => setTargetForm(f => ({ ...f, address: e.target.value }))} placeholder="10.0.1.100" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">端口</label><Input type="number" value={targetForm.port} onChange={e => setTargetForm(f => ({ ...f, port: parseInt(e.target.value) || 0 }))} /></div>
            <div><label className="text-sm font-medium">CI ID (可选)</label><Input value={targetForm.ci_id} onChange={e => setTargetForm(f => ({ ...f, ci_id: e.target.value }))} placeholder="自动生成" /></div>
          </div>
          <div>
            <label className="text-sm font-medium">探测协议</label>
            <div className="flex gap-2 flex-wrap mt-1">
              {PROTOCOL_OPTIONS.map(proto => (
                <label key={proto} className="flex items-center gap-1 text-xs">
                  <input type="checkbox" checked={selectedProbes.includes(proto)}
                    onChange={e => {
                      if (e.target.checked) setTargetForm(f => ({ ...f, probes: [...f.probes, proto] }))
                      else setTargetForm(f => ({ ...f, probes: f.probes.filter(p => p !== proto) }))
                    }} />
                  {proto.toUpperCase()}
                </label>
              ))}
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setTargetDialogOpen(false)}>取消</Button>
          <Button type="primary" onClick={handleAddTarget} disabled={!targetForm.name || !targetForm.address}>添加</Button>
        </div>
      </Modal>

      {/* 新建规则弹窗 */}
      <Modal title="新建告警规则" open={ruleDialogOpen} onCancel={() => setRuleDialogOpen(false)} footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">规则名称 *</label><Input value={ruleForm.name} onChange={e => setRuleForm(f => ({ ...f, name: e.target.value }))} placeholder="CPU 过高" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">指标</label>
              <Select value={ruleForm.metric_name} onChange={v => setRuleForm(f => ({ ...f, metric_name: v }))} options={[
                { label: 'CPU 使用率', value: 'cpu_usage_percent' },
                { label: '内存使用率', value: 'memory_usage_percent' },
                { label: 'TCP 重传数', value: 'tcp_retransmit_count' },
                { label: '磁盘 IO 延迟', value: 'disk_io_latency_ms' },
                { label: '丢包率', value: 'packet_loss_percent' },
              ]} className="w-full" />
            </div>
            <div><label className="text-sm font-medium">操作符</label>
              <Select value={ruleForm.operator} onChange={v => setRuleForm(f => ({ ...f, operator: v }))} options={[
                { label: '>', value: '>' }, { label: '<', value: '<' }, { label: '>=', value: '>=' }, { label: '<=', value: '<=' },
              ]} className="w-full" />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">阈值</label><Input type="number" value={ruleForm.threshold} onChange={e => setRuleForm(f => ({ ...f, threshold: parseFloat(e.target.value) || 0 }))} /></div>
            <div><label className="text-sm font-medium">持续时间(秒)</label><Input type="number" value={ruleForm.duration} onChange={e => setRuleForm(f => ({ ...f, duration: parseInt(e.target.value) || 0 }))} /></div>
          </div>
          <div><label className="text-sm font-medium">严重度</label>
            <Select value={ruleForm.severity} onChange={v => setRuleForm(f => ({ ...f, severity: v }))} options={[
              { label: 'Info', value: 'info' }, { label: 'Warning', value: 'warning' }, { label: 'Critical', value: 'critical' },
            ]} className="w-full" />
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setRuleDialogOpen(false)}>取消</Button>
          <Button type="primary" onClick={handleCreateRule} disabled={!ruleForm.name}>创建</Button>
        </div>
      </Modal>
    </div>
  )
}

function CollectMetricChart({ points }: { points: { t: number; v: number }[] }) {
  const W = 520, H = 110, PAD = 10
  if (points.length < 2) {
    return <p className="text-[10px] text-muted-foreground py-4 text-center">数据点不足(≥2 个采样才可绘制)</p>
  }
  const vals = points.map(p => p.v)
  const min = Math.min(...vals)
  const max = Math.max(...vals)
  const range = max - min || 1
  const pts = points.map((p, i) => {
    const x = PAD + (i / (points.length - 1)) * (W - PAD * 2)
    const y = H - PAD - ((p.v - min) / range) * (H - PAD * 2)
    return `${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
  const first = new Date(points[0].t)
  const last = new Date(points[points.length - 1].t)
  const fmt = (d: Date) => `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full" preserveAspectRatio="none" role="img" aria-label="指标趋势">
        <polyline points={pts} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" className="text-blue-500" />
      </svg>
      <div className="flex justify-between text-[10px] text-muted-foreground mt-0.5">
        <span>{fmt(first)}</span>
        <span>min {min.toFixed(1)} · max {max.toFixed(1)}</span>
        <span>{fmt(last)}</span>
      </div>
    </div>
  )
}
