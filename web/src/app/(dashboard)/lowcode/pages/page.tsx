
import { useEffect, useState, useCallback } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Select, Modal, Popconfirm } from 'antd'
import { apiFetch, apiList } from '@/lib/utils'
import {
  Plus, Trash2, FileText, Pencil, Search, X,
  Layout, List, Eye, Layers, GripVertical,
  Type, AlignLeft, ListChecks, CheckSquare, ToggleLeft,
  Calendar, Hash, Table, ChevronLeft, Upload, ChevronRight,
  Undo, Redo, Save, History, Monitor, Tablet, Smartphone,
} from 'lucide-react'
import { DragDropContainer, ComponentLibrary, PropertyPanel, DragItem } from '@/components/lowcode/DragDrop'
import { LivePreview, ResponsivePreview } from '@/components/lowcode/LivePreview'

const PAGE_TYPES = [
  { value: 'form', label: '表单' },
  { value: 'list', label: '列表' },
  { value: 'detail', label: '详情' },
  { value: 'custom', label: '自定义' },
]

const TYPE_ICONS: Record<string, any> = {
  form: Layout, list: List, detail: Eye, custom: Layers,
}

interface Page {
  id: string; name: string; description: string; type: string;
  model_id: string; components: DragItem[]
}

interface Model { id: string; name: string }

// 历史记录接口
interface HistoryRecord {
  id: string;
  components: DragItem[];
  timestamp: number;
  description: string;
}

