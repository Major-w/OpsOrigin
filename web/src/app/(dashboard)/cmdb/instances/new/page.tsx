
import { useEffect, useState } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { Link } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Input, Select, message } from 'antd'
import { useCan } from '@/stores/permissions'
import { apiFetch, apiList } from '@/lib/utils'
import { ChevronLeft, Save, Network } from 'lucide-react'
import { getMessage } from '@/lib/message'

// 与 models 页对齐的字段类型定义(驱动 ref/enum/unique/user 渲染)
type FieldType =
  | 'string' | 'longtext' | 'number' | 'float' | 'enum' | 'multi-enum'
  | 'boolean' | 'ref' | 'user' | 'date' | 'datetime' | 'json' | 'list'

interface ModelFieldDef {
  name: string
  type: FieldType
  required?: boolean
  description?: string
  options?: string[]
  multi?: boolean
  unique?: boolean
  ref_model?: string
  ref_display?: string
  autofill?: string[]
  pattern?: string
  maxLength?: number
  min?: number
  max?: number
}

type Cardinality = 'one_to_one' | 'one_to_many' | 'many_to_many'
interface RelationDef {
  id?: string
  name: string
  relation_type: string
  target_model: string
  cardinality: Cardinality
  reverse_name?: string
  description?: string
}

interface ModelInfo {
  id: string
  name: string
  category?: string
  parent_id?: string
  abstract?: boolean
  schema?: Record<string, any>
  relations?: RelationDef[]
  fields?: ModelFieldDef[]
}

// schemaToFields: 后端 schema(map[字段名]AttrDef) -> 前端字段数组(带 name)
function schemaToFields(schema: Record<string, any> = {}): ModelFieldDef[] {
  return Object.entries(schema).map(([name, def]) => ({
    name,
    type: (def?.type || 'string') as FieldType,
    required: !!def?.required,
    description: def?.description || '',
    options: def?.options,
    multi: !!def?.multi,
    unique: !!def?.unique,
    ref_model: def?.ref_model,
    ref_display: def?.ref_display,
    autofill: def?.autofill,
    pattern: def?.pattern,
    maxLength: def?.max_length,
    min: def?.min,
    max: def?.max,
  }))
}

interface FormState {
  name: string
  type_id: string
  description: string
  attributes: Record<string, any>
  labels: Record<string, string>
}

interface PendingRelation {
  relation_type: string
  relation_id?: string
  target: string
}

