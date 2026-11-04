
// FormRulesEditor —— 表单级「UI 联动规则」与「跨字段组合校验」编辑器。
// 与 FormDesigner 配套：FormDesigner 管字段布局，本组件管 schema.behavior_rules / combo_rules。
// 条件求值内核复用 DynamicForm 的 evalCondition（与后端 form 包语义一致）。

import { useState } from 'react'
import { Button, Input, Select } from 'antd'
import { ChevronDown, ChevronRight, Plus, Trash2 } from 'lucide-react'
import {
  CONDITION_OPS,
  type BehaviorRule,
  type BehaviorAction,
  type ComboRule,
  type DataLinkRule,
  type DecisionTable,
  type DecisionRow,
  type FormCondition,
  type FormSchema,
  type FormFieldDef,
} from './DynamicForm'

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
                          onChange={v => updateSet({ mode: v, value: '' })}
                          options={[{ value: 'fixed', label: '固定为' }, { value: 'copy_from', label: '复制字段' }]} />
                        {mode === 'fixed' ? (
                          <Input size="small" className="w-36 shrink-0" placeholder="写入值" value={s.value || ''}
                            onChange={e => updateSet({ value: e.target.value })} />
                        ) : (
                          <Select size="small" className="w-36 shrink-0" placeholder="来源字段" showSearch optionFilterProp="label"
                            value={s.value || undefined} onChange={v => updateSet({ value: v })} options={fieldOptions} />
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
              典型用法：环境 ∈ 生产,预发 且 影响范围 = 全局 → 级别=P1、生效时间=非工作时间。单元格留空表示不限；多个候选值用英文逗号分隔。自上而下首个命中行生效，✓/✕ 启停行。
            </p>
            {decisionTables.map((dt, i) => {
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
                          <th className="w-8" />
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
                              <td className="px-1 text-center">
                                <button type="button" title={enabled ? '停用该行' : '启用该行'}
                                  className={`text-xs ${enabled ? 'text-blue-600 dark:text-blue-400' : 'text-muted-foreground'}`}
                                  onClick={() => updateRow({ enabled: !enabled })}>
                                  {enabled ? '✓' : '✕'}
                                </button>
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
            <Button size="small" type="dashed" className="w-full"
              onClick={() => setDecisionTables([...decisionTables, { name: '', condition_keys: [], output_keys: [], rows: [{ cells: {}, outputs: {} }] }])}>
              <Plus className="h-3.5 w-3.5 mr-1" />添加决策表
            </Button>
          </div>
        )}
      </div>

      {/* 汇总提示：帮助配置者确认生效情况 */}
      {(behaviorRules.length > 0 || comboRules.length > 0 || dataLinks.length > 0 || decisionTables.length > 0) && (
        <p className="text-[10px] text-muted-foreground">
          已配置 {behaviorRules.length} 条联动规则（{behaviorRules.map(r => `${nameOf(r.targets?.[0] || '')}${ACTION_LABEL[r.action] || r.action}`).join('、')}）
          {comboRules.length > 0 && `，${comboRules.length} 条组合校验`}
          {dataLinks.length > 0 && `，${dataLinks.length} 条数据联动`}
          {decisionTables.length > 0 && `，${decisionTables.length} 张决策表`}。运行期前端实时生效，提交时后端同样校验。
        </p>
      )}
    </div>
  )
}

export default FormRulesEditor
