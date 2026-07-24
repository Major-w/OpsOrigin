'use client'

import { useEffect, useState, useRef, useMemo } from 'react'
import { useRouter } from 'next/navigation'
import { useScriptStore, type Script } from '@/stores/script'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { DataList, type ViewMode, type Column, type FilterOption, type BulkAction } from '@/components/ui/data-list'
import { Code2, Terminal, Hash, Clock, GitBranch, Star, Download, Upload, Plus, Trash2, Copy, Pencil, Loader2, FileCode, CheckCircle, FileText } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { toast } from '@/stores/toast'

const SCRIPT_TYPES = [
  { value: 'shell', label: 'Shell', color: 'text-emerald-500' },
  { value: 'python', label: 'Python', color: 'text-blue-500' },
  { value: 'bat', label: 'Batch', color: 'text-yellow-500' },
]

const TYPE_COLORS: Record<string, string> = {
  shell: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/20',
  python: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20',
  bat: 'bg-yellow-500/10 text-yellow-600 dark:text-yellow-400 border-yellow-500/20',
}

const STARRED_KEY = 'opsnexus_starred_scripts'

function loadStarred(): Set<string> {
  try {
    const raw = localStorage.getItem(STARRED_KEY)
    if (!raw) return new Set()
    const arr = JSON.parse(raw)
    return Array.isArray(arr) ? new Set(arr.filter((x: any) => typeof x === 'string')) : new Set()
  } catch {
    return new Set()
  }
}

function saveStarred(ids: Set<string>) {
  try {
    localStorage.setItem(STARRED_KEY, JSON.stringify(Array.from(ids)))
  } catch {}
}