export default function NewInstancePage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const preType = searchParams.get('type_id') || ''
  const canWrite = useCan('cmdb:write')

  const [models, setModels] = useState<ModelInfo[]>([])
  const [form, setForm] = useState<FormState>({
    name: '', type_id: preType, description: '', attributes: {}, labels: {},
  })
  const [submitting, setSubmitting] = useState(false)
  // 自定义属性(模型未定义字段的兜底录入), 与结构化字段隔离, 避免互相覆盖
  const [customAttrs, setCustomAttrs] = useState<{ key: string; value: string }[]>([])
  const [refOptions, setRefOptions] = useState<Record<string, { id: string; name: string; attributes?: Record<string, any> }[]>>({})
  // user 类型字段: 平台用户列表(存 username)
  const [userOptions, setUserOptions] = useState<{ id: string; username: string; email?: string }[]>([])
  // 关系预选: 每个模型声明的关系对应一个目标实例选择
  const [pendingRels, setPendingRels] = useState<Record<string, string>>({})

  useEffect(() => {
    apiList<{ id: string; username: string; email?: string }>('/users')
      .then(setUserOptions)
      .catch(() => setUserOptions([]))
  }, [])

  useEffect(() => {
    apiList<ModelInfo>('/cmdb/model-type?limit=1000')
      .then(list => {
        // 展开 schema -> fields, 过滤掉抽象父类(不可实例化)
        const expanded = list
          .filter(m => !m.abstract)
          .map(m => ({ ...m, fields: m.fields || schemaToFields(m.schema), relations: m.relations || [] }))
        setModels(expanded)
        // 预选类型: 若 URL 指定则加载其引用选项
        const pre = expanded.find(m => m.id === preType)
        if (pre) (pre.fields || []).forEach(f => { if (f.ref_model) ensureRefOptions(f.ref_model) })
      })
      .catch(() => setModels([]))
  }, [preType])

  const ensureRefOptions = async (refModel: string) => {
    if (!refModel || refOptions[refModel]) return
    try {
      const list = await apiList<{ id: string; name: string; attributes?: Record<string, any> }>(`/cmdb/ci?type_id=${refModel}&limit=200`)
      setRefOptions(s => ({ ...s, [refModel]: list }))
    } catch { /* 忽略, 下拉保持空 */ }
  }

  const currentModel = models.find(m => m.id === form.type_id)
  const fields = currentModel?.fields || []

  const setAttr = (name: string, v: any) =>
    setForm(f => ({ ...f, attributes: { ...f.attributes, [name]: v } }))

  const handleSubmit = async () => {
    if (!canWrite) { getMessage().error('无写入权限'); return }
    if (!form.name.trim()) { getMessage().error('请填写名称'); return }
    if (!form.type_id) { getMessage().error('请选择类型'); return }
    setSubmitting(true)
    try {
      // 合并结构化字段(attributes)与自定义属性(兜底), 二者隔离不互相覆盖
      const extra: Record<string, any> = {}
      for (const { key, value } of customAttrs) {
        const k = key.trim()
        if (!k) continue
        // 尝试按类型解析: 数字/布尔/JSON, 失败则按字符串
        if (value === 'true') extra[k] = true
        else if (value === 'false') extra[k] = false
        else if (value !== '' && !isNaN(Number(value))) extra[k] = Number(value)
        else {
          try { extra[k] = JSON.parse(value) } catch { extra[k] = value }
        }
      }
      const created = await apiFetch<{ id?: string }>('/cmdb/ci', {
        method: 'POST',
        body: JSON.stringify({ ...form, attributes: { ...form.attributes, ...extra }, channel: 100 }),
      })
      const ciID = created?.id
      // 2) 建立关系(按模型 relations 预选的目标)
      if (ciID && currentModel?.relations) {
        const rels: PendingRelation[] = []
        for (const def of currentModel.relations) {
          const target = pendingRels[def.relation_type]
          if (target) rels.push({ relation_type: def.relation_type, relation_id: def.id, target })
        }
        for (const r of rels) {
          try {
            await apiFetch('/cmdb/relation', {
              method: 'POST',
              body: JSON.stringify({ source_id: ciID, target_id: r.target, relation_type: r.relation_type }),
            })
          } catch (e: any) {
            getMessage().error(`关系「${r.relation_type}」建立失败: ${e.message}`)
          }
        }
      }
      getMessage().success('配置项创建成功')
      navigate('/cmdb/instances')
    } catch (e: any) {
      getMessage().error('创建失败: ' + e.message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="p-6 max-w-3xl mx-auto space-y-4">
      {/* 顶部操作栏: 返回 + 保存 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Link to="/cmdb/instances">
            <Button type="text" size="middle"><ChevronLeft className="h-4 w-4" />返回</Button>
          </Link>
          <h1 className="text-2xl font-bold">新建配置项</h1>
        </div>
        <Button onClick={handleSubmit} disabled={submitting || !canWrite}>
          <Save className="h-4 w-4 mr-1" />{submitting ? '保存中…' : '保存'}
        </Button>
      </div>

      <Card>
        <CardHeader><CardTitle className="text-base">基本信息</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <div>
            <label className="text-sm font-medium">名称 *</label>
            <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="如 web-01" />
          </div>
          <div>
            <label className="text-sm font-medium">类型</label>
            <Select
              className="w-full mt-1"
              value={form.type_id || undefined}
              onChange={t => {
                setForm(f => ({ ...f, type_id: t, attributes: {} }))
                setCustomAttrs([])
                const m = models.find(x => x.id === t)
                ;(m?.fields || []).forEach(f => { if (f.ref_model) ensureRefOptions(f.ref_model) })
              }}
              options={[{ value: '', label: '选择类型…' }, ...models.map(m => ({ value: m.id, label: m.name || m.id }))]}
            />
          </div>
          <div>
            <label className="text-sm font-medium">描述</label>
            <Input.TextArea
              rows={2}
              value={form.description}
              onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
          </div>
        </CardContent>
      </Card>

      {/* 模型字段(schema 驱动自动生成) */}
      <Card>
        <CardHeader><CardTitle className="text-base">模型字段（{form.type_id || '未选择类型'}）</CardTitle></CardHeader>
        <CardContent>
          {!form.type_id ? (
            <p className="text-sm text-muted-foreground">请先选择类型以加载字段。</p>
          ) : fields.length === 0 ? (
            <p className="text-sm text-muted-foreground">该模型未定义字段，可直接在底部填写自定义属性。</p>
          ) : (
            <div className="space-y-3">
              {fields.map(f => {
                if (f.type === 'ref' && f.ref_model) {
                  const opts = refOptions[f.ref_model] || []
                  return (
                    <div key={f.name}>
                      <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一）'}</label>
                      <Select
                        className="w-full mt-1"
                        value={(form.attributes[f.name] as string) || undefined}
                        onChange={v => {
                          setAttr(f.name, v)
                          ensureRefOptions(f.ref_model!)
                          if (f.autofill?.length) {
                            const target = opts.find(o => o.id === v)
                            const tattrs = target?.attributes
                            if (tattrs) {
                              setForm(fm => {
                                const attrs = { ...fm.attributes }
                                for (const k of f.autofill as string[]) {
                                  if (tattrs[k] !== undefined) attrs[k] = tattrs[k]
                                }
                                return { ...fm, attributes: attrs }
                              })
                            }
                          }
                        }}
                        options={[{ value: '', label: `选择 ${f.ref_model} 实例...` }, ...opts.map(o => ({ value: o.id, label: o.name || o.id }))]}
                      />
                      <p className="text-[11px] text-muted-foreground mt-0.5">引用 {f.ref_model} 模型实例（{f.description || ''}）</p>
                    </div>
                  )
                }
                if (f.type === 'user') {
                  return (
                    <div key={f.name}>
                      <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一）'}</label>
                      <Select className="w-full mt-1"
                        value={(form.attributes[f.name] as string) || undefined} onChange={v => setAttr(f.name, v)}
                        options={[{ value: '', label: '选择用户...' }, ...userOptions.map(u => ({ value: u.username, label: `${u.username}${u.email ? `（${u.email}）` : ''}` }))]}
                      />
                      <p className="text-[11px] text-muted-foreground mt-0.5">{f.description || '从平台用户列表选择（存用户名）'}</p>
                    </div>
                  )
                }
                if (f.type === 'enum' && f.options?.length) {
                  return (
                    <div key={f.name}>
                      <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一）'}</label>
                      <Select className="w-full mt-1"
                        value={(form.attributes[f.name] as string) || undefined} onChange={v => setAttr(f.name, v)}
                        options={[{ value: '', label: '选择...' }, ...(f.options || []).map(o => ({ value: o, label: o }))]}
                      />
                    </div>
                  )
                }
                if (f.type === 'multi-enum' && f.options?.length) {
                  const cur = Array.isArray(form.attributes[f.name]) ? (form.attributes[f.name] as string[]) : []
                  return (
                    <div key={f.name}>
                      <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一，不可重复）'}</label>
                      <div className="flex flex-wrap gap-x-3 gap-y-1 mt-1">
                        {(f.options || []).map(o => (
                          <label key={o} className="flex items-center gap-1 text-xs">
                            <input type="checkbox" checked={cur.includes(o)}
                              onChange={e => setAttr(f.name, e.target.checked ? [...cur, o] : cur.filter(x => x !== o))} className="rounded" />{o}
                          </label>
                        ))}
                      </div>
                    </div>
                  )
                }
                if (f.type === 'list') {
                  const cur = Array.isArray(form.attributes[f.name]) ? (form.attributes[f.name] as string[]).join(',') : ((form.attributes[f.name] as string) || '')
                  return (
                    <div key={f.name}>
                      <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一，不可重复）'}</label>
                      <Input className="mt-1 h-9 text-sm" value={cur}
                        onChange={e => setAttr(f.name, e.target.value.split(',').map(s => s.trim()).filter(Boolean))}
                        placeholder="逗号分隔，如 a,b,c" />
                    </div>
                  )
                }
                if (f.type === 'longtext') {
                  return (
                    <div key={f.name}>
                      <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一，不可重复）'}</label>
                      <Input.TextArea className="mt-1"
                        rows={3}
                        value={(form.attributes[f.name] as string) || ''} onChange={e => setAttr(f.name, e.target.value)} placeholder={f.description || ''} />
                    </div>
                  )
                }
                if (f.type === 'date') {
                  return (
                    <div key={f.name}>
                      <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一，不可重复）'}</label>
                      <input type="date" className="flex h-9 w-full rounded-md border border-input bg-background px-2 py-1 text-sm mt-1"
                        value={(form.attributes[f.name] as string) || ''} onChange={e => setAttr(f.name, e.target.value)} />
                    </div>
                  )
                }
                if (f.type === 'datetime') {
                  return (
                    <div key={f.name}>
                      <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一，不可重复）'}</label>
                      <input type="datetime-local" className="flex h-9 w-full rounded-md border border-input bg-background px-2 py-1 text-sm mt-1"
                        value={(form.attributes[f.name] as string) || ''} onChange={e => setAttr(f.name, e.target.value)} />
                    </div>
                  )
                }
                if (f.type === 'float' || f.type === 'number') {
                  return (
                    <div key={f.name}>
                      <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一，不可重复）'}</label>
                      <input type="number" step="any" className="flex h-9 w-full rounded-md border border-input bg-background px-2 py-1 text-sm mt-1"
                        value={(form.attributes[f.name] as any) ?? ''} onChange={e => setAttr(f.name, e.target.value === '' ? '' : Number(e.target.value))} placeholder={f.description || ''} />
                    </div>
                  )
                }
                return (
                  <div key={f.name}>
                    <label className="text-sm font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一，不可重复）'}</label>
                    <Input className="mt-1 h-9 text-sm" value={(form.attributes[f.name] as string) || ''}
                      onChange={e => setAttr(f.name, e.target.value)} placeholder={f.description || ''} />
                  </div>
                )
              })}
            </div>
          )}
        </CardContent>
      </Card>

      {/* 关系预选(按模型 relations 声明) */}
      {currentModel?.relations && currentModel.relations.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base flex items-center gap-2"><Network className="h-4 w-4" />关联资源（可选）</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {currentModel.relations.map(def => {
              const opts = refOptions[def.target_model] || []
                  return (
                    <div key={def.relation_type}>
                      <label className="text-sm font-medium">{def.name}</label>
                      <Select
                        className="w-full mt-1"
                        value={pendingRels[def.relation_type] || undefined}
                        onChange={v => { setPendingRels(r => ({ ...r, [def.relation_type]: v })); ensureRefOptions(def.target_model) }}
                        options={[{ value: '', label: '不建立（稍后在详情页添加）' }, ...opts.map(o => ({ value: o.id, label: o.name || o.id }))]}
                      />
                  <p className="text-[11px] text-muted-foreground mt-0.5">{def.description || '保存实例时一并建立该关系'}</p>
                </div>
              )
            })}
          </CardContent>
        </Card>
      )}

      {/* 自定义属性 / 标签兜底 */}
      <Card>
        <CardHeader><CardTitle className="text-base">高级</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <div>
            <div className="flex items-center justify-between">
              <label className="text-sm font-medium">自定义属性</label>
              <Button type="default" size="middle" onClick={() => setCustomAttrs(a => [...a, { key: '', value: '' }])}>
                添加属性
              </Button>
            </div>
            <p className="text-[11px] text-muted-foreground mt-0.5 mb-2">用于录入模型未定义的自由字段，与上方结构化字段互不影响。</p>
            {customAttrs.length === 0 ? (
              <p className="text-[11px] text-muted-foreground border border-dashed rounded-md px-3 py-4 text-center">暂无自定义属性，点击「添加属性」新增。</p>
            ) : (
              <div className="space-y-2">
                {customAttrs.map((item, idx) => (
                  <div key={idx} className="flex items-center gap-2">
                    <Input
                      className="w-2/5 font-mono"
                      value={item.key}
                      onChange={e => setCustomAttrs(a => a.map((x, i) => i === idx ? { ...x, key: e.target.value } : x))}
                      placeholder="属性名" />
                    <Input
                      className="flex-1"
                      value={item.value}
                      onChange={e => setCustomAttrs(a => a.map((x, i) => i === idx ? { ...x, value: e.target.value } : x))}
                      placeholder="属性值（自动识别数字/布尔/JSON）" />
                    <Button type="text" icon={<span className="text-muted-foreground">×</span>} onClick={() => setCustomAttrs(a => a.filter((_, i) => i !== idx))} />
                  </div>
                ))}
              </div>
            )}
          </div>
          <div>
            <label className="text-sm font-medium">标签 (key=value, 逗号分隔)</label>
            <Input value={Object.entries(form.labels || {}).map(([k, v]) => `${k}=${v}`).join(',')}
              onChange={e => {
                const labels: Record<string, string> = {}
                e.target.value.split(',').forEach(pair => {
                  const [k, v] = pair.split('=')
                  if (k && v) labels[k.trim()] = v.trim()
                })
                setForm(f => ({ ...f, labels }))
              }}
              placeholder="env=prod,region=us-east-1" />
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
