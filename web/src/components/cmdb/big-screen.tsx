
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import RGL, { WidthProvider, type Layout } from 'react-grid-layout'
import 'react-grid-layout/css/styles.css'
import 'react-resizable/css/styles.css'
import { EChartsWrapper } from '@/components/charts/echarts'
import { apiFetch, apiFetchRaw } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { cn } from '@/lib/utils'
import {
  Boxes, Database, Share2, Server, BarChart3, PieChart, ListOrdered,
  Tags, Pencil, Check, RotateCcw, Plus, X, LayoutDashboard, RefreshCw,
  Maximize2, Minimize2, LogOut, Activity, Sparkles, AlertTriangle, GripVertical, Tv,
} from 'lucide-react'
import type { EChartsOption } from 'echarts'

const GridLayout = WidthProvider(RGL)

// KPI 数字滚动动画
function useCountUp(value: number, duration = 1200) {
  const [disp, setDisp] = useState(value)
  const fromRef = useRef(value)
  const raf = useRef<number | null>(null)
  useEffect(() => {
    const from = fromRef.current
    const to = value
    if (from === to) { setDisp(to); return }
    const start = performance.now()
    const tick = (t: number) => {
      const p = Math.min(1, (t - start) / duration)
      const eased = 1 - Math.pow(1 - p, 3)
      setDisp(Math.round(from + (to - from) * eased))
      if (p < 1) raf.current = requestAnimationFrame(tick)
      else fromRef.current = to
    }
    raf.current = requestAnimationFrame(tick)
    return () => { if (raf.current) cancelAnimationFrame(raf.current) }
  }, [value, duration])
  return disp
}
function CountUp({ value, className }: { value: number; className?: string }) {
  const v = useCountUp(value)
  return <span className={className}>{v.toLocaleString()}</span>
}

// ============================================================================
// CMDB · IT 资源统计大屏（驾驶舱）
// 深色驾驶舱风格；默认按「左-中-右」三栏编排，每一块面板都可拖拽 / 缩放 /
// 改名 / 移除 / 追加，编排结果按用户持久化到服务端（/cmdb/dashboard）。
// mode:
//   - 'embed'     嵌在后台框架内（资源统计页），随页面滚动。
//   - 'standalone'独立投放大屏：整屏固定、固定深色、每 30s 自动刷新、可全屏/退出。
// ============================================================================

interface Stats {
  total_models: number
  total_instances: number
  total_relations: number
  by_category: { category: string; models: number; instances: number }[]
  by_status: { status: string; count: number }[]
  by_type: { type_id: string; type_name: string; category: string; count: number }[]
  by_attr: { key: string; value: string; count: number }[]
}
const STATUS_LABELS: Record<string, string> = { active: '正常', inactive: '停用', archived: '归档' }

// AI 洞察卡（后端由统计快照规则派生，确定性、零 LLM 调用）
export interface Insight { level: 'info' | 'warn' | 'risk'; title: string; detail: string; metric?: string }

// 驾驶舱面板：默认按「左-中-右」三栏编排，但每一块都可拖拽 / 缩放 / 改名 / 移除 / 追加。
// 面板类型是「内容语义」而非「任意组件」——同一语义面板只允许存在一个实例。
type WidgetType =
  | 'sphere' | 'overview' | 'kpi'
  | 'cat-dist' | 'cat-grid' | 'status-list' | 'status-pie'
  | 'type-top' | 'attr-top' | 'insight'
interface WidgetDef { id: string; type: WidgetType; title: string }

const WIDGET_META: Record<WidgetType, { label: string; icon: typeof Boxes; defaultW: number; defaultH: number; title: string; accent: string }> = {
  sphere: { label: '资产总量', icon: Activity, defaultW: 6, defaultH: 6, title: '资产总量', accent: '#22d3ee' },
  overview: { label: '资源总览', icon: Server, defaultW: 3, defaultH: 4, title: '资源总览', accent: '#38bdf8' },
  kpi: { label: '核心指标', icon: LayoutDashboard, defaultW: 6, defaultH: 4, title: '核心指标', accent: '#38bdf8' },
  'cat-dist': { label: '资源分类分布', icon: BarChart3, defaultW: 3, defaultH: 6, title: '资源分类分布', accent: '#60a5fa' },
  'cat-grid': { label: '分类实例分布', icon: Boxes, defaultW: 3, defaultH: 5, title: '分类实例分布', accent: '#a78bfa' },
  'status-list': { label: '实例状态', icon: PieChart, defaultW: 3, defaultH: 5, title: '实例状态', accent: '#34d399' },
  'status-pie': { label: '状态占比', icon: PieChart, defaultW: 3, defaultH: 5, title: '状态占比', accent: '#34d399' },
  'type-top': { label: '模型实例 Top', icon: ListOrdered, defaultW: 3, defaultH: 5, title: '模型实例 Top', accent: '#38bdf8' },
  'attr-top': { label: '属性聚合 Top', icon: Tags, defaultW: 3, defaultH: 5, title: '属性聚合 Top', accent: '#fbbf24' },
  insight: { label: 'AI 配置洞察', icon: Sparkles, defaultW: 3, defaultH: 5, title: 'AI 配置洞察', accent: '#a78bfa' },
}

