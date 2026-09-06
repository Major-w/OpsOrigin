// 模型配置表单：新增与编辑共用。
// 保存按来源分流：default=主 LLM（逐字段 PUT，只提交变更项）；provider=模型列表（整体覆盖保存）。
import { useEffect, useState } from 'react'
import { Button, Input, Select, AutoComplete, Checkbox, Radio } from 'antd'
import { Loader2, Save, Activity, CheckCircle2 } from 'lucide-react'
import { cn } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { testModelConnection, type DefaultLLMConfig, type LLMPreset, type LLMProvider, type ModelRecord } from '@/lib/ai-models'
import { useAuthStore } from '@/stores/auth'

interface FormState {
  name: string
  model: string
  base_url: string
  api_key: string
  weight: number
  /** 能力类型可多选：一个模型可同时支持文字+图片(如视觉理解模型) */
  types: string[]
  visibility: string
  primary: boolean
  /** 创建者用户 ID（编辑 private 模型时保留，避免保存时丢失归属） */
  created_by?: string
  timeout: number
  embedding_model: string
  embedding_base_url: string
}

const EMPTY_FORM: FormState = {
  name: '', model: '', base_url: '', api_key: '', weight: 50,
  types: ['text'], visibility: 'public', primary: false,
  timeout: 60, embedding_model: '', embedding_base_url: '',
}

const TYPE_OPTIONS = [
  { value: 'text', label: '文字（纯文本对话/推理）' },
  { value: 'image', label: '图片（多模态视觉）' },
  { value: 'video', label: '视频' },
]

function fromRecord(r: ModelRecord, def: DefaultLLMConfig): FormState {
  if (r.source === 'default') {
    return {
      ...EMPTY_FORM,
      name: r.name, model: def.model, base_url: def.base_url, api_key: '', weight: 0,
      timeout: def.timeout || 60, embedding_model: def.embedding_model || '', embedding_base_url: def.embedding_base_url || '',
    }
  }
  return {
    ...EMPTY_FORM,
    name: r.name, model: r.model, base_url: r.base_url, weight: r.weight ?? 50,
    types: r.types?.length ? r.types : ['text'], visibility: r.visibility || 'public',
    primary: !!r.primary, created_by: r.created_by,
  }
}

interface ModelFormProps {
  mode: 'create' | 'edit'
  record?: ModelRecord | null
  presets: LLMPreset[]
  defaultLLM: DefaultLLMConfig
  /** provider 保存：newName 为空表示新增 */
  onSaveProvider: (p: LLMProvider, originalName?: string) => Promise<void>
  /** 主 LLM 保存：patch 的 key 为 api_key/model/base_url/timeout/embedding_model/embedding_base_url */
  onSaveDefault: (patch: Record<string, string>) => Promise<void>
  onSaved: () => void
  onCancel: () => void
}

