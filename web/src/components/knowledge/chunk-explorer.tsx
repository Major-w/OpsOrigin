import { useEffect, useMemo, useState } from 'react'
import { apiFetch } from '@/lib/utils'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Loader2, Boxes, Hash, ChevronDown, ChevronRight, FileCode2, Table2, List, Type } from 'lucide-react'
import { getMessage } from '@/lib/message'
import { cn } from '@/lib/utils'

interface ChunkItem {
  id: string
  heading?: string
  content: string
}

// inferKind 按内容形态给块打标(后端 knowledge_chunks 未持久化 kind, 此处轻量推断供可视化)。
// 仅用于展示, 不影响检索; 与 internal/knowledge/chunk.go 的 blockKind 口径尽量一致。
function inferKind(content: string): 'text' | 'code' | 'table' | 'list' {
  const lines = content.split('\n')
  let codeFence = false
  for (const ln of lines) {
    const t = ln.trim()
    if (t.startsWith('```')) {
      codeFence = !codeFence
      continue
    }
    if (codeFence) continue
    if (t.startsWith('|') && t.includes('|', 1)) return 'table'
    if (t.startsWith('- ') || t.startsWith('* ') || t.startsWith('+ ')) return 'list'
    if (/^\d+\.\s/.test(t)) return 'list'
  }
  if (codeFence) return 'code'
  if (content.includes('```')) return 'code'
  return 'text'
}

const KIND_META: Record<string, { label: string; icon: typeof Type; cls: string }> = {
  text: { label: '文本', icon: Type, cls: 'bg-sky-500/15 text-sky-600 dark:text-sky-300' },
  code: { label: '代码', icon: FileCode2, cls: 'bg-violet-500/15 text-violet-600 dark:text-violet-300' },
  table: { label: '表格', icon: Table2, cls: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-300' },
  list: { label: '列表', icon: List, cls: 'bg-amber-500/15 text-amber-600 dark:text-amber-300' },
}

export function ChunkExplorer({ articleId }: { articleId: string }) {
  const [chunks, setChunks] = useState<ChunkItem[]>([])
  const [loading, setLoading] = useState(true)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    apiFetch<ChunkItem[]>(`/knowledge/${articleId}/chunks`)
      .then(c => { if (!cancelled) setChunks(c || []) })
      .catch(() => { if (!cancelled) { setChunks([]); getMessage().warning('分块数据加载失败') } })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [articleId])

  const stats = useMemo(() => {
    const total = chunks.reduce((s, c) => s + c.content.length, 0)
    const kinds = chunks.reduce<Record<string, number>>((m, c) => {
      const k = inferKind(c.content)
      m[k] = (m[k] || 0) + 1
      return m
    }, {})
    return { count: chunks.length, total, avg: chunks.length ? Math.round(total / chunks.length) : 0, kinds }
  }, [chunks])

  if (loading) {
    return (
      <Card>
        <CardContent className="py-10 flex flex-col items-center justify-center text-muted-foreground">
          <Loader2 className="h-5 w-5 animate-spin mb-2" />
          <span className="text-sm">加载分块…</span>
        </CardContent>
      </Card>
    )
  }

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-sm">
          <Boxes className="h-4 w-4 text-muted-foreground" />
          分块可视化（{stats.count} 段）
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0 space-y-3">
        {stats.count === 0 ? (
          <p className="text-xs text-muted-foreground py-3 text-center">
            该文章暂无切块（草稿或未发布、正文为空时不会生成检索片段）
          </p>
        ) : (
          <>
            {/* 概览统计: 总片段数 / 总字符 / 平均每段大小 / 形态分布 */}
            <div className="flex flex-wrap gap-2 text-[11px]">
              <span className="px-2 py-1 rounded-full bg-muted">共 {stats.count} 段</span>
              <span className="px-2 py-1 rounded-full bg-muted">{stats.total} 字符</span>
              <span className="px-2 py-1 rounded-full bg-muted">平均 {stats.avg} 字/段</span>
              {Object.entries(stats.kinds).map(([k, n]) => {
                const m = KIND_META[k] || KIND_META.text
                const Icon = m.icon
                return (
                  <span key={k} className={cn('px-2 py-1 rounded-full flex items-center gap-1', m.cls)}>
                    <Icon className="h-3 w-3" />{m.label} {n}
                  </span>
                )
              })}
            </div>

            {/* 逐块: 序号 + 标题路径 + 形态 + 内容(可展开) */}
            <div className="space-y-2 max-h-[560px] overflow-y-auto pr-1">
              {chunks.map((c, i) => {
                const k = inferKind(c.content)
                const m = KIND_META[k] || KIND_META.text
                const Icon = m.icon
                const open = expanded[c.id]
                const isLong = c.content.length > 260
                return (
                  <div key={c.id} className="rounded-lg border border-gray-200 dark:border-gray-800">
                    <button
                      type="button"
                      className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/50 transition-colors"
                      onClick={() => setExpanded(e => ({ ...e, [c.id]: !e[c.id] }))}
                    >
                      <span className="shrink-0 flex items-center gap-1 text-[11px] font-mono text-muted-foreground">
                        <Hash className="h-3 w-3" />{i + 1}
                      </span>
                      <span className={cn('shrink-0 px-1.5 py-0.5 rounded-full flex items-center gap-1', m.cls)}>
                        <Icon className="h-3 w-3" />{m.label}
                      </span>
                      <span className="flex-1 min-w-0 truncate text-xs font-medium">
                        {c.heading || '（无标题）'}
                      </span>
                      <span className="shrink-0 text-[10px] text-muted-foreground">{c.content.length} 字</span>
                      {isLong ? (
                        open ? <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" /> : <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
                      ) : null}
                    </button>
                    <div className="px-3 pb-2">
                      <pre className={cn(
                        'whitespace-pre-wrap break-words text-[11px] leading-relaxed text-foreground/90 font-mono',
                        !open && isLong && 'max-h-20 overflow-hidden',
                      )}>
                        {c.content}
                      </pre>
                      {isLong && !open && (
                        <button
                          type="button"
                          className="text-[11px] text-blue-500 hover:underline mt-1"
                          onClick={() => setExpanded(e => ({ ...e, [c.id]: true }))}
                        >
                          展开全文
                        </button>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
