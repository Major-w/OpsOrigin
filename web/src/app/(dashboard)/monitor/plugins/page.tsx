
/**
 * 监控插件目录 —— 「这类组件该采什么指标、怎么连」的统一答案。
 */

import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Badge, Input, Modal, Tag, Space } from 'antd'
import { apiFetch, apiList, cn } from '@/lib/utils'
import {
  Search, RefreshCw, Blocks, Database, Server, Network, Layers,
  Globe, Cpu, HardDrive, CheckCircle2, ArrowRight, Plug,
} from 'lucide-react'
import { getMessage } from '@/lib/message'

interface ConfigField {
  key: string
  label: string
  type: 'text' | 'password' | 'number' | 'select'
  required?: boolean
  default?: string
  placeholder?: string
  help?: string
}

interface MonitorPlugin {
  key: string
  name: string
  category: string
  description: string
  default_port?: number
  protocol: string
  metrics: string[]
  config?: ConfigField[]
  experimental?: boolean
  in_use: number
  targets?: string[]
}

const CATEGORY_META: Record<string, { label: string; icon: any; color: string }> = {
  os: { label: '操作系统', icon: Server, color: 'text-blue-600 dark:text-blue-400' },
  database: { label: '数据库', icon: Database, color: 'text-violet-600 dark:text-violet-400' },
  middleware: { label: '中间件', icon: Layers, color: 'text-amber-600 dark:text-amber-400' },
  web: { label: 'Web / 应用', icon: Globe, color: 'text-blue-600 dark:text-blue-400' },
  container: { label: '容器与编排', icon: Cpu, color: 'text-cyan-600 dark:text-cyan-400' },
  network: { label: '网络设备', icon: Network, color: 'text-orange-600 dark:text-orange-400' },
  hardware: { label: '硬件带外', icon: HardDrive, color: 'text-slate-600 dark:text-slate-400' },
}

const PROTOCOL_LABELS: Record<string, string> = {
  agent: 'Agent 上报', tcp: 'TCP 直连', http: 'HTTP API',
  exporter: 'Exporter 拉取', snmp: 'SNMP', ipmi: 'IPMI',
}

