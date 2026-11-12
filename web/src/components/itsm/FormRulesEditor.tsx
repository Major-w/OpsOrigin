
// FormRulesEditor —— 表单级「UI 联动规则」与「跨字段组合校验」编辑器。
// 与 FormDesigner 配套：FormDesigner 管字段布局，本组件管 schema.behavior_rules / combo_rules。
// 条件求值内核复用 DynamicForm 的 evalCondition（与后端 form 包语义一致）。

import { useEffect, useState } from 'react'
import { Button, Input, Select } from 'antd'
import { ArrowDown, ArrowUp, ChevronDown, ChevronRight, Copy, Package, Play, Plus, Trash2 } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import {
  CONDITION_OPS,
  type BehaviorRule,
  type BehaviorAction,
  type ComboRule,
  type DataLinkRule,
  type DecisionTable,
  type DecisionRow,
  type DecisionTableAssetRef,
  type FormCondition,
  type FormSchema,
  type FormFieldDef,
} from './DynamicForm'

interface AssetOption {
  id: string
  name: string
  status: string
  version: number
  table?: DecisionTable
}

const ACTION_OPTIONS: { value: BehaviorAction; label: string; hint: string }[] = [
  { value: 'show', label: '显示', hint: '条件命中时显示目标字段' },
  { value: 'hide', label: '隐藏', hint: '条件命中时隐藏目标字段（必填随之失效）' },
  { value: 'required', label: '设为必填', hint: '条件命中时目标字段必填' },
  { value: 'optional', label: '设为选填', hint: '条件命中时解除目标字段必填' },
  { value: 'readonly', label: '设为只读', hint: '条件命中时目标字段只读' },
  { value: 'editable', label: '设为可编辑', hint: '条件命中时解除目标字段只读' },
]

const ACTION_LABEL: Record<string, string> = Object.fromEntries(ACTION_OPTIONS.map(a => [a.value, a.label]))

function emptyCondition(): FormCondition {
  return { field: '', op: 'eq', value: '' }
}

function ConditionRows({ conditions, logic, fields, onChange }: {
  conditions: FormCondition[]
  logic: 'and' | 'or' | undefined
  fields: FormFieldDef[]
  onChange: (next: { conditions?: FormCondition[]; logic?: 'and' | 'or' }) => void
}) {
  const update = (i: number, patch: Partial<FormCondition>) => {
    onChange({ conditions: conditions.map((c, j) => (j === i ? { ...c, ...patch } : c)) })
  }
  return (
    <div className="space-y-1.5">
      {conditions.map((c, i) => (
        <div key={i} className="flex items-center gap-1.5">
          {i === 0 && <span className="text-[10px] text-muted-foreground w-8 shrink-0">当</span>}
          {i > 0 && (
            <Select size="small" className="w-16 shrink-0" value={logic || 'and'}
              onChange={v => onChange({ logic: v })}
              options={[{ value: 'and', label: '且' }, { value: 'or', label: '或' }]} />
          )}
          <Select size="small" className="min-w-28 flex-1" placeholder="选择字段" showSearch optionFilterProp="label"
            value={c.field || undefined}
            onChange={v => update(i, { field: v })}
            options={fields.map(f => ({ value: f.key, label: `${f.label || f.key} (${f.key})` }))} />
          <Select size="small" className="w-32 shrink-0" value={c.op || 'eq'}
            onChange={v => update(i, { op: v })}
            options={CONDITION_OPS.map(o => ({ value: o.value, label: o.label }))} />
          {CONDITION_OPS.find(o => o.value === (c.op || 'eq'))?.needValue !== false && (
            <Input size="small" className="w-28" placeholder="值" value={c.value}
              onChange={e => update(i, { value: e.target.value })} />
          )}
          <button type="button" className="text-muted-foreground hover:text-destructive shrink-0"
            onClick={() => onChange({ conditions: conditions.filter((_, j) => j !== i) })}
            aria-label="删除条件">
            <Trash2 className="h-3 w-3" />
          </button>
        </div>
      ))}
      <Button size="small" type="dashed" className="h-6 text-[11px]"
        onClick={() => onChange({ conditions: [...conditions, emptyCondition()] })}>
        <Plus className="h-3 w-3 mr-0.5" />加条件
      </Button>
    </div>
  )
}

function TargetPicker({ targets, fields, onChange }: {
  targets: string[]
  fields: FormFieldDef[]
  onChange: (next: string[]) => void
}) {
  const toggle = (key: string) => {
    onChange(targets.includes(key) ? targets.filter(k => k !== key) : [...targets, key])
  }
  return (
    <div className="flex flex-wrap gap-1">
      {fields.map(f => {
        const on = targets.includes(f.key)
        return (
          <button type="button" key={f.key} onClick={() => toggle(f.key)}
            className={`text-[10px] px-2 py-1 rounded border transition-colors ${on ? 'bg-primary text-primary-foreground border-primary' : 'border-input text-muted-foreground hover:bg-muted/40'}`}>
            {f.label || f.key}
          </button>
        )
      })}
      {fields.length === 0 && <span className="text-[10px] text-muted-foreground">表单还没有字段</span>}
    </div>
  )
}

