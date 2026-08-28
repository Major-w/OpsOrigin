
import { Cloud, Cpu, User, Ticket, Terminal, Radar, AlertTriangle } from 'lucide-react'

export type SourceKind = 'manual' | 'itsm' | 'agent' | 'cloud_api' | 'auto_script' | 'discovery'

export interface AttrMeta {
  source: SourceKind
  confidence: number
  last_seen_at?: string
}

const SOURCE_CONFIG: Record<SourceKind, { label: string; icon: typeof Cloud; classes: string }> = {
  manual: { label: '人工', icon: User, classes: 'bg-blue-500/10 text-blue-600 dark:text-blue-400' },
  itsm: { label: 'ITSM', icon: Ticket, classes: 'bg-sky-500/10 text-sky-600 dark:text-sky-400' },
  agent: { label: 'Agent', icon: Cpu, classes: 'bg-violet-500/10 text-violet-600 dark:text-violet-400' },
  cloud_api: { label: '云API', icon: Cloud, classes: 'bg-amber-500/10 text-amber-600 dark:text-amber-400' },
  auto_script: { label: '脚本', icon: Terminal, classes: 'bg-cyan-500/10 text-cyan-600 dark:text-cyan-400' },
  discovery: { label: '发现', icon: Radar, classes: 'bg-slate-500/10 text-slate-600 dark:text-slate-400' },
}

export function confidenceLevel(c: number): 'high' | 'mid' | 'low' {
  if (c >= 0.8) return 'high'
  if (c >= 0.5) return 'mid'
  return 'low'
}

interface SourceBadgeProps {
  meta?: AttrMeta
  showConfidence?: boolean
}

/** 单字段数据来源徽标：图标 + 来源 + 置信度；低置信度自动加红色描边与警示图标。 */
export function SourceBadge({ meta, showConfidence = true }: SourceBadgeProps) {
  if (!meta || !meta.source) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
        未记录
      </span>
    )
  }
  const cfg = SOURCE_CONFIG[meta.source] ?? SOURCE_CONFIG.discovery
  const Icon = cfg.icon
  const low = confidenceLevel(meta.confidence) === 'low'
  const title = `来源: ${cfg.label} · 置信度: ${Math.round(meta.confidence * 100)}%${meta.last_seen_at ? ' · 最近: ' + meta.last_seen_at : ''}${low ? ' · 低置信度，建议人工核实' : ''}`
  return (
    <span
      title={title}
      className={`inline-flex items-center gap-1 rounded-full px-1.5 py-0.5 text-[10px] font-medium transition-transform hover:scale-105 ${cfg.classes} ${low ? 'ring-1 ring-red-400/60' : ''}`}
    >
      {low && <AlertTriangle className="h-2.5 w-2.5 text-red-500" />}
      <Icon className="h-2.5 w-2.5" />
      {cfg.label}
      {showConfidence && <span className="opacity-70">{Math.round(meta.confidence * 100)}%</span>}
    </span>
  )
}

interface ProvenanceSummaryProps {
  meta?: Record<string, AttrMeta>
}

/** 资产级数据可信概览：平均置信度 + 来源分布 + 低置信字段提醒。 */
export function ProvenanceSummary({ meta }: ProvenanceSummaryProps) {
  const entries = meta ? Object.values(meta) : []
  if (entries.length === 0) return null

  const avg = entries.reduce((s, m) => s + m.confidence, 0) / entries.length
  const bySource: Record<string, number> = {}
  for (const m of entries) bySource[m.source] = (bySource[m.source] || 0) + 1
  const lowCount = entries.filter((m) => m.confidence < 0.5).length
  const avgLevel = confidenceLevel(avg)

  return (
    <div className="rounded-lg border bg-muted/30 p-3 space-y-2">
      <div className="flex items-center justify-between">
        <span className="text-xs text-muted-foreground">数据可信概览 · {entries.length} 字段</span>
        <span
          className={`text-sm font-semibold ${
            avgLevel === 'low'
              ? 'text-red-500'
              : avgLevel === 'mid'
                ? 'text-amber-500'
                : 'text-blue-500'
          }`}
        >
          {Math.round(avg * 100)}%
        </span>
      </div>
      <div className="flex flex-wrap gap-1">
        {Object.entries(bySource).map(([src, n]) => (
          <span key={src} className="inline-flex items-center gap-0.5">
            <SourceBadge meta={{ source: src as SourceKind, confidence: 1 }} showConfidence={false} />
            <span className="text-[10px] text-muted-foreground">×{n}</span>
          </span>
        ))}
      </div>
      {lowCount > 0 && (
        <div className="flex items-center gap-1 text-[11px] text-amber-600 dark:text-amber-400">
          <AlertTriangle className="h-3 w-3 shrink-0" />
          {lowCount} 个字段为低置信度（自动发现），建议人工核实
        </div>
      )}
    </div>
  )
}
