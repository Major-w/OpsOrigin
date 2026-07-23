'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { BarChart3, Zap, TrendingUp, Clock, Calendar, Activity } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'

interface TokenStats {
  period: string; total_calls: number; prompt_tokens: number; completion_tokens: number
  total_tokens: number; by_hour: { hour: string; tokens: number; calls: number }[]
  by_day: { date: string; tokens: number; calls: number }[]
}

function BarChart({ data, maxVal, label }: { data: { label: string; value: number }[]; maxVal: number; label: string }) {
  const { theme } = useThemeStore()
  const w = 600; const h = 200; const pad = 30
  const barW = Math.max(4, (w - pad * 2) / data.length - 2)
  const bar = (color: string, x: number, v: number) => {
    const height = maxVal > 0 ? (v / maxVal) * (h - pad * 2) : 0
    return <rect key={x} x={x} y={h - pad - height} width={barW} height={height} rx="2" fill={color} opacity="0.85" />
  }
  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="w-full h-auto">
      {data.map((d, i) => bar(theme === 'dark' ? '#34D399' : '#059669', pad + i * (barW + 1), d.value))}
      <line x1={pad} y1={h - pad} x2={w - pad} y2={h - pad} stroke={theme === 'dark' ? '#374151' : '#e5e7eb'} strokeWidth="1" />
      <text x={pad} y={pad / 2} fill={theme === 'dark' ? '#9ca3af' : '#6b7280'} fontSize="10">{label}</text>
    </svg>
  )
}

function formatNum(n: number) {
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
  return String(n)
}

export default function TokenAnalyticsPage() {
  const { theme } = useThemeStore()
  const [period, setPeriod] = useState('24h')
  const [stats, setStats] = useState<TokenStats | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => { loadStats() }, [period])

  const loadStats = async () => {
    setLoading(true)
    try {
      const r = await fetch(`/api/v1/ai/token-stats?period=${period}`)
      if (r.ok) setStats(await r.json())
    } catch {}
    setLoading(false)
  }

  const hourData = (stats?.by_hour || []).map(h => ({ label: h.hour, value: h.tokens }))
  const maxHour = Math.max(1, ...hourData.map(h => h.value))
  const dayData = (stats?.by_day || []).map(d => ({ label: d.date, value: d.tokens }))
  const maxDay = Math.max(1, ...dayData.map(d => d.value))

  return (
    <div className="p-6 h-[calc(100vh-120px)] flex flex-col">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <BarChart3 className="h-6 w-6 text-emerald-500" />
            Token 用量分析
          </h1>
          <p className="text-sm text-muted-foreground">查看 LLM 调用的 Token 消耗趋势</p>
        </div>
        <div className="flex gap-1">
          {['today','24h','7d'].map(p => (
            <Badge key={p} variant={period === p ? 'default' : 'outline'} className="cursor-pointer text-[11px] px-3 py-1"
              onClick={() => setPeriod(p)}>{p === 'today' ? '今日' : p === '24h' ? '24小时' : '7天'}</Badge>
          ))}
        </div>
      </div>

      {loading ? (
        <div className="flex-1 flex items-center justify-center"><Activity className="h-8 w-8 animate-spin text-muted-foreground" /></div>
      ) : (
        <div className="flex-1 overflow-auto space-y-4">
          {/* 汇总卡片 */}
          <div className="grid grid-cols-4 gap-3">
            <Card>
              <CardContent className="p-4 text-center">
                <Zap className="h-5 w-5 mx-auto mb-1 text-amber-500" />
                <p className="text-2xl font-bold">{formatNum(stats?.total_tokens || 0)}</p>
                <p className="text-[10px] text-muted-foreground uppercase tracking-wider">总 Token</p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-4 text-center">
                <Activity className="h-5 w-5 mx-auto mb-1 text-blue-500" />
                <p className="text-2xl font-bold">{stats?.total_calls || 0}</p>
                <p className="text-[10px] text-muted-foreground uppercase tracking-wider">调用次数</p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-4 text-center">
                <TrendingUp className="h-5 w-5 mx-auto mb-1 text-green-500" />
                <p className="text-2xl font-bold">{formatNum(stats?.prompt_tokens || 0)}</p>
                <p className="text-[10px] text-muted-foreground uppercase tracking-wider">Prompt</p>
              </CardContent>
            </Card>
            <Card>
              <CardContent className="p-4 text-center">
                <Clock className="h-5 w-5 mx-auto mb-1 text-purple-500" />
                <p className="text-2xl font-bold">{formatNum(stats?.completion_tokens || 0)}</p>
                <p className="text-[10px] text-muted-foreground uppercase tracking-wider">Completion</p>
              </CardContent>
            </Card>
          </div>

          {/* 按小时分布 */}
          <Card>
            <CardHeader className="pb-2"><CardTitle className="text-sm flex items-center gap-2"><Clock className="h-4 w-4" />按小时分布</CardTitle></CardHeader>
            <CardContent>
              {hourData.length > 0 ? <BarChart data={hourData} maxVal={maxHour} label="Tokens / 小时" /> : <p className="text-xs text-muted-foreground py-4 text-center">暂无数据</p>}
            </CardContent>
          </Card>

          {/* 按天分布 */}
          <Card>
            <CardHeader className="pb-2"><CardTitle className="text-sm flex items-center gap-2"><Calendar className="h-4 w-4" />按天分布</CardTitle></CardHeader>
            <CardContent>
              {dayData.length > 0 ? <BarChart data={dayData} maxVal={maxDay} label="Tokens / 天" /> : <p className="text-xs text-muted-foreground py-4 text-center">暂无数据</p>}
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  )
}