export default function LowcodePagesPage() {
  const [pages, setPages] = useState<Page[]>([])
  const [models, setModels] = useState<Model[]>([])
  const [search, setSearch] = useState('')
  const [dialog, setDialog] = useState<'create' | 'edit' | null>(null)
  const [editPage, setEditPage] = useState<Page | null>(null)
  const [form, setForm] = useState({ name: '', description: '', type: 'form', model_id: '' })
  const [builderOpen, setBuilderOpen] = useState(false)
  const [builderPage, setBuilderPage] = useState<Page | null>(null)
  const [selectedItem, setSelectedItem] = useState<DragItem | null>(null)
  const [pendingPageId, setPendingPageId] = useState<string | null>(null)

  // 撤销/重做历史记录
  const [history, setHistory] = useState<HistoryRecord[]>([])
  const [historyIndex, setHistoryIndex] = useState(-1)
  const [showHistory, setShowHistory] = useState(false)
  const [previewMode, setPreviewMode] = useState<'live' | 'responsive'>('live')

  const fetchPages = useCallback(() => {
    apiList<Page>('/lowcode/pages').then(setPages).catch(() => setPages([]))
  }, [])

  const fetchModels = useCallback(() => {
    apiList<Model>('/lowcode/models').then(setModels).catch(() => setModels([]))
  }, [])

  useEffect(() => { fetchPages(); fetchModels() }, [fetchPages, fetchModels])

  const filtered = pages.filter(p =>
    p.name.toLowerCase().includes(search.toLowerCase()) ||
    p.description?.toLowerCase().includes(search.toLowerCase())
  )

  const openCreate = () => {
    setForm({ name: '', description: '', type: 'form', model_id: '' })
    setDialog('create')
  }

  const openEdit = (p: Page) => {
    setEditPage(p)
    setForm({ name: p.name, description: p.description, type: p.type, model_id: p.model_id || '' })
    setDialog('edit')
  }

  const handleSave = async () => {
    if (!form.name) return
    try {
      if (dialog === 'create') {
        await apiFetch('/lowcode/pages', { method: 'POST', body: JSON.stringify({ ...form, components: [] }) })
      } else if (editPage) {
        await apiFetch(`/lowcode/pages/${editPage.id}`, { method: 'PUT', body: JSON.stringify(form) })
      }
      fetchPages()
    } catch {}
    setDialog(null)
  }

  const handleDelete = async () => {
    if (!pendingPageId) return
    try { await apiFetch(`/lowcode/pages/${pendingPageId}`, { method: 'DELETE' }); fetchPages() } catch {}
    setPendingPageId(null)
  }

  const openBuilder = (p: Page) => {
    setBuilderPage({ ...p, components: p.components || [] })
    setSelectedItem(null)
    // 初始化历史记录
    const initialHistory: HistoryRecord = {
      id: `h-${Date.now()}`,
      components: p.components || [],
      timestamp: Date.now(),
      description: '初始状态',
    }
    setHistory([initialHistory])
    setHistoryIndex(0)
    setBuilderOpen(true)
  }

  // 添加组件
  const addComponent = (component: Omit<DragItem, 'id' | 'sortOrder'>) => {
    if (!builderPage) return
    const newItem: DragItem = {
      ...component,
      id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      sortOrder: builderPage.components.length,
    }
    const newComponents = [...builderPage.components, newItem]
    setBuilderPage({ ...builderPage, components: newComponents })
    addToHistory(newComponents, '添加组件')
  }

  // 更新组件列表
  const handleItemsChange = (newItems: DragItem[]) => {
    if (!builderPage) return
    setBuilderPage({ ...builderPage, components: newItems })
    addToHistory(newItems, '重新排序')
  }

  // 选中组件
  const handleItemSelect = (item: DragItem | null) => {
    setSelectedItem(item)
  }

  // 更新选中组件的属性
  const handleItemPropsChange = (props: Record<string, any>) => {
    if (!builderPage || !selectedItem) return
    const newComponents = builderPage.components.map(c =>
      c.id === selectedItem.id ? { ...c, props } : c
    )
    setBuilderPage({ ...builderPage, components: newComponents })
    setSelectedItem({ ...selectedItem, props })
    addToHistory(newComponents, '更新组件属性')
  }

  // 添加到历史记录
  const addToHistory = (components: DragItem[], description: string) => {
    const newRecord: HistoryRecord = {
      id: `h-${Date.now()}`,
      components: [...components],
      timestamp: Date.now(),
      description,
    }
    // 移除当前位置之后的历史记录
    const newHistory = history.slice(0, historyIndex + 1)
    newHistory.push(newRecord)
    setHistory(newHistory)
    setHistoryIndex(newHistory.length - 1)
  }

  // 撤销
  const undo = () => {
    if (historyIndex > 0) {
      const newIndex = historyIndex - 1
      setHistoryIndex(newIndex)
      const record = history[newIndex]
      if (builderPage) {
        setBuilderPage({ ...builderPage, components: [...record.components] })
      }
    }
  }

  // 重做
  const redo = () => {
    if (historyIndex < history.length - 1) {
      const newIndex = historyIndex + 1
      setHistoryIndex(newIndex)
      const record = history[newIndex]
      if (builderPage) {
        setBuilderPage({ ...builderPage, components: [...record.components] })
      }
    }
  }

  // 保存页面
  const saveBuilder = async () => {
    if (!builderPage) return
    try {
      await apiFetch(`/lowcode/pages/${builderPage.id}`, { method: 'PUT', body: JSON.stringify({ components: builderPage.components }) })
      fetchPages()
    } catch {}
    setBuilderOpen(false)
  }

  // 渲染预览
  const renderPreview = () => {
    if (!builderPage) return null

    if (previewMode === 'responsive') {
      return (
        <ResponsivePreview
          components={builderPage.components}
          className="h-full"
        />
      )
    }

    return (
      <LivePreview
        components={builderPage.components}
        className="h-full"
      />
    )
  }

  if (builderOpen && builderPage) {
    return (
      <div className="space-y-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Button type="text" size="middle" onClick={() => setBuilderOpen(false)}>
              <ChevronLeft className="h-4 w-4" />返回
            </Button>
            <div>
              <h1 className="text-lg font-bold">{builderPage.name} - 页面搭建</h1>
              <p className="text-xs text-muted-foreground">拖拽组件构建页面，点击组件编辑属性</p>
            </div>
          </div>
          <div className="flex items-center gap-2">
            {/* 预览模式切换 */}
            <div className="flex items-center gap-1 border rounded p-1">
              <Button
                type={previewMode === 'live' ? 'primary' : 'text'}
                size="middle"
                onClick={() => setPreviewMode('live')}
                title="实时预览"
              >
                <Monitor className="h-4 w-4" />
              </Button>
              <Button
                type={previewMode === 'responsive' ? 'primary' : 'text'}
                size="middle"
                onClick={() => setPreviewMode('responsive')}
                title="响应式预览"
              >
                <Tablet className="h-4 w-4" />
              </Button>
            </div>

            {/* 撤销/重做按钮 */}
            <div className="flex items-center gap-1 border rounded p-1">
              <Button
                type="text"
                size="middle"
                onClick={undo}
                disabled={historyIndex <= 0}
                title="撤销"
              >
                <Undo className="h-4 w-4" />
              </Button>
              <Button
                type="text"
                size="middle"
                onClick={redo}
                disabled={historyIndex >= history.length - 1}
                title="重做"
              >
                <Redo className="h-4 w-4" />
              </Button>
            </div>

            {/* 历史记录按钮 */}
            <Button
              type="default"
              size="middle"
              onClick={() => setShowHistory(!showHistory)}
            >
              <History className="h-4 w-4 mr-1" />
              历史 ({history.length})
            </Button>

            <Button onClick={saveBuilder}>
              <Save className="h-4 w-4 mr-1" />
              保存页面
            </Button>
          </div>
        </div>

        <div className="grid grid-cols-[280px_1fr_280px] gap-4 min-h-[calc(100vh-200px)]">
          {/* 组件库 */}
          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">组件库</CardTitle>
            </CardHeader>
            <CardContent className="max-h-[calc(100vh-280px)] overflow-y-auto">
              <ComponentLibrary onAddComponent={addComponent} />
            </CardContent>
          </Card>

          {/* 页面预览 */}
          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">页面预览</CardTitle>
            </CardHeader>
            <CardContent className="max-h-[calc(100vh-280px)] overflow-y-auto">
              {/* 拖拽区域 */}
              <div className="mb-4">
                <div className="text-xs text-gray-500 mb-2">拖拽组件到此处</div>
                <DragDropContainer
                  items={builderPage.components}
                  onItemsChange={handleItemsChange}
                  onItemSelect={handleItemSelect}
                  className="min-h-[200px]"
                />
              </div>

              {/* 实时预览区域 */}
              <div className="border-t pt-4">
                {renderPreview()}
              </div>
            </CardContent>
          </Card>

          {/* 属性配置 */}
          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm">属性配置</CardTitle>
            </CardHeader>
            <CardContent>
              <PropertyPanel
                item={selectedItem}
                onChange={handleItemPropsChange}
              />
            </CardContent>
          </Card>
        </div>

        {/* 历史记录面板 */}
        {showHistory && (
          <Modal title="历史记录" open={showHistory} onCancel={() => setShowHistory(false)} footer={null}>
            <div className="max-h-96 overflow-y-auto">
              {history.map((record, index) => (
                <div
                  key={record.id}
                  className={`p-3 border-b cursor-pointer hover:bg-gray-50 ${
                    index === historyIndex ? 'bg-blue-50 border-l-4 border-l-blue-500' : ''
                  }`}
                  onClick={() => {
                    setHistoryIndex(index)
                    if (builderPage) {
                      setBuilderPage({ ...builderPage, components: [...record.components] })
                    }
                  }}
                >
                  <div className="flex items-center justify-between">
                    <span className="text-sm font-medium">{record.description}</span>
                    <span className="text-xs text-gray-500">
                      {new Date(record.timestamp).toLocaleTimeString()}
                    </span>
                  </div>
                  <div className="text-xs text-gray-500 mt-1">
                    {record.components.length} 个组件
                  </div>
                </div>
              ))}
            </div>
          </Modal>
        )}
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">页面搭建器</h1>
          <p className="text-sm text-muted-foreground">可视化构建表单和列表页面</p>
        </div>
        <Button onClick={openCreate}><Plus className="h-4 w-4" />新建页面</Button>
      </div>

      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
        <Input className="pl-9" placeholder="搜索页面名称、描述..." value={search} onChange={e => setSearch(e.target.value)} />
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {filtered.map(p => {
          const Icon = TYPE_ICONS[p.type] || FileText
          return (
            <Card key={p.id} className="group hover:border-[rgba(255,255,255,0.15)] transition-colors">
              <CardHeader className="py-4">
                <div className="flex items-start justify-between">
                  <div className="flex items-center gap-2">
                    <Icon className="h-5 w-5 text-blue-400" />
                    <div>
                      <CardTitle className="text-base">{p.name}</CardTitle>
                      {p.description && <p className="text-xs text-muted-foreground mt-1">{p.description}</p>}
                    </div>
                  </div>
                  <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                    <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={() => openEdit(p)}><Pencil className="h-3.5 w-3.5" /></Button>
                    <Popconfirm title="确定删除该页面? 此操作不可恢复。" onConfirm={handleDelete}>
                      <Button type="text" size="middle"><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                    </Popconfirm>
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <div className="flex items-center gap-2 mb-3">
                  <Tag color="default">{PAGE_TYPES.find(t => t.value === p.type)?.label || p.type}</Tag>
                  <Tag>{p.components?.length || 0} 组件</Tag>
                  {p.model_id && <Tag color="processing">{models.find(m => m.id === p.model_id)?.name || '已关联模型'}</Tag>}
                </div>
                <Button type="default" size="middle" className="w-full" onClick={() => openBuilder(p)}>
                  <Layers className="h-3.5 w-3.5" />页面搭建
                </Button>
              </CardContent>
            </Card>
          )
        })}
        {filtered.length === 0 && (
          <div className="col-span-full text-center py-12 text-muted-foreground text-sm">
            {search ? '没有匹配的页面' : '暂无页面定义，点击上方按钮创建'}
          </div>
        )}
      </div>

      <Modal title={dialog === 'create' ? '新建页面' : '编辑页面'} open={!!dialog} onCancel={() => setDialog(null)} footer={<><Button type="default" onClick={() => setDialog(null)}>取消</Button><Button onClick={handleSave} disabled={!form.name}>{dialog === 'create' ? '创建' : '保存'}</Button></>}>
        <div className="space-y-3">
          <div>
            <label className="text-sm font-medium">页面名称 *</label>
            <Input className="mt-1" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="用户管理" />
          </div>
          <div>
            <label className="text-sm font-medium">描述</label>
            <Input className="mt-1" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} placeholder="用户管理页面" />
          </div>
          <div>
            <label className="text-sm font-medium">页面类型</label>
            <Select className="mt-1" options={PAGE_TYPES} value={form.type} onChange={v => setForm(f => ({ ...f, type: v as string }))} />
          </div>
          <div>
            <label className="text-sm font-medium">关联模型</label>
            <Select
              className="mt-1"
              options={[{ value: '', label: '无' }, ...models.map(m => ({ value: m.id, label: m.name }))]}
              value={form.model_id}
              onChange={v => setForm(f => ({ ...f, model_id: v as string }))}
            />
          </div>
        </div>
      </Modal>
    </div>
  )
}