// 默认编排＝驾驶舱三栏：左 3 列（总览/分类分布/实例状态）、中 6 列（球体/状态占比/分类实例）、右 3 列（模型Top/属性Top/洞察）
const DEFAULT_WIDGETS: WidgetDef[] = [
  { id: 'overview', type: 'overview', title: '资源总览' },
  { id: 'cat-dist', type: 'cat-dist', title: '资源分类分布' },
  { id: 'status-list', type: 'status-list', title: '实例状态' },
  { id: 'sphere', type: 'sphere', title: '资产总量' },
  { id: 'status-pie', type: 'status-pie', title: '状态占比' },
  { id: 'cat-grid', type: 'cat-grid', title: '分类实例分布' },
  { id: 'type-top', type: 'type-top', title: '模型实例 Top' },
  { id: 'attr-top', type: 'attr-top', title: '属性聚合 Top' },
  { id: 'insight', type: 'insight', title: 'AI 配置洞察' },
]
const DEFAULT_LAYOUT: Layout[] = [
  { i: 'overview', x: 0, y: 0, w: 3, h: 4 },
  { i: 'cat-dist', x: 0, y: 4, w: 3, h: 6 },
  { i: 'status-list', x: 0, y: 10, w: 3, h: 5 },
  { i: 'sphere', x: 3, y: 0, w: 6, h: 6 },
  { i: 'status-pie', x: 3, y: 6, w: 3, h: 5 },
  { i: 'cat-grid', x: 6, y: 6, w: 3, h: 5 },
  { i: 'type-top', x: 9, y: 0, w: 3, h: 5 },
  { i: 'attr-top', x: 9, y: 5, w: 3, h: 5 },
  { i: 'insight', x: 9, y: 10, w: 3, h: 5 },
]

// 面板内容区滚动 / 居中策略
const BODY_CLASS: Partial<Record<WidgetType, string>> = {
  sphere: 'flex items-center justify-center',
  'cat-dist': 'overflow-auto',
  'cat-grid': 'overflow-auto',
  'status-list': 'overflow-auto',
  'type-top': 'overflow-auto',
  'attr-top': 'overflow-auto',
  insight: 'overflow-auto space-y-2',
}

const KNOWN_TYPES = new Set<string>(Object.keys(WIDGET_META))

// 校验服务端存档是否属于「当前版本的面板体系」。上一代「自由布局」的
// widget.type（kpi/category/status/type/attr）会被判为过期，直接回落默认编排。
function isCurrentLayout(dash: unknown): boolean {
  const d = dash as { widgets?: unknown; layout?: unknown } | null
  if (!d || !Array.isArray(d.widgets) || !Array.isArray(d.layout) || !d.widgets.length) return false
  return d.widgets.every(w => {
    const x = w as { id?: unknown; type?: unknown }
    return typeof x?.id === 'string' && typeof x?.type === 'string' && KNOWN_TYPES.has(x.type)
  })
}

// ---- echarts 深色选项构造 ----
const AXIS = { axisLine: { lineStyle: { color: 'rgba(148,163,184,0.35)' } }, axisLabel: { color: '#94a3b8' }, splitLine: { lineStyle: { color: 'rgba(148,163,184,0.12)' } } }
const PALETTE = ['#38bdf8', '#34d399', '#a78bfa', '#fbbf24', '#f87171', '#22d3ee', '#60a5fa', '#fb923c']

function barOption(cats: string[], values: number[], horizontal = true): EChartsOption {
  return {
    backgroundColor: 'transparent',
    animation: true,
    animationDuration: 1100,
    animationEasing: 'cubicOut',
    grid: { left: horizontal ? 90 : 8, right: 16, top: 12, bottom: horizontal ? 8 : 28, containLabel: !horizontal },
    tooltip: { trigger: 'axis', backgroundColor: 'rgba(15,23,42,0.92)', borderColor: 'rgba(56,189,248,0.35)', textStyle: { color: '#e2e8f0' } },
    xAxis: horizontal ? { type: 'value', ...AXIS } : { type: 'category', data: cats, ...AXIS, axisLabel: { color: '#94a3b8', interval: 0, rotate: cats.length > 6 ? 30 : 0 } },
    yAxis: horizontal ? { type: 'category', data: cats, ...AXIS } : { type: 'value', ...AXIS },
    series: [{
      type: 'bar',
      data: values,
      barWidth: '55%',
      itemStyle: {
        borderRadius: 5,
        color: {
          type: 'linear', x: 0, y: 0, x2: horizontal ? 1 : 0, y2: 1,
          colorStops: [{ offset: 0, color: '#38bdf8' }, { offset: 0.7, color: '#0ea5e9' }, { offset: 1, color: '#0284c7' }],
        },
        shadowColor: 'rgba(56,189,248,0.45)',
        shadowBlur: 14,
      },
      label: { show: true, position: horizontal ? 'right' : 'top', color: '#e2e8f0', fontSize: 11, textShadowColor: 'rgba(0,0,0,0.6)', textShadowBlur: 3 },
    }],
  }
}
function pieOption(items: { name: string; value: number }[]): EChartsOption {
  const total = items.reduce((s, it) => s + it.value, 0)
  return {
    backgroundColor: 'transparent',
    animation: true,
    animationDuration: 1100,
    animationEasing: 'cubicOut',
    tooltip: { trigger: 'item', backgroundColor: 'rgba(15,23,42,0.92)', borderColor: 'rgba(56,189,248,0.35)', textStyle: { color: '#e2e8f0' } },
    legend: { bottom: 0, left: 'center', textStyle: { color: '#94a3b8', fontSize: 11 }, icon: 'circle', itemGap: 12 },
    title: {
      text: total.toLocaleString(),
      subtext: '实例总数',
      left: 'center', top: '34%',
      textStyle: { color: '#e2e8f0', fontSize: 18, fontWeight: 700, textShadowColor: 'rgba(56,189,248,0.5)', textShadowBlur: 10 },
      subtextStyle: { color: '#94a3b8', fontSize: 10 },
    },
    series: [{
      type: 'pie',
      radius: ['42%', '68%'],
      center: ['50%', '44%'],
      itemStyle: { borderColor: 'rgba(10,14,26,0.7)', borderWidth: 2 },
      label: { show: false },
      emphasis: { label: { show: true, color: '#fff', fontSize: 12, formatter: '{b}\n{c} ({d}%)' } },
      data: items.map((it, i) => ({
        name: it.name,
        value: it.value,
        itemStyle: {
          color: PALETTE[i % PALETTE.length],
          shadowColor: PALETTE[i % PALETTE.length],
          shadowBlur: 16,
        },
      })),
    }],
  }
}

