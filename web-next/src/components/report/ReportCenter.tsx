'use client'

// 报表中心（可复用）：自助看板 + 数据探索 + AI 生成。
// 既作为全局「报表中心」页面主体，也可被各业务模块作为「自定义报表」入口内嵌，
// 通过 modules 限定可选数据集范围（默认可一键切回全部数据集）。
import { useEffect, useMemo, useState } from 'react'
import {
  BarChart3,
  Plus,
  Play,
  Trash2,
  RefreshCw,
  LayoutDashboard,
  Compass,
  Save,
  X,
  UserRound,
  Sparkles,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Dialog } from '@/components/ui/dialog'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ReportRenderer } from '@/components/report/ReportRenderer'
import { AIWorkspace } from '@/components/report/AIWorkspace'
import { toast } from '@/stores/toast'
import {
  useReportStore,
  type Dataset,
  type Query,
  type Result,
  type Widget,
  type Granularity,
} from '@/stores/report'

const WIDGET_TYPES = [
  { value: 'line', label: '折线图' },
  { value: 'bar', label: '柱状图' },
  { value: 'pie', label: '饼图' },
  { value: 'number', label: '数值卡' },
  { value: 'table', label: '表格' },
]

const GRANULARITIES: { value: Granularity; label: string }[] = [
  { value: '', label: '不按时间聚合' },
  { value: 'hour', label: '按小时' },
  { value: 'day', label: '按天' },
  { value: 'week', label: '按周' },
  { value: 'month', label: '按月' },
  { value: 'quarter', label: '按季度' },
  { value: 'year', label: '按年' },
]

const TIME_RANGES = [
  { value: '', label: '全部时间' },
  { value: '24h', label: '最近 24 小时' },
  { value: '7d', label: '最近 7 天' },
  { value: '30d', label: '最近 30 天' },
  { value: '90d', label: '最近 90 天' },
]

function newWidgetID() {
  return 'w_' + Math.random().toString(36).slice(2, 10)
}

export interface ReportCenterProps {
  /** 页头标题 */
  title?: string
  /** 页头副标题；未指定时按数据集数量自动生成 */
  description?: string
  /** 限定可选数据集所属模块（如 ['itsm']）；用户可一键切回全部 */
  modules?: string[]
  /** 模块限定时的提示文案 */
  scopeLabel?: string
}

