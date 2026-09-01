
import { useEffect, useState, useRef, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useScriptStore, type Script } from '@/stores/script'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { DataList, type ViewMode, type Column, type FilterOption, type BulkAction } from '@/components/ui/data-list'
import { StatisticCardGroup, StatisticCard } from '@/components/ui/statistic-card'
import { Code2, Terminal, Hash, Clock, GitBranch, Star, Download, Upload as UploadIcon, Plus, Trash2, Copy, Pencil, Loader2, FileCode, CheckCircle, FileText } from 'lucide-react'
import { cn, formatTime, apiFetch } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { Button, Modal, Tag, Upload } from 'antd'
const SCRIPT_TYPES = [
  { value: 'shell', label: 'Shell', color: 'text-blue-500' },
  { value: 'python', label: 'Python', color: 'text-blue-500' },
  { value: 'bat', label: 'Batch', color: 'text-yellow-500' },
]

const TYPE_COLORS: Record<string, string> = {
  shell: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20',
  python: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20',
  bat: 'bg-yellow-500/10 text-yellow-600 dark:text-yellow-400 border-yellow-500/20',
}

const STARRED_KEY = 'opsorigin_starred_scripts'

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
  const navigate = useNavigate()
  const { scripts, loading, fetch, remove, duplicate } = useScriptStore()
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

  // 审批状态 badge 文案/样式
  const approvalMeta = (s: string | undefined): { label: string; cls: string } => {
    switch (s) {
      case 'draft': return { label: '待批准', cls: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/20' }
      case 'pending': return { label: '审批中', cls: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20' }
      case 'approved': return { label: '已批准', cls: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20' }
      case 'rejected': return { label: '已拒绝', cls: 'bg-red-500/10 text-red-600 dark:text-red-400 border-red-500/20' }
      default: return { label: '—', cls: '' }
    }
  }

  // 批准 AI 生成/待审批的脚本
  const handleApprove = async (sc: Script) => {
    try {
      await apiFetch(`/scripts/${sc.id}/approve`, { method: 'POST' })
      getMessage().success(`已批准脚本「${sc.name}」，现在可被 AI 与自动化调用`)
      fetch()
    } catch (e: any) {
      console.error('approve error:', e)
      getMessage().error(`批准失败: ${e.message || '未知错误'}`)
    }
  }

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
    { key: 'name', label: '脚本名称', sortable: true, filterable: true, width: 'flex-1 min-w-0 max-w-[240px] shrink-0' },
    { key: 'type', label: '类型', sortable: true, filterable: true, width: 'w-24 shrink-0' },
    { key: 'approval_status', label: '审批', width: 'w-20 shrink-0' },
    { key: 'timeout', label: '超时', sortable: true, width: 'w-20 shrink-0' },
    { key: 'input_params', label: '入参数', width: 'w-20 shrink-0' },
    { key: 'version', label: '版本', sortable: true, width: 'w-20 shrink-0' },
    { key: 'updated_at', label: '更新时间', sortable: true, width: 'w-40 shrink-0' },
    { key: '_actions', label: '操作', width: 'w-32 shrink-0' },
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
          getMessage().warning('没有可导出的脚本')
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
        getMessage().success(`已导出 ${exportData.length} 个脚本`)
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
        getMessage().error(`导入失败: ${err.message}`)
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
      if (success > 0) getMessage().success(`成功导入 ${success} 个脚本`)
      if (errors.length > 0) getMessage().error(`导入错误 ${errors.length} 条: ${errors[0]}`)
    } catch (err: any) {
      getMessage().error(`导入失败: ${err.message}`)
    } finally {
      setImporting(false)
    }
  }

  // 行渲染（列表视图）
  const renderListItem = (sc: Script) => (
    <>
      <div className="flex items-center gap-2 min-w-0 flex-1 max-w-[240px] shrink-0">
        <button
          onClick={(e) => { e.stopPropagation(); toggleStar(sc.id) }}
          className="shrink-0 p-0.5 rounded hover:bg-gray-100 dark:hover:bg-[rgba(255,255,255,0.06)]"
          title={starredIds.has(sc.id) ? '取消收藏' : '收藏'}
        >
          <Star className={cn("h-3.5 w-3.5", starredIds.has(sc.id) ? "fill-amber-400 text-amber-400" : "text-muted-foreground/40")} />
        </button>
        <Code2 className={cn("h-4 w-4 shrink-0", SCRIPT_TYPES.find(t => t.value === sc.type)?.color || 'text-muted-foreground')} />
        <span className="text-sm font-medium truncate cursor-pointer hover:text-blue-500"
          onClick={(e) => { e.stopPropagation(); navigate(`/scripts/${sc.id}`) }}>
          {sc.name}
        </span>
      </div>
      <div className="w-24 shrink-0">
        <Tag color="default" className={cn("text-[10px]", TYPE_COLORS[sc.type])}>{typeLabel(sc.type)}</Tag>
      </div>
      <div className="w-20 shrink-0">
        {sc.approval_status && sc.approval_status !== 'approved' && (
          <Tag color="default" className={cn("text-[10px]", approvalMeta(sc.approval_status).cls)}>
            {approvalMeta(sc.approval_status).label}
          </Tag>
        )}
      </div>
      <div className="w-20 shrink-0 text-xs text-muted-foreground flex items-center gap-0.5">
        <Clock className="h-2.5 w-2.5" />{sc.timeout}s
      </div>
      <div className="w-20 shrink-0 text-xs text-muted-foreground flex items-center gap-0.5">
        <Hash className="h-2.5 w-2.5" />{sc.input_params?.length || 0}
      </div>
      <div className="w-20 shrink-0 text-xs text-muted-foreground flex items-center gap-0.5">
        <GitBranch className="h-2.5 w-2.5" />v{sc.version}
      </div>
      <div className="w-40 shrink-0 text-xs text-muted-foreground/70 hidden md:block">{formatTime(sc.updated_at)}</div>
      <div className="w-32 shrink-0 flex justify-end gap-2">
        {(sc.approval_status === 'draft' || sc.approval_status === 'pending') && (
          <button
            className="h-6 px-2 inline-flex items-center justify-center gap-1 rounded text-[11px] text-amber-600 dark:text-amber-400 hover:bg-amber-500/10 cursor-pointer whitespace-nowrap"
            onClick={(e) => { e.stopPropagation(); handleApprove(sc) }}
            title="批准后脚本可被 AI 与自动化调用"
          >
            <CheckCircle className="h-3 w-3" />批准
          </button>
        )}
        <button className="h-6 w-6 inline-flex items-center justify-center rounded text-gray-500 hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-white dark:hover:bg-white/5 cursor-pointer" onClick={(e) => { e.stopPropagation(); navigate(`/scripts/${sc.id}`) }} title="编辑">
          <Pencil className="h-3.5 w-3.5" />
        </button>
        <button className="h-6 w-6 inline-flex items-center justify-center rounded text-gray-500 hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-white dark:hover:bg-white/5 cursor-pointer" onClick={(e) => { e.stopPropagation(); duplicate(sc.id) }} title="复制">
          <Copy className="h-3.5 w-3.5" />
        </button>
        <button className="h-6 w-6 inline-flex items-center justify-center rounded text-red-400 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-500/10 cursor-pointer" onClick={(e) => { e.stopPropagation(); setConfirmDelete(sc) }} title="删除">
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>
    </>
  )

  // 网格视图卡片
  const renderGridItem = (sc: Script) => (
    <Card key={sc.id} className="hover:border-blue-500/20 transition-colors cursor-pointer" onClick={() => navigate(`/scripts/${sc.id}`)}>
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
            <Button type="text" size="middle" className="h-6 w-6 text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={() => duplicate(sc.id)} title="复制">
              <Copy className="h-3 w-3" />
            </Button>
            <Button type="text" size="middle" className="h-6 w-6 text-destructive" onClick={() => setConfirmDelete(sc)} title="删除">
              <Trash2 className="h-3 w-3" />
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-2 pt-0">
        <div className="flex items-center gap-2">
           <Tag color="default" className={cn("text-[10px]", TYPE_COLORS[sc.type])}>{typeLabel(sc.type)}</Tag>
          {sc.approval_status && sc.approval_status !== 'approved' && (
            <Tag color="default" className={cn("text-[10px]", approvalMeta(sc.approval_status).cls)}>
              {approvalMeta(sc.approval_status).label}
            </Tag>
          )}
          <span className="text-[10px] text-muted-foreground flex items-center gap-0.5"><Clock className="h-2.5 w-2.5" />{sc.timeout}s</span>
          <span className="text-[10px] text-muted-foreground flex items-center gap-0.5"><Hash className="h-2.5 w-2.5" />{sc.input_params?.length || 0}</span>
          <span className="text-[10px] text-muted-foreground flex items-center gap-0.5"><GitBranch className="h-2.5 w-2.5" />v{sc.version}</span>
        </div>
        {(sc.approval_status === 'draft' || sc.approval_status === 'pending') && (
          <div className="flex gap-2" onClick={(e) => e.stopPropagation()}>
            <Button
              type="default" size="middle" className="h-6 text-[11px] text-amber-600 dark:text-amber-400 border-amber-500/30 hover:bg-amber-500/10"
              onClick={() => handleApprove(sc)}
            >
              <CheckCircle className="h-3 w-3 mr-1" />批准脚本
            </Button>
          </div>
        )}
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
      <StatisticCardGroup loading={loading} columns={5}>
        <StatisticCard title="脚本总数" value={stats.total} icon={<FileCode className="h-3 w-3" />} />
        <StatisticCard title="Shell" value={stats.shell} icon={<Terminal className="h-3 w-3" />} className="text-blue-500" />
        <StatisticCard title="Python" value={stats.python} icon={<Code2 className="h-3 w-3" />} className="text-blue-500" />
        <StatisticCard title="其他类型" value={stats.other} icon={<FileText className="h-3 w-3" />} className="text-amber-500" />
        <StatisticCard title="已收藏" value={stats.starred} icon={<Star className="h-3 w-3" />} className="text-amber-500" />
      </StatisticCardGroup>

      {/* 列表 */}
      <DataList
        data={scripts}
        loading={loading}
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
            <Button type="default" size="middle" onClick={handleImportClick} disabled={importing}>
              {importing ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <UploadIcon className="h-4 w-4 mr-1" />}
              导入
            </Button>
            <Button type="default" size="middle" onClick={() => {
              if (scripts.length === 0) { getMessage().warning('没有可导出的脚本'); return }
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
              getMessage().success(`已导出 ${exportData.length} 个脚本`)
            }}>
              <Download className="h-4 w-4 mr-1" />导出
            </Button>
            <Button size="middle" onClick={() => navigate('/scripts/new')}>
              <Plus className="h-4 w-4 mr-1" />新建脚本
            </Button>
          </div>
        }
      />

      {/* 确认删除对话框 */}
      <Modal
        open={!!confirmDelete}
        onCancel={() => setConfirmDelete(null)}
        onOk={handleConfirmDelete}
        okText="确认删除"
        okButtonProps={{ danger: true }}
        cancelText="取消"
        title={<span className="flex items-center gap-2"><Trash2 className="h-5 w-5 text-red-500" />确认删除脚本</span>}
      >
        <p className="text-sm text-muted-foreground">
          确认删除脚本 <span className="font-medium text-foreground">{confirmDelete?.name}</span> ({confirmDelete ? typeLabel(confirmDelete.type) : ''})? 此操作不可恢复，相关执行历史和版本记录将一并删除。
        </p>
      </Modal>
    </div>
  )
}