function KPIGrid({ stats }: { stats: Stats }) {
  const items = [
    { n: stats.total_models, label: '资源模型', icon: Boxes, color: '#38bdf8', grad: 'from-sky-500/20 to-blue-600/10' },
    { n: stats.total_instances, label: '配置项实例', icon: Database, color: '#60a5fa', grad: 'from-blue-500/20 to-indigo-600/10' },
    { n: stats.total_relations, label: '关联关系', icon: Share2, color: '#a78bfa', grad: 'from-violet-500/20 to-purple-600/10' },
    { n: (stats.by_status || []).reduce((s, x) => s + x.count, 0), label: '活跃资产', icon: Server, color: '#34d399', grad: 'from-emerald-500/20 to-teal-600/10' },
  ]
  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-3 h-full items-center">
      {items.map(it => (
        <div key={it.label} className={cn(
          'bs-kpi-card relative overflow-hidden rounded-2xl border border-white/10 bg-gradient-to-br px-5 py-4 flex items-center gap-4',
          it.grad
        )}>
          <div className="absolute -right-4 -top-4 h-20 w-20 rounded-full blur-2xl" style={{ background: it.color }} />
          <div className="h-12 w-12 shrink-0 rounded-xl flex items-center justify-center" style={{ background: `${it.color}22`, boxShadow: `0 0 18px ${it.color}33` }}>
            <it.icon className="h-6 w-6" style={{ color: it.color }} />
          </div>
          <div className="min-w-0 z-10">
            <p className="text-3xl font-bold tabular-nums truncate" style={{ color: it.color, textShadow: `0 0 18px ${it.color}66` }}>
              <CountUp value={it.n} />
            </p>
            <p className="text-xs text-slate-300 truncate tracking-wide">{it.label}</p>
          </div>
        </div>
      ))}
    </div>
  )
}

// AI 配置洞察卡：由统计基线规则派生，按风险等级配色
function InsightCard({ it }: { it: Insight }) {
  const tone = it.level === 'risk'
    ? { c: '#f87171', bg: 'rgba(248,113,113,0.12)', bd: 'rgba(248,113,113,0.32)' }
    : it.level === 'warn'
    ? { c: '#fbbf24', bg: 'rgba(251,191,36,0.12)', bd: 'rgba(251,191,36,0.32)' }
    : { c: '#38bdf8', bg: 'rgba(56,189,248,0.12)', bd: 'rgba(56,189,248,0.32)' }
  const Icon = it.level === 'risk' ? AlertTriangle : it.level === 'warn' ? AlertTriangle : Sparkles
  return (
    <div className="rounded-xl border p-3 relative overflow-hidden" style={{ background: tone.bg, borderColor: tone.bd }}>
      <div className="absolute -right-3 -top-3 h-14 w-14 rounded-full blur-2xl" style={{ background: tone.c }} />
      <div className="flex items-start gap-2 relative z-10">
        <Icon className="h-4 w-4 mt-0.5 shrink-0" style={{ color: tone.c }} />
        <div className="min-w-0">
          <p className="text-[13px] font-semibold text-slate-100 leading-snug">{it.title}</p>
          {it.metric && <span className="inline-block mt-1 text-[10px] px-1.5 py-0.5 rounded-full border" style={{ color: tone.c, borderColor: tone.bd }}>{it.metric}</span>}
          <p className="text-[11px] text-slate-300/90 mt-1.5 leading-relaxed">{it.detail}</p>
        </div>
      </div>
    </div>
  )
}

