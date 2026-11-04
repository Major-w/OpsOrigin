// 数据字典管理页（对齐后端 FieldDict 真实协议：name/description/fields: FieldDef[]）。
// 旧版页面发 {key,label,options} 旧协议后端解析不到（字段全空），已按真实协议重做。
// 交互原则：能选择的尽量选择 —— 字段类型下拉、必填开关、选项行式编辑器 + 常用模板快填。
import { useEffect, useMemo, useState } from 'react'
import { Database, Plus, Trash2, Pencil, Search, RefreshCw } from 'lucide-react'
import { apiFetch, apiList, cn } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { Button, Input, Modal, Select, Switch, Tag, Tooltip } from 'antd'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'

/** 与后端 form.FieldDef 对齐的子集（字典场景用不到条件显隐/动态 source 等高级属性） */
interface DictField {
  key: string
  label: string
  type: string
  options?: { value: string; label: string }[]
  required?: boolean
  placeholder?: string
  help?: string
}

interface FieldDict {
  id: string
  name: string
  description?: string
  fields: DictField[]
  /** 后端 list 附带：引用本字典的表单 scope 列表 */
  used_by?: string[]
  updated_at?: string
}

/** 字段类型下拉（分组对齐 form.FieldType 全集，选择代替手输） */
const TYPE_GROUPS = [
  {
    label: '选择类',
    options: [
      { value: 'select', label: '单选下拉' },
      { value: 'multiselect', label: '多选' },
      { value: 'bool', label: '是/否' },
      { value: 'cascader', label: '级联选择' },
    ],
  },
  {
    label: '文本类',
    options: [
      { value: 'text', label: '单行文本' },
      { value: 'textarea', label: '多行文本' },
      { value: 'richtext', label: '富文本' },
      { value: 'password', label: '密码' },
      { value: 'link', label: '链接' },
    ],
  },
  {
    label: '数值与日期',
    options: [
      { value: 'number', label: '数字' },
      { value: 'date', label: '日期' },
      { value: 'datetime', label: '日期时间' },
      { value: 'time', label: '时间' },
      { value: 'timerange', label: '时间段' },
      { value: 'duration', label: '时长' },
    ],
  },
  {
    label: '特殊',
    options: [
      { value: 'user', label: '选人' },
      { value: 'ci', label: '选配置项' },
      { value: 'attachment', label: '附件' },
      { value: 'table', label: '表格' },
      { value: 'expression', label: '计算字段' },
    ],
  },
]

const TYPE_LABEL: Record<string, string> = Object.fromEntries(
  TYPE_GROUPS.flatMap(g => g.options.map(o => [o.value, o.label])),
)
const OPTION_TYPES = new Set(['select', 'multiselect', 'cascader'])

/** 常用选项模板（选择代替手敲 value:label） */
const OPTION_TEMPLATES: { name: string; options: { value: string; label: string }[] }[] = [
  { name: '事件优先级', options: [{ value: 'low', label: '低' }, { value: 'medium', label: '中' }, { value: 'high', label: '高' }, { value: 'critical', label: '紧急' }] },
  { name: '问题严重度', options: [{ value: 'p1', label: 'P1 致命' }, { value: 'p2', label: 'P2 严重' }, { value: 'p3', label: 'P3 一般' }, { value: 'p4', label: 'P4 轻微' }] },
  { name: '处理状态', options: [{ value: 'new', label: '新建' }, { value: 'processing', label: '处理中' }, { value: 'resolved', label: '已解决' }, { value: 'closed', label: '已关闭' }] },
  { name: '是否确认', options: [{ value: 'yes', label: '是' }, { value: 'no', label: '否' }] },
  { name: '影响范围', options: [{ value: 'global', label: '全局' }, { value: 'department', label: '部门级' }, { value: 'single', label: '单个用户' }] },
]

