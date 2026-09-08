
import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useInspections } from '@/stores/ai'
import { apiFetch, cn } from '@/lib/utils'
import { FlaskConical, RefreshCw, Play, RotateCcw, AlertTriangle, CheckCircle2, Loader2 } from 'lucide-react'
import { getMessage } from '@/lib/message'
import { Button, Input, Modal, Select, Tag } from 'antd'

// L3-2 仿真验证飞轮: 故障注入 -> Agent 处置评估 -> 报告/回滚
interface FaultScenario {
  id: string
  name: string
  type: string
  severity: 'critical' | 'major' | 'minor'
  description: string
  recovery: string
}

interface InjectionRun {
  id: string
  scenario_id: string
  scenario_name: string
  target_id?: string
  target_name?: string
  inspection_id?: string
  status: 'injecting' | 'recovered' | 'failed'
  started_at: string
  recovered_at?: string
  agent_responded: boolean
  agent_summary?: string
  findings: number
  matched_scenario: boolean
  score: number
  report?: string
  error?: string
}

const SEV: Record<string, string> = { critical: '严重', major: '重大', minor: '轻微' }

export default function FaultSimPage() {
  const { inspections, loadInspections } = useInspections()
  const [scenarios, setScenarios] = useState<FaultScenario[]>([])
  const [runs, setRuns] = useState<InjectionRun[]>([])
  const [loading, setLoading] = useState(false)
  const [form, setForm] = useState({ scenario_id: '', target_id: '', target_name: '', inspection_id: '' })
  const [starting, setStarting] = useState(false)
  const [detail, setDetail] = useState<InjectionRun | null>(null)

  const loadAll = async () => {
    setLoading(true)
    try {
      const [scs, rs] = await Promise.all([
        apiFetch<FaultScenario[]>('/ai/faultsim/scenarios'),
        apiFetch<InjectionRun[]>('/ai/faultsim/injections'),
      ])
      setScenarios(scs)
      setRuns(rs)
    } catch (e: any) {
      getMessage().error(`加载演练数据失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadAll(); loadInspections() }, [])

  const startInjection = async () => {
    if (!form.scenario_id) return getMessage().error('请选择故障场景')
    setStarting(true)
    try {
      const run = await apiFetch<InjectionRun>('/ai/faultsim/injections', {
        method: 'POST',
        body: JSON.stringify({
          scenario_id: form.scenario_id,
          target_id: form.target_id || 'host-unknown',
          target_name: form.target_name,
          inspection_id: form.inspection_id || undefined,
        }),
      })
      getMessage().success(`已注入「${run.scenario_name}」，Agent 正在评估处置`)
      setForm(f => ({ ...f, scenario_id: '', target_id: '', target_name: '' }))
      loadAll()
      // 轮询等待评估完成
      const poll = setInterval(async () => {
        const rs = await apiFetch<InjectionRun[]>('/ai/faultsim/injections').catch(() => null)
        if (!rs) { clearInterval(poll); return }
        setRuns(rs)
        const cur = rs.find(r => r.id === run.id)
        if (cur && (cur.report || cur.error)) clearInterval(poll)
      }, 1500)
      setTimeout(() => clearInterval(poll), 30000)
    } catch (e: any) {
      getMessage().error(`注入失败: ${e.message}`)
    } finally {
      setStarting(false)
    }
  }

  const recover = async (id: string) => {
    try {
      await apiFetch(`/ai/faultsim/injections/${id}/recover`, { method: 'POST' })
      getMessage().success('演练已恢复（解除注入）')
      loadAll()
    } catch (e: any) {
      getMessage().error(`恢复失败: ${e.message}`)
    }
  }

  const isBusy = (s: string) => s === 'injecting'

  return (
    <div className="p-6 flex flex-col gap-4 h-full overflow-auto">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">故障仿真演练</h1>
          <p className="text-xs text-muted-foreground">L3 仿真验证飞轮：注入故障 → Agent 巡检处置评估 → 评分报告 → 一键恢复（可回滚）。对齐 STAROps 仿真飞轮。</p>
        </div>
        <Button size="middle" type="default" className="gap-1" onClick={loadAll} disabled={loading}>
          <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />刷新
        </Button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {scenarios.map(sc => (
          <Card key={sc.id} className={cn('transition-all cursor-pointer hover:border-primary/40', form.scenario_id === sc.id && 'border-primary/60 bg-primary/5')}
            onClick={() => setForm(f => ({ ...f, scenario_id: sc.id }))}>
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm flex items-center gap-2">
                  <FlaskConical className="h-4 w-4 text-blue-600 dark:text-blue-400" />
                  {sc.name}
                </CardTitle>
                <Tag color={sc.severity === 'critical' ? 'error' : sc.severity === 'major' ? 'warning' : 'default'}>
                  {SEV[sc.severity]}
                </Tag>
              </div>
            </CardHeader>
            <CardContent>
              <p className="text-xs text-muted-foreground">{sc.description}</p>
              <p className="text-[11px] text-blue-600 dark:text-blue-400 mt-2">恢复方式：{sc.recovery}</p>
            </CardContent>
          </Card>
        ))}
      </div>

      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">启动演练</CardTitle></CardHeader>
        <CardContent className="flex gap-2 flex-wrap items-end">
          <div className="flex-1 min-w-[140px]">
            <label className="text-xs text-muted-foreground">故障场景</label>
            <Select className="mt-1 w-full" placeholder="选择场景" allowClear value={form.scenario_id || undefined} onChange={v => setForm(f => ({ ...f, scenario_id: v ?? '' }))}>
              {scenarios.map(s => <Select.Option key={s.id} value={s.id}>{s.name}</Select.Option>)}
            </Select>
          </div>
          <div className="flex-1 min-w-[120px]">
            <label className="text-xs text-muted-foreground">注入目标（CI/主机）</label>
            <Input value={form.target_id} onChange={e => setForm(f => ({ ...f, target_id: e.target.value }))} placeholder="host-01" />
          </div>
          <div className="flex-1 min-w-[120px]">
            <label className="text-xs text-muted-foreground">目标名称</label>
            <Input value={form.target_name} onChange={e => setForm(f => ({ ...f, target_name: e.target.value }))} placeholder="web-01" />
          </div>
          <div className="flex-1 min-w-[140px]">
            <label className="text-xs text-muted-foreground">评估巡检（可选）</label>
            <Select className="mt-1 w-full" placeholder="不触发评估" allowClear value={form.inspection_id || undefined} onChange={v => setForm(f => ({ ...f, inspection_id: v ?? '' }))}>
              {inspections.filter(i => i.enabled).map(i => <Select.Option key={i.id} value={i.id}>{i.name}</Select.Option>)}
            </Select>
          </div>
          <Button className="gap-1" onClick={startInjection} disabled={starting || !form.scenario_id}>
            {starting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
            注入故障
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">演练记录</CardTitle></CardHeader>
        <CardContent className="p-0">
          {runs.length === 0 ? (
            <p className="px-4 py-6 text-sm text-muted-foreground text-center">暂无演练记录。选择一个故障场景注入，观察 Agent 处置能力。</p>
          ) : (
            <div className="divide-y">
              {runs.map(r => (
                <div key={r.id} className="px-4 py-3 flex items-center gap-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium">{r.scenario_name}</span>
                      <Tag color={r.status === 'recovered' ? 'success' : r.status === 'failed' ? 'error' : 'warning'}>
                        {r.status === 'recovered' ? '已恢复' : r.status === 'failed' ? '失败' : '演练中'}
                      </Tag>
                      {r.matched_scenario && <Tag className="text-blue-600">命中识别</Tag>}
                    </div>
                    <div className="text-[11px] text-muted-foreground truncate">
                      {r.target_name || r.target_id} · 发现 {r.findings} 项异常 · Agent {r.agent_responded ? '已响应' : '未响应'} · {new Date(r.started_at).toLocaleString()}
                    </div>
                  </div>
                  <Tag color={r.score >= 80 ? 'success' : r.score >= 60 ? 'warning' : 'default'} className="shrink-0">得分 {r.score}</Tag>
                  <Button size="middle" type="text" onClick={() => setDetail(r)}>报告</Button>
                  {isBusy(r.status) && (
                    <Button size="middle" type="default" className="gap-1 shrink-0" onClick={() => recover(r.id)}>
                      <RotateCcw className="h-3 w-3" />恢复
                    </Button>
                  )}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Modal
        open={!!detail}
        title={detail ? `演练报告：${detail.scenario_name}` : '演练报告'}
        width={600}
        onCancel={() => setDetail(null)}
        footer={[<Button key="close" type="primary" onClick={() => setDetail(null)}>关闭</Button>]}
      >
        {detail && (
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <Tag color={detail.matched_scenario ? 'success' : 'error'} className="gap-1">
                {detail.matched_scenario ? <CheckCircle2 className="h-3 w-3" /> : <AlertTriangle className="h-3 w-3" />}
                {detail.matched_scenario ? 'Agent 已识别故障' : 'Agent 未识别故障'}
              </Tag>
              <span className="text-xs text-muted-foreground">目标：{detail.target_name || detail.target_id || '-'}</span>
            </div>
            <div className="rounded-lg bg-muted/50 px-3 py-2 whitespace-pre-wrap text-xs leading-relaxed max-h-[45vh] overflow-y-auto">
              {detail.report || detail.error || '演练进行中…'}
            </div>
            {detail.agent_summary && (
              <div className="text-xs text-muted-foreground">
                <div className="font-medium mb-1">Agent 摘要</div>
                <p className="whitespace-pre-wrap">{detail.agent_summary}</p>
              </div>
            )}
            <div className="flex items-center justify-between text-xs text-muted-foreground">
              <span className="text-base font-semibold text-primary">{detail.score} 分</span>
              <span>开始 {new Date(detail.started_at).toLocaleString()}</span>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