export default function MonitorPluginsPage() {
  const [plugins, setPlugins] = useState<MonitorPlugin[]>([])
  const [loading, setLoading] = useState(true)
  const [q, setQ] = useState('')
  const [cat, setCat] = useState<string>('')
  const [onlyInUse, setOnlyInUse] = useState(false)

  const [active, setActive] = useState<MonitorPlugin | null>(null)
  const [form, setForm] = useState<Record<string, string>>({})
  const [name, setName] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const load = async () => {
    setLoading(true)
    try { setPlugins(await apiList<MonitorPlugin>('/monitor/plugins')) }
    catch { setPlugins([]); getMessage().error('加载插件目录失败') }
    finally { setLoading(false) }
  }

  useEffect(() => { load() }, [])

  const categories = useMemo(() => {
    const seen: string[] = []
    plugins.forEach(p => { if (!seen.includes(p.category)) seen.push(p.category) })
    return seen
  }, [plugins])

  const filtered = useMemo(() => {
    const kw = q.trim().toLowerCase()
    return plugins.filter(p => {
      if (cat && p.category !== cat) return false
      if (onlyInUse && p.in_use === 0) return false
      if (!kw) return true
      return p.name.toLowerCase().includes(kw)
        || p.key.includes(kw)
        || p.description.toLowerCase().includes(kw)
        || p.metrics.some(m => m.toLowerCase().includes(kw))
    })
  }, [plugins, q, cat, onlyInUse])

  const grouped = useMemo(() => {
    const m = new Map<string, MonitorPlugin[]>()
    filtered.forEach(p => {
      const arr = m.get(p.category) || []
      arr.push(p)
      m.set(p.category, arr)
    })
    return Array.from(m.entries())
  }, [filtered])

  const totalInUse = plugins.reduce((s, p) => s + p.in_use, 0)

  const openConnect = (p: MonitorPlugin) => {
    setActive(p)
    setName('')
    const init: Record<string, string> = {}
    ;(p.config || []).forEach(f => { init[f.key] = f.default || '' })
    if (p.default_port && init.port === undefined) init.port = String(p.default_port)
    setForm(init)
  }

  const submit = async () => {
    if (!active) return
    const missing = (active.config || []).filter(f => f.required && !form[f.key]?.trim())
    if (missing.length > 0) { getMessage().error(`请填写：${missing.map(f => f.label).join('、')}`); return }
    if (!name.trim()) { getMessage().error('请填写目标名称'); return }

    setSubmitting(true)
    try {
      const labels: Record<string, string> = { plugin: active.key }
      Object.entries(form).forEach(([k, v]) => { if (v) labels[k] = v })

      await apiFetch('/monitor/targets', {
        method: 'POST',
        body: JSON.stringify({
          name: name.trim(),
          address: form.address || form.url || '',
          port: form.port ? Number(form.port) : (active.default_port || 0),
          probes: active.protocol === 'http' || active.protocol === 'exporter' ? ['http'] : ['tcp'],
          labels,
        }),
      })
      getMessage().success(`已接入 ${active.name}`)
      setActive(null)
      load()
    } catch (e: any) {
      getMessage().error(e?.message || '接入失败')
    } finally { setSubmitting(false) }
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">监控插件</h1>
          <p className="text-sm text-muted-foreground">
            按技术栈选择插件即可接入监控，插件声明了该采哪些指标与所需连接参数
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Link to="/monitor/collection" className="text-xs text-blue-600 dark:text-blue-400 hover:underline inline-flex items-center gap-0.5">
            监控采集调度<ArrowRight className="h-3 w-3" />
          </Link>
          <button onClick={load} className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
            <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
          </button>
        </div>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {[
          { n: plugins.length, label: '可用插件', icon: Blocks, color: 'blue' as const },
          { n: categories.length, label: '覆盖分类', icon: Layers, color: 'blue' as const },
          { n: plugins.filter(p => p.in_use > 0).length, label: '已启用插件', icon: CheckCircle2, color: 'violet' as const },
          { n: totalInUse, label: '接入目标数', icon: Plug, color: 'amber' as const },
        ].map(s => (
          <Card key={s.label}><CardContent className="p-4 flex items-center gap-3">
            <div className={cn('h-10 w-10 rounded-lg flex items-center justify-center',
              s.color === 'blue' && 'bg-blue-100 dark:bg-blue-500/20',
              s.color === 'blue' && 'bg-blue-100 dark:bg-blue-500/20',
              s.color === 'violet' && 'bg-violet-100 dark:bg-violet-500/20',
              s.color === 'amber' && 'bg-amber-100 dark:bg-amber-500/20')}>
              <s.icon className={cn('h-5 w-5',
                s.color === 'blue' && 'text-blue-600 dark:text-blue-400',
                s.color === 'blue' && 'text-blue-600 dark:text-blue-400',
                s.color === 'violet' && 'text-violet-600 dark:text-violet-400',
                s.color === 'amber' && 'text-amber-600 dark:text-amber-400')} />
            </div>
            <div><p className="text-2xl font-bold">{s.n}</p><p className="text-xs text-muted-foreground">{s.label}</p></div>
          </CardContent></Card>
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative w-72">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input className="pl-9" placeholder="搜索插件名 / 指标，例如 lag、堆内存" value={q} onChange={e => setQ(e.target.value)} />
        </div>
        <Button type={cat === '' ? 'primary' : 'default'} size="middle" onClick={() => setCat('')}>全部</Button>
        {categories.map(c => {
          const meta = CATEGORY_META[c] || { label: c, icon: Blocks }
          return (
            <Button key={c} type={cat === c ? 'primary' : 'default'} size="middle" onClick={() => setCat(c)}>
              <meta.icon className="h-3.5 w-3.5 mr-1" />{meta.label}
            </Button>
          )
        })}
        <Button type={onlyInUse ? 'primary' : 'default'} size="middle" onClick={() => setOnlyInUse(v => !v)}>
          <CheckCircle2 className="h-3.5 w-3.5 mr-1" />仅看已启用
        </Button>
      </div>

      {loading ? (
        <Card><CardContent className="py-16 text-center text-sm text-muted-foreground">加载中…</CardContent></Card>
      ) : filtered.length === 0 ? (
        <Card><CardContent className="py-16 text-center text-sm text-muted-foreground">没有匹配的插件，换个关键词试试。</CardContent></Card>
      ) : grouped.map(([category, list]) => {
        const meta = CATEGORY_META[category] || { label: category, icon: Blocks, color: 'text-muted-foreground' }
        return (
          <section key={category} className="space-y-2">
            <h2 className="text-sm font-semibold flex items-center gap-2">
              <meta.icon className={cn('h-4 w-4', meta.color)} />{meta.label}
              <Tag className="text-[10px]">{list.length}</Tag>
            </h2>
            <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
              {list.map(p => (
                <Card
                  key={p.key}
                  className={cn(
                    'transition-all duration-300 hover:-translate-y-0.5 hover:shadow-md',
                    p.in_use > 0 && 'border-blue-300 dark:border-blue-500/40'
                  )}
                >
                  <CardHeader className="py-3 pb-2">
                    <div className="flex items-start justify-between gap-2">
                      <CardTitle className="text-sm flex items-center gap-1.5">
                        {p.name}
                        {p.experimental && <Tag className="text-[9px]">实验性</Tag>}
                      </CardTitle>
                      {p.in_use > 0 && (
                        <Tag color="green" className="text-[10px] shrink-0">已接入 {p.in_use}</Tag>
                      )}
                    </div>
                    <div className="flex items-center gap-1.5 pt-0.5">
                      <Tag className="text-[9px]">{PROTOCOL_LABELS[p.protocol] || p.protocol}</Tag>
                      {p.default_port ? <Tag className="text-[9px]">:{p.default_port}</Tag> : null}
                    </div>
                  </CardHeader>
                  <CardContent className="space-y-2.5">
                    <p className="text-xs text-muted-foreground leading-relaxed">{p.description}</p>
                    <div className="flex flex-wrap gap-1">
                      {p.metrics.slice(0, 4).map(m => (
                        <Tag key={m} className="text-[9px] font-mono">{m}</Tag>
                      ))}
                      {p.metrics.length > 4 && (
                        <Tag className="text-[9px]">+{p.metrics.length - 4}</Tag>
                      )}
                    </div>
                    {p.targets && p.targets.length > 0 && (
                      <p className="text-[10px] text-muted-foreground truncate">
                        目标：{p.targets.slice(0, 3).join('、')}{p.targets.length > 3 ? ` 等 ${p.targets.length} 个` : ''}
                      </p>
                    )}
                    <Button
                      type={p.in_use > 0 ? 'default' : 'primary'}
                      className="w-full"
                      onClick={() => openConnect(p)}
                      disabled={p.protocol === 'agent'}
                    >
                      {p.protocol === 'agent'
                        ? '随 Agent 自动生效'
                        : p.in_use > 0 ? '再接入一个目标' : '接入监控'}
                    </Button>
                  </CardContent>
                </Card>
              ))}
            </div>
          </section>
        )
      })}

      <Modal title={active ? `接入 ${active.name}` : ''} open={!!active} onCancel={() => setActive(null)} footer={null}>
        {active && (
          <div className="space-y-3">
            <p className="text-xs text-muted-foreground">{active.description}</p>
            <div className="space-y-1.5">
              <label className="text-xs font-medium">目标名称 <span className="text-red-500">*</span></label>
              <Input value={name} onChange={e => setName(e.target.value)} placeholder={`${active.name}-生产`} />
            </div>
            {(active.config || []).map(f => (
              <div key={f.key} className="space-y-1.5">
                <label className="text-xs font-medium">
                  {f.label}{f.required && <span className="text-red-500 ml-0.5">*</span>}
                </label>
                <Input
                  type={f.type === 'password' ? 'password' : f.type === 'number' ? 'number' : 'text'}
                  value={form[f.key] ?? ''}
                  placeholder={f.placeholder}
                  onChange={e => setForm(prev => ({ ...prev, [f.key]: e.target.value }))}
                />
                {f.help && <p className="text-[10px] text-muted-foreground">{f.help}</p>}
              </div>
            ))}
            <div className="rounded-md border border-dashed p-2.5">
              <p className="text-[10px] text-muted-foreground">
                将采集：{active.metrics.join('、')}
              </p>
            </div>
          </div>
        )}
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setActive(null)}>取消</Button>
          <Button type="primary" onClick={submit} disabled={submitting}>{submitting ? '接入中…' : '确认接入'}</Button>
        </div>
      </Modal>
    </div>
  )
}
