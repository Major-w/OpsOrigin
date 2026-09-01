
import { useEffect, useState, useRef, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useScriptStore, type Script } from '@/stores/script'
import { StatisticCardGroup, StatisticCard } from '@/components/ui/statistic-card'
import { ProTable, type ProColumns, type ActionType, type ProFormInstance } from '@ant-design/pro-components'
import type { ColumnsState } from '@ant-design/pro-components'
import { Code2, Terminal, Hash, Clock, GitBranch, Star, Download, Upload as UploadIcon, Plus, Trash2, Copy, Pencil, Loader2, FileCode, CheckCircle, FileText } from 'lucide-react'
import { cn, formatTime, apiFetch } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { Button, Modal, Tag, Popconfirm, message } from 'antd'
import type { TablePaginationConfig } from 'antd'

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
  const actionRef = useRef<ActionType>()
  const formRef = useRef<ProFormInstance>()
  const [starredIds, setStarredIds] = useState<Set<string>>(new Set())
  const [importing, setImporting] = useState(false)
  const importInputRef = useRef<HTMLInputElement | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<Script | null>(null)
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([])
  const [typeFilter, setTypeFilter] = useState<string | null>(null)
  const [searchValues, setSearchValues] = useState<Record<string, any>>({})
  const [columnsState, setColumnsState] = useState<Record<string, ColumnsState>>({})


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

  const handleCardClick = (filterType: string | null) => {
    setTypeFilter(prev => prev === filterType ? null : filterType)
  }

  const filteredScripts = useMemo(() => {
    let result = scripts
    if (typeFilter) {
      if (typeFilter === 'starred') result = result.filter(s => starredIds.has(s.id))
      else result = result.filter(s => s.type === typeFilter)
    }
    if (searchValues._q) {
      const q = searchValues._q.toLowerCase()
      result = result.filter(s =>
        s.name?.toLowerCase().includes(q) ||
        s.type?.toLowerCase().includes(q) ||
        s.approval_status?.toLowerCase().includes(q) ||
        String(s.timeout).includes(q) ||
        String(s.version).includes(q) ||
        s.description?.toLowerCase().includes(q) ||
        s.input_params?.some(p => p.name?.toLowerCase().includes(q)) ||
        s.updated_at?.toLowerCase().includes(q)
      )
    }
    if (searchValues.name) {
      result = result.filter(s => s.name.toLowerCase().includes(searchValues.name.toLowerCase()))
    }
    if (searchValues.type) {
      result = result.filter(s => s.type === searchValues.type)
    }
    if (searchValues.approval_status) {
      result = result.filter(s => s.approval_status === searchValues.approval_status)
    }
    return result
  }, [scripts, typeFilter, starredIds, searchValues])

  const typeLabel = (t: string) => SCRIPT_TYPES.find(st => st.value === t)?.label || t

  const approvalMeta = (s: string | undefined): { label: string; cls: string } => {
    switch (s) {
      case 'draft': return { label: '待批准', cls: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/20' }
      case 'pending': return { label: '审批中', cls: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20' }
      case 'approved': return { label: '已批准', cls: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20' }
      case 'rejected': return { label: '已拒绝', cls: 'bg-red-500/10 text-red-600 dark:text-red-400 border-red-500/20' }
      default: return { label: '—', cls: '' }
    }
  }

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

  const stats = useMemo(() => {
    const shellCount = scripts.filter(s => s.type === 'shell').length
    const pythonCount = scripts.filter(s => s.type === 'python').length
    const otherCount = scripts.length - shellCount - pythonCount
    const starredCount = scripts.filter(s => starredIds.has(s.id)).length
    return { total: scripts.length, shell: shellCount, python: pythonCount, other: otherCount, starred: starredCount }
  }, [scripts, starredIds])

  const handleImportClick = () => { importInputRef.current?.click() }

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
      const { create } = useScriptStore.getState()
      for (let i = 0; i < arr.length; i++) {
        const item = arr[i] || {}
        let name = String(item.name || '').trim()
        if (!name) { errors.push(`第 ${i + 1} 条: 缺少 name 字段`); continue }
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

  const handleExportAll = () => {
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
    a.href = url; a.download = `scripts-export-${stamp}.json`
    document.body.appendChild(a); a.click(); document.body.removeChild(a)
    URL.revokeObjectURL(url)
    getMessage().success(`已导出 ${exportData.length} 个脚本`)
  }

  const handleConfirmDelete = async () => {
    if (!confirmDelete) return
    const ok = await remove(confirmDelete.id)
    if (ok) setConfirmDelete(null)
  }

  const columns: ProColumns<Script>[] = [
    {
      title: '模糊搜索',
      dataIndex: '_q',
      hideInTable: true,
      hideInSetting: true,
      fieldProps: { placeholder: '搜索脚本名称、类型、状态、超时、版本...' },
    },
    {
      title: '脚本名称',
      dataIndex: 'name',
      fixed: 'left',
      width: 240,
      render: (_, sc) => (
        <div className="flex items-center gap-2 min-w-0">
          <button
            onClick={(e) => { e.stopPropagation(); toggleStar(sc.id) }}
            className="shrink-0 p-0.5 rounded hover:bg-gray-100 dark:hover:bg-[rgba(255,255,255,0.06)]"
          >
            <Star className={cn("h-3.5 w-3.5", starredIds.has(sc.id) ? "fill-amber-400 text-amber-400" : "text-muted-foreground/40")} />
          </button>
          <Code2 className={cn("h-4 w-4 shrink-0", SCRIPT_TYPES.find(t => t.value === sc.type)?.color || 'text-muted-foreground')} />
          <span className="text-sm font-medium truncate cursor-pointer hover:text-blue-500"
            onClick={(e) => { e.stopPropagation(); navigate(`/scripts/${sc.id}`) }}>
            {sc.name}
          </span>
        </div>
      ),
      fieldProps: { placeholder: '搜索脚本名称' },
    },
    {
      title: '类型',
      dataIndex: 'type',
      width: 100,
      filters: SCRIPT_TYPES.map(t => ({ text: t.label, value: t.value })),
      onFilter: (value, record) => record.type === value,
      render: (_, sc) => (
        <Tag color="default" className={cn("text-[10px]", TYPE_COLORS[sc.type])}>{typeLabel(sc.type)}</Tag>
      ),
    },
    {
      title: '审批',
      dataIndex: 'approval_status',
      width: 80,
      filters: [
        { text: '待批准', value: 'draft' },
        { text: '审批中', value: 'pending' },
        { text: '已批准', value: 'approved' },
        { text: '已拒绝', value: 'rejected' },
      ],
      onFilter: (value, record) => record.approval_status === value,
      render: (_, sc) => (
        sc.approval_status && sc.approval_status !== 'approved' ? (
          <Tag color="default" className={cn("text-[10px]", approvalMeta(sc.approval_status).cls)}>
            {approvalMeta(sc.approval_status).label}
          </Tag>
        ) : null
      ),
    },
    {
      title: '超时',
      dataIndex: 'timeout',
      width: 80,
      fieldProps: { placeholder: '搜索超时' },
      sorter: (a, b) => (a.timeout || 0) - (b.timeout || 0),
      render: (_, sc) => (
        <span className="text-xs text-muted-foreground flex items-center gap-0.5"><Clock className="h-2.5 w-2.5" />{sc.timeout}s</span>
      ),
    },
    {
      title: '入参数',
      dataIndex: 'input_params',
      width: 80,
      fieldProps: { placeholder: '搜索入参数' },
      render: (_, sc) => (
        <span className="text-xs text-muted-foreground flex items-center gap-0.5"><Hash className="h-2.5 w-2.5" />{sc.input_params?.length || 0}</span>
      ),
    },
    {
      title: '版本',
      dataIndex: 'version',
      width: 80,
      fieldProps: { placeholder: '搜索版本' },
      sorter: (a, b) => (a.version || 0) - (b.version || 0),
      render: (_, sc) => (
        <span className="text-xs text-muted-foreground flex items-center gap-0.5"><GitBranch className="h-2.5 w-2.5" />v{sc.version}</span>
      ),
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 160,
      fieldProps: { placeholder: '搜索更新时间' },
      sorter: (a, b) => new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime(),
      render: (_, sc) => (
        <span className="text-xs text-muted-foreground/70 hidden md:block">{formatTime(sc.updated_at)}</span>
      ),
    },
    {
      title: '操作',
      valueType: 'option',
      fixed: 'right',
      width: 120,
      render: (_, sc) => [
        (sc.approval_status === 'draft' || sc.approval_status === 'pending') && (
          <button
            key="approve"
            className="h-6 px-2 inline-flex items-center justify-center gap-1 rounded text-[11px] text-amber-600 dark:text-amber-400 hover:bg-amber-500/10 cursor-pointer whitespace-nowrap"
            onClick={(e) => { e.stopPropagation(); handleApprove(sc) }}
            title="批准后脚本可被 AI 与自动化调用"
          >
            <CheckCircle className="h-3 w-3" />批准
          </button>
        ),
        <button
          key="edit"
          className="h-6 w-6 inline-flex items-center justify-center rounded text-gray-500 hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-white dark:hover:bg-white/5 cursor-pointer"
          onClick={(e) => { e.stopPropagation(); navigate(`/scripts/${sc.id}`) }}
          title="编辑"
        >
          <Pencil className="h-3.5 w-3.5" />
        </button>,
        <button
          key="copy"
          className="h-6 w-6 inline-flex items-center justify-center rounded text-gray-500 hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-white dark:hover:bg-white/5 cursor-pointer"
          onClick={(e) => { e.stopPropagation(); duplicate(sc.id) }}
          title="复制"
        >
          <Copy className="h-3.5 w-3.5" />
        </button>,
        <button
          key="delete"
          className="h-6 w-6 inline-flex items-center justify-center rounded text-red-400 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-500/10 cursor-pointer"
          onClick={(e) => { e.stopPropagation(); setConfirmDelete(sc) }}
          title="删除"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>,
      ],
    },
  ]

  const searchColumns = useMemo(() => {
    return columns.map(col => {
      const key = (col.dataIndex || col.key) as string
      if (!key) return col
      const state = columnsState[key]
      if (state && state.show === false) return { ...col, hideInSearch: true }
      return col
    })
  }, [columns, columnsState])

  return (
    <div className="p-6 space-y-4">
      <input ref={importInputRef} type="file" accept=".json" className="hidden" onChange={handleImportFile} />

      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">脚本管理</h1>
          <p className="text-sm text-muted-foreground">运维脚本管理、参数化定义与执行历史</p>
        </div>
      </div>

      <StatisticCardGroup loading={loading} columns={5}>
        <StatisticCard title="脚本总数" value={stats.total} icon={<FileCode className="h-3 w-3" />} onClick={() => handleCardClick(null)} active={typeFilter === null} />
        <StatisticCard title="Shell" value={stats.shell} icon={<Terminal className="h-3 w-3" />} className="text-blue-500" onClick={() => handleCardClick('shell')} active={typeFilter === 'shell'} />
        <StatisticCard title="Python" value={stats.python} icon={<Code2 className="h-3 w-3" />} className="text-blue-500" onClick={() => handleCardClick('python')} active={typeFilter === 'python'} />
        <StatisticCard title="其他类型" value={stats.other} icon={<FileText className="h-3 w-3" />} className="text-amber-500" />
        <StatisticCard title="已收藏" value={stats.starred} icon={<Star className="h-3 w-3" />} className="text-amber-500" onClick={() => handleCardClick('starred')} active={typeFilter === 'starred'} />
      </StatisticCardGroup>

      <ProTable<Script>
        actionRef={actionRef}
        formRef={formRef}
        columns={searchColumns}
        dataSource={filteredScripts}
        loading={loading}
        rowKey="id"
        columnsState={{ value: columnsState, onChange: setColumnsState }}
        search={{ labelWidth: 'auto', defaultCollapsed: true, span: 6 }}
        onSubmit={(values) => setSearchValues(values)}
        onReset={() => setSearchValues({})}
        pagination={{ defaultPageSize: 20, showSizeChanger: true, showTotal: (total) => `共 ${total} 条` }}
        rowSelection={{ selectedRowKeys, onChange: setSelectedRowKeys }}
        toolBarRender={() => [
          selectedRowKeys.length > 0 && (
            <Popconfirm
              key="bulk-delete"
              title={`确认删除选中的 ${selectedRowKeys.length} 个脚本？`}
              onConfirm={async () => {
                for (const key of selectedRowKeys) { await remove(key as string) }
                setSelectedRowKeys([])
                getMessage().success('批量删除成功')
              }}
              okText="确认"
              cancelText="取消"
            >
              <Button danger size="middle">
                <Trash2 className="h-3.5 w-3.5 mr-1" />批量删除 ({selectedRowKeys.length})
              </Button>
            </Popconfirm>
          ),
          <Button key="import" type="default" size="middle" onClick={handleImportClick} disabled={importing}>
            {importing ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <UploadIcon className="h-4 w-4 mr-1" />}
            导入
          </Button>,
          <Button key="export" type="default" size="middle" onClick={handleExportAll}>
            <Download className="h-4 w-4 mr-1" />导出
          </Button>,
          <Button key="new" type="primary" size="middle" onClick={() => navigate('/scripts/new')}>
            <Plus className="h-4 w-4 mr-1" />新建脚本
          </Button>,
        ].filter(Boolean)}
        options={{ density: true, fullScreen: true, reload: () => fetch(), setting: true }}
        headerTitle={false}
      />

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