/** 单个驾驶舱面板的内容渲染（外壳由 Panel 提供，标题 / 编辑操作也归 Panel）。 */
function WidgetBody({ type, stats, insights }: { type: WidgetType; stats: Stats; insights: Insight[] }) {
  const cats = stats.by_category || []
  const statusItems = (stats.by_status || []).map(s => ({ skey: s.status, name: STATUS_LABELS[s.status] || s.status, value: s.count }))
  const types = [...(stats.by_type || [])].sort((a, b) => b.count - a.count).slice(0, 8)
  const attrs = [...(stats.by_attr || [])].sort((a, b) => b.count - a.count).slice(0, 6)
  const sats = statusItems.slice(0, 4)
  const statusMax = Math.max(1, ...statusItems.map(s => s.value))
  const typeMax = Math.max(1, ...types.map(t => t.count))
  const attrMax = Math.max(1, ...attrs.map(a => a.count))
  const catMax = Math.max(1, ...cats.map(c => c.instances))
  const statusColor = (k: string) =>
    k === 'active' ? '#34d399' : k === 'inactive' ? '#fbbf24' : k === 'archived' ? '#94a3b8' : '#38bdf8'
  const top = [
    { label: '资源模型', value: stats.total_models, icon: Boxes, color: '#38bdf8' },
    { label: '配置项实例', value: stats.total_instances, icon: Database, color: '#60a5fa' },
    { label: '关联关系', value: stats.total_relations, icon: Share2, color: '#a78bfa' },
  ]

  switch (type) {
    case 'sphere':
      return (
        <div className="flex flex-wrap items-center justify-center gap-3">
          <div className="flex flex-col gap-2">
            {sats.slice(0, 2).map(s => <Sat key={s.skey} name={s.name} value={s.value} color={statusColor(s.skey)} />)}
          </div>
          <div className="bsv-sphere flex shrink-0 flex-col items-center justify-center">
            <span className="text-[10px] tracking-[0.2em] text-sky-200/80">配置项实例</span>
            <span className="text-4xl font-bold tabular-nums text-white" style={{ textShadow: '0 0 26px rgba(56,189,248,0.85)' }}>
              <CountUp value={stats.total_instances} />
            </span>
            <span className="mt-0.5 text-[10px] text-slate-300">{stats.total_models} 个模型 · {stats.total_relations} 条关系</span>
          </div>
          <div className="flex flex-col gap-2">
            {sats.slice(2, 4).map(s => <Sat key={s.skey} name={s.name} value={s.value} color={statusColor(s.skey)} />)}
          </div>
        </div>
      )

    case 'overview':
      return (
        <div className="grid grid-cols-3 gap-2">
          {top.map(it => (
            <div key={it.label} className="bsv-sat px-2 py-2 text-center">
              <it.icon className="h-3.5 w-3.5 mx-auto" style={{ color: it.color }} />
              <p className="mt-1 text-lg font-bold leading-none tabular-nums" style={{ color: it.color, textShadow: `0 0 16px ${it.color}77` }}>
                <CountUp value={it.value} />
              </p>
              <p className="mt-1 text-[10px] text-slate-300 truncate">{it.label}</p>
            </div>
          ))}
        </div>
      )

    case 'kpi':
      return <KPIGrid stats={stats} />

    case 'cat-dist':
      return cats.length
        ? <>{cats.map((c, i) => <StatRow key={c.category} label={c.category} value={c.instances} max={catMax} color={PALETTE[i % PALETTE.length]} />)}</>
        : <Empty />

    case 'cat-grid':
      return cats.length ? (
        <div className="grid grid-cols-2 gap-2">
          {cats.slice(0, 6).map((c, i) => (
            <div key={c.category} className="rounded-md border border-white/10 bg-white/[0.03] px-2.5 py-2">
              <p className="truncate text-[11px] text-slate-300" title={c.category}>{c.category}</p>
              <p className="mt-0.5 text-base font-semibold leading-none tabular-nums" style={{ color: PALETTE[i % PALETTE.length] }}>
                <CountUp value={c.instances} />
              </p>
              <p className="mt-0.5 text-[10px] text-slate-500">{c.models} 个模型</p>
            </div>
          ))}
        </div>
      ) : <Empty />

    case 'status-list':
      return statusItems.length
        ? <>{statusItems.map(s => <StatRow key={s.skey} label={s.name} value={s.value} max={statusMax} color={statusColor(s.skey)} />)}</>
        : <Empty />

    case 'status-pie':
      return statusItems.length
        ? <EChartsWrapper option={pieOption(statusItems.map(s => ({ name: s.name, value: s.value })))} height="100%" style={{ height: '100%' }} />
        : <Empty />

    case 'type-top':
      return types.length
        ? <>{types.map((t, i) => <StatRow key={t.type_id || t.type_name} label={t.type_name} value={t.count} max={typeMax} color={PALETTE[i % PALETTE.length]} />)}</>
        : <Empty />

    case 'attr-top':
      return attrs.length
        ? <>{attrs.map((a, i) => <StatRow key={`${a.key}-${a.value}`} label={`${a.key}: ${a.value}`} value={a.count} max={attrMax} color={PALETTE[i % PALETTE.length]} />)}</>
        : <Empty />

    case 'insight':
      return insights.length
        ? <>{insights.slice(0, 4).map((it, i) => <InsightCard key={i} it={it} />)}</>
        : <Empty text="暂无洞察" />

    default:
      return null
  }
}