interface Props {
  schema: FormSchema
  onChange: (schema: FormSchema) => void
}

export function FormRulesEditor({ schema, onChange }: Props) {
  const [openBehavior, setOpenBehavior] = useState(false)
  const [openCombo, setOpenCombo] = useState(false)
  const [openLink, setOpenLink] = useState(false)
  const [openDecision, setOpenDecision] = useState(false)
  const behaviorRules = schema.behavior_rules || []
  const comboRules = schema.combo_rules || []
  const dataLinks = schema.data_links || []
  const decisionTables = schema.decision_tables || []
  // 可选字段 = 全部字段（含内置），联动/校验经常要引用 risk_level 等系统字段
  const fields = schema.fields || []
  const nameOf = (key: string) => fields.find(f => f.key === key)?.label || key
  const fieldOptions = fields.map(f => ({ value: f.key, label: `${f.label || f.key} (${f.key})` }))

  const setBehavior = (rules: BehaviorRule[]) => onChange({ ...schema, behavior_rules: rules })
  const setCombo = (rules: ComboRule[]) => onChange({ ...schema, combo_rules: rules })
  const setDataLinks = (rules: DataLinkRule[]) => onChange({ ...schema, data_links: rules })
  const setDecisionTables = (tables: DecisionTable[]) => onChange({ ...schema, decision_tables: tables })

  // 决策表试算：直接把当前草稿 schema + 样例值送后端走与提交时相同的推导通道。
  const [previewOpen, setPreviewOpen] = useState(false)
  const [previewValues, setPreviewValues] = useState<Record<string, string>>({})
  const [previewResult, setPreviewResult] = useState<{
    values: Record<string, unknown>
    fired: string[]
    warnings: string[]
  } | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)

  // ---- 决策表资产引用 ----
  // schema 落库只保存 asset_ref 标记；内容由后端按已发布版本展开（GET schema / 试算 / 提交）。
  // 设计器里引用态只读，避免把展开快照当成本表单内容编辑（保存会被后端归一剥离）。
  const [assetOptions, setAssetOptions] = useState<AssetOption[]>([])
  const [assetLoading, setAssetLoading] = useState(false)
  const [refPicker, setRefPicker] = useState<string | undefined>(undefined)
  const [refVersions, setRefVersions] = useState<Record<string, number[]>>({})

  const loadAssets = async () => {
    setAssetLoading(true)
    try {
      const list = await apiList<AssetOption>('/itsm/decision-tables')
      setAssetOptions((list || []).filter(a => a.status === 'published'))
    } catch (e) {
      getMessage().error(`加载决策表资产失败: ${String(e)}`)
    } finally {
      setAssetLoading(false)
    }
  }
  useEffect(() => { loadAssets() }, [])

  const loadVersions = async (id: string) => {
    if (refVersions[id]) return
    try {
      const list = await apiList<{ version: number }>(`/itsm/decision-tables/${id}/versions`)
      setRefVersions(v => ({ ...v, [id]: (list || []).map(x => x.version).sort((a, b) => b - a) }))
    } catch {
      setRefVersions(v => ({ ...v, [id]: [] }))
    }
  }

  const addAssetRef = async (id: string) => {
    setRefPicker(undefined)
    if (!id) return
    const meta = assetOptions.find(a => a.id === id)
    let table = meta?.table
    try {
      const detail = await apiFetch<{ table: DecisionTable }>(`/itsm/decision-tables/${id}`)
      if (detail?.table) table = detail.table
    } catch { /* 列表缓存兜底 */ }
    setDecisionTables([...decisionTables, {
      name: meta?.name || '决策表资产',
      condition_keys: table?.condition_keys || [],
      output_keys: table?.output_keys || [],
      rows: table?.rows || [],
      asset_ref: { asset_id: id, version: 0 },
    }])
    loadVersions(id)
    getMessage().success('已引用资产；保存后按已发布版本实时展开')
  }

  const setRefVersion = async (i: number, version: number) => {
    const dt = decisionTables[i]
    const ref: DecisionTableAssetRef = { asset_id: dt.asset_ref!.asset_id, version }
    let next: DecisionTable = { ...dt, asset_ref: ref }
    try {
      if (version > 0) {
        const list = await apiList<{ version: number; table: DecisionTable }>(
          `/itsm/decision-tables/${ref.asset_id}/versions`)
        const v = (list || []).find(x => x.version === version)
        if (v?.table) next = { ...next, ...v.table }
      } else {
        const detail = await apiFetch<{ table: DecisionTable }>(`/itsm/decision-tables/${ref.asset_id}`)
        if (detail?.table) next = { ...next, ...detail.table }
      }
    } catch (e) {
      getMessage().error(`读取版本内容失败: ${String(e)}`)
    }
    setDecisionTables(decisionTables.map((x, j) => (j === i ? next : x)))
  }

  const previewFields = Array.from(new Set(decisionTables.flatMap(dt => dt.condition_keys || [])))
  const runPreview = async () => {
    setPreviewLoading(true)
    try {
      const res = await apiFetch<{ values: Record<string, unknown>; fired: string[]; warnings: string[] }>(
        '/itsm/form/rules-preview',
        { method: 'POST', body: JSON.stringify({ schema, values: previewValues }) },
      )
      setPreviewResult({
        values: res.values || {},
        fired: res.fired || [],
        warnings: res.warnings || [],
      })
    } catch (e) {
      setPreviewResult({ values: {}, fired: [], warnings: [`试算失败: ${String(e)}`] })
    } finally {
      setPreviewLoading(false)
    }
  }

  // 行顺序决定「首个命中」，因此提供上移/下移/复制/插入/删除本行。
  const moveRow = (dt: DecisionTable, i: number, ri: number, to: number) => {
    const rows = [...(dt.rows || [])]
    if (to < 0 || to >= rows.length) return
    const [r] = rows.splice(ri, 1)
    rows.splice(to, 0, r)
    setDecisionTables(decisionTables.map((x, j) => (j === i ? { ...x, rows } : x)))
  }
  const duplicateRow = (dt: DecisionTable, i: number, ri: number) => {
    const rows = [...(dt.rows || [])]
    const src = rows[ri]
    const copy: DecisionRow = { cells: { ...(src.cells || {}) }, outputs: { ...(src.outputs || {}) } }
    if (src.enabled !== undefined) copy.enabled = src.enabled
    rows.splice(ri + 1, 0, copy) // 不带 id，避免复制行与源行 React key 冲突
    setDecisionTables(decisionTables.map((x, j) => (j === i ? { ...x, rows } : x)))
  }
  const deleteRow = (dt: DecisionTable, i: number, ri: number) => {
    const rows = (dt.rows || []).filter((_, j) => j !== ri)
    setDecisionTables(decisionTables.map((x, j) => (j === i ? { ...x, rows } : x)))
  }

  return (
    <div className="space-y-2">
      {/* —— UI 联动规则 —— */}
      <div className="border rounded-md">
        <button type="button" className="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium hover:bg-muted/40 rounded-t-md"
          onClick={() => setOpenBehavior(v => !v)}>
          {openBehavior ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          UI 联动规则
          <span className="text-[10px] font-normal text-muted-foreground">
            按字段值自动控制其他字段的显示/隐藏/必填/只读（{behaviorRules.length ? `${behaviorRules.length} 条` : '未配置'}）
          </span>
        </button>
        {openBehavior && (
          <div className="px-3 pb-3 space-y-3 border-t pt-3">
            <p className="text-[11px] text-muted-foreground">
              典型用法：变更类型 = 紧急 → 回滚方案 设为必填。规则按顺序叠加，后声明的覆盖先声明的。
            </p>
            {behaviorRules.map((r, i) => (
              <div key={r.id || i} className="rounded-md border bg-muted/20 p-2.5 space-y-2">
                <div className="flex items-center gap-2">
                  <span className="text-[11px] font-medium text-muted-foreground">规则 {i + 1}</span>
                  <Input size="small" className="h-6 flex-1 text-[11px]" placeholder="规则名称（可选）"
                    value={r.name || ''} onChange={e => setBehavior(behaviorRules.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
                  <button type="button" className="text-muted-foreground hover:text-destructive"
                    onClick={() => setBehavior(behaviorRules.filter((_, j) => j !== i))} aria-label="删除规则">
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
                <ConditionRows
                  conditions={r.conditions?.length ? r.conditions : [emptyCondition()]}
                  logic={r.logic}
                  fields={fields}
                  onChange={next => setBehavior(behaviorRules.map((x, j) => (j === i ? { ...x, ...next } : x)))} />
                <div className="flex items-center gap-2">
                  <span className="text-[10px] text-muted-foreground w-8 shrink-0">则</span>
                  <Select size="small" className="w-32" value={r.action || 'hide'}
                    onChange={v => setBehavior(behaviorRules.map((x, j) => (j === i ? { ...x, action: v } : x)))}
                    options={ACTION_OPTIONS.map(a => ({ value: a.value, label: a.label }))} />
                  <span className="text-[10px] text-muted-foreground">{ACTION_OPTIONS.find(a => a.value === (r.action || 'hide'))?.hint}</span>
                </div>
                <div className="flex items-start gap-2">
                  <span className="text-[10px] text-muted-foreground w-8 shrink-0 pt-1">目标</span>
                  <TargetPicker
                    targets={r.targets || []}
                    fields={fields}
                    onChange={next => setBehavior(behaviorRules.map((x, j) => (j === i ? { ...x, targets: next } : x)))} />
                </div>
                <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground cursor-pointer">
                  <input type="checkbox" className="h-3 w-3" checked={!!r.revert}
                    onChange={e => setBehavior(behaviorRules.map((x, j) => (j === i ? { ...x, revert: e.target.checked } : x)))} />
                  条件不满足时自动恢复默认态（Reverse if false，对齐 SN UI Policy）
                </label>
              </div>
            ))}
            <Button size="small" type="dashed" className="w-full"
              onClick={() => setBehavior([...behaviorRules, { name: '', conditions: [emptyCondition()], logic: 'and', action: 'hide', targets: [] }])}>
              <Plus className="h-3.5 w-3.5 mr-1" />添加联动规则
            </Button>
          </div>
        )}
      </div>

      {/* —— 跨字段组合校验 —— */}
      <div className="border rounded-md">
        <button type="button" className="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium hover:bg-muted/40 rounded-t-md"
          onClick={() => setOpenCombo(v => !v)}>
          {openCombo ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          组合校验
          <span className="text-[10px] font-normal text-muted-foreground">
            跨字段条件校验，阻断或提示（{comboRules.length ? `${comboRules.length} 条` : '未配置'}）
          </span>
        </button>
        {openCombo && (
          <div className="px-3 pb-3 space-y-3 border-t pt-3">
            <p className="text-[11px] text-muted-foreground">
              典型用法：变更类型 = 紧急 且 回滚方案为空 → 阻断并提示「紧急变更必须填写回滚方案」。前后端双重校验。
            </p>
            {comboRules.map((r, i) => (
              <div key={r.id || i} className="rounded-md border bg-muted/20 p-2.5 space-y-2">
                <div className="flex items-center gap-2">
                  <span className="text-[11px] font-medium text-muted-foreground">规则 {i + 1}</span>
                  <Select size="small" className="w-24" value={r.severity || 'block'}
                    onChange={v => setCombo(comboRules.map((x, j) => (j === i ? { ...x, severity: v } : x)))}
                    options={[{ value: 'block', label: '阻断提交' }, { value: 'warn', label: '仅提示' }]} />
                  <button type="button" className="text-muted-foreground hover:text-destructive ml-auto"
                    onClick={() => setCombo(comboRules.filter((_, j) => j !== i))} aria-label="删除规则">
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
                <ConditionRows
                  conditions={r.conditions?.length ? r.conditions : [emptyCondition()]}
                  logic={r.logic}
                  fields={fields}
                  onChange={next => setCombo(comboRules.map((x, j) => (j === i ? { ...x, ...next } : x)))} />
                <div className="flex items-center gap-2">
                  <span className="text-[10px] text-muted-foreground w-8 shrink-0">提示</span>
                  <Input size="small" className="h-7 text-[11px]" placeholder="命中时的提示文案"
                    value={r.message || ''} onChange={e => setCombo(comboRules.map((x, j) => (j === i ? { ...x, message: e.target.value } : x)))} />
                </div>
              </div>
            ))}
            <Button size="small" type="dashed" className="w-full"
              onClick={() => setCombo([...comboRules, { name: '', conditions: [emptyCondition()], logic: 'and', message: '', severity: 'block' }])}>
              <Plus className="h-3.5 w-3.5 mr-1" />添加组合校验
            </Button>
          </div>
        )}
      </div>

      {/* —— 数据联动（选 A 自动带出 B） —— */}
      <div className="border rounded-md">
        <button type="button" className="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium hover:bg-muted/40 rounded-t-md"
          onClick={() => setOpenLink(v => !v)}>
          {openLink ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          数据联动（自动带出）
          <span className="text-[10px] font-normal text-muted-foreground">
            按字段值自动填充其他字段（{dataLinks.length ? `${dataLinks.length} 条` : '未配置'}）
          </span>
        </button>
        {openLink && (
          <div className="px-3 pb-3 space-y-3 border-t pt-3">
            <p className="text-[11px] text-muted-foreground">
              典型用法：环境 = 生产 → 影响范围 自动置为「全局」；选择配置项 → 受影响系统 复制其名称。字段变更即触发，后端校验同样先推导。
            </p>
            {dataLinks.map((r, i) => (
              <div key={r.id || i} className="rounded-md border bg-muted/20 p-2.5 space-y-2">
                <div className="flex items-center gap-2">
                  <span className="text-[11px] font-medium text-muted-foreground">规则 {i + 1}</span>
                  <Input size="small" className="h-6 flex-1 text-[11px]" placeholder="规则名称（可选）"
                    value={r.name || ''} onChange={e => setDataLinks(dataLinks.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
                  <button type="button" className="text-muted-foreground hover:text-destructive"
                    onClick={() => setDataLinks(dataLinks.filter((_, j) => j !== i))} aria-label="删除规则">
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
                <ConditionRows
                  conditions={r.conditions?.length ? r.conditions : [emptyCondition()]}
                  logic={r.logic}
                  fields={fields}
                  onChange={next => setDataLinks(dataLinks.map((x, j) => (j === i ? { ...x, ...next } : x)))} />
                <div className="space-y-1.5">
                  {(r.sets || []).map((s, si) => {
                    const mode = s.mode || 'fixed'
                    const updateSet = (patch: Partial<typeof s>) =>
                      setDataLinks(dataLinks.map((x, j) => (j === i ? { ...x, sets: (x.sets || []).map((y, sj) => (sj === si ? { ...y, ...patch } : y)) } : x)))
                    return (
                      <div key={si} className="flex items-center gap-1.5">
                        {si === 0 && <span className="text-[10px] text-muted-foreground w-8 shrink-0">则</span>}
                        {si > 0 && <span className="w-8 shrink-0" />}
                        <Select size="small" className="min-w-28 flex-1" placeholder="目标字段" showSearch optionFilterProp="label"
                          value={s.target || undefined} onChange={v => updateSet({ target: v })} options={fieldOptions} />
                        <Select size="small" className="w-28 shrink-0" value={mode}
                          onChange={v => updateSet({
                            mode: v,
                            value: '',
                            ci_attr: v === 'ci_attr' ? (s.ci_attr || { ci_type: '', name_field: '' }) : undefined,
                            submitter: v === 'submitter_attr' ? (s.submitter || { attr: 'department' }) : undefined,
                          })}
                          options={[{ value: 'fixed', label: '固定为' }, { value: 'copy_from', label: '复制字段' }, { value: 'ci_attr', label: 'CI属性' }, { value: 'submitter_attr', label: '提单人属性' }]} />
                        {mode === 'fixed' ? (
                          <Input size="small" className="w-36 shrink-0" placeholder="写入值" value={s.value || ''}
                            onChange={e => updateSet({ value: e.target.value })} />
                        ) : mode === 'copy_from' ? (
                          <Select size="small" className="w-36 shrink-0" placeholder="来源字段" showSearch optionFilterProp="label"
                            value={s.value || undefined} onChange={v => updateSet({ value: v })} options={fieldOptions} />
                        ) : mode === 'submitter_attr' ? (
                          <div className="flex gap-1.5 flex-1">
                            <Select size="small" className="flex-1" placeholder="提单人属性" value={s.value || s.submitter?.attr || undefined}
                              onChange={v => updateSet({ value: v, submitter: { ...(s.submitter || {}), attr: v } })}
                              options={[{ value: 'department', label: '部门(department)' }, { value: 'email', label: '邮箱(email)' }]} />
                            <Input size="small" className="flex-1" placeholder="兜底值(可选)" value={s.submitter?.fallback || ''}
                              onChange={e => updateSet({ submitter: { ...(s.submitter || { attr: s.value || 'department' }), fallback: e.target.value } })} />
                          </div>
                        ) : (
                          <div className="flex flex-col gap-1 flex-1">
                            <div className="flex gap-1.5">
                              <Input size="small" className="flex-1" placeholder="CMDB 属性(如 owner)" value={s.value || ''}
                                onChange={e => updateSet({ value: e.target.value })} />
                              <Input size="small" className="flex-1" placeholder="模型类型名(如 门店)" value={s.ci_attr?.ci_type || ''}
                                onChange={e => updateSet({ ci_attr: { ...(s.ci_attr || { name_field: '' }), ci_type: e.target.value } })} />
                            </div>
                            <div className="flex gap-1.5">
                              <Select size="small" className="flex-1" placeholder="名称来源字段(其值=CI名)" showSearch optionFilterProp="label"
                                value={s.ci_attr?.name_field || undefined} onChange={v => updateSet({ ci_attr: { ...(s.ci_attr || { ci_type: '' }), name_field: v } })} options={fieldOptions} />
                              <Input size="small" className="flex-1" placeholder="兜底值(可选)" value={s.ci_attr?.fallback || ''}
                                onChange={e => updateSet({ ci_attr: { ...(s.ci_attr || { ci_type: '', name_field: '' }), fallback: e.target.value } })} />
                            </div>
                          </div>
                        )}
                        <button type="button" className="text-muted-foreground hover:text-destructive shrink-0"
                          onClick={() => setDataLinks(dataLinks.map((x, j) => (j === i ? { ...x, sets: (x.sets || []).filter((_, sj) => sj !== si) } : x)))} aria-label="删除赋值">
                          <Trash2 className="h-3 w-3" />
                        </button>
                      </div>
                    )
                  })}
                  <Button size="small" type="dashed" className="h-6 text-[11px]"
                    onClick={() => setDataLinks(dataLinks.map((x, j) => (j === i ? { ...x, sets: [...(x.sets || []), { target: '', mode: 'fixed' as const, value: '' }] } : x)))}>
                    <Plus className="h-3 w-3 mr-0.5" />加赋值
                  </Button>
                </div>
              </div>
            ))}
            <Button size="small" type="dashed" className="w-full"
              onClick={() => setDataLinks([...dataLinks, { name: '', conditions: [emptyCondition()], logic: 'and', sets: [{ target: '', mode: 'fixed', value: '' }] }])}>
              <Plus className="h-3.5 w-3.5 mr-1" />添加数据联动
            </Button>
          </div>
        )}
      </div>

      {/* —— 决策表 —— */}
      <div className="border rounded-md">
        <button type="button" className="w-full flex items-center gap-2 px-3 py-2 text-sm font-medium hover:bg-muted/40 rounded-t-md"
          onClick={() => setOpenDecision(v => !v)}>
          {openDecision ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          决策表
          <span className="text-[10px] font-normal text-muted-foreground">
            条件列 × 输出列矩阵，首个命中行生效（{decisionTables.length ? `${decisionTables.length} 张` : '未配置'}）
          </span>
        </button>
        {openDecision && (
          <div className="px-3 pb-3 space-y-3 border-t pt-3">
            <p className="text-[11px] text-muted-foreground">
              典型用法：环境 ∈ 生产,预发 且 影响范围 = 全局 → 级别=P1、生效时间=非工作时间。单元格留空表示不限；多个候选值用英文逗号分隔。自上而下首个命中行生效，✓/✕ 启停行，↑↓ 调整行序。输出列支持 {'${字段名}'} 插值（如 {'${门店}-${工种}-l1'}），可拼出动态组名。
            </p>
            {decisionTables.map((dt, i) => {
              // 资产引用态：内容由后端展开，本表单只持有引用标记 → 只读展示 + 版本模式 + 转内嵌。
              if (dt.asset_ref) {
                const ref = dt.asset_ref
                const meta = assetOptions.find(a => a.id === ref.asset_id)
                const versions = refVersions[ref.asset_id] || []
                return (
                  <div key={dt.id || i} className="rounded-md border bg-muted/20 p-2.5 space-y-2">
                    <div className="flex items-center gap-2 flex-wrap">
                      <Package className="h-3.5 w-3.5 text-primary shrink-0" />
                      <span className="text-[11px] font-medium truncate">{meta?.name || dt.name || ref.asset_id}</span>
                      <span className="text-[10px] px-1.5 py-0.5 rounded border text-muted-foreground">资产引用</span>
                      <div className="ml-auto flex items-center gap-1">
                        <Select size="small" className="w-36 text-[11px]"
                          value={ref.version || 0}
                          onOpenChange={open => { if (open) loadVersions(ref.asset_id) }}
                          onChange={v => setRefVersion(i, v)}
                          options={[
                            { value: 0, label: '跟随最新版' },
                            ...versions.filter(v => v !== 0).map(v => ({ value: v, label: `锁定 v${v}` })),
                          ]} />
                        <Button size="small" type="text" className="text-[11px] h-6"
                          title="把当前展开快照复制成本表单内嵌决策表（此后不再跟随资产升级）"
                          onClick={() => setDecisionTables(decisionTables.map((x, j) =>
                            j === i ? { ...x, asset_ref: undefined } : x))}>
                          <Copy className="h-3 w-3 mr-0.5" />转内嵌
                        </Button>
                        <button type="button" className="text-muted-foreground hover:text-destructive"
                          onClick={() => setDecisionTables(decisionTables.filter((_, j) => j !== i))} aria-label="移除引用">
                          <Trash2 className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    </div>
                    <p className="text-[10px] text-muted-foreground">
                      由已发布版本实时展开，本表单不保存行矩阵；
                      {(dt.condition_keys || []).length} 条件列 / {(dt.output_keys || []).length} 输出列 / {(dt.rows || []).length} 行。
                      {!meta && <span className="text-amber-600 dark:text-amber-400">资产可能已下线或当前无权限，请检查。</span>}
                    </p>
                    <div className="flex flex-wrap gap-1">
                      {(dt.condition_keys || []).map(k => <span key={k} className="text-[10px] px-1.5 py-0.5 rounded border">{k}</span>)}
                      {(dt.output_keys || []).map(k => <span key={k} className="text-[10px] px-1.5 py-0.5 rounded border text-blue-600 dark:text-blue-400">{k}</span>)}
                    </div>
                  </div>
                )
              }
              const update = (patch: Partial<DecisionTable>) => setDecisionTables(decisionTables.map((x, j) => (j === i ? { ...x, ...patch } : x)))
              const ckeys = dt.condition_keys || []
              const okeys = dt.output_keys || []
              return (
                <div key={dt.id || i} className="rounded-md border bg-muted/20 p-2.5 space-y-2">
                  <div className="flex items-center gap-2">
                    <span className="text-[11px] font-medium text-muted-foreground">表 {i + 1}</span>
                    <Input size="small" className="h-6 flex-1 text-[11px]" placeholder="决策表名称（可选）"
                      value={dt.name || ''} onChange={e => update({ name: e.target.value })} />
                    <button type="button" className="text-muted-foreground hover:text-destructive"
                      onClick={() => setDecisionTables(decisionTables.filter((_, j) => j !== i))} aria-label="删除决策表">
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>
                  <div className="flex items-start gap-2">
                    <span className="text-[10px] text-muted-foreground w-14 shrink-0 pt-1">条件列</span>
                    <TargetPicker targets={ckeys} fields={fields} onChange={next => update({ condition_keys: next })} />
                  </div>
                  <div className="flex items-start gap-2">
                    <span className="text-[10px] text-muted-foreground w-14 shrink-0 pt-1">输出列</span>
                    <TargetPicker targets={okeys} fields={fields} onChange={next => update({ output_keys: next })} />
                  </div>
                  <div className="overflow-x-auto border rounded-md bg-background">
                    <table className="w-full text-xs">
                      <thead>
                        <tr className="border-b bg-muted/40 text-muted-foreground">
                          {ckeys.map(k => <th key={k} className="px-2 py-1.5 text-left font-medium whitespace-nowrap">{nameOf(k)}</th>)}
                          {okeys.map(k => <th key={k} className="px-2 py-1.5 text-left font-medium whitespace-nowrap text-blue-600 dark:text-blue-400">{nameOf(k)}</th>)}
                          <th className="w-24 px-1 py-1.5 text-center font-medium whitespace-nowrap">行操作</th>
                        </tr>
                      </thead>
                      <tbody>
                        {(dt.rows || []).map((row, ri) => {
                          const updateRow = (patch: Partial<DecisionRow>) => update({ rows: (dt.rows || []).map((y, rj) => (rj === ri ? { ...y, ...patch } : y)) })
                          const enabled = row.enabled !== false
                          return (
                            <tr key={row.id || ri} className={`border-b last:border-b-0 ${enabled ? '' : 'opacity-40'}`}>
                              {ckeys.map(k => (
                                <td key={k} className="px-1 py-1">
                                  <Input size="small" variant="borderless" className="h-7 text-[11px]" placeholder="不限"
                                    value={row.cells?.[k] || ''}
                                    onChange={e => updateRow({ cells: { ...(row.cells || {}), [k]: e.target.value } })} />
                                </td>
                              ))}
                              {okeys.map(k => (
                                <td key={k} className="px-1 py-1">
                                  <Input size="small" variant="borderless" className="h-7 text-[11px]" placeholder="—"
                                    value={row.outputs?.[k] || ''}
                                    onChange={e => updateRow({ outputs: { ...(row.outputs || {}), [k]: e.target.value } })} />
                                </td>
                              ))}
                              <td className="px-1 text-center whitespace-nowrap">
                                <div className="flex items-center justify-center gap-0.5">
                                  <button type="button" title="上移（首个命中行优先）" disabled={ri === 0}
                                    className="text-muted-foreground hover:text-primary disabled:opacity-30"
                                    onClick={() => moveRow(dt, i, ri, ri - 1)}><ArrowUp className="h-3 w-3" /></button>
                                  <button type="button" title="下移" disabled={ri === (dt.rows || []).length - 1}
                                    className="text-muted-foreground hover:text-primary disabled:opacity-30"
                                    onClick={() => moveRow(dt, i, ri, ri + 1)}><ArrowDown className="h-3 w-3" /></button>
                                  <button type="button" title="复制该行到下方"
                                    className="text-muted-foreground hover:text-primary"
                                    onClick={() => duplicateRow(dt, i, ri)}><Copy className="h-3 w-3" /></button>
                                  <button type="button" title={enabled ? '停用该行' : '启用该行'}
                                    className={`text-xs ${enabled ? 'text-blue-600 dark:text-blue-400' : 'text-muted-foreground'}`}
                                    onClick={() => updateRow({ enabled: !enabled })}>
                                    {enabled ? '✓' : '✕'}
                                  </button>
                                  <button type="button" title="删除本行"
                                    className="text-muted-foreground hover:text-destructive"
                                    onClick={() => deleteRow(dt, i, ri)}><Trash2 className="h-3 w-3" /></button>
                                </div>
                              </td>
                            </tr>
                          )
                        })}
                        {(dt.rows || []).length === 0 && (
                          <tr><td colSpan={ckeys.length + okeys.length + 1} className="px-2 py-2 text-center text-muted-foreground italic">暂无行</td></tr>
                        )}
                      </tbody>
                    </table>
                  </div>
                  <div className="flex gap-2">
                    <Button size="small" type="dashed" className="h-6 text-[11px]"
                      onClick={() => update({ rows: [...(dt.rows || []), { cells: {}, outputs: {} }] })}>
                      <Plus className="h-3 w-3 mr-0.5" />加行
                    </Button>
                    {(dt.rows || []).length > 0 && (
                      <Button size="small" type="dashed" className="h-6 text-[11px]"
                        onClick={() => update({ rows: (dt.rows || []).slice(0, -1) })}>
                        <Trash2 className="h-3 w-3 mr-0.5" />删末行
                      </Button>
                    )}
                  </div>
                </div>
              )
            })}
            <div className="flex gap-2">
              <Button size="small" type="dashed" className="flex-1"
                onClick={() => setDecisionTables([...decisionTables, { name: '', condition_keys: [], output_keys: [], rows: [{ cells: {}, outputs: {} }] }])}>
                <Plus className="h-3.5 w-3.5 mr-1" />添加内嵌决策表
              </Button>
              <Select size="small" className="flex-1" placeholder="引用决策表资产（已发布）"
                value={refPicker} loading={assetLoading} showSearch optionFilterProp="label"
                onOpenChange={open => { if (open) loadAssets() }}
                onChange={v => addAssetRef(v)}
                options={assetOptions.map(a => ({
                  value: a.id,
                  label: `${a.name} (v${a.version} · ${a.table?.condition_keys?.length || 0}条件列)`,
                }))}
                notFoundContent="暂无已发布资产，请先到「决策表资产」页发布" />
            </div>
            <p className="text-[10px] text-muted-foreground">
              引用资产后本表单只保存引用标记，资产发布新版本时所有「跟随最新版」的表单自动生效；
              也可「锁定 vN」固定到不可变快照。资产列必须在本表单字段中声明。
            </p>

            {/* 试算：用样例值走后端同一推导通道，验证命中行与输出值 */}
            <div className="rounded-md border bg-muted/20 p-2.5 space-y-2">
              <button type="button" className="flex items-center gap-1.5 text-[11px] font-medium"
                onClick={() => setPreviewOpen(v => !v)}>
                {previewOpen ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                试算（样例值验证命中行与输出）
              </button>
              {previewOpen && (
                <div className="space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    {previewFields.length === 0 && <span className="text-[11px] text-muted-foreground">决策表还没有条件列</span>}
                    {previewFields.map(k => (
                      <label key={k} className="flex items-center gap-1 text-[11px]">
                        <span className="text-muted-foreground">{nameOf(k)}</span>
                        <Input size="small" className="h-6 w-32 text-[11px]" placeholder="样例值"
                          value={previewValues[k] || ''}
                          onChange={e => setPreviewValues({ ...previewValues, [k]: e.target.value })} />
                      </label>
                    ))}
                    <Button size="small" type="primary" className="h-6 text-[11px]" loading={previewLoading} onClick={runPreview}>
                      <Play className="h-3 w-3 mr-0.5" />运行试算
                    </Button>
                  </div>
                  {previewResult && (
                    <div className="space-y-1 text-[11px]">
                      <div className="text-muted-foreground">触发规则（含命中行号）：</div>
                      {previewResult.fired.length
                        ? previewResult.fired.map((f, n) => <div key={n} className="text-blue-600 dark:text-blue-400">· {f}</div>)
                        : <div className="text-muted-foreground">无规则命中</div>}
                      {previewResult.warnings.length > 0 && (
                        <div className="text-amber-600 dark:text-amber-400">
                          {previewResult.warnings.map((w, n) => <div key={n}>· {w}</div>)}
                        </div>
                      )}
                      <div className="text-muted-foreground">推导后的输出列：</div>
                      <div className="rounded border bg-background p-1.5 font-mono">
                        {Array.from(new Set(decisionTables.flatMap(dt => dt.output_keys || []))).map(k => (
                          <div key={k}>{nameOf(k)} = {String(previewResult.values[k] ?? '（未赋值）')}</div>
                        ))}
                        {decisionTables.length === 0 && <div>尚未配置决策表</div>}
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>
        )}
      </div>

      {/* 汇总提示：帮助配置者确认生效情况 */}
      {(behaviorRules.length > 0 || comboRules.length > 0 || dataLinks.length > 0 || decisionTables.length > 0) && (
        <p className="text-[10px] text-muted-foreground">
          已配置 {behaviorRules.length} 条联动规则（{behaviorRules.map(r => `${nameOf(r.targets?.[0] || '')}${ACTION_LABEL[r.action] || r.action}`).join('、')}）
          {comboRules.length > 0 && `，${comboRules.length} 条组合校验`}
          {dataLinks.length > 0 && `，${dataLinks.length} 条数据联动`}
          {decisionTables.length > 0 && `，${decisionTables.length} 张决策表（其中 ${decisionTables.filter(d => d.asset_ref).length} 张引用资产）`}。运行期前端实时生效，提交时后端同样校验。
        </p>
      )}
    </div>
  )
}

export default FormRulesEditor