export function ReportCenter({ title = '报表中心', description, modules, scopeLabel }: ReportCenterProps) {
  const {
    datasets,
    dashboards,
    current,
    rendered,
    rendering,
    loadDatasets,
    listDashboards,
    getDashboard,
    renderDashboard,
    createDashboard,
    updateDashboard,
    deleteDashboard,
    runQuery,
    closeDashboard,
  } = useReportStore()

  const [tab, setTab] = useState<'dashboards' | 'explore' | 'ai'>('dashboards')
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDesc, setNewDesc] = useState('')

  // —— 探索器状态 ——
  const [dsName, setDsName] = useState('')
  const [dims, setDims] = useState<string[]>([])
  const [mets, setMets] = useState<string[]>([])
  const [gran, setGran] = useState<Granularity>('')
  const [last, setLast] = useState('')
  const [chartType, setChartType] = useState('table')
  const [limit, setLimit] = useState(200)
  const [preview, setPreview] = useState<Result | null>(null)
  const [running, setRunning] = useState(false)
  const [addOpen, setAddOpen] = useState(false)
  const [widgetTitle, setWidgetTitle] = useState('')
  const [targetDash, setTargetDash] = useState('')
  const [scoped, setScoped] = useState<boolean>(!!modules?.length)
  const [pendingDashboard, setPendingDashboard] = useState<string | null>(null)

  useEffect(() => {
    loadDatasets()
    listDashboards().catch((e) => toast.error('加载看板失败: ' + (e as Error).message))
  }, [loadDatasets, listDashboards])

  // 按模块过滤可选数据集（仅影响探索器下拉，不影响已有看板）
  const visibleDatasets = useMemo(
    () => (scoped && modules?.length ? datasets.filter((d) => modules.includes(d.module)) : datasets),
    [datasets, modules, scoped]
  )

  const ds: Dataset | undefined = useMemo(
    () => datasets.find((d) => d.name === dsName),
    [datasets, dsName]
  )

  // 选中数据集变化时重置字段选择
  useEffect(() => {
    setDims([])
    setMets([])
    setGran('')
    setPreview(null)
  }, [dsName])

  // 切换范围后若当前数据集已不在可选范围内则清空
  useEffect(() => {
    if (dsName && !visibleDatasets.some((d) => d.name === dsName)) setDsName('')
  }, [visibleDatasets, dsName])

  const buildQuery = (): Query => ({
    dataset: dsName,
    dimensions: dims,
    metrics: mets,
    granularity: gran,
    timeRange: last ? { last } : undefined,
    limit,
  })

  const handleRun = async () => {
    if (!dsName) return toast.error('请先选择数据集')
    if (mets.length === 0) return toast.error('请至少选择一个指标')
    setRunning(true)
    try {
      const res = await runQuery(buildQuery())
      setPreview(res)
    } catch (e) {
      toast.error('查询失败: ' + (e as Error).message)
    } finally {
      setRunning(false)
    }
  }

  const handleCreate = async () => {
    if (!newName.trim()) return toast.error('请填写看板名称')
    try {
      await createDashboard({ name: newName.trim(), description: newDesc.trim(), widgets: [] })
      setCreateOpen(false)
      setNewName('')
      setNewDesc('')
    } catch (e) {
      toast.error('创建失败: ' + (e as Error).message)
    }
  }

  const handleAddWidget = async () => {
    const dashID = targetDash || current?.id
    if (!dashID) return toast.error('请选择目标看板')
    const dash = dashID === current?.id ? current : null
    try {
      // 需要完整看板对象才能追加 widget
      const base = dash ?? (await (async () => {
        await getDashboard(dashID)
        return useReportStore.getState().current
      })())
      if (!base) return toast.error('目标看板不存在')

      const w: Widget = {
        id: newWidgetID(),
        title: widgetTitle.trim() || ds?.label || '新图表',
        type: chartType,
        query: buildQuery(),
        layout: { x: 0, y: (base.widgets?.length || 0) * 4, w: 6, h: 4 },
      }
      await updateDashboard(dashID, {
        name: base.name,
        description: base.description,
        widgets: [...(base.widgets || []), w],
      })
      setAddOpen(false)
      setWidgetTitle('')
      setTab('dashboards')
    } catch (e) {
      toast.error('加入看板失败: ' + (e as Error).message)
    }
  }

  const handleRemoveWidget = async (wid: string) => {
    if (!current) return
    try {
      await updateDashboard(current.id, {
        name: current.name,
        description: current.description,
        widgets: (current.widgets || []).filter((w) => w.id !== wid),
      })
    } catch (e) {
      toast.error('删除图表失败: ' + (e as Error).message)
    }
  }

  const toggle = (arr: string[], v: string, setter: (a: string[]) => void) => {
    setter(arr.includes(v) ? arr.filter((x) => x !== v) : [...arr, v])
  }

  return (
    <div className="space-y-6">
      {/* 页头 */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
            <BarChart3 className="h-6 w-6 text-emerald-500" />
            {title}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {description || `跨模块统一数据集查询与自助看板，共 ${visibleDatasets.length} 个数据集`}
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => { loadDatasets(); listDashboards() }}>
            <RefreshCw className="mr-1.5 h-4 w-4" />
            刷新
          </Button>
          <Button onClick={() => setCreateOpen(true)}>
            <Plus className="mr-1.5 h-4 w-4" />
            新建看板
          </Button>
        </div>
      </div>

      {/* Tab 切换 */}
      <div className="inline-flex rounded-lg border border-border bg-muted/40 p-1">
        <button
          onClick={() => setTab('dashboards')}
          className={`flex items-center gap-1.5 rounded-md px-4 py-1.5 text-sm transition-all ${
            tab === 'dashboards' ? 'bg-background shadow-sm font-medium' : 'text-muted-foreground hover:text-foreground'
          }`}
        >
          <LayoutDashboard className="h-4 w-4" />
          我的看板
        </button>
        <button
          onClick={() => setTab('explore')}
          className={`flex items-center gap-1.5 rounded-md px-4 py-1.5 text-sm transition-all ${
            tab === 'explore' ? 'bg-background shadow-sm font-medium' : 'text-muted-foreground hover:text-foreground'
          }`}
        >
          <Compass className="h-4 w-4" />
          数据探索
        </button>
        <button
          onClick={() => setTab('ai')}
          className={`flex items-center gap-1.5 rounded-md px-4 py-1.5 text-sm transition-all ${
            tab === 'ai' ? 'bg-background shadow-sm font-medium' : 'text-muted-foreground hover:text-foreground'
          }`}
        >
          <Sparkles className={`h-4 w-4 ${tab === 'ai' ? 'text-violet-500' : ''}`} />
          AI 生成
        </button>
      </div>

      {/* —— AI 定义工作空间：自然语言 → 受约束的看板草稿 —— */}
      {tab === 'ai' && (
        <AIWorkspace
          onSaved={() => {
            setTab('dashboards')
          }}
        />
      )}

      {/* —— 看板 —— */}
      {tab === 'dashboards' && !current && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {dashboards.map((d) => (
            <Card
              key={d.id}
              className="group cursor-pointer transition-all duration-300 hover:-translate-y-0.5 hover:shadow-lg"
              onClick={() => getDashboard(d.id)}
            >
              <CardHeader className="pb-2">
                <div className="flex items-start justify-between">
                  <CardTitle className="text-base">{d.name}</CardTitle>
                  <Badge variant="secondary">{d.widgetCount} 图表</Badge>
                </div>
              </CardHeader>
              <CardContent>
                <p className="line-clamp-2 text-sm text-muted-foreground">
                  {d.description || '暂无描述'}
                </p>
                {(d.createdByName || d.createdBy) && (
                  <div className="mt-2 flex items-center gap-1.5 text-xs text-muted-foreground">
                    <UserRound className="h-3.5 w-3.5 shrink-0 opacity-70" />
                    <span className="truncate">
                      {d.createdByName || d.createdBy} 创建
                    </span>
                  </div>
                )}
                <div className="mt-3 flex items-center justify-between">
                  <span className="text-xs text-muted-foreground">
                    {new Date(d.updatedAt).toLocaleString('zh-CN')}
                  </span>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="opacity-0 transition-opacity group-hover:opacity-100"
                    onClick={(e) => {
                      e.stopPropagation()
                      setPendingDashboard(d.id)
                    }}
                  >
                    <Trash2 className="h-4 w-4 text-destructive" />
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
          {dashboards.length === 0 && (
            <Card className="col-span-full border-dashed">
              <CardContent className="flex flex-col items-center justify-center py-14 text-center">
                <LayoutDashboard className="mb-3 h-10 w-10 text-muted-foreground/40" />
                <p className="text-sm text-muted-foreground">
                  还没有看板，可以手动搭建，也可以直接描述需求让 AI 生成
                </p>
                <div className="mt-4 flex gap-2">
                  <Button variant="outline" onClick={() => setTab('explore')}>
                    <Compass className="mr-1.5 h-4 w-4" />
                    手动探索
                  </Button>
                  <Button onClick={() => setTab('ai')}>
                    <Sparkles className="mr-1.5 h-4 w-4" />
                    让 AI 生成
                  </Button>
                </div>
              </CardContent>
            </Card>
          )}
        </div>
      )}

      {/* —— 看板详情 —— */}
      {tab === 'dashboards' && current && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-lg font-medium">{current.name}</h2>
              {current.description && (
                <p className="text-sm text-muted-foreground">{current.description}</p>
              )}
            </div>
            <div className="flex gap-2">
              <Button variant="outline" size="sm" onClick={() => renderDashboard(current.id)}>
                <RefreshCw className={`mr-1.5 h-4 w-4 ${rendering ? 'animate-spin' : ''}`} />
                刷新数据
              </Button>
              <Button variant="ghost" size="sm" onClick={closeDashboard}>
                <X className="mr-1.5 h-4 w-4" />
                返回列表
              </Button>
            </div>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            {rendered.map((w) => (
              <Card key={w.id} className="transition-shadow duration-300 hover:shadow-md">
                <CardHeader className="flex-row items-center justify-between space-y-0 pb-2">
                  <CardTitle className="text-sm font-medium">{w.title}</CardTitle>
                  <Button variant="ghost" size="sm" onClick={() => handleRemoveWidget(w.id)}>
                    <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
                  </Button>
                </CardHeader>
                <CardContent>
                  {w.error ? (
                    <div className="rounded-md bg-destructive/10 p-3 text-sm text-destructive">
                      {w.error}
                    </div>
                  ) : (
                    <ReportRenderer type={w.type} data={w.data} height={260} />
                  )}
                </CardContent>
              </Card>
            ))}
            {rendered.length === 0 && !rendering && (
              <Card className="col-span-full border-dashed">
                <CardContent className="py-12 text-center text-sm text-muted-foreground">
                  这个看板还没有图表，去「数据探索」添加一个
                </CardContent>
              </Card>
            )}
          </div>
        </div>
      )}

      {/* —— 数据探索 —— */}
      {tab === 'explore' && (
        <div className="grid gap-4 lg:grid-cols-[320px_1fr]">
          {/* 左：查询构建器 */}
          <Card className="h-fit">
            <CardHeader className="pb-3">
              <CardTitle className="text-sm">查询条件</CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div>
                <div className="mb-1.5 flex items-center justify-between">
                  <label className="text-xs text-muted-foreground">数据集</label>
                  {!!modules?.length && (
                    <button
                      className="text-[11px] text-emerald-600 hover:underline dark:text-emerald-400"
                      onClick={() => setScoped((s) => !s)}
                    >
                      {scoped ? '显示全部数据集' : `仅看${scopeLabel || '本模块'}`}
                    </button>
                  )}
                </div>
                <Select
                  value={dsName}
                  onChange={(e) => setDsName(e.target.value)}
                  options={[
                    { value: '', label: '请选择…' },
                    ...visibleDatasets.map((d) => ({ value: d.name, label: `${d.label} (${d.module})` })),
                  ]}
                />
                {ds?.description && (
                  <p className="mt-1.5 text-xs text-muted-foreground">{ds.description}</p>
                )}
              </div>

              {ds && (
                <>
                  <div>
                    <label className="mb-1.5 block text-xs text-muted-foreground">
                      分组维度（可多选）
                    </label>
                    <div className="flex flex-wrap gap-1.5">
                      {ds.dimensions.filter((d) => d.groupable).map((d) => (
                        <button
                          key={d.name}
                          onClick={() => toggle(dims, d.name, setDims)}
                          className={`rounded-full border px-2.5 py-1 text-xs transition-all ${
                            dims.includes(d.name)
                              ? 'border-emerald-500 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                              : 'border-border hover:border-emerald-500/50'
                          }`}
                        >
                          {d.label}
                        </button>
                      ))}
                    </div>
                  </div>

                  <div>
                    <label className="mb-1.5 block text-xs text-muted-foreground">
                      指标（至少一个）
                    </label>
                    <div className="flex flex-wrap gap-1.5">
                      {ds.metrics.map((m) => (
                        <button
                          key={m.name}
                          onClick={() => toggle(mets, m.name, setMets)}
                          className={`rounded-full border px-2.5 py-1 text-xs transition-all ${
                            mets.includes(m.name)
                              ? 'border-cyan-500 bg-cyan-500/10 text-cyan-600 dark:text-cyan-400'
                              : 'border-border hover:border-cyan-500/50'
                          }`}
                        >
                          {m.label}
                        </button>
                      ))}
                    </div>
                  </div>

                  {ds.timeField && (
                    <>
                      <div>
                        <label className="mb-1.5 block text-xs text-muted-foreground">时间范围</label>
                        <Select
                          value={last}
                          onChange={(e) => setLast(e.target.value)}
                          options={TIME_RANGES}
                        />
                      </div>
                      <div>
                        <label className="mb-1.5 block text-xs text-muted-foreground">时间粒度</label>
                        <Select
                          value={gran}
                          onChange={(e) => setGran(e.target.value as Granularity)}
                          options={GRANULARITIES}
                        />
                      </div>
                    </>
                  )}

                  <div>
                    <label className="mb-1.5 block text-xs text-muted-foreground">返回行数上限</label>
                    <Input
                      type="number"
                      value={limit}
                      onChange={(e) => setLimit(Number(e.target.value) || 200)}
                    />
                  </div>

                  <div className="flex gap-2 pt-1">
                    <Button className="flex-1" onClick={handleRun} disabled={running}>
                      <Play className={`mr-1.5 h-4 w-4 ${running ? 'animate-pulse' : ''}`} />
                      {running ? '查询中…' : '运行查询'}
                    </Button>
                  </div>
                </>
              )}
            </CardContent>
          </Card>

          {/* 右：结果预览 */}
          <Card>
            <CardHeader className="flex-row items-center justify-between space-y-0 pb-3">
              <CardTitle className="text-sm">
                结果预览
                {preview && (
                  <span className="ml-2 font-normal text-muted-foreground">
                    {preview.rowCount} 行 · {preview.elapsedMs}ms
                    {preview.truncated && ' · 已截断'}
                  </span>
                )}
              </CardTitle>
              <div className="flex items-center gap-2">
                <Select
                  className="h-8 w-32"
                  value={chartType}
                  onChange={(e) => setChartType(e.target.value)}
                  options={WIDGET_TYPES}
                />
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!preview}
                  onClick={() => {
                    setWidgetTitle(ds?.label || '')
                    setTargetDash(current?.id || dashboards[0]?.id || '')
                    setAddOpen(true)
                  }}
                >
                  <Save className="mr-1.5 h-4 w-4" />
                  加入看板
                </Button>
              </div>
            </CardHeader>
            <CardContent>
              {preview ? (
                <ReportRenderer type={chartType} data={preview} height={340} />
              ) : (
                <div className="flex h-[340px] flex-col items-center justify-center text-center">
                  <Compass className="mb-3 h-10 w-10 text-muted-foreground/40" />
                  <p className="text-sm text-muted-foreground">
                    选择数据集与指标后点击「运行查询」
                  </p>
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {/* 新建看板 */}
      <Dialog open={createOpen} onClose={() => setCreateOpen(false)} title="新建看板">
        <div className="space-y-3">
          <div>
            <label className="mb-1.5 block text-xs text-muted-foreground">看板名称</label>
            <Input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="例如：工单运营周报" />
          </div>
          <div>
            <label className="mb-1.5 block text-xs text-muted-foreground">描述（可选）</label>
            <Input value={newDesc} onChange={(e) => setNewDesc(e.target.value)} placeholder="这个看板用来看什么" />
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={() => setCreateOpen(false)}>取消</Button>
            <Button onClick={handleCreate}>创建</Button>
          </div>
        </div>
      </Dialog>

      {/* 加入看板 */}
      <Dialog open={addOpen} onClose={() => setAddOpen(false)} title="加入看板">
        <div className="space-y-3">
          <div>
            <label className="mb-1.5 block text-xs text-muted-foreground">图表标题</label>
            <Input value={widgetTitle} onChange={(e) => setWidgetTitle(e.target.value)} placeholder="图表标题" />
          </div>
          <div>
            <label className="mb-1.5 block text-xs text-muted-foreground">目标看板</label>
            <Select
              value={targetDash}
              onChange={(e) => setTargetDash(e.target.value)}
              options={
                dashboards.length > 0
                  ? dashboards.map((d) => ({ value: d.id, label: d.name }))
                  : [{ value: '', label: '请先新建看板' }]
              }
            />
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="outline" onClick={() => setAddOpen(false)}>取消</Button>
            <Button onClick={handleAddWidget} disabled={dashboards.length === 0}>加入</Button>
          </div>
        </div>
      </Dialog>

      {/* 删除看板确认 */}
      <ConfirmDialog
        open={!!pendingDashboard}
        title="删除看板"
        description="确认删除该看板？此操作不可撤销。"
        confirmLabel="删除"
        onClose={() => setPendingDashboard(null)}
        onConfirm={async () => { if (pendingDashboard) { await deleteDashboard(pendingDashboard); setPendingDashboard(null) } }}
      />
    </div>
  )
}

export default ReportCenter