export function ModelForm({
  mode, record, presets, defaultLLM, onSaveProvider, onSaveDefault, onSaved, onCancel,
}: ModelFormProps) {
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [submitting, setSubmitting] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<string | null>(null)
  // 选中的服务商预设名：决定模型 ID 是否提供下拉清单
  const [presetName, setPresetName] = useState('')

  const isDefault = record?.source === 'default'

  useEffect(() => {
    if (mode === 'edit' && record) {
      setForm(fromRecord(record, defaultLLM))
      setPresetName(presets.find(p => p.base_url && record.base_url.startsWith(p.base_url.replace(/\/+$/, '')))?.name || '')
    } else if (mode === 'create') {
      setForm(EMPTY_FORM)
      setPresetName('')
    }
  }, [mode, record, defaultLLM, presets])

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setForm(f => ({ ...f, [k]: v }))

  const activePreset = presets.find(p => p.name === presetName)
  const presetModelOptions = (activePreset?.models || []).map(m => ({ value: m }))

  const applyPreset = (name: string) => {
    setPresetName(name || '')
    const p = presets.find(x => x.name === name)
    if (!p) return
    // 自定义服务商: 不预填, 清空接入参数由用户手动填写
    if (p.name === 'custom' || !p.base_url) {
      setForm(f => ({ ...f, base_url: '', model: '', embedding_model: '', embedding_base_url: '' }))
      getMessage().info('自定义服务商：请手动填写 API Base URL 与模型 ID')
      return
    }
    setForm(f => ({
      ...f,
      base_url: p.base_url || f.base_url,
      model: p.default_model || f.model,
      embedding_model: p.embedding_model || f.embedding_model,
      embedding_base_url: p.embedding_base_url || p.base_url || f.embedding_base_url,
      weight: p.weight ?? f.weight,
    }))
    getMessage().info(`已填入 ${p.label} 的默认接入参数，请补全 API Key 后保存`)
  }

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    setTestResult(await testModelConnection())
    setTesting(false)
  }

  const handleSubmit = async () => {
    if (!isDefault && !form.name.trim()) return getMessage().error('请填写模型名称（唯一标识）')
    if (!form.model.trim()) return getMessage().error('请填写模型 ID')
    if (!form.base_url.trim()) return getMessage().error('请填写 API Base URL')
    if (!isDefault && (!form.types || form.types.length === 0)) return getMessage().error('请勾选模型能力类型（文字/图片/视频，可多选）')

    setSubmitting(true)
    try {
      if (isDefault) {
        const patch: Record<string, string> = {}
        if (form.model !== defaultLLM.model) patch.model = form.model
        if (form.base_url !== defaultLLM.base_url) patch.base_url = form.base_url
        if (String(form.timeout) !== String(defaultLLM.timeout)) patch.timeout = String(parseInt(String(form.timeout), 10) || 60)
        if (form.embedding_model !== (defaultLLM.embedding_model || '')) patch.embedding_model = form.embedding_model
        if (form.embedding_base_url !== (defaultLLM.embedding_base_url || '')) patch.embedding_base_url = form.embedding_base_url
        if (form.api_key) patch.api_key = form.api_key
        if (Object.keys(patch).length === 0) {
          getMessage().info('配置未发生变化')
          setSubmitting(false)
          return
        }
        await onSaveDefault(patch)
      } else {
        await onSaveProvider(
          {
            name: form.name.trim(), model: form.model.trim(), base_url: form.base_url.trim(),
            api_key: form.api_key, weight: Number(form.weight) || 0,
            types: form.types?.length ? form.types : ['text'], visibility: form.visibility || 'public',
            created_by: form.visibility === 'private'
              ? (form.created_by || String(useAuthStore.getState().user?.user_id || ''))
              : '',
            primary: form.primary,
          },
          mode === 'edit' ? record?.id : undefined,
        )
      }
      getMessage().success('已保存，即时热生效')
      set('api_key', '')
      onSaved()
    } catch (e: any) {
      getMessage().error(`保存失败: ${e.message}`)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="space-y-3 max-w-3xl">
      <div>
        <label className="text-xs font-medium mb-1 block">服务商预设 <span className="text-muted-foreground font-normal">（选中后自动填充下方接入参数与模型清单）</span></label>
        <Select
          allowClear
          placeholder="选择模型服务商…"
          className="w-full"
          value={presetName || undefined}
          onChange={applyPreset}
          options={presets.map(p => ({ value: p.name, label: `${p.label}${p.builtin === false ? '（自定义）' : ''}` }))}
        />
      </div>

      {!isDefault && (
        <div>
          <label className="text-xs font-medium mb-1 block">模型名称 <span className="text-muted-foreground font-normal">（唯一标识，Agent 表单按此选择）</span></label>
          <Input
            placeholder="如 deepseek / qwen-max / finetuned-ops"
            value={form.name}
            onChange={e => set('name', e.target.value)}
            disabled={mode === 'edit'}
            className={cn('text-sm', mode === 'edit' && 'font-mono')}
          />
          {mode === 'edit' && <p className="text-[11px] text-muted-foreground mt-1">名称作为唯一标识，编辑时不可修改；如需更换请删除后新增。</p>}
        </div>
      )}

      <div>
        <label className="text-xs font-medium mb-1 block">
          模型 ID
          {presetModelOptions.length > 0 && <span className="text-muted-foreground font-normal">（可从 {activePreset?.label} 预置清单下拉选择，或输入其他模型）</span>}
        </label>
        <AutoComplete
          options={presetModelOptions}
          value={form.model}
          onChange={v => set('model', v)}
          // 不过滤: 点击下拉始终展示厂商全量预置清单(清单较短, 过滤反而把其他项藏掉); 仍可自由输入
          filterOption={false}
          // 未选预设时不可填(先选服务商或「自定义服务商」), 选中后始终可自由输入
          disabled={!presetName}
        >
          <Input placeholder="deepseek-chat" className="font-mono text-xs" />
        </AutoComplete>
      </div>

      <div>
        <label className="text-xs font-medium mb-1 block">API Base URL</label>
        <Input placeholder="https://api.deepseek.com" value={form.base_url} onChange={e => set('base_url', e.target.value)} className="font-mono text-xs" />
      </div>

      <div>
        <label className="text-xs font-medium mb-1 block">API Key <span className="text-muted-foreground font-normal">（留空表示不修改；服务端不回显明文）</span></label>
        <Input.Password placeholder="sk-..." value={form.api_key} onChange={e => set('api_key', e.target.value)} className="font-mono text-xs" />
      </div>

      {!isDefault && (
        <>
          <div>
            <label className="text-xs font-medium mb-1 block">权重 <span className="text-muted-foreground font-normal">（0-100，越大越优先；auto 模式按权重+健康自动匹配）</span></label>
            <Input type="number" min={0} max={100} value={form.weight} onChange={e => set('weight', parseInt(e.target.value, 10) || 0)} className="w-32 text-sm" />
          </div>

          <div>
            <label className="text-xs font-medium mb-1 block">
              模型能力类型 <span className="text-muted-foreground font-normal">（可多选；auto 模式按请求内容自动匹配对应能力的模型）</span>
            </label>
            <Checkbox.Group
              value={form.types}
              onChange={v => set('types', (v as string[]).length ? (v as string[]) : ['text'])}
              options={TYPE_OPTIONS}
            />
          </div>

          <div>
            <label className="text-xs font-medium mb-1 block">可见性</label>
            <Radio.Group
              value={form.visibility || 'public'}
              onChange={e => set('visibility', e.target.value)}
              options={[
                { value: 'public', label: '所有人可用' },
                { value: 'private', label: '仅自己可用' },
              ]}
            />
          </div>

          <div>
            <Checkbox checked={form.primary} onChange={e => set('primary', e.target.checked)}>
              设为主 LLM
              <span className="text-muted-foreground font-normal">（作为平台兜底模型，知识库 embedding 等内部调用默认使用它；建议只标记一个）</span>
            </Checkbox>
          </div>
        </>
      )}

      {isDefault && (
        <div className="space-y-3 pt-3 border-t border-dashed">
          <div>
            <label className="text-xs font-medium mb-1 block">Embedding 模型 <span className="text-muted-foreground font-normal">（知识库语义检索用，不同厂商维度不同）</span></label>
            <Input placeholder="text-embedding-3-small / embedding-3" value={form.embedding_model} onChange={e => set('embedding_model', e.target.value)} className="font-mono text-xs" />
          </div>
          <div>
            <label className="text-xs font-medium mb-1 block">Embedding Base URL <span className="text-muted-foreground font-normal">（留空复用上方 LLM Base URL）</span></label>
            <Input placeholder="留空则复用 LLM Base URL" value={form.embedding_base_url} onChange={e => set('embedding_base_url', e.target.value)} className="font-mono text-xs" />
          </div>
          <div>
            <label className="text-xs font-medium mb-1 block">超时（秒）</label>
            <Input type="number" min={5} value={form.timeout} onChange={e => set('timeout', parseInt(e.target.value, 10) || 60)} className="w-32 text-sm" />
          </div>
        </div>
      )}

      <div className="flex items-center gap-2 pt-2">
        <Button type="primary" size="middle" onClick={handleSubmit} disabled={submitting}>
          {submitting ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Save className="h-3.5 w-3.5 mr-1" />}
          {mode === 'create' ? '创建模型' : '保存配置'}
        </Button>
        <Button size="middle" onClick={handleTest} disabled={testing}>
          {testing ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Activity className="h-3.5 w-3.5 mr-1" />}
          测试连接
        </Button>
        <Button size="middle" onClick={onCancel}>取消</Button>
        {testResult && <span className="text-xs text-muted-foreground">{testResult}</span>}
      </div>

      <div className="flex items-center gap-2 text-xs p-3 rounded-lg bg-blue-500/5 border border-blue-500/10">
        <CheckCircle2 className="h-4 w-4 text-blue-500 shrink-0" />
        <span>保存后即时热生效，已连接的 Agent 无需重启。聊天输入框选择「自动」时按权重、健康、模型类型自动匹配。</span>
      </div>
    </div>
  )
}
