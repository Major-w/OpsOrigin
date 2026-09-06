// ============================================================================
// 模型注册表数据层：模型列表 Provider(JSON 数组, 即唯一模型定义入口)
// 供模型列表 / 模型详情复用，避免各页自拼装。
//
// 存储事实（后端）：
//   - 模型列表：sysconfig 的 OPSORIGIN_LLM_PROVIDERS（JSON 数组，含权重/类型/可见性/主LLM标记，
//     用于多模型 auto 路由），保存后立即热生效（sysconfig 热更新钩子），无需重启 Core。
//   - 旧「默认模型(主 LLM)」sysconfig 单条配置仍作为平台级兜底 + embedding 配置存在
//     （AI 设置页管理），但不再作为模型列表的一行展示。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { apiFetch, apiFetchRaw } from './utils'

/** 模型列表中的一条 provider（与后端 OPSORIGIN_LLM_PROVIDERS 元素一一对应） */
export interface LLMProvider {
  name: string
  model: string
  base_url: string
  api_key: string
  weight: number
  /** 模型能力类型列表(可多选, 一个模型可同时支持多种): text / image / video */
  types?: string[]
  /** @deprecated 旧版单类型字段(逗号分隔或单值), 读取时兼容归一化到 types */
  type?: string
  /** 可见性: public(所有人可用, 默认) / private(仅创建者可用) */
  visibility?: string
  /** 创建者用户 ID（visibility=private 时仅该用户可用） */
  created_by?: string
  /** 主 LLM 标记: 作为平台兜底 + embedding 等内部调用的默认模型 */
  primary?: boolean
}

/** /ai/models 返回的健康状态项 */
export interface ModelHealth {
  name: string
  model: string
  base_url: string
  weight: number
  healthy: boolean
  types?: string[]
  visibility?: string
  created_by?: string
  primary?: boolean
}

/** 服务商预设（/ai/llm-presets） */
export interface LLMPreset {
  name: string
  label: string
  base_url: string
  default_model: string
  embedding_model: string
  embedding_base_url?: string
  doc?: string
  builtin?: boolean
  weight?: number
  /** 该厂商常见模型 ID 清单（供新增模型时下拉选择，仍可自由输入） */
  models?: string[]
}

/** 主 LLM 配置（API Key 服务端脱敏，读取为空串，仅写入有意义） */
export interface DefaultLLMConfig {
  model: string
  base_url: string
  timeout: number
  embedding_model: string
  embedding_base_url: string
}

/** 统一模型记录：列表页 / 详情页共同消费（source 联合类型保留 default 分支以兼容旧表单分支） */
export interface ModelRecord {
  /** provider 用自身 name 做 id */
  id: string
  source: 'default' | 'provider'
  /** 展示名 */
  name: string
  /** 模型 ID，如 deepseek-chat */
  model: string
  base_url: string
  /** 路由权重（越大越优先） */
  weight: number | null
  /** 健康状态（来自 /ai/models；未匹配到为 null） */
  healthy: boolean | null
  /** 命中的服务商预设标签（用于列表展示「服务商」列） */
  providerLabel?: string
  /** 模型能力类型列表(可多选): text / image / video */
  types?: string[]
  /** 可见性: public / private */
  visibility?: string
  /** 创建者用户 ID */
  created_by?: string
  /** 主 LLM 标记 */
  primary?: boolean
}

const DEFAULT_LLM_ID = 'default'

/** 从 sysconfig 配置数组中取某个 key 的当前值（兼容 [{category,items}] 与扁平数组两种形态） */
function pickConfigValue(cfgArr: any, key: string): string | undefined {
  if (!Array.isArray(cfgArr)) return undefined
  for (const cat of cfgArr) {
    const items = cat?.items || cat?.configs || []
    for (const it of items) {
      if (it?.key === key) return it.current_value ?? it.value ?? ''
    }
  }
  return undefined
}

export interface ModelRegistryState {
  loading: boolean
  error: string
  models: ModelRecord[]
  /** 池内 provider 原始列表（含 api_key，池页/详情编辑用） */
  providers: LLMProvider[]
  defaultLLM: DefaultLLMConfig
  presets: LLMPreset[]
}

const EMPTY: ModelRegistryState = {
  loading: true,
  error: '',
  models: [],
  providers: [],
  defaultLLM: { model: '', base_url: '', timeout: 60, embedding_model: '', embedding_base_url: '' },
  presets: [],
}