/** 整字典模板（新建时一键生成） */
const DICT_TEMPLATES: { name: string; description: string; fields: DictField[] }[] = [
  {
    name: '事件优先级', description: '工单/事件的优先级分级',
    fields: [{ key: 'priority', label: '优先级', type: 'select', required: true, options: OPTION_TEMPLATES[0].options }],
  },
  {
    name: '问题严重度', description: '故障严重程度分级（P1~P4）',
    fields: [{ key: 'severity', label: '严重度', type: 'select', required: true, options: OPTION_TEMPLATES[1].options }],
  },
  {
    name: '处理状态', description: '工单处理进度状态',
    fields: [{ key: 'status', label: '处理状态', type: 'select', required: true, options: OPTION_TEMPLATES[2].options }],
  },
  {
    name: '变更影响范围', description: '变更操作的影响面评估',
    fields: [
      { key: 'impact', label: '影响范围', type: 'select', required: true, options: OPTION_TEMPLATES[4].options },
      { key: 'rollback_needed', label: '是否需要回滚预案', type: 'bool', required: true },
    ],
  },
]

const emptyField = (): DictField => ({ key: '', label: '', type: 'select', options: [{ value: '', label: '' }] })

export default function FormDictsPage() {
  const [dicts, setDicts] = useState<FieldDict[]>([])
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [keyword, setKeyword] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<{ name: string; description: string; fields: DictField[] }>({ name: '', description: '', fields: [emptyField()] })
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<FieldDict | null>(null)

  const load = async () => {
    setLoading(true)
    setLoadError(null)
    try {
      const data = await apiList<FieldDict>('/itsm/form-dicts')
      setDicts(data || [])
    } catch (e: any) {
      setLoadError(e.message || '加载数据字典失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const filtered = useMemo(() => dicts.filter(d =>
    !keyword ||
    d.name?.toLowerCase().includes(keyword.toLowerCase()) ||
    d.description?.toLowerCase().includes(keyword.toLowerCase())
  ), [dicts, keyword])

  const openCreate = (tpl?: typeof DICT_TEMPLATES[number]) => {
    setEditingId(null)
    setForm(tpl
      ? { name: tpl.name, description: tpl.description, fields: tpl.fields.map(f => ({ ...f, options: f.options?.map(o => ({ ...o })) })) }
      : { name: '', description: '', fields: [emptyField()] })
    setDialogOpen(true)
  }

  const openEdit = (dict: FieldDict) => {
    setEditingId(dict.id)
    setForm({
      name: dict.name || '',
      description: dict.description || '',
      fields: (dict.fields?.length ? dict.fields : [emptyField()]).map(f => ({ ...f, options: f.options ? f.options.map(o => ({ ...o })) : undefined })),
    })
    setDialogOpen(true)
  }

  const patchField = (idx: number, patch: Partial<DictField>) => {
    setForm(f => ({
      ...f,
      fields: f.fields.map((fd, i) => (i === idx ? { ...fd, ...patch } : fd)),
    }))
  }

  const validate = (): string | null => {
    if (!form.name.trim()) return '请填写字典名称'
    const seen = new Set<string>()
    for (let i = 0; i < form.fields.length; i++) {
      const f = form.fields[i]
      if (!f.key.trim()) return `字段 ${i + 1}：请填写字段标识（key）`
      if (seen.has(f.key.trim())) return `字段标识「${f.key}」重复`
      seen.add(f.key.trim())
      if (OPTION_TYPES.has(f.type) && !(f.options || []).some(o => o.value.trim())) {
        return `字段「${f.label || f.key}」：选择类字段至少需要一个选项`
      }
    }
    return null
  }

  const save = async () => {
    const err = validate()
    if (err) { getMessage().error(err); return }
    setSaving(true)
    try {
      const fields = form.fields.map(f => ({
        ...f,
        key: f.key.trim(),
        label: f.label.trim() || f.key.trim(),
        options: OPTION_TYPES.has(f.type)
          ? (f.options || []).filter(o => o.value.trim()).map(o => ({ value: o.value.trim(), label: o.label.trim() || o.value.trim() }))
          : undefined,
      }))
      await apiFetch('/itsm/form-dicts', {
        method: 'POST',
        body: JSON.stringify({
          id: editingId || undefined,
          name: form.name.trim(),
          description: form.description.trim(),
          fields,
        }),
      })
      getMessage().success(editingId ? '更新成功' : '创建成功')
      setDialogOpen(false)
      load()
    } catch (e: any) {
      getMessage().error(`保存失败: ${e.message}`)
    } finally {
      setSaving(false)
    }
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await apiFetch(`/itsm/form-dicts/${deleteTarget.id}`, { method: 'DELETE' })
      getMessage().success('删除成功')
      load()
    } catch (e: any) {
      getMessage().error(`删除失败: ${e.message}`)
    }
    setDeleteTarget(null)
  }

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">数据字典</h1>
          <p className="text-sm text-muted-foreground mt-1">可跨表单复用的字段定义集合，表单设计器中通过「引用数据字典」展开</p>
        </div>
        <div className="flex items-center gap-2">
          <Select
            placeholder="从模板新建"
            style={{ width: 160 }}
            allowClear
            value={null}
            onChange={(v) => { const tpl = DICT_TEMPLATES.find(t => t.name === v); if (tpl) openCreate(tpl) }}
            options={DICT_TEMPLATES.map(t => ({ value: t.name, label: t.name }))}
          />
          <Button type="primary" onClick={() => openCreate()}>
            <Plus className="w-4 h-4 mr-1" />
            新建字典
          </Button>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <Input
            placeholder="搜索字典名称或描述..."
            value={keyword}
            onChange={e => setKeyword(e.target.value)}
            className="pl-9"
          />
        </div>
        <Button type="default" onClick={load} disabled={loading}>
          <RefreshCw className={cn('w-4 h-4 mr-1', loading && 'animate-spin')} />
          刷新
        </Button>
      </div>

      {loadError ? (
        <ErrorState message={`加载数据字典失败: ${loadError}`} onRetry={load} />
      ) : loading ? (
        <div className="text-center py-12 text-muted-foreground">加载中...</div>
      ) : filtered.length === 0 ? (
        <div className="border rounded-lg py-12 text-center">
          <Database className="w-12 h-12 mx-auto text-muted-foreground mb-4" />
          <p className="text-muted-foreground">{keyword ? '无匹配的字典' : '暂无数据字典，可从模板一键创建'}</p>
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {filtered.map(dict => (
            <div key={dict.id} className="border rounded-lg p-4 hover:shadow-md transition-shadow bg-card">
              <div className="flex items-start justify-between">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <h3 className="text-base font-semibold truncate">{dict.name}</h3>
                    {(dict.used_by?.length || 0) > 0 && (
                      <Tooltip title={`被引用：${dict.used_by!.join('、')}`}>
                        <Tag color="blue" className="text-xs shrink-0">被 {dict.used_by!.length} 个表单引用</Tag>
                      </Tooltip>
                    )}
                  </div>
                  {dict.description && <p className="text-xs text-muted-foreground mt-1 line-clamp-1">{dict.description}</p>}
                </div>
                <div className="flex gap-1 shrink-0">
                  <Button type="text" size="small" onClick={() => openEdit(dict)} aria-label="编辑">
                    <Pencil className="w-3.5 h-3.5" />
                  </Button>
                  <Button type="text" size="small" danger onClick={() => setDeleteTarget(dict)} aria-label="删除">
                    <Trash2 className="w-3.5 h-3.5" />
                  </Button>
                </div>
              </div>
              <div className="mt-3 space-y-1.5">
                {dict.fields.slice(0, 4).map((f, i) => (
                  <div key={i} className="flex items-center gap-1.5 text-xs">
                    <Tag className="text-[10px] shrink-0" color="default">{TYPE_LABEL[f.type] || f.type}</Tag>
                    <span className="truncate">{f.label || f.key}</span>
                    {f.required && <span className="text-red-500">*</span>}
                    {!!f.options?.length && <span className="text-muted-foreground shrink-0">({f.options.length} 项)</span>}
                  </div>
                ))}
                {dict.fields.length > 4 && (
                  <p className="text-xs text-muted-foreground">+{dict.fields.length - 4} 个字段</p>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      <Modal
        open={dialogOpen}
        onCancel={() => setDialogOpen(false)}
        title={editingId ? '编辑数据字典' : '新建数据字典'}
        width={720}
        footer={null}
      >
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">字典名称 <span className="text-red-500">*</span></label>
              <Input
                value={form.name}
                onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="如: 事件优先级"
              />
            </div>
            <div>
              <label className="text-sm font-medium">描述</label>
              <Input
                value={form.description}
                onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
                placeholder="字典用途说明（可选）"
              />
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <label className="text-sm font-medium">字段定义</label>
              <Button
                size="small"
                onClick={() => setForm(f => ({ ...f, fields: [...f.fields, emptyField()] }))}
              >
                <Plus className="w-3.5 h-3.5 mr-1" />添加字段
              </Button>
            </div>
            <div className="space-y-3">
              {form.fields.map((f, i) => (
                <div key={i} className="border rounded-md p-3 space-y-2 bg-muted/20">
                  <div className="flex items-center gap-2">
                    <Input
                      className="w-40"
                      value={f.key}
                      onChange={e => patchField(i, { key: e.target.value })}
                      placeholder="字段标识 (key)"
                    />
                    <Input
                      className="flex-1"
                      value={f.label}
                      onChange={e => patchField(i, { label: e.target.value })}
                      placeholder="显示名称"
                    />
                    <Select
                      className="w-36"
                      value={f.type}
                      onChange={v => patchField(i, {
                        type: v,
                        // 切到选择类时补一个空选项行；切走时清掉 options
                        ...(OPTION_TYPES.has(v)
                          ? { options: f.options?.length ? f.options : [emptyField().options![0]] }
                          : { options: undefined }),
                      })}
                      options={TYPE_GROUPS as any}
                      showSearch
                      optionFilterProp="label"
                    />
                    <Tooltip title="必填">
                      <Switch size="small" checked={!!f.required} onChange={v => patchField(i, { required: v })} />
                    </Tooltip>
                    <Button
                      type="text" size="small" danger
                      disabled={form.fields.length <= 1}
                      onClick={() => setForm(fd => ({ ...fd, fields: fd.fields.filter((_, j) => j !== i) }))}
                      aria-label="删除字段"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                  </div>
                  {OPTION_TYPES.has(f.type) && (
                    <div className="pl-1 space-y-1.5">
                      <div className="flex items-center gap-2">
                        <span className="text-xs text-muted-foreground w-16">选项列表</span>
                        <Select
                          size="small"
                          className="w-44"
                          placeholder="模板快填"
                          value={null}
                          onChange={(tplName) => {
                            const tpl = OPTION_TEMPLATES.find(t => t.name === tplName)
                            if (tpl) patchField(i, { options: tpl.options.map(o => ({ ...o })) })
                          }}
                          options={OPTION_TEMPLATES.map(t => ({ value: t.name, label: t.name }))}
                        />
                        <Button size="small" type="text" onClick={() => patchField(i, { options: [...(f.options || []), { value: '', label: '' }] })}>
                          <Plus className="w-3 h-3 mr-0.5" />选项
                        </Button>
                      </div>
                      {(f.options || []).map((opt, oi) => (
                        <div key={oi} className="flex items-center gap-2">
                          <Input
                            className="w-40"
                            size="small"
                            value={opt.value}
                            onChange={e => patchField(i, { options: (f.options || []).map((o, j) => j === oi ? { ...o, value: e.target.value } : o) })}
                            placeholder="存储值 (value)"
                          />
                          <Input
                            className="flex-1"
                            size="small"
                            value={opt.label}
                            onChange={e => patchField(i, { options: (f.options || []).map((o, j) => j === oi ? { ...o, label: e.target.value } : o) })}
                            placeholder="显示名 (label)"
                          />
                          <Button
                            type="text" size="small" danger
                            disabled={(f.options || []).length <= 1}
                            onClick={() => patchField(i, { options: (f.options || []).filter((_, j) => j !== oi) })}
                            aria-label="删除选项"
                          >
                            <Trash2 className="w-3 h-3" />
                          </Button>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>

          <div className="flex justify-end gap-2">
            <Button onClick={() => setDialogOpen(false)}>取消</Button>
            <Button type="primary" onClick={save} disabled={saving}>
              {saving ? '保存中...' : '保存'}
            </Button>
          </div>
        </div>
      </Modal>

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={`删除字典「${deleteTarget?.name || ''}」`}
        description={(deleteTarget?.used_by?.length || 0) > 0
          ? `⚠️ 该字典正被 ${deleteTarget!.used_by!.length} 个表单引用（${deleteTarget!.used_by!.join('、')}），删除后这些表单的引用将失效。确定删除？`
          : '删除后不可恢复。'}
        confirmLabel="删除"
      />
    </div>
  )
}