export default function ScriptsPage() {
  const router = useRouter()
  const { scripts, fetch, remove, duplicate } = useScriptStore()
  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [starredIds, setStarredIds] = useState<Set<string>>(new Set())
  const [importing, setImporting] = useState(false)
  const importInputRef = useRef<HTMLInputElement | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<Script | null>(null)

  useEffect(() => { fetch() }, [])

  useEffect(() => { setStarredIds(loadStarred()) }, [])

  const toggleStar = (id: string) => {
    setStarredIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      saveStarred(next)
      return next
    })
  }

  const typeLabel = (t: string) => SCRIPT_TYPES.find(st => st.value === t)?.label || t

  // 统计卡片数据
  const stats = useMemo(() => {
    const shellCount = scripts.filter(s => s.type === 'shell').length
    const pythonCount = scripts.filter(s => s.type === 'python').length
    const otherCount = scripts.length - shellCount - pythonCount
    const starredCount = scripts.filter(s => starredIds.has(s.id)).length
    return { total: scripts.length, shell: shellCount, python: pythonCount, other: otherCount, starred: starredCount }
  }, [scripts, starredIds])

  // DataList 列定义
  const columns: Column<Script>[] = [
    { key: 'name', label: '脚本名称', sortable: true, filterable: true },
    { key: 'type', label: '类型', sortable: true, filterable: true },
    { key: 'timeout', label: '超时', sortable: true },
    { key: 'input_params', label: '入参数' },
    { key: 'version', label: '版本', sortable: true },
    { key: 'updated_at', label: '更新时间', sortable: true },
  ]

  // 筛选选项
  const filters: FilterOption[] = [
    {
      key: 'type', label: '类型', type: 'select',
      options: SCRIPT_TYPES.map(t => ({ label: t.label, value: t.value })),
    },
  ]

  // 批量操作
  const bulkActions: BulkAction[] = [
    {
      key: 'delete',
      label: '批量删除',
      icon: <Trash2 className="h-3 w-3 mr-1" />,
      variant: 'destructive',
      confirmMessage: '确认删除选中的脚本？此操作不可恢复。',
      onClick: async (items) => {
        for (const item of items) { await remove((item as Script).id) }
      },
    },
    {
      key: 'export',
      label: '批量导出',
      icon: <Download className="h-3 w-3 mr-1" />,
      onClick: (items) => {
        const list = items as Script[]
        if (list.length === 0) {
          toast.warning('没有可导出的脚本')
          return
        }
        const exportData = list.map(s => ({
          name: s.name, type: s.type, content: s.content, description: s.description,
          timeout: s.timeout, input_params: s.input_params, output_params: s.output_params,
          run_as: s.run_as, labels: s.labels,
        }))
        const blob = new Blob([JSON.stringify(exportData, null, 2)], { type: 'application/json' })
        const url = URL.createObjectURL(blob)
        const d = new Date()
        const stamp = `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}`
        const a = document.createElement('a')
        a.href = url
        a.download = `scripts-export-${stamp}.json`
        document.body.appendChild(a)
        a.click()
        document.body.removeChild(a)
        URL.revokeObjectURL(url)
        toast.success(`已导出 ${exportData.length} 个脚本`)
      },
    },
  ]

  // 导入功能
  const handleImportClick = () => {
    importInputRef.current?.click()
  }

  const handleImportFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (e.target) e.target.value = ''
    if (!file) return
    setImporting(true)
    try {
      const text = await file.text()
      let arr: any[]
      try {
        arr = JSON.parse(text)
        if (!Array.isArray(arr)) throw new Error('导入文件不是有效的脚本数组')
      } catch (err: any) {
        toast.error(`导入失败: ${err.message}`)
        return
      }
      const existingNames = new Set(scripts.map(s => s.name))
      let success = 0
      const errors: string[] = []
      // 复用 store.create
      const { create } = useScriptStore.getState()
      for (let i = 0; i < arr.length; i++) {
        const item = arr[i] || {}
        let name = String(item.name || '').trim()
        if (!name) {
          errors.push(`第 ${i + 1} 条: 缺少 name 字段`)
          continue
        }
        if (existingNames.has(name)) name = `${name} (导入)`
        const payload = {
          name, type: item.type || 'shell', content: item.content || '',
          description: item.description || '', timeout: Number(item.timeout) || 300,
          input_params: item.input_params, output_params: item.output_params,
          run_as: item.run_as, labels: item.labels,
        }
        const ok = await create(payload)
        if (ok) { success++; existingNames.add(name) }
        else errors.push(`第 ${i + 1} 条 (${name}): 创建失败`)
      }
      if (success > 0) toast.success(`成功导入 ${success} 个脚本`)
      if (errors.length > 0) toast.error(`导入错误 ${errors.length} 条: ${errors[0]}`)
    } catch (err: any) {
      toast.error(`导入失败: ${err.message}`)
    } finally {
      setImporting(false)
    }
  }

  // 行渲染（列表视图）
  const renderListItem = (sc: Script) => (
    <>
      <div className="flex items-center gap-2 min-w-0 flex-1">
        <button
          onClick={(e) => { e.stopPropagation(); toggleStar(sc.id) }}
          className="shrink-0 p-0.5 rounded hover:bg-gray-100 dark:hover:bg-[rgba(255,255,255,0.06)]"
          title={starredIds.has(sc.id) ? '取消收藏' : '收藏'}
        >
          <Star className={cn("h-3.5 w-3.5", starredIds.has(sc.id) ? "fill-amber-400 text-amber-400" : "text-muted-foreground/40")} />
        </button>
        <Code2 className={cn("h-4 w-4 shrink-0", SCRIPT_TYPES.find(t => t.value === sc.type)?.color || 'text-muted-foreground')} />
        <span className="text-sm font-medium truncate cursor-pointer hover:text-emerald-500"
          onClick={(e) => { e.stopPropagation(); router.push(`/scripts/${sc.id}`) }}>
          {sc.name}
        </span>
      </div>
      <div className="w-20 shrink-0">
        <Badge variant="outline" className={cn("text-[10px] px-1.5 py-0", TYPE_COLORS[sc.type])}>{typeLabel(sc.type)}</Badge>
      </div>
      <div className="w-16 shrink-0 text-xs text-muted-foreground flex items-center gap-0.5">
        <Clock className="h-2.5 w-2.5" />{sc.timeout}s
      </div>
      <div className="w-16 shrink-0 text-xs text-muted-foreground flex items-center gap-0.5">
        <Hash className="h-2.5 w-2.5" />{sc.input_params?.length || 0}
      </div>
      <div className="w-16 shrink-0 text-xs text-muted-foreground flex items-center gap-0.5">
        <GitBranch className="h-2.5 w-2.5" />v{sc.version}
      </div>
      <div className="w-28 shrink-0 text-xs text-muted-foreground/70 hidden md:block">{formatTime(sc.updated_at)}</div>
      <div className="w-28 shrink-0 flex justify-end gap-1">
        <Button variant="ghost" size="icon" className="h-6 w-6" onClick={(e) => { e.stopPropagation(); router.push(`/scripts/${sc.id}`) }} title="编辑">
          <Pencil className="h-3 w-3" />
        </Button>
        <Button variant="ghost" size="icon" className="h-6 w-6" onClick={(e) => { e.stopPropagation(); duplicate(sc.id) }} title="复制">
          <Copy className="h-3 w-3" />
        </Button>
        <Button variant="ghost" size="icon" className="h-6 w-6 text-destructive" onClick={(e) => { e.stopPropagation(); setConfirmDelete(sc) }} title="删除">
          <Trash2 className="h-3 w-3" />
        </Button>
      </div>
    </>
  )

  // 网格视图卡片
  const renderGridItem = (sc: Script) => (
    <Card key={sc.id} className="hover:border-emerald-500/20 transition-colors cursor-pointer" onClick={() => router.push(`/scripts/${sc.id}`)}>
      <CardHeader className="py-3 pb-2">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 min-w-0">
            <button
              onClick={(e) => { e.stopPropagation(); toggleStar(sc.id) }}
              className="shrink-0 p-0.5 rounded hover:bg-gray-100 dark:hover:bg-[rgba(255,255,255,0.06)]"
            >
              <Star className={cn("h-3.5 w-3.5", starredIds.has(sc.id) ? "fill-amber-400 text-amber-400" : "text-muted-foreground/40")} />
            </button>
            <Code2 className={cn("h-4 w-4 shrink-0", SCRIPT_TYPES.find(t => t.value === sc.type)?.color || 'text-muted-foreground')} />
            <span className="text-sm font-medium truncate">{sc.name}</span>
          </div>
          <div className="flex gap-0.5 shrink-0" onClick={(e) => e.stopPropagation()}>
            <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => duplicate(sc.id)} title="复制">
              <Copy className="h-3 w-3" />
            </Button>
            <Button variant="ghost" size="icon" className="h-6 w-6 text-destructive" onClick={() => setConfirmDelete(sc)} title="删除">
              <Trash2 className="h-3 w-3" />
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-2 pt-0">
        <div className="flex items-center gap-2">
          <Badge variant="outline" className={cn("text-[10px]", TYPE_COLORS[sc.type])}>{typeLabel(sc.type)}</Badge>
          <span className="text-[10px] text-muted-foreground flex items-center gap-0.5"><Clock className="h-2.5 w-2.5" />{sc.timeout}s</span>
          <span className="text-[10px] text-muted-foreground flex items-center gap-0.5"><Hash className="h-2.5 w-2.5" />{sc.input_params?.length || 0}</span>
          <span className="text-[10px] text-muted-foreground flex items-center gap-0.5"><GitBranch className="h-2.5 w-2.5" />v{sc.version}</span>
        </div>
        {sc.description && <p className="text-xs text-muted-foreground line-clamp-2">{sc.description}</p>}
        <pre className="text-[10px] text-muted-foreground font-mono line-clamp-3 bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] rounded p-2">{sc.content}</pre>
        <div className="flex items-center gap-2 text-[10px] text-muted-foreground/70 pt-1 border-t">
          <span>更新 {formatTime(sc.updated_at)}</span>
        </div>
      </CardContent>
    </Card>
  )

  // 确认删除对话框（简单内联）
  const handleConfirmDelete = async () => {
    if (!confirmDelete) return
    const ok = await remove(confirmDelete.id)
    if (ok) setConfirmDelete(null)
  }

  return (
    <div className="p-6 space-y-4">
      {/* 隐藏的文件输入用于导入 */}
      <input
        ref={importInputRef}
        type="file"
        accept=".json"
        className="hidden"
        onChange={handleImportFile}
      />

      {/* 标题行 */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">脚本管理</h1>
          <p className="text-sm text-muted-foreground">运维脚本管理、参数化定义与执行历史</p>
        </div>
      </div>

      {/* 统计卡片 */}
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3">
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><FileCode className="h-3 w-3 inline mr-1" />脚本总数</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Terminal className="h-3 w-3 inline mr-1" />Shell</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{stats.shell}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Code2 className="h-3 w-3 inline mr-1" />Python</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-blue-500">{stats.python}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><FileText className="h-3 w-3 inline mr-1" />其他类型</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-amber-500">{stats.other}</span></CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Star className="h-3 w-3 inline mr-1" />已收藏</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-amber-500">{stats.starred}</span></CardContent>
        </Card>
      </div>

      {/* 列表 */}
      <DataList
        data={scripts}
        columns={columns}
        viewMode={viewMode}
        filters={filters}
        bulkActions={bulkActions}
        listView={renderListItem}
        gridView={renderGridItem}
        emptyMessage="暂无脚本，点击「新建脚本」创建"
        searchPlaceholder="搜索脚本名称/类型/描述..."
        onRefresh={() => fetch()}
        customHeader={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={handleImportClick} disabled={importing}>
              {importing ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Upload className="h-4 w-4 mr-1" />}
              导入
            </Button>
            <Button variant="outline" size="sm" onClick={() => {
              if (scripts.length === 0) { toast.warning('没有可导出的脚本'); return }
              const exportData = scripts.map(s => ({
                name: s.name, type: s.type, content: s.content, description: s.description,
                timeout: s.timeout, input_params: s.input_params, output_params: s.output_params,
                run_as: s.run_as, labels: s.labels,
              }))
              const blob = new Blob([JSON.stringify(exportData, null, 2)], { type: 'application/json' })
              const url = URL.createObjectURL(blob)
              const d = new Date()
              const stamp = `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}`
              const a = document.createElement('a')
              a.href = url
              a.download = `scripts-export-${stamp}.json`
              document.body.appendChild(a)
              a.click()
              document.body.removeChild(a)
              URL.revokeObjectURL(url)
              toast.success(`已导出 ${exportData.length} 个脚本`)
            }}>
              <Download className="h-4 w-4 mr-1" />导出
            </Button>
            <Button size="sm" onClick={() => router.push('/scripts/new')}>
              <Plus className="h-4 w-4 mr-1" />新建脚本
            </Button>
          </div>
        }
      />

      {/* 确认删除对话框（简单内联） */}
      {confirmDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setConfirmDelete(null)} />
          <div className="relative z-50 w-full max-w-md bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg">
            <h3 className="text-lg font-semibold mb-2 flex items-center gap-2">
              <Trash2 className="h-5 w-5 text-destructive" />确认删除脚本
            </h3>
            <p className="text-sm text-muted-foreground mb-4">
              确认删除脚本 <span className="font-medium text-foreground">{confirmDelete.name}</span> ({typeLabel(confirmDelete.type)})? 此操作不可恢复，相关执行历史和版本记录将一并删除。
            </p>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setConfirmDelete(null)}>取消</Button>
              <Button variant="destructive" onClick={handleConfirmDelete}>
                <Trash2 className="h-3 w-3 mr-1" />确认删除
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
