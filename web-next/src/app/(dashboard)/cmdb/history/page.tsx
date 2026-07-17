'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { apiFetch } from '@/lib/utils'
import { History, Search, GitCompare, Clock, User, ArrowRight, ChevronDown, ChevronUp } from 'lucide-react'
import { cn } from '@/lib/utils'

interface HistoryEntry {
  id: string
  ci_id: string
  name: string
  type_id: string
  status: string
  version: number
  attributes: Record<string, any>
  labels: Record<string, string>
  operator: string
  change_type: string
  created_at: string
}

const ACTION_CONFIG: Record<string, { label: string; color: string }> = {
  create: { label: '创建', color: 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400' },
  update: { label: '更新', color: 'bg-blue-100 text-blue-700 dark:bg-blue-500/20 dark:text-blue-400' },
  delete: { label: '删除', color: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-400' },
}

export default function CMDBHistoryPage() {
  const [history, setHistory] = useState<HistoryEntry[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [actionFilter, setActionFilter] = useState('')
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [compareMode, setCompareMode] = useState(false)
  const [compareItems, setCompareItems] = useState<string[]>([])
  const [compareData, setCompareData] = useState<HistoryEntry[]>([])

  const fetchHistory = async () => {
    setLoading(true)
    try {
      const params = new URLSearchParams()
      if (actionFilter) params.set('change_type', actionFilter)
      params.set('limit', '100')
      const data = await apiFetch<{ items: HistoryEntry[]; total: number }>(`/cmdb/history?${params}`)
      setHistory(data.items || [])
      setTotal(data.total || 0)
    } catch { setHistory([]) }
    setLoading(false)
  }

  useEffect(() => { fetchHistory() }, [actionFilter])

  const filtered = history.filter(h =>
    !search || h.name.toLowerCase().includes(search.toLowerCase()) || h.type_id.toLowerCase().includes(search.toLowerCase())
  )

  const toggleExpand = (id: string) => setExpandedId(expandedId === id ? null : id)

  const toggleCompare = async (id: string) => {
    const newItems = compareItems.includes(id)
      ? compareItems.filter(i => i !== id)
      : compareItems.length >= 2 ? [compareItems[1], id] : [...compareItems, id]
    setCompareItems(newItems)

    if (newItems.length === 2) {
      // 获取两个版本的详情用于对比
      const entries = history.filter(h => newItems.includes(h.id))
      setCompareData(entries)
    }
  }

  const getFieldDiff = (oldAttrs: Record<string, any>, newAttrs: Record<string, any>) => {
    const diffs: { field: string; oldValue?: string; newValue?: string }[] = []
    const allKeys = new Set([...Object.keys(oldAttrs || {}), ...Object.keys(newAttrs || {})])
    for (const key of allKeys) {
      const oldVal = oldAttrs?.[key]
      const newVal = newAttrs?.[key]
      if (JSON.stringify(oldVal) !== JSON.stringify(newVal)) {
        diffs.push({ field: key, oldValue: oldVal !== undefined ? String(oldVal) : undefined, newValue: newVal !== undefined ? String(newVal) : undefined })
      }
    }
    return diffs
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">版本与历史追溯</h1>
          <p className="text-sm text-muted-foreground">记录 CI 属性及关系的变更历史，支持任意时间点回溯与差异对比</p>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">共 {total} 条记录</span>
          <Button variant={compareMode ? "default" : "outline"} onClick={() => { setCompareMode(!compareMode); setCompareItems([]); setCompareData([]) }}>
            <GitCompare className="h-4 w-4" />{compareMode ? '退出对比' : '差异对比'}
          </Button>
        </div>
      </div>

      <div className="flex gap-3">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input placeholder="搜索 CI 名称..." value={search} onChange={e => setSearch(e.target.value)} className="pl-9" />
        </div>
        <div className="flex gap-1">
          {Object.entries(ACTION_CONFIG).map(([key, cfg]) => (
            <button key={key} className={cn("px-2 py-1 rounded text-[10px] border transition-colors", actionFilter === key ? cfg.color + " border-current" : "border-gray-200 dark:border-gray-700 hover:border-gray-300")} onClick={() => setActionFilter(actionFilter === key ? '' : key)}>{cfg.label}</button>
          ))}
        </div>
      </div>

      {/* 差异对比面板 */}
      {compareMode && compareData.length === 2 && (
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><GitCompare className="h-4 w-4" />差异对比</CardTitle></CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-4">
              {compareData.map(entry => {
                const ac = ACTION_CONFIG[entry.change_type] || ACTION_CONFIG.update
                return (
                  <div key={entry.id} className="border rounded-lg p-3 space-y-2">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-sm">{entry.name}</span>
                      <Badge className={cn("text-[10px]", ac.color)}>{ac.label}</Badge>
                      <span className="text-[10px] text-muted-foreground">v{entry.version}</span>
                    </div>
                    <p className="text-xs text-muted-foreground">{entry.created_at?.slice(0, 19).replace('T', ' ')} · {entry.operator}</p>
                    {entry.attributes && Object.keys(entry.attributes).length > 0 && (
                      <div className="space-y-1 mt-2">
                        {Object.entries(entry.attributes).map(([k, v]) => (
                          <div key={k} className="text-xs flex items-center gap-2">
                            <span className="font-medium min-w-[80px]">{k}:</span>
                            <span>{String(v)}</span>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
            {/* 字段级差异 */}
            {compareData.length === 2 && (() => {
              const diffs = getFieldDiff(compareData[0].attributes, compareData[1].attributes)
              if (diffs.length === 0) return null
              return (
                <div className="mt-4 p-3 rounded-lg bg-gray-50 dark:bg-gray-800/50">
                  <p className="text-xs font-medium mb-2">字段差异</p>
                  <div className="space-y-1">
                    {diffs.map((d, i) => (
                      <div key={i} className="text-xs flex items-center gap-2">
                        <span className="font-medium min-w-[100px]">{d.field}</span>
                        {d.oldValue && <span className="text-red-500 bg-red-50 dark:bg-red-500/10 px-1.5 py-0.5 rounded">{d.oldValue}</span>}
                        {d.oldValue && d.newValue && <ArrowRight className="h-3 w-3" />}
                        {d.newValue && <span className="text-green-600 bg-green-50 dark:bg-green-500/10 px-1.5 py-0.5 rounded">{d.newValue}</span>}
                      </div>
                    ))}
                  </div>
                </div>
              )
            })()}
          </CardContent>
        </Card>
      )}

      {/* 历史时间线 */}
      <Card>
        <CardContent className="p-0">
          {loading ? (
            <div className="text-center py-8 text-muted-foreground">加载中...</div>
          ) : filtered.length === 0 ? (
            <div className="text-center py-8 text-muted-foreground">暂无历史记录</div>
          ) : (
            <div className="divide-y">
              {filtered.map(entry => {
                const ac = ACTION_CONFIG[entry.change_type] || { label: entry.change_type, color: 'bg-gray-100 text-gray-700' }
                const isExpanded = expandedId === entry.id
                const isComparing = compareItems.includes(entry.id)
                return (
                  <div key={entry.id} className={cn("transition-colors", isComparing && "bg-emerald-50 dark:bg-emerald-500/5")}>
                    <div className="flex items-center gap-4 px-4 py-3 hover:bg-gray-50 dark:hover:bg-gray-800/50 cursor-pointer" onClick={() => compareMode ? toggleCompare(entry.id) : toggleExpand(entry.id)}>
                      {compareMode && (
                        <input type="checkbox" checked={isComparing} onChange={() => toggleCompare(entry.id)} className="rounded" onClick={e => e.stopPropagation()} />
                      )}
                      <div className="h-8 w-8 rounded-full bg-gray-100 dark:bg-gray-800 flex items-center justify-center shrink-0">
                        <History className="h-4 w-4 text-muted-foreground" />
                      </div>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="text-sm font-medium">{entry.name}</span>
                          <Badge variant="outline" className="text-[10px]">{entry.type_id}</Badge>
                          <Badge className={cn("text-[10px]", ac.color)}>{ac.label}</Badge>
                          <span className="text-[10px] text-muted-foreground">v{entry.version}</span>
                        </div>
                        <div className="flex items-center gap-3 mt-0.5 text-xs text-muted-foreground">
                          <span className="flex items-center gap-1"><Clock className="h-3 w-3" />{entry.created_at?.slice(0, 19).replace('T', ' ')}</span>
                          <span className="flex items-center gap-1"><User className="h-3 w-3" />{entry.operator}</span>
                        </div>
                      </div>
                      {isExpanded ? <ChevronUp className="h-4 w-4 text-muted-foreground" /> : <ChevronDown className="h-4 w-4 text-muted-foreground" />}
                    </div>
                    {isExpanded && entry.attributes && Object.keys(entry.attributes).length > 0 && (
                      <div className="px-16 pb-3 space-y-1">
                        {Object.entries(entry.attributes).map(([k, v]) => (
                          <div key={k} className="flex items-center gap-3 text-xs py-1 px-3 rounded bg-gray-50 dark:bg-gray-800/50">
                            <span className="font-medium min-w-[100px]">{k}</span>
                            <span>{String(v)}</span>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