/** 把池内 provider 合并为统一模型记录列表，并匹配健康状态与服务商标签 */
function mergeModels(
  providers: LLMProvider[],
  health: ModelHealth[],
  presets: LLMPreset[],
): ModelRecord[] {
  const labelOf = (baseUrl: string): string | undefined => {
    if (!baseUrl) return undefined
    const hit = presets.find(p => p.base_url && baseUrl.startsWith(p.base_url.replace(/\/+$/, '')))
    return hit?.label
  }
  const healthOf = (model: string, baseUrl: string): boolean | null => {
    const h = health.find(x => x.model === model && x.base_url === baseUrl)
    return h ? h.healthy : null
  }
  /** 类型归一化: 优先 types; 兼容旧 type 单字段(逗号分隔); 全空默认 text */
  const typesOf = (p: LLMProvider): string[] => {
    if (p.types?.length) return p.types
    if (p.type) return p.type.split(',').map(s => s.trim()).filter(Boolean)
    return ['text']
  }
  const list: ModelRecord[] = []
  for (const p of providers) {
    list.push({
      id: p.name,
      source: 'provider',
      name: p.name,
      model: p.model,
      base_url: p.base_url,
      weight: typeof p.weight === 'number' ? p.weight : 0,
      healthy: healthOf(p.model, p.base_url),
      providerLabel: labelOf(p.base_url),
      types: typesOf(p),
      visibility: p.visibility || 'public',
      created_by: p.created_by,
      primary: !!p.primary,
    })
  }
  return list
}

export function useModelRegistry() {
  const [state, setState] = useState<ModelRegistryState>(EMPTY)

  const load = useCallback(async () => {
    setState(prev => ({ ...prev, loading: true, error: '' }))
    try {
      const [cfgRes, poolRes, presetRes] = await Promise.all([
        apiFetch<any>('/system/config').catch(() => null),
        apiFetch<any>('/ai/models').catch(() => []),
        apiFetch<any>('/ai/llm-presets').catch(() => ({ presets: [] })),
      ])
      const cfgArr = (cfgRes as any)?.config || cfgRes
      let providers: LLMProvider[] = []
      const provVal = pickConfigValue(cfgArr, 'OPSORIGIN_LLM_PROVIDERS')
      if (provVal && provVal !== '[]') {
        try {
          const parsed = JSON.parse(provVal)
          if (Array.isArray(parsed)) providers = parsed
        } catch { /* 配置损坏时按空池处理，不阻塞页面 */ }
      }
      const timeoutRaw = pickConfigValue(cfgArr, 'OPSORIGIN_LLM_TIMEOUT')
      const defaultLLM: DefaultLLMConfig = {
        model: pickConfigValue(cfgArr, 'OPSORIGIN_LLM_MODEL') || '',
        base_url: pickConfigValue(cfgArr, 'OPSORIGIN_LLM_BASE_URL') || '',
        timeout: parseInt(timeoutRaw || '60', 10) || 60,
        embedding_model: pickConfigValue(cfgArr, 'OPSORIGIN_LLM_EMBEDDING_MODEL') || '',
        embedding_base_url: pickConfigValue(cfgArr, 'OPSORIGIN_LLM_EMBEDDING_BASE_URL') || '',
      }
      const presets: LLMPreset[] = Array.isArray((presetRes as any)?.presets) ? (presetRes as any).presets : []
      const health: ModelHealth[] = Array.isArray(poolRes) ? poolRes : []
      setState({
        loading: false,
        error: '',
        models: mergeModels(providers, health, presets),
        providers,
        defaultLLM,
        presets,
      })
    } catch (e: any) {
      setState(prev => ({ ...prev, loading: false, error: e?.message || '加载模型配置失败' }))
    }
  }, [])

  useEffect(() => { load() }, [load])

  /** 整体保存模型池（新增/编辑/删除/改权重都走这里，幂等覆盖） */
  const saveProviders = useCallback(async (list: LLMProvider[]) => {
    await apiFetch('/system/config', {
      method: 'PUT',
      body: JSON.stringify({ updates: { OPSORIGIN_LLM_PROVIDERS: JSON.stringify(list) } }),
    })
  }, [])

  /** 保存主 LLM 单字段（key 不带 OPSORIGIN_LLM_ 前缀） */
  const saveDefaultField = useCallback(async (key: string, value: string) => {
    await apiFetch('/system/config', {
      method: 'PUT',
      body: JSON.stringify({ updates: { [`OPSORIGIN_LLM_${key.toUpperCase()}`]: value } }),
    })
  }, [])

  return { ...state, reload: load, saveProviders, saveDefaultField }
}

/** 连通性自检：仅探活平台健康端点，不触发模型调用 */
export async function testModelConnection(): Promise<string> {
  try {
    const r = await apiFetchRaw('/ops/health')
    if (r.status !== 200) throw new Error('HTTP ' + r.status)
    return '✅ 平台服务可达，配置基本正确'
  } catch (e: any) {
    const msg = e?.message || ''
    if (msg.startsWith('HTTP')) return '❌ 服务不可达（' + msg + '），请检查 Base URL'
    return '❌ 网络错误，请检查 Base URL'
  }
}

/** 按模型维度拉取微调任务（后端无过滤参数，本地按 base_model 过滤） */
export async function fetchFinetuneTasks(): Promise<any[]> {
  const d = await apiFetch<any>('/ai/finetune/tasks')
  return Array.isArray(d) ? d : []
}

export const DEFAULT_MODEL_ID = DEFAULT_LLM_ID
