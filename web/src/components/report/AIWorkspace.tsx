
import { useCallback, useEffect, useState } from 'react'
import {
  Sparkles,
  Wand2,
  Save,
  RotateCcw,
  ShieldCheck,
  AlertTriangle,
  Lightbulb,
  Loader2,
  Download,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { ReportRenderer } from '@/components/report/ReportRenderer'
import {
  useReportStore,
  type DashboardDraft,
  type Result,
  type WorkspaceSuggestion,
} from '@/stores/report'
import { getMessage } from '@/lib/message'

interface PreviewState {
  data?: Result
  error?: string
}

/**
 * AI 定义工作空间。
 *
 * 交互契约：AI 只产"草稿"——生成结果先在本地预览，用户确认后才调用与人工搭建
 * 完全一致的创建接口落库。因此本组件不持有任何写权限旁路，
 * 也不会把未经校验的图表塞进别人的看板。
 */
export function AIWorkspace({ onSaved }: { onSaved?: () => void }) {
  const { generateDashboard, loadWorkspaceSuggestions, runQuery, createDashboard } =
    useReportStore()

  const [prompt, setPrompt] = useState('')
  const [suggestions, setSuggestions] = useState<WorkspaceSuggestion[]>([])
  const [draft, setDraft] = useState<DashboardDraft | null>(null)
  const [previews, setPreviews] = useState<Record<string, PreviewState>>({})
  const [generating, setGenerating] = useState(false)
  const [previewing, setPreviewing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [name, setName] = useState('')

  useEffect(() => {
    loadWorkspaceSuggestions().then(setSuggestions)
  }, [loadWorkspaceSuggestions])

  // 逐 widget 取数：并行发起，单图失败只影响自己，不拖垮整张预览
  const runPreviews = useCallback(
    async (d: DashboardDraft) => {
      setPreviewing(true)
      setPreviews({})
      const entries = await Promise.all(
        (d.dashboard.widgets || []).map(async (w) => {
          try {
            const data = await runQuery(w.query)
            return [w.id, { data }] as const
          } catch (e) {
            return [w.id, { error: (e as Error).message }] as const
          }
        })
      )
      setPreviews(Object.fromEntries(entries))
      setPreviewing(false)
    },
    [runQuery]
  )

  const handleGenerate = async () => {
    const p = prompt.trim()
    if (!p) return getMessage().error('先描述一下你想看什么数据')
    setGenerating(true)
    setDraft(null)
    setPreviews({})
    try {
      const d = await generateDashboard(p)
      setDraft(d)
      setName(d.dashboard.name || '')
      await runPreviews(d)
    } catch (e) {
      getMessage().error('生成失败: ' + (e as Error).message)
    } finally {
      setGenerating(false)
    }
  }

  const handleSave = async () => {
    if (!draft) return
    const finalName = name.trim() || draft.dashboard.name
    if (!finalName) return getMessage().error('请填写看板名称')
    setSaving(true)
    try {
      await createDashboard({
        name: finalName,
        description: draft.dashboard.description,
        widgets: draft.dashboard.widgets,
      })
      setDraft(null)
      setPreviews({})
      setPrompt('')
      onSaved?.()
    } catch (e) {
      getMessage().error('保存失败: ' + (e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const widgets = draft?.dashboard.widgets || []

  // 下载草稿为 JSON(未保存的看板也可留档)
  const downloadDraftJson = () => {
    if (!draft) return
    const blob = new Blob([JSON.stringify(draft, null, 2)], { type: 'application/json;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `dashboard-draft-${Date.now()}.json`
    a.click()
    URL.revokeObjectURL(url)
    getMessage().success('草稿已导出为 JSON')
  }

  return (
    <div className="space-y-4">
      {/* —— 提示词输入区 —— */}
      <Card className="relative overflow-hidden border-violet-500/20">
        {/* 顶部渐变高光，生成中做呼吸动效 */}
        <div
          className={`pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-violet-500/70 to-transparent ${
            generating ? 'animate-breathing' : ''
          }`}
        />
        <div className="pointer-events-none absolute -right-16 -top-16 h-40 w-40 rounded-full bg-violet-500/10 blur-3xl" />
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <Sparkles className="h-4 w-4 text-violet-500" />
            用一句话描述你想看的数据
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            onKeyDown={(e) => {
              if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') handleGenerate()
            }}
            rows={3}
            maxLength={1000}
            placeholder="例如：看最近 30 天各优先级工单的处理量趋势，再加一张按状态分布的饼图，以及资产总数"
            className="w-full resize-none rounded-lg border border-border bg-background/60 px-3 py-2.5 text-sm outline-none transition-all duration-300 placeholder:text-muted-foreground/60 focus:border-violet-500/60 focus:shadow-[0_0_0_3px_rgba(139,92,246,0.12)]"
          />

          {suggestions.length > 0 && !draft && (
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="flex items-center gap-1 text-xs text-muted-foreground">
                <Lightbulb className="h-3.5 w-3.5" />
                试试：
              </span>
              {suggestions.slice(0, 5).map((s) => (
                <button
                  key={s.dataset}
                  onClick={() => setPrompt(s.prompt)}
                  className="rounded-full border border-border px-2.5 py-1 text-xs text-muted-foreground transition-all duration-300 hover:-translate-y-0.5 hover:border-violet-500/50 hover:text-foreground"
                >
                  {s.label}
                </button>
              ))}
            </div>
          )}

          <div className="flex items-center justify-between gap-3">
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <ShieldCheck className="h-3.5 w-3.5 shrink-0 text-blue-500" />
              AI 只能在已注册的数据集字段内组图，不会生成 SQL，也看不到你的业务数据
            </p>
            <Button onClick={handleGenerate} disabled={generating || !prompt.trim()}>
              {generating ? (
                <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />
              ) : (
                <Wand2 className="mr-1.5 h-4 w-4" />
              )}
              {generating ? '生成中…' : '生成看板'}
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* —— 生成中骨架 —— */}
      {generating && (
        <div className="grid gap-4 md:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <Card key={i} className="animate-fade-in-up" style={{ animationDelay: `${i * 60}ms` }}>
              <CardHeader className="pb-2">
                <div className="h-4 w-32 animate-pulse rounded bg-muted" />
              </CardHeader>
              <CardContent>
                <div className="h-[220px] animate-pulse rounded-lg bg-muted/60" />
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* —— 草稿预览 —— */}
      {draft && !generating && (
        <div className="space-y-4">
          <Card className="border-violet-500/20 bg-violet-500/[0.03]">
            <CardContent className="space-y-3 py-4">
              <div className="flex flex-wrap items-center gap-3">
                <div className="min-w-[220px] flex-1">
                  <label className="mb-1.5 block text-xs text-muted-foreground">看板名称</label>
                  <Input value={name} onChange={(e) => setName(e.target.value)} />
                </div>
                <div className="flex items-end gap-2 pb-[1px]">
                  <Button variant="outline" onClick={downloadDraftJson} disabled={!draft}>
                    <Download className="mr-1.5 h-4 w-4" />
                    下载草稿
                  </Button>
                  <Button variant="outline" onClick={handleGenerate} disabled={generating}>
                    <RotateCcw className="mr-1.5 h-4 w-4" />
                    重新生成
                  </Button>
                  <Button onClick={handleSave} disabled={saving || widgets.length === 0}>
                    {saving ? (
                      <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />
                    ) : (
                      <Save className="mr-1.5 h-4 w-4" />
                    )}
                    保存为看板
                  </Button>
                </div>
              </div>

              {draft.explanation && (
                <p className="text-sm text-muted-foreground">{draft.explanation}</p>
              )}

              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="secondary">{widgets.length} 个图表</Badge>
                {previewing && (
                  <span className="flex items-center gap-1 text-xs text-muted-foreground">
                    <Loader2 className="h-3 w-3 animate-spin" />
                    正在取数预览…
                  </span>
                )}
              </div>

              {draft.warnings && draft.warnings.length > 0 && (
                <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3">
                  <div className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-amber-600 dark:text-amber-400">
                    <AlertTriangle className="h-3.5 w-3.5" />
                    平台已按数据集白名单自动修正 {draft.warnings.length} 处
                  </div>
                  <ul className="space-y-0.5 text-xs text-muted-foreground">
                    {draft.warnings.map((wn, i) => (
                      <li key={i}>· {wn}</li>
                    ))}
                  </ul>
                </div>
              )}
            </CardContent>
          </Card>

          <div className="grid gap-4 md:grid-cols-2">
            {widgets.map((w, i) => {
              const p = previews[w.id]
              return (
                <Card
                  key={w.id}
                  className="animate-fade-in-up transition-shadow duration-300 hover:shadow-md"
                  style={{ animationDelay: `${i * 60}ms` }}
                >
                  <CardHeader className="flex-row items-center justify-between space-y-0 pb-2">
                    <CardTitle className="text-sm font-medium">{w.title}</CardTitle>
                    <Badge variant="secondary" className="font-normal">
                      {w.query.dataset}
                    </Badge>
                  </CardHeader>
                  <CardContent>
                    {p?.error ? (
                      <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                        {p.error}
                      </div>
                    ) : p?.data ? (
                      <ReportRenderer type={w.type} data={p.data} height={240} />
                    ) : (
                      <div className="h-[240px] animate-pulse rounded-lg bg-muted/60" />
                    )}
                  </CardContent>
                </Card>
              )
            })}
          </div>
        </div>
      )}

      {/* —— 空态 —— */}
      {!draft && !generating && (
        <Card className="border-dashed">
          <CardContent className="flex flex-col items-center justify-center py-14 text-center">
            <Sparkles className="mb-3 h-10 w-10 text-violet-500/40" />
            <p className="text-sm text-muted-foreground">
              描述需求后点击「生成看板」，AI 会在已注册数据集内挑维度、指标和图表类型
            </p>
            <p className="mt-1 text-xs text-muted-foreground/70">
              生成结果先预览，确认无误再保存 · ⌘/Ctrl + Enter 快速生成
            </p>
          </CardContent>
        </Card>
      )}
    </div>
  )
}