// ---------------------------------------------------------------------------
// 驾驶舱面板外壳：科技蓝边框 + 四角切角 + 标题栏（编辑态即拖拽把手）
// ---------------------------------------------------------------------------
function Panel({ title, icon: Icon, accent = '#38bdf8', className, bodyClass, edit, onRename, onRemove, children }: {
  title: string
  icon: typeof Boxes
  accent?: string
  className?: string
  bodyClass?: string
  edit?: boolean
  onRename?: (title: string) => void
  onRemove?: () => void
  children: ReactNode
}) {
  return (
    <div className={cn('bsv-panel flex flex-col min-h-0 h-full w-full', className)}>
      <span className="bsv-corner" style={{ left: -1, top: -1, borderRight: 'none', borderBottom: 'none' }} />
      <span className="bsv-corner" style={{ right: -1, bottom: -1, borderLeft: 'none', borderTop: 'none' }} />
      <div className={cn('bsv-panel-head', edit && 'widget-drag-handle cursor-move select-none')}>
        {edit && <GripVertical className="h-3.5 w-3.5 shrink-0 text-sky-300/70" />}
        <span className="bsv-panel-bar" style={{ background: accent, color: accent }} />
        <Icon className="h-3.5 w-3.5 shrink-0" style={{ color: accent }} />
        {edit ? (
          <input
            value={title}
            title={title}
            aria-label="面板标题"
            onChange={e => onRename?.(e.target.value)}
            onClick={e => e.stopPropagation()}
            className="bg-transparent text-[12px] font-medium text-slate-100 outline-none border-b border-dashed border-white/25 focus:border-sky-400 min-w-0 w-28"
          />
        ) : (
          <span className="text-[12px] font-medium text-slate-100 tracking-wide truncate">{title}</span>
        )}
        {edit && (
          <button
            onClick={e => { e.stopPropagation(); onRemove?.() }}
            className="ml-auto shrink-0 p-0.5 rounded text-slate-400 hover:text-rose-400 hover:bg-white/10 transition-colors"
            title="移除面板"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      <div className={cn('flex-1 min-h-0 p-2.5', bodyClass)}>{children}</div>
    </div>
  )
}

/** 带底纹的横向占比条（名称 + 条 + 数值） */
function StatRow({ label, value, max, color }: { label: string; value: number; max: number; color: string }) {
  const pct = max > 0 ? Math.max(3, Math.round((value / max) * 100)) : 3
  return (
    <div className="flex items-center gap-2 py-[5px]">
      <span className="w-[92px] shrink-0 truncate text-[11px] text-slate-300" title={label}>{label}</span>
      <div className="bsv-row h-2.5 flex-1">
        <div className="bsv-row-fill" style={{ width: `${pct}%`, background: `linear-gradient(90deg, ${color}, ${color}55)` }} />
      </div>
      <span className="w-12 shrink-0 text-right text-[11px] font-medium tabular-nums text-slate-100">{value.toLocaleString()}</span>
    </div>
  )
}

function Sat({ name, value, color }: { name: string; value: number; color: string }) {
  return (
    <div className="bsv-sat min-w-[104px] px-3 py-2 text-center">
      <p className="text-[10px] text-slate-300 truncate">{name}</p>
      <p className="text-xl font-semibold leading-tight tabular-nums" style={{ color, textShadow: `0 0 14px ${color}66` }}>
        <CountUp value={value} />
      </p>
    </div>
  )
}

function Empty({ text = '暂无数据' }: { text?: string }) {
  return <div className="h-full min-h-[48px] flex items-center justify-center text-[11px] text-slate-500">{text}</div>
}

export default function CMDBBigScreen({ mode = 'embed' }: { mode?: 'embed' | 'standalone' }) {
  const navigate = useNavigate()
  const isStandalone = mode === 'standalone'
  const [stats, setStats] = useState<Stats | null>(null)
  const [insights, setInsights] = useState<Insight[]>([])
  const [loading, setLoading] = useState(true)
  const [widgets, setWidgets] = useState<WidgetDef[]>(DEFAULT_WIDGETS)
  const [layout, setLayout] = useState<Layout[]>(DEFAULT_LAYOUT)
  // 驾驶舱编排态：面板可拖拽 / 缩放 / 改名 / 移除 / 追加，保存后按用户持久化。
  const [edit, setEdit] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [addOpen, setAddOpen] = useState(false)
  const addRef = useRef<HTMLDivElement>(null)
  const layoutRef = useRef<Layout[]>(DEFAULT_LAYOUT)
  const [now, setNow] = useState<Date>(() => new Date())
  const [isFs, setIsFs] = useState(false)

  // 投放态（standalone）是纯展示：不暴露任何编辑入口，也不允许进入编辑态。
  const canEdit = !isStandalone
  const editing = canEdit && edit

  const activeTypes = useMemo(() => new Set(widgets.map(w => w.type)), [widgets])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [st, dash, ins] = await Promise.all([
        apiFetch<Stats>('/cmdb/stats'),
        apiFetchRaw('/cmdb/dashboard').then(r => r.ok ? r.json() : null).catch(() => null),
        apiFetchRaw('/cmdb/dashboard/insights').then(r => r.ok ? r.json() : []).catch(() => []),
      ])
      setStats(st)
      // 只有存档属于当前面板体系才采用；上一代「自由布局」存档自动回落默认编排。
      if (isCurrentLayout(dash)) {
        const d = dash as { widgets: WidgetDef[]; layout: Layout[] }
        setWidgets(d.widgets)
        setLayout(d.layout)
        layoutRef.current = d.layout
        setDirty(false)
      }
      setInsights(Array.isArray(ins) ? ins : [])
    } catch { setStats(null) } finally { setLoading(false) }
  }, [])

  useEffect(() => { load() }, [load])

  // 投放大屏模式：实时时钟 + 每 30s 自动刷新（编辑态不打断拖拽）
  useEffect(() => {
    if (!isStandalone) return
    const clock = setInterval(() => setNow(new Date()), 1000)
    const refresh = setInterval(() => { if (!editing) load() }, 30000)
    return () => { clearInterval(clock); clearInterval(refresh) }
  }, [isStandalone, editing, load])

  useEffect(() => {
    const onDoc = (e: MouseEvent) => { if (addRef.current && !addRef.current.contains(e.target as Node)) setAddOpen(false) }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [])

  useEffect(() => {
    const onFs = () => setIsFs(!!document.fullscreenElement)
    document.addEventListener('fullscreenchange', onFs)
    return () => document.removeEventListener('fullscreenchange', onFs)
  }, [])

  const toggleFs = () => {
    if (document.fullscreenElement) document.exitFullscreen?.()
    else document.documentElement.requestFullscreen?.()
  }
  const exitScreen = () => {
    if (document.fullscreenElement) document.exitFullscreen?.()
    navigate('/cmdb/stats')
  }

  const save = useCallback(async (nextWidgets = widgets, nextLayout = layout) => {
    setSaving(true)
    try {
      // 只落真实编排参数，丢掉 RGL 回传的 moved/static 等内部字段。
      const slim = nextLayout.map(({ i, x, y, w, h }) => ({ i, x, y, w, h }))
      const r = await apiFetchRaw('/cmdb/dashboard', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ widgets: nextWidgets, layout: slim }),
      }, { redirectOn401: false })
      if (r.ok) { getMessage().success('驾驶舱编排已保存'); setDirty(false) }
      else getMessage().error('保存失败')
    } catch { getMessage().error('保存失败，请稍后重试') } finally { setSaving(false) }
  }, [widgets, layout])

  // RGL 回传：仅在真实变化时更新 state，避免压实回传造成的无谓重渲染。
  const handleLayoutChange = useCallback((l: Layout[]) => {
    if (JSON.stringify(layoutRef.current) === JSON.stringify(l)) return
    layoutRef.current = l
    setLayout(l)
    if (editing) setDirty(true)
  }, [editing])

  const addWidget = (type: WidgetType) => {
    if (activeTypes.has(type)) return
    const meta = WIDGET_META[type]
    const id = `${type}-${Date.now()}`
    const maxY = layoutRef.current.reduce((m, it) => Math.max(m, it.y + it.h), 0)
    setWidgets(prev => [...prev, { id, type, title: meta.title }])
    const next = [...layoutRef.current, { i: id, x: 0, y: maxY, w: meta.defaultW, h: meta.defaultH }]
    layoutRef.current = next
    setLayout(next)
    setAddOpen(false)
    setDirty(true)
  }
  const removeWidget = (id: string) => {
    setWidgets(prev => prev.filter(w => w.id !== id))
    const next = layoutRef.current.filter(l => l.i !== id)
    layoutRef.current = next
    setLayout(next)
    setDirty(true)
  }
  const renameWidget = (id: string, title: string) => {
    setWidgets(prev => prev.map(w => w.id === id ? { ...w, title } : w))
    setDirty(true)
  }
  const resetDefault = () => {
    setWidgets(DEFAULT_WIDGETS)
    layoutRef.current = DEFAULT_LAYOUT
    setLayout(DEFAULT_LAYOUT)
    save(DEFAULT_WIDGETS, DEFAULT_LAYOUT)
  }

  const rootCls = isStandalone
    ? 'fixed inset-0 z-50 h-screen w-screen flex flex-col overflow-hidden'
    : 'min-h-full relative'

  return (
    <div className={cn(rootCls, 'bigscreen-root')}>
      {isStandalone && <><div className="bs-scan" /><div className="bs-particles" /></>}
      <style>{`
        .rgl-screen .react-grid-item.react-grid-placeholder { background: rgba(56,189,248,0.22) !important; border: 1px dashed rgba(56,189,248,0.55); border-radius: 8px; }
        .rgl-screen .react-grid-item > .react-resizable-handle::after { border-right-color: rgba(148,163,184,0.6) !important; border-bottom-color: rgba(148,163,184,0.6) !important; }
        .rgl-screen .react-grid-item.resizing, .rgl-screen .react-grid-item.react-draggable-dragging { z-index: 30; }
        .rgl-screen--edit .react-grid-item > .bsv-panel { outline: 1px dashed rgba(56,189,248,0.35); outline-offset: 0; }
        .rgl-screen .react-grid-item.resizing > .bsv-panel,
        .rgl-screen .react-grid-item.react-draggable-dragging > .bsv-panel {
          border-color: rgba(56,189,248,0.6);
          box-shadow: 0 0 0 1px rgba(56,189,248,0.35), 0 18px 44px rgba(2,10,30,0.62);
        }
        .rgl-screen .react-grid-item { transition: all 200ms ease; }

        /* 科技蓝背景 + 网格 + 流动光晕 */
        .bigscreen-root { background:
          radial-gradient(900px 520px at 10% -8%, rgba(56,189,248,0.22), transparent 60%),
          radial-gradient(1000px 620px at 95% -4%, rgba(99,102,241,0.18), transparent 55%),
          radial-gradient(1200px 820px at 50% 130%, rgba(14,165,233,0.14), transparent 60%),
          linear-gradient(135deg,#04122e 0%,#020b1e 55%,#01060f 100%); }
        .bigscreen-root::before { content:''; position:absolute; inset:0; pointer-events:none;
          background-image: linear-gradient(rgba(56,189,248,0.05) 1px, transparent 1px), linear-gradient(90deg, rgba(56,189,248,0.05) 1px, transparent 1px);
          background-size: 44px 44px;
          -webkit-mask-image: radial-gradient(circle at 50% 38%, #000 0%, transparent 82%);
          mask-image: radial-gradient(circle at 50% 38%, #000 0%, transparent 82%); }
        .bigscreen-root::after { content:''; position:absolute; inset:-25%; pointer-events:none;
          background: radial-gradient(640px 640px at 30% 30%, rgba(56,189,248,0.12), transparent 58%);
          animation: bsGlow 13s ease-in-out infinite alternate; }
        @keyframes bsGlow { from { transform: translate(-12%,-12%);} to { transform: translate(22%,16%);} }

        /* 扫描线（仅投放大屏） */
        .bs-scan { position:absolute; left:0; right:0; height:160px; pointer-events:none; z-index:1;
          background: linear-gradient(180deg, transparent, rgba(56,189,248,0.10) 45%, rgba(56,189,248,0.28) 50%, transparent);
          animation: bsScan 7.5s linear infinite; }
        @keyframes bsScan { 0% { top:-160px; } 100% { top:100%; } }

        /* 粒子背景 */
        .bs-particles { position:absolute; inset:0; pointer-events:none; z-index:0; overflow:hidden; }
        .bs-particles i { position:absolute; display:block; width:2px; height:2px; border-radius:50%; background:rgba(148,163,184,0.45); animation: bsParticle 25s linear infinite; }
        .bs-particles i:nth-child(1) { left:10%; top:20%; animation-duration:28s; animation-delay:-2s; }
        .bs-particles i:nth-child(2) { left:25%; top:60%; animation-duration:34s; animation-delay:-8s; }
        .bs-particles i:nth-child(3) { left:42%; top:35%; animation-duration:30s; animation-delay:-5s; }
        .bs-particles i:nth-child(4) { left:58%; top:80%; animation-duration:26s; animation-delay:-12s; }
        .bs-particles i:nth-child(5) { left:72%; top:25%; animation-duration:32s; animation-delay:-3s; }
        .bs-particles i:nth-child(6) { left:85%; top:70%; animation-duration:29s; animation-delay:-15s; }
        .bs-particles i:nth-child(7) { left:5%; top:85%; animation-duration:35s; animation-delay:-10s; }
        .bs-particles i:nth-child(8) { left:95%; top:45%; animation-duration:27s; animation-delay:-6s; }
        @keyframes bsParticle { 0% { transform:translateY(0) scale(1); opacity:0; } 10% { opacity:1; } 90% { opacity:1; } 100% { transform:translateY(-120vh) scale(0.4); opacity:0; } }

        /* KPI 卡片 */
        .bs-kpi-card { box-shadow: inset 0 0 24px rgba(56,189,248,0.06), 0 8px 28px rgba(2,10,30,0.4); transition:transform .3s, box-shadow .3s; }
        .bs-kpi-card:hover { transform:translateY(-2px); box-shadow: inset 0 0 30px rgba(56,189,248,0.10), 0 12px 36px rgba(2,10,30,0.55); }

        /* 标题流光 */
        .bs-title-line { height:1px; background:linear-gradient(90deg, transparent, rgba(56,189,248,0.5), #38bdf8, rgba(56,189,248,0.5), transparent); background-size:200% 100%;
          animation: bsFlow 4s linear infinite; }
        @keyframes bsFlow { 0% { background-position:200% 0; } 100% { background-position:-200% 0; } }
        .bs-title-glow { text-shadow: 0 0 20px rgba(56,189,248,0.55), 0 0 40px rgba(56,189,248,0.25); }
        @keyframes bsPulse { 0%,100% { opacity:1; } 50% { opacity:0.35; } }
        .bs-live { animation: bsPulse 1.6s ease-in-out infinite; }

        /* 底部 AI 洞察 ticker（无缝滚动） */
        .bs-ticker-track { animation: bsTicker 28s linear infinite; }
        @keyframes bsTicker { 0% { transform: translateX(0); } 100% { transform: translateX(-50%); } }
        .bs-ticker:hover .bs-ticker-track { animation-play-state: paused; }

        /* ================= 驾驶舱面板 ================= */
        .bsv-panel { position:relative; border:1px solid rgba(56,189,248,0.18); border-radius:8px; overflow:hidden;
          background: linear-gradient(180deg, rgba(12,22,44,0.75), rgba(8,14,30,0.55));
          box-shadow: inset 0 0 34px rgba(56,189,248,0.055), 0 10px 28px rgba(2,10,30,0.45); }
        .bsv-panel-head { display:flex; align-items:center; gap:6px; height:32px; padding:0 10px; flex-shrink:0;
          border-bottom:1px dashed rgba(56,189,248,0.22);
          background: linear-gradient(90deg, rgba(56,189,248,0.16), rgba(56,189,248,0.02) 65%, transparent); }
        .bsv-panel-bar { width:3px; height:12px; border-radius:2px; box-shadow:0 0 8px currentColor; }
        .bsv-corner { position:absolute; width:9px; height:9px; border:1px solid rgba(56,189,248,0.55); pointer-events:none; }
        /* 中心球体 */
        .bsv-sphere { position:relative; width:156px; height:156px; border-radius:50%;
          background: radial-gradient(circle at 50% 42%, rgba(125,211,252,0.62), rgba(14,116,144,0.28) 46%, rgba(2,10,30,0.1) 72%);
          box-shadow: 0 0 70px rgba(56,189,248,0.42), inset 0 0 60px rgba(56,189,248,0.3); }
        .bsv-sphere::after { content:''; position:absolute; inset:-13%; border-radius:50%;
          background: radial-gradient(circle, transparent 56%, rgba(56,189,248,0.32) 58%, transparent 66%);
          animation: bsvSpin 10s linear infinite; }
        @keyframes bsvSpin { to { transform: rotate(360deg); } }
        /* 卫星指标卡 */
        .bsv-sat { border:1px solid rgba(56,189,248,0.3); border-radius:6px;
          background: linear-gradient(180deg, rgba(56,189,248,0.16), rgba(2,10,30,0.4));
          box-shadow: 0 0 18px rgba(56,189,248,0.16); }
        /* 横条进度行 */
        .bsv-row { position:relative; border-radius:5px; overflow:hidden; background: rgba(148,163,184,0.1); }
        .bsv-row-fill { position:absolute; top:0; bottom:0; left:0; border-radius:5px; transition: width .8s cubic-bezier(.22,1,.36,1); }
      `}</style>

      {/* 标题区 */}
      <div className={cn('px-6 py-4 flex items-center justify-between flex-wrap gap-3 shrink-0 z-10', !isStandalone && 'relative')}>
        <div className="flex items-center gap-3 min-w-0">
          <div className="h-10 w-10 rounded-xl flex items-center justify-center shrink-0" style={{ background: 'linear-gradient(135deg,rgba(56,189,248,0.22),rgba(99,102,241,0.15))', boxShadow: '0 0 18px rgba(56,189,248,0.35)' }}>
            <Activity className="h-5 w-5 text-sky-300" />
          </div>
          <div>
            <h1 className="text-lg font-bold text-slate-100 flex items-center gap-2 bs-title-glow tracking-wide">
              IT 资源统计大屏
              <span className="text-xs font-normal text-sky-300/80 align-middle">· 驾驶舱</span>
              {isStandalone && <span className="bs-live ml-0.5 inline-block h-2 w-2 rounded-full bg-emerald-400 shadow-[0_0_8px_rgba(52,211,153,0.9)]" title="实时刷新" />}
            </h1>
            <div className="h-px w-full bs-title-line mt-1.5" />
            <p className="text-[11px] text-slate-400 mt-1">
              {isStandalone
                ? 'CMDB 配置项总览 · 投放模式（每 30s 自动刷新，编排请在资源统计页调整）'
                : 'CMDB 配置项总览 · 面板可按需编排（拖拽 / 缩放 / 改名 / 增删），编排按当前用户保存'}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {isStandalone && (
            <div className="hidden lg:flex items-center gap-3 text-xs text-slate-400 tabular-nums mr-1">
              <span>{now.toLocaleDateString('zh-CN')}</span>
              <span className="text-slate-600">|</span>
              <span className="text-sky-300/90 font-medium">{now.toLocaleTimeString('zh-CN', { hour12: false })}</span>
              <span className="text-slate-600">|</span>
              <span>每 30s 自动刷新</span>
            </div>
          )}
          {!isStandalone && (
            <button
              onClick={() => navigate('/cmdb/screen')}
              title="整屏投放：隐藏后台框架、自动刷新、支持全屏"
              className="inline-flex items-center gap-1 text-sm px-3 py-1.5 rounded-lg border border-sky-400/40 bg-sky-500/15 text-sky-300 hover:bg-sky-500/25 transition-colors"
            >
              <Tv className="h-4 w-4" />大屏
            </button>
          )}
          <button onClick={load} disabled={loading} className="inline-flex items-center gap-1 text-sm px-3 py-1.5 rounded-lg border border-white/10 bg-white/5 text-slate-200 hover:bg-white/10 transition-colors disabled:opacity-50">
            <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
          </button>
          {editing && (
            <div className="relative" ref={addRef}>
              <button onClick={() => setAddOpen(o => !o)} className="inline-flex items-center gap-1 text-sm px-3 py-1.5 rounded-lg border border-sky-400/40 bg-sky-500/15 text-sky-300 hover:bg-sky-500/25 transition-colors">
                <Plus className="h-4 w-4" />添加面板
              </button>
              {addOpen && (
                <div className="absolute right-0 z-40 mt-1 w-52 rounded-xl border border-white/10 bg-slate-900/95 backdrop-blur shadow-xl p-1.5">
                  {(Object.keys(WIDGET_META) as WidgetType[]).map(t => {
                    const m = WIDGET_META[t]
                    const disabled = activeTypes.has(t)
                    return (
                      <button key={t} disabled={disabled} onClick={() => addWidget(t)}
                        className={cn('w-full flex items-center gap-2 px-2.5 py-2 rounded-lg text-sm text-left transition-colors',
                          disabled ? 'opacity-40 cursor-not-allowed text-slate-400' : 'text-slate-200 hover:bg-white/10')}>
                        <m.icon className="h-4 w-4" style={{ color: m.accent }} />{m.label}{disabled && <span className="ml-auto text-[10px]">已添加</span>}
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )}
          {editing && (
            <button onClick={resetDefault} className="inline-flex items-center gap-1 text-sm px-3 py-1.5 rounded-lg border border-white/10 bg-white/5 text-slate-200 hover:bg-white/10 transition-colors">
              <RotateCcw className="h-4 w-4" />重置默认
            </button>
          )}
          {canEdit && (
            <button
              onClick={async () => { if (editing && dirty) await save(); setEdit(e => !e) }}
              className={cn('inline-flex items-center gap-1 text-sm px-3.5 py-1.5 rounded-lg font-medium transition-colors',
                editing ? 'bg-emerald-500/90 text-white hover:bg-emerald-500' : 'bg-sky-500 text-white hover:bg-sky-400')}
            >
              {editing ? <><Check className="h-4 w-4" />{saving ? '保存中…' : '完成'}</> : <><Pencil className="h-4 w-4" />编辑编排</>}
            </button>
          )}
          {isStandalone && (
            <>
              <button onClick={toggleFs} className="inline-flex items-center gap-1 text-sm px-3 py-1.5 rounded-lg border border-white/10 bg-white/5 text-slate-200 hover:bg-white/10 transition-colors">
                {isFs ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}{isFs ? '退出全屏' : '全屏'}
              </button>
              <button onClick={exitScreen} title="退出投放，返回资源统计页" className="inline-flex items-center gap-1 text-sm px-3 py-1.5 rounded-lg border border-rose-400/30 bg-rose-500/10 text-rose-300 hover:bg-rose-500/20 transition-colors">
                <LogOut className="h-4 w-4" />退出
              </button>
            </>
          )}
        </div>
      </div>

      {/* 编排提示条（仅编辑态） */}
      {editing && (
        <div className="mx-5 mb-2 shrink-0 z-10 flex items-center gap-2 rounded-lg border border-sky-400/25 bg-sky-500/[0.08] px-3 py-2 text-[11px] text-sky-200/90">
          <GripVertical className="h-3.5 w-3.5 shrink-0" />
          <span className="min-w-0">
            拖动面板<span className="text-sky-100">标题栏</span>换位置 · 拖右下角<span className="text-sky-100">调整大小</span> · 点标题可直接<span className="text-sky-100">改名</span> · 右上角 × <span className="text-sky-100">移除</span> · 「添加面板」补回
          </span>
          {dirty && <span className="ml-auto shrink-0 text-amber-300">有未保存改动，点「完成」保存</span>}
        </div>
      )}

      {!stats ? (
        <div className={cn('px-5 py-20 text-center text-slate-400 text-sm', isStandalone && 'flex-1')}>
          {loading ? '加载中…' : '暂无统计数据'}
        </div>
      ) : (
        <div className={cn('px-5 pb-5 z-10', isStandalone && 'flex-1 min-h-0 overflow-y-auto')}>
          <GridLayout
            className={cn('rgl-screen layout', editing && 'rgl-screen--edit')}
            layout={layout}
            cols={12}
            rowHeight={30}
            margin={[12, 12]}
            isDraggable={editing}
            isResizable={editing}
            draggableHandle=".widget-drag-handle"
            onLayoutChange={handleLayoutChange}
            compactType="vertical"
          >
            {widgets.map(w => (
              <div key={w.id} data-widget={w.id} data-widget-type={w.type}>
                <Panel
                  title={w.title}
                  icon={WIDGET_META[w.type].icon}
                  accent={WIDGET_META[w.type].accent}
                  edit={editing}
                  bodyClass={BODY_CLASS[w.type]}
                  onRename={t => renameWidget(w.id, t)}
                  onRemove={() => removeWidget(w.id)}
                >
                  <WidgetBody type={w.type} stats={stats} insights={insights} />
                </Panel>
              </div>
            ))}
          </GridLayout>
        </div>
      )}

      {/* 投放大屏：底部 AI 洞察滚动 ticker（DataV 风格） */}
      {isStandalone && insights.length > 0 && (
        <div className="bs-ticker shrink-0 flex items-center gap-3 px-5 h-9 border-t border-white/10 bg-black/25 overflow-hidden z-10">
          <span className="shrink-0 text-[11px] font-semibold text-violet-300 flex items-center gap-1">
            <Sparkles className="h-3.5 w-3.5" />AI 洞察
          </span>
          <div className="bs-ticker-track flex items-center gap-10 text-[11px] text-slate-300 whitespace-nowrap">
            {[...insights, ...insights].map((it, i) => (
              <span key={i} className="flex items-center gap-1.5">
                <span className="h-1.5 w-1.5 rounded-full" style={{ background: it.level === 'risk' ? '#f87171' : it.level === 'warn' ? '#fbbf24' : '#38bdf8' }} />
                {it.title}{it.metric ? `（${it.metric}）` : ''}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
