'use client'

import { useEffect, useRef, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { Activity, RefreshCw, FileText, Loader2 } from 'lucide-react'

interface StatusItem {
  name: string
  label: string
  status: string
  detail: string
  latency_ms: number
}

const SOURCES = [
  { value: 'core', label: 'Core 主服务' },
  { value: 'postgres', label: 'PostgreSQL' },
  { value: 'redis', label: 'Redis' },
  { value: 'minio', label: 'MinIO' },
  { value: 'dgraph-alpha', label: 'Dgraph' },
  { value: 'frontend', label: '前端' },
]

const statusBadge = (s: string) =>
  s === 'ok' ? 'success'
    : s === 'down' ? 'destructive'
    : s === 'degraded' ? 'warning'
    : 'outline'

const statusLabel: Record<string, string> = {
  ok: '正常', down: '异常', degraded: '降级', unknown: '未知',
}

export default function SystemHealthPage() {
  const [items, setItems] = useState<StatusItem[]>([])
  const [serverTime, setServerTime] = useState('')
  const [loading, setLoading] = useState(false)
  const [auto, setAuto] = useState(true)

  const [source, setSource] = useState('core')
  const [lines, setLines] = useState(200)
  const [logs, setLogs] = useState<string[]>([])
  const [logLoading, setLogLoading] = useState(false)
  const [logError, setLogError] = useState('')
  const logRef = useRef<HTMLPreElement>(null)

  const loadStatus = async () => {
    setLoading(true)
    try {
      const r = await fetch('/api/v1/system/status')
      if (r.ok) {
        const d = await r.json()
        setItems(d.items || [])
        setServerTime(d.server_time || '')
      }
    } catch {}
    setLoading(false)
  }

  const loadLogs = async () => {
    setLogLoading(true)
    setLogError('')
    try {
      const r = await fetch(`/api/v1/system/logs?source=${source}&lines=${lines}`)
      const d = await r.json()
      if (!r.ok) { setLogError(d.error || `加载失败(${r.status})`); setLogs([]) }
      else setLogs(d.lines || [])
    } catch (e: any) {
      setLogError('加载日志失败: ' + e.message)
      setLogs([])
    }
    setLogLoading(false)
  }

  useEffect(() => { loadStatus() }, [])
  useEffect(() => { loadLogs() }, [])
  useEffect(() => {
    if (!auto) return
    const t = setInterval(loadStatus, 10000)
    return () => clearInterval(t)
  }, [auto])
  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = 0
  }, [logs])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold flex items-center gap-2"><Activity className="h-5 w-5 text-emerald-500" />系统自监控</h1>
          <p className="text-xs text-muted-foreground mt-1">主要服务与组件状态探活 · 组件日志查看（core 走进程内缓冲，其他走 docker logs）</p>
        </div>
        <div className="flex items-center gap-2">
          <Badge variant="outline" className="text-[10px]">{serverTime}</Badge>
          <Button size="sm" variant="outline" onClick={() => setAuto(v => !v)}>{auto ? '自动刷新 10s' : '自动刷新关'}</Button>
          <Button size="sm" onClick={loadStatus} disabled={loading}>
            {loading ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5 mr-1" />}刷新
          </Button>
        </div>
      </div>

      {/* 状态卡片 */}
      <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-3">
        {items.map(it => (
          <Card key={it.name}>
            <CardContent className="p-4">
              <div className="flex items-center justify-between">
                <p className="text-sm font-medium">{it.label}</p>
                <span className={cn('h-2.5 w-2.5 rounded-full shrink-0',
                  it.status === 'ok' ? 'bg-emerald-500'
                    : it.status === 'down' ? 'bg-red-500'
                    : it.status === 'degraded' ? 'bg-amber-500' : 'bg-gray-400')} />
              </div>
              <p className="text-xs mt-1"><Badge variant={statusBadge(it.status) as any} className="text-[10px]">{statusLabel[it.status] || it.status}</Badge></p>
              <p className="text-[11px] text-muted-foreground mt-2 line-clamp-3 min-h-[32px]">{it.detail}</p>
              {it.latency_ms > 0 && <p className="text-[10px] text-muted-foreground mt-1">{it.latency_ms} ms</p>}
            </CardContent>
          </Card>
        ))}
      </div>

      {/* 日志查看 */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base flex items-center gap-2"><FileText className="h-4 w-4" />组件日志</CardTitle>
          <CardDescription>选择组件与行数后加载；core 日志来自进程内缓冲（重启后清空），其他组件日志来自 docker logs（需已挂载 docker socket）。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center gap-2 flex-wrap">
            <select
              className="flex h-9 rounded-md border border-input bg-background px-2 text-sm"
              value={source}
              onChange={e => setSource(e.target.value)}
            >
              {SOURCES.map(s => <option key={s.value} value={s.value}>{s.label}</option>)}
            </select>
            <select
              className="flex h-9 rounded-md border border-input bg-background px-2 text-sm"
              value={String(lines)}
              onChange={e => setLines(parseInt(e.target.value) || 200)}
            >
              {[100, 200, 500, 1000].map(n => <option key={n} value={n}>{n} 行</option>)}
            </select>
            <Button size="sm" variant="outline" onClick={loadLogs} disabled={logLoading}>
              {logLoading ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5 mr-1" />}加载日志
            </Button>
          </div>
          {logError && <p className="text-xs text-red-500">{logError}</p>}
          <pre
            ref={logRef}
            className="rounded-md border bg-background p-3 text-[11px] font-mono leading-relaxed overflow-auto max-h-[520px] whitespace-pre-wrap break-all"
          >
            {logs.length === 0
              ? (logError ? '' : '暂无日志')
              : logs.map((l, i) => <div key={i} className="border-b border-border/40 py-0.5">{l}</div>)}
          </pre>
        </CardContent>
      </Card>
    </div>
  )
}
