
import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Input, Tag } from 'antd'
import { apiFetch } from '@/lib/utils'
import { TrendingUp, TrendingDown, Minus, AlarmClock, Loader2, Activity } from 'lucide-react'
import { message } from 'antd'

interface PredictResult {
  slope: number
  intercept: number
  r2: number
  trend: 'up' | 'down' | 'flat'
  forecast: number[]
  time_to_threshold_hours: number
  hits_threshold: boolean
}

const DEFAULT_SERIES = '50,55,62,70,79,88'

export default function PredictPage() {
  const [seriesText, setSeriesText] = useState(DEFAULT_SERIES)
  const [threshold, setThreshold] = useState(95)
  const [stepHours, setStepHours] = useState(1)
  const [result, setResult] = useState<PredictResult | null>(null)
  const [loading, setLoading] = useState(false)

  const runPredict = async () => {
    const series = seriesText.split(',').map(s => parseFloat(s.trim())).filter(n => !isNaN(n))
    if (series.length < 2) return message.error('至少输入 2 个数据点（逗号分隔）')
    setLoading(true)
    try {
      const r = await apiFetch<PredictResult>('/monitor/predict', {
        method: 'POST',
        body: JSON.stringify({ series, threshold, step_hours: stepHours }),
      })
      setResult(r)
    } catch (e: unknown) {
      message.error(`预测失败: ${(e as Error).message}`)
    } finally {
      setLoading(false)
    }
  }

  const chart = (() => {
    if (!result) return null
    const actual = seriesText.split(',').map(s => parseFloat(s.trim())).filter(n => !isNaN(n))
    const all = [...actual, ...result.forecast]
    const maxV = Math.max(...all, threshold) * 1.1
    const minV = Math.min(...all) * 0.9
    const W = 600, H = 200, P = 24
    const x = (i: number) => P + (i / Math.max(1, all.length - 1)) * (W - 2 * P)
    const y = (v: number) => H - P - ((v - minV) / Math.max(1e-9, maxV - minV)) * (H - 2 * P)
    const actualPts = actual.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' ')
    const forePts = all.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(' ')
    const thY = y(threshold)
    return (
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full">
        <line x1={P} y1={thY} x2={W - P} y2={thY} stroke="#E24B4A" strokeWidth="1" strokeDasharray="5 4" />
        <text x={W - P - 2} y={thY - 5} textAnchor="end" fontSize="10" fill="#A32D2D">阈值 {threshold}</text>
        <polyline points={actualPts} fill="none" stroke="#378ADD" strokeWidth="2" />
        <polyline points={forePts} fill="none" stroke="#378ADD" strokeWidth="2" strokeDasharray="6 4" opacity="0.55" />
        <line x1={x(actual.length - 1)} y1={H - P} x2={x(actual.length - 1)} y2={P} stroke="#888780" strokeWidth="0.5" strokeDasharray="3 3" />
        <text x={x(actual.length - 1) - 4} y={H - P + 12} fontSize="10" fill="#5F5E5A">现在</text>
      </svg>
    )
  })()

  const TrendIcon = result?.trend === 'up' ? TrendingUp : result?.trend === 'down' ? TrendingDown : Minus

  return (
    <div className="flex flex-col gap-4 h-full overflow-auto">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">趋势预测</h1>
          <p className="text-xs text-muted-foreground">L2 预测性运维：线性回归 + 外推预测 + 触达阈值时间（ETT），把「发现问题」提前到「预测即将出问题」。</p>
        </div>
      </div>

      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm flex items-center gap-2"><Activity className="h-4 w-4 text-blue-600 dark:text-blue-400" />输入指标序列</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <div>
            <label className="text-xs text-muted-foreground">序列（逗号分隔，按时间顺序，如 CPU 使用率采样）</label>
            <Input className="mt-1 font-mono" value={seriesText} onChange={e => setSeriesText(e.target.value)} placeholder="50,55,62,70,79,88" />
          </div>
          <div className="flex gap-3 flex-wrap">
            <div className="w-36">
              <label className="text-xs text-muted-foreground">告警阈值</label>
              <Input type="number" className="mt-1" value={threshold} onChange={e => setThreshold(parseFloat(e.target.value) || 0)} />
            </div>
            <div className="w-36">
              <label className="text-xs text-muted-foreground">采样间隔（小时/点）</label>
              <Input type="number" className="mt-1" value={stepHours} onChange={e => setStepHours(parseFloat(e.target.value) || 1)} />
            </div>
            <div className="flex items-end">
              <Button type="primary" className="gap-1" onClick={runPredict} disabled={loading}>
                {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <TrendingUp className="h-4 w-4" />}
                预测
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      {result && (
        <Card>
          <CardHeader className="pb-2">
            <div className="flex items-center gap-2 flex-wrap">
              <CardTitle className="text-sm">预测结果</CardTitle>
              <Tag color={result.trend === 'up' ? 'orange' : result.trend === 'down' ? 'green' : 'default'} className="gap-1 text-[10px]">
                <TrendIcon className="h-3 w-3" />
                {result.trend === 'up' ? '上升' : result.trend === 'down' ? '下降' : '平稳'}
              </Tag>
              {result.hits_threshold && (
                <Tag color="red" className="gap-1 text-[10px]">
                  <AlarmClock className="h-3 w-3" />
                  {result.time_to_threshold_hours < 1
                    ? `约 ${(result.time_to_threshold_hours * 60).toFixed(0)} 分钟后触达阈值`
                    : `约 ${result.time_to_threshold_hours.toFixed(1)} 小时后触达阈值`}
                </Tag>
              )}
            </div>
          </CardHeader>
          <CardContent className="space-y-3">
            {chart}
            <div className="grid grid-cols-4 gap-2 text-center">
              <div className="rounded-lg bg-muted/50 px-2 py-2">
                <div className="text-[11px] text-muted-foreground">每点变化</div>
                <div className="text-sm font-semibold">{result.slope > 0 ? '+' : ''}{result.slope.toFixed(2)}</div>
              </div>
              <div className="rounded-lg bg-muted/50 px-2 py-2">
                <div className="text-[11px] text-muted-foreground">拟合优度 R²</div>
                <div className="text-sm font-semibold">{result.r2.toFixed(2)}</div>
              </div>
              <div className="rounded-lg bg-muted/50 px-2 py-2">
                <div className="text-[11px] text-muted-foreground">预测点</div>
                <div className="text-sm font-semibold">{result.forecast.length}</div>
              </div>
              <div className="rounded-lg bg-muted/50 px-2 py-2">
                <div className="text-[11px] text-muted-foreground">ETT 触达阈值</div>
                <div className="text-sm font-semibold text-red-500">{result.hits_threshold ? `${result.time_to_threshold_hours.toFixed(1)}h` : '—'}</div>
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">使用场景</CardTitle></CardHeader>
        <CardContent className="text-xs text-muted-foreground space-y-1">
          <p>· 容量规划：输入磁盘/内存/带宽使用率历史 → 预测何时触达扩容阈值，提前开单</p>
          <p>· 故障预警：输入延迟/错误率序列 → 趋势上扬且 R² 高时提前告警，避免故障爆发</p>
          <p>· 与「容量规划」场景包联动：预测触达阈值时自动生成扩容建议工单</p>
        </CardContent>
      </Card>
    </div>
  )
}
