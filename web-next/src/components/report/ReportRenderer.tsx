'use client'

import { EChartsWrapper } from '@/components/charts'
import type { EChartsOption } from 'echarts'
import type { Result, Column } from '@/stores/report'

function num(v: unknown): number {
  if (v === null || v === undefined || v === '') return 0
  const n = typeof v === 'number' ? v : Number(v)
  return Number.isFinite(n) ? n : 0
}

function fmt(v: unknown): string {
  if (v === null || v === undefined) return '-'
  if (typeof v === 'string' && /^\d{4}-\d{2}-\d{2}T/.test(v)) {
    // 截断 ISO 时间到日期或分钟，避免 x 轴过长
    return v.slice(0, 16).replace('T', ' ')
  }
  return String(v)
}

function dimColumns(cols: Column[]): Column[] {
  return cols.filter((c) => c.role === 'dimension')
}
function metricColumns(cols: Column[]): Column[] {
  return cols.filter((c) => c.role === 'metric')
}

function LineBar({ data, bar, height }: { data: Result; bar: boolean; height: number }) {
  const dims = dimColumns(data.columns)
  const mets = metricColumns(data.columns)
  // x 轴优先用时间桶，否则第一个维度
  const xCol =
    data.columns.find((c) => c.name === 'time_bucket') ?? dims[0] ?? data.columns[0]
  const xIdx = data.columns.indexOf(xCol)
  const categories = data.rows.map((r) => fmt(r[xIdx]))

  const series = mets.map((mc) => {
    const idx = data.columns.indexOf(mc)
    return {
      name: mc.label,
      type: bar ? ('bar' as const) : ('line' as const),
      smooth: !bar,
      showSymbol: false,
      areaStyle: bar ? undefined : { opacity: 0.08 },
      emphasis: { focus: 'series' as const },
      data: data.rows.map((r) => num(r[idx])),
    }
  })

  const option: EChartsOption = {
    tooltip: { trigger: 'axis' },
    legend: { top: 0, type: 'scroll' },
    grid: { left: 8, right: 16, bottom: 8, top: 36, containLabel: true },
    xAxis: {
      type: 'category',
      data: categories,
      boundaryGap: bar,
      axisLabel: { hideOverlap: true },
    },
    yAxis: { type: 'value' },
    series,
  }
  return <EChartsWrapper option={option} height={height} />
}

function Pie({ data, height }: { data: Result; height: number }) {
  const dims = dimColumns(data.columns)
  const mets = metricColumns(data.columns)
  const nameCol = dims[0]
  const valCol = mets[0]
  if (!nameCol || !valCol || data.rows.length === 0) {
    return <div className="text-sm text-muted-foreground p-4">饼图需要至少一个维度与一个指标</div>
  }
  const nIdx = data.columns.indexOf(nameCol)
  const vIdx = data.columns.indexOf(valCol)
  const pieData = data.rows.map((r) => ({ name: fmt(r[nIdx]), value: num(r[vIdx]) }))
  const option: EChartsOption = {
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { bottom: 0, type: 'scroll' },
    series: [
      {
        type: 'pie',
        radius: ['38%', '68%'],
        center: ['50%', '46%'],
        avoidLabelOverlap: true,
        itemStyle: { borderRadius: 6, borderWidth: 2 },
        label: { show: false },
        data: pieData,
      },
    ],
  }
  return <EChartsWrapper option={option} height={height} />
}

function NumberCard({ data }: { data: Result }) {
  const mets = metricColumns(data.columns)
  const valCol = mets[0]
  if (!valCol) return <div className="text-sm text-muted-foreground p-4">无指标可展示</div>
  const idx = data.columns.indexOf(valCol)
  let value = 0
  if (data.rows.length === 1) value = num(data.rows[0][idx])
  else value = data.rows.reduce((s, r) => s + num(r[idx]), 0)
  const display = valCol.decimals ? value.toFixed(valCol.decimals) : Math.round(value).toLocaleString()
  return (
    <div className="flex h-full flex-col items-center justify-center p-4">
      <div className="text-4xl font-bold bg-gradient-to-r from-emerald-500 to-cyan-500 bg-clip-text text-transparent">
        {display}
      </div>
      <div className="mt-1 text-sm text-muted-foreground">
        {valCol.label}
        {valCol.unit ? ` (${valCol.unit})` : ''}
      </div>
    </div>
  )
}

function Table({ data }: { data: Result }) {
  return (
    <div className="max-h-[320px] overflow-auto">
      <table className="w-full text-sm">
        <thead className="sticky top-0 bg-muted/60 backdrop-blur">
          <tr>
            {data.columns.map((c) => (
              <th key={c.name} className="px-3 py-2 text-left font-medium whitespace-nowrap">
                {c.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {data.rows.map((r, i) => (
            <tr key={i} className="border-t border-border/60 hover:bg-muted/40">
              {r.map((v, j) => (
                <td key={j} className="px-3 py-1.5 whitespace-nowrap">
                  {fmt(v)}
                </td>
              ))}
            </tr>
          ))}
          {data.rows.length === 0 && (
            <tr>
              <td colSpan={data.columns.length} className="px-3 py-6 text-center text-muted-foreground">
                无数据
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}

export function ReportRenderer({
  type,
  data,
  height = 280,
}: {
  type: string
  data?: Result
  height?: number
}) {
  if (!data || !data.columns || data.columns.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
        暂无数据
      </div>
    )
  }
  switch (type) {
    case 'line':
      return <LineBar data={data} bar={false} height={height} />
    case 'bar':
      return <LineBar data={data} bar height={height} />
    case 'pie':
      return <Pie data={data} height={height} />
    case 'number':
      return <NumberCard data={data} />
    case 'table':
    default:
      return <Table data={data} />
  }
}
