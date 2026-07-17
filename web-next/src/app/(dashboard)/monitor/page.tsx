'use client'

import { useEffect, useState } from 'react'
import { useMonitorStore, type MonitorTarget, type AlertEvent } from '@/stores/monitor'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatTime, apiFetch } from '@/lib/utils'
import { useWebSocket } from '@/lib/useWebSocket'
import { Plus, Trash2, Play, CheckCircle, XCircle, AlertTriangle, Activity, Eye } from 'lucide-react'

const severityVariant: Record<string, 'destructive' | 'warning' | 'secondary'> = { critical: 'destructive', high: 'destructive', warning: 'warning', info: 'secondary' }
const statusVariant: Record<string, 'success' | 'destructive' | 'secondary'> = { up: 'success', down: 'destructive', unknown: 'secondary' }
const statusLabels: Record<string, string> = { up: '正常', down: '宕机', unknown: '未知' }
const PROTOCOL_OPTIONS = ['tcp', 'http', 'icmp', 'dns', 'ssl', 'udp']

function targetStatusVariant(s: string): 'success' | 'destructive' | 'secondary' | 'outline' {
  if (s === 'up') return 'success'
  if (s === 'down') return 'destructive'
  return 'secondary'
}

export default function MonitorPage() {
  const store = useMonitorStore()
  const { targets, alerts, rules, alertStats, loading, error, probeResult, probeResults } = store
  const [tab, setTab] = useState('targets')
  const [targetDialogOpen, setTargetDialogOpen] = useState(false)
  const [targetForm, setTargetForm] = useState({ name: '', address: '', port: 80, probes: ['tcp'], ci_id: '' })
  const [ruleDialogOpen, setRuleDialogOpen] = useState(false)
  const [ruleForm, setRuleForm] = useState({ name: '', metric_name: 'cpu_usage_percent', operator: '>', threshold: 80, duration: 60, severity: 'warning' })
  const [probeDialogOpen, setProbeDialogOpen] = useState(false)
  const [probeForm, setProbeForm] = useState({ address: '', port: 80, protocol: 'tcp' })
  const [alertDetail, setAlertDetail] = useState<AlertEvent | null>(null)
  const [alertDialog, setAlertDialog] = useState(false)

  useEffect(() => {
    store.fetchTargets()
    store.fetchAlerts()
    store.fetchAlertStats()
    store.fetchRules()
    store.fetchMetrics()
  }, [])

  const refresh = () => {
    store.fetchTargets()
    store.fetchAlerts()
    store.fetchAlertStats()
    store.fetchRules()
    store.fetchMetrics()
  }

  useWebSocket({
    onEvent: (data: any) => {
      if (data.type === 'alert') refresh()
    },
  })

  const handleAddTarget = async () => {
    await store.addTarget(targetForm)
    setTargetDialogOpen(false)
    setTargetForm({ name: '', address: '', port: 80, probes: ['tcp'], ci_id: '' })
    refresh()
  }

  const handleProbe = async () => {
    await store.probe(probeForm)
  }

  const handleCreateRule = async () => {
    await store.createRule(ruleForm)
    setRuleDialogOpen(false)
    setRuleForm({ name: '', metric_name: 'cpu_usage_percent', operator: '>', threshold: 80, duration: 60, severity: 'warning' })
  }

  const selectedProbes = targetForm.probes.filter(Boolean)

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">监控中心</h1><p className="text-sm text-muted-foreground">主动探测与智能告警</p></div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={refresh}><Activity className="h-3 w-3 mr-1" />刷新</Button>
          <Button size="sm" onClick={() => setTargetDialogOpen(true)}><Plus className="h-4 w-4" />添加目标</Button>
        </div>
      </div>

      <div className="grid grid-cols-4 gap-3">
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">监控目标</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold">{targets.length}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">活跃告警</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold">{alertStats.active_alerts || 0}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">严重告警</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-destructive">{alertStats.critical || 0}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">告警规则</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold">{rules.length}</span></CardContent>
        </Card>
      </div>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="targets">监控目标 ({targets.length})</TabsTrigger>
          <TabsTrigger value="alerts"><AlertTriangle className="w-3 h-3 mr-1" />告警 ({alerts.length})</TabsTrigger>
          <TabsTrigger value="rules">告警规则 ({rules.length})</TabsTrigger>
          <TabsTrigger value="probe">手动探测</TabsTrigger>
          <TabsTrigger value="metrics">指标</TabsTrigger>
        </TabsList>

        <TabsContent value="targets" className="mt-4">
          <Card>
            <CardContent className="p-0">
              {targets.length === 0 ? (
                <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无监控目标。从 CMDB 创建 CI 后自动发现，或手动添加。</p>
              ) : (
                <table className="w-full text-sm">
                  <thead><tr className="border-b text-left text-muted-foreground">
                    <th className="pb-2 px-4 font-medium">名称</th>
                    <th className="pb-2 px-4 font-medium">地址</th>
                    <th className="pb-2 px-4 font-medium">探测</th>
                    <th className="pb-2 px-4 font-medium">状态</th>
                    <th className="pb-2 px-4 font-medium">最后检查</th>
                    <th className="pb-2 px-4 font-medium w-20"></th>
                  </tr></thead>
                  <tbody>
                    {targets.map(t => (
                      <tr key={t.id} className="border-b last:border-0">
                        <td className="py-2 px-4 font-medium">{t.name}</td>
                        <td className="py-2 px-4 text-muted-foreground">{t.address}{t.port ? `:${t.port}` : ''}</td>
                        <td className="py-2 px-4">
                          <div className="flex gap-1">{t.probes?.map(p => <Badge key={p} variant="outline" className="text-xs">{p}</Badge>)}</div>
                        </td>
                        <td className="py-2 px-4"><Badge variant={targetStatusVariant(t.status)}>{statusLabels[t.status] || t.status}</Badge></td>
                        <td className="py-2 px-4 text-muted-foreground text-xs">{t.last_checked ? formatTime(t.last_checked) : '未检查'}</td>
                        <td className="py-2 px-4">
                          <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => store.deleteTarget(t.id)}><Trash2 className="h-3 w-3" /></Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="alerts" className="mt-4">
          <Card>
            <CardContent className="p-0">
              {alerts.length === 0 ? (
                <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无活跃告警。</p>
              ) : (
                <table className="w-full text-sm">
                  <thead><tr className="border-b text-left text-muted-foreground">
                    <th className="pb-2 px-4 font-medium">严重度</th>
                    <th className="pb-2 px-4 font-medium">标题</th>
                    <th className="pb-2 px-4 font-medium">规则</th>
                    <th className="pb-2 px-4 font-medium">触发时间</th>
                    <th className="pb-2 px-4 font-medium w-40">操作</th>
                  </tr></thead>
                  <tbody>
                    {alerts.map(a => (
                      <tr key={a.id} className="border-b last:border-0">
                        <td className="py-2 px-4"><Badge variant={severityVariant[a.severity] || 'secondary'} className="text-xs">{a.severity}</Badge></td>
                        <td className="py-2 px-4 max-w-xs truncate">{a.title}</td>
                        <td className="py-2 px-4 text-muted-foreground text-xs">{a.rule_name}</td>
                        <td className="py-2 px-4 text-muted-foreground text-xs">{formatTime(a.triggered_at)}</td>
                        <td className="py-2 px-4">
                          <div className="flex gap-1">
                            <Button variant="outline" size="sm" className="text-xs h-7" onClick={() => { setAlertDetail(a); setAlertDialog(true) }}><Eye className="h-3 w-3 mr-0.5" />详情</Button>
                            <Button variant="outline" size="sm" className="text-xs h-7" onClick={() => { store.ackAlert(a.id); refresh() }}><CheckCircle className="h-3 w-3 mr-0.5" />确认</Button>
                            <Button variant="outline" size="sm" className="text-xs h-7" onClick={() => { store.resolveAlert(a.id); refresh() }}><XCircle className="h-3 w-3 mr-0.5" />解决</Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="rules" className="mt-4">
          <div className="flex justify-end mb-2">
            <Button size="sm" onClick={() => setRuleDialogOpen(true)}><Plus className="h-3 w-3 mr-1" />新建规则</Button>
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
                        <td className="py-2 px-4"><Badge variant={severityVariant[r.severity] || 'secondary'} className="text-xs">{r.severity}</Badge></td>
                        <td className="py-2 px-4">{r.enabled ? <Badge variant="success" className="text-xs">启用</Badge> : <Badge variant="outline" className="text-xs">禁用</Badge>}</td>
                        <td className="py-2 px-4"><Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => store.deleteRule(r.id)}><Trash2 className="h-3 w-3" /></Button></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="probe" className="mt-4">
          <Card>
            <CardHeader><CardTitle className="text-sm">手动探测</CardTitle></CardHeader>
            <CardContent className="space-y-3">
              <div className="grid grid-cols-3 gap-3">
                <div><label className="text-sm font-medium">地址</label><Input value={probeForm.address} onChange={e => setProbeForm(f => ({ ...f, address: e.target.value }))} placeholder="example.com" /></div>
                <div><label className="text-sm font-medium">端口</label><Input type="number" value={probeForm.port} onChange={e => setProbeForm(f => ({ ...f, port: parseInt(e.target.value) || 0 }))} /></div>
                <div><label className="text-sm font-medium">协议</label>
                  <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={probeForm.protocol} onChange={e => setProbeForm(f => ({ ...f, protocol: e.target.value }))}>
                    {PROTOCOL_OPTIONS.map(p => <option key={p} value={p}>{p.toUpperCase()}</option>)}
                  </select>
                </div>
              </div>
              <Button onClick={handleProbe} disabled={!probeForm.address}><Play className="h-3 w-3 mr-1" />开始探测</Button>
              {probeResult && (
                <div className="border rounded-md p-3 mt-2">
                  {probeResult.success ? (
                    <div className="flex items-center gap-2 text-emerald-500"><CheckCircle className="h-4 w-4" /><span className="font-medium">探测成功</span><span className="text-muted-foreground text-sm">延迟 {probeResult.latency_ms}ms</span></div>
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
        </TabsContent>

        <TabsContent value="metrics" className="mt-4">
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
                          <td className="py-1.5 px-4"><Badge variant={r.success ? 'success' : 'destructive'} className="text-xs">{r.success ? '成功' : '失败'}</Badge></td>
                          <td className="py-1.5 px-4 text-muted-foreground text-xs">{formatTime(r.timestamp)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      <Dialog open={targetDialogOpen} onClose={() => setTargetDialogOpen(false)} title="添加监控目标">
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
        <DialogFooter><Button variant="outline" onClick={() => setTargetDialogOpen(false)}>取消</Button><Button onClick={handleAddTarget} disabled={!targetForm.name || !targetForm.address}>添加</Button></DialogFooter>
      </Dialog>

      <Dialog open={ruleDialogOpen} onClose={() => setRuleDialogOpen(false)} title="新建告警规则">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">规则名称 *</label><Input value={ruleForm.name} onChange={e => setRuleForm(f => ({ ...f, name: e.target.value }))} placeholder="CPU 过高" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">指标</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={ruleForm.metric_name} onChange={e => setRuleForm(f => ({ ...f, metric_name: e.target.value }))}>
                <option value="cpu_usage_percent">CPU 使用率</option>
                <option value="memory_usage_percent">内存使用率</option>
                <option value="tcp_retransmit_count">TCP 重传数</option>
                <option value="disk_io_latency_ms">磁盘 IO 延迟</option>
                <option value="packet_loss_percent">丢包率</option>
              </select>
            </div>
            <div><label className="text-sm font-medium">操作符</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={ruleForm.operator} onChange={e => setRuleForm(f => ({ ...f, operator: e.target.value }))}>
                <option value=">">{'>'}</option><option value="<">{'<'}</option><option value=">=">{'>='}</option><option value="<=">{'<='}</option>
              </select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">阈值</label><Input type="number" value={ruleForm.threshold} onChange={e => setRuleForm(f => ({ ...f, threshold: parseFloat(e.target.value) || 0 }))} /></div>
            <div><label className="text-sm font-medium">持续时间(秒)</label><Input type="number" value={ruleForm.duration} onChange={e => setRuleForm(f => ({ ...f, duration: parseInt(e.target.value) || 0 }))} /></div>
          </div>
          <div><label className="text-sm font-medium">严重度</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={ruleForm.severity} onChange={e => setRuleForm(f => ({ ...f, severity: e.target.value }))}>
              <option value="info">Info</option><option value="warning">Warning</option><option value="critical">Critical</option>
            </select>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setRuleDialogOpen(false)}>取消</Button><Button onClick={handleCreateRule} disabled={!ruleForm.name}>创建</Button></DialogFooter>
      </Dialog>

      <Dialog open={alertDialog} onClose={() => setAlertDialog(false)} title="告警详情">
        {alertDetail && (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-2 text-sm">
              <div><span className="text-muted-foreground">标题:</span> {alertDetail.title}</div>
              <div><span className="text-muted-foreground">严重度:</span> <Badge variant={severityVariant[alertDetail.severity] || 'secondary'} className="text-xs">{alertDetail.severity}</Badge></div>
              <div><span className="text-muted-foreground">规则:</span> {alertDetail.rule_name}</div>
              <div><span className="text-muted-foreground">源 CI:</span> {alertDetail.source_ci_id || '未知'}</div>
              <div className="col-span-2"><span className="text-muted-foreground">描述:</span> {alertDetail.description || '无'}</div>
              <div><span className="text-muted-foreground">触发:</span> {formatTime(alertDetail.triggered_at)}</div>
              <div><span className="text-muted-foreground">抑制:</span> {alertDetail.is_suppressed ? '是' : '否'}</div>
              {alertDetail.root_cause_ci && <div className="col-span-2"><span className="text-muted-foreground">根因 CI:</span> {alertDetail.root_cause_ci}</div>}
              {alertDetail.resolved_at && <div className="col-span-2"><span className="text-muted-foreground">解决于:</span> {alertDetail.resolved_at}</div>}
            </div>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setAlertDialog(false)}>关闭</Button>
          {alertDetail && !alertDetail.resolved_at && (
            <>
              <Button variant="outline" size="sm" onClick={() => { store.ackAlert(alertDetail.id); setAlertDialog(false); refresh() }}><CheckCircle className="h-3 w-3 mr-1" />确认</Button>
              <Button variant="outline" size="sm" onClick={() => { store.resolveAlert(alertDetail.id); setAlertDialog(false); refresh() }}><XCircle className="h-3 w-3 mr-1" />解决</Button>
            </>
          )}
        </DialogFooter>
      </Dialog>
    </div>
  )
}
