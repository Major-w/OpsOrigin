'use client'

import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'

// ============================================================================
// AI 数字员工 & 主动巡检（P0）
// 与后端契约对齐：
//   GET  /api/v1/ai/agent-templates            岗位模板目录
//   POST /api/v1/ai/agent-templates/{id}/instantiate  一键上岗
//   GET  /api/v1/ai/inspection-templates       巡检模板目录
//   CRUD /api/v1/ai/inspections                巡检任务
//   POST /api/v1/ai/inspections/{id}/run       立即执行
//   GET  /api/v1/ai/inspections/{id}/runs      运行历史
// ============================================================================

/** Agent 可复用能力块 */
export interface AgentSkill {
  name: string
  description: string
  instructions: string
}

/** 岗位数字员工模板 */
export interface AgentTemplate {
  id: string
  name: string
  role: string
  icon: string
  description: string
  model_name: string
  system_prompt: string
  knowledge_categories?: string[]
  tools?: string[]
  /** 该岗位建议自动创建的关联巡检模板 ID（上岗时可开启，形成数字员工×巡检闭环） */
  inspection_template_id?: string
  skills?: AgentSkill[]
  tags?: string[]
}

/** 巡检周期 */
export type InspectionSchedule = '@hourly' | '@every_6h' | '@daily' | '@weekly'

/** 场景包推荐的标准化工单类型(仅展示, 不自动落库) */
export interface SceneCatalogItem {
  name: string
  description?: string
  category?: string
}

/** 业务场景包(岗位 + 巡检 + 推荐工单一键组合) */
export interface SceneTemplate {
  id: string
  name: string
  category: string
  description: string
  icon: string
  agent_template_id: string
  inspection_template_ids?: string[]
  knowledge_categories?: string[]
  suggested_catalog?: SceneCatalogItem[]
  tags?: string[]
}

/** 巡检模板 */
export interface InspectionTemplate {
  id: string
  name: string
  category: string
  description: string
  prompt: string
  scope_hint: string
  default_schedule: InspectionSchedule
}

/** 巡检任务 */
export interface Inspection {
  id: string
  tenant_id: string
  name: string
  template_id?: string
  agent_id?: string
  team_id?: string
  prompt: string
  scope: string
  schedule: InspectionSchedule
  enabled: boolean
  last_run_at?: string
  last_status?: string
  next_run_at: string
  created_at: string
  updated_at: string
}

/** 巡检发现转出的 ITSM 工单记录 */
export interface Escalation {
  finding_idx: number
  level: 'high' | 'medium' | 'low'
  title: string
  ticket_id: string
  ticket_title?: string
  created_at: string
}

/** 对比实验元数据(策略优化 A/B) */
export interface ExperimentMeta {
  prompt_override?: string
  scope_override?: string
  created_by?: string
}

/** 巡检运行记录 */
export interface InspectionRun {
  id: string
  inspection_id: string
  tenant_id: string
  triggered_by: 'scheduled' | 'manual' | 'experiment'
  status: 'running' | 'success' | 'failed'
  summary: string
  findings?: string
  error?: string
  started_at: string
  finished_at?: string
  /** 已转工单的发现 */
  escalations?: Escalation[]
  /** 对比实验元数据(非实验运行为空) */
  experiment?: ExperimentMeta
}

/** 创建/更新巡检任务的入参 */
export interface InspectionPayload {
  name: string
  template_id?: string
  agent_id?: string
  prompt: string
  scope: string
  schedule: InspectionSchedule
  enabled: boolean
}

interface WorkforceState {
  templates: AgentTemplate[]
  templatesLoading: boolean

  inspectionTemplates: InspectionTemplate[]
  inspections: Inspection[]
  inspectionsLoading: boolean
  /** 按巡检任务 ID 归档的运行历史 */
  runs: Record<string, InspectionRun[]>
  /** 正在执行中的巡检任务 ID 集合(用于按钮 loading) */
  running: string[]
  /** 正在转工单的运行记录 ID 集合(用于按钮 loading) */
  escalating: string[]

  /** 场景包目录 */
  sceneTemplates: SceneTemplate[]
  /** 正在启用的场景 ID 集合(用于按钮 loading) */
  sceneLaunching: string[]

  loadTemplates: () => Promise<void>
  instantiateTemplate: (id: string, name?: string, withInspection?: boolean) => Promise<boolean>

  loadSceneTemplates: () => Promise<void>
  instantiateScene: (id: string, name?: string, autoCreateTickets?: boolean) => Promise<boolean>

  loadInspectionTemplates: () => Promise<void>
  loadInspections: () => Promise<void>
  createInspection: (payload: InspectionPayload) => Promise<boolean>
  updateInspection: (id: string, payload: InspectionPayload) => Promise<boolean>
  deleteInspection: (id: string) => Promise<boolean>
  runInspection: (id: string) => Promise<boolean>
  /** 执行一次对比实验(策略优化): 用覆盖 prompt/scope 跑同一巡检 */
  runExperiment: (id: string, meta: ExperimentMeta) => Promise<InspectionRun | null>
  loadRuns: (id: string) => Promise<void>
  /** 将某次巡检运行记录中达到指定级别(含)的发现转 ITSM 工单, 返回更新后的运行记录 */
  escalateRun: (inspectionId: string, runId: string, severity?: 'high' | 'medium' | 'low') => Promise<InspectionRun | null>
}

export const useWorkforceStore = create<WorkforceState>((set, get) => ({
  templates: [],
  templatesLoading: false,
  inspectionTemplates: [],
  inspections: [],
  inspectionsLoading: false,
  runs: {},
  running: [],
  escalating: [],
  sceneTemplates: [],
  sceneLaunching: [],

  // ------------------------- 岗位数字员工 -------------------------

  loadTemplates: async () => {
    set({ templatesLoading: true })
    try {
      const templates = await apiList<AgentTemplate>('/ai/agent-templates')
      set({ templates, templatesLoading: false })
    } catch (e: unknown) {
      set({ templatesLoading: false })
      toast.error(`加载岗位模板失败: ${(e as Error).message}`)
    }
  },

  instantiateTemplate: async (id, name, withInspection) => {
    try {
      const resp = await apiFetch<{ agent: unknown; inspection: Inspection | null }>(`/ai/agent-templates/${id}/instantiate`, {
        method: 'POST',
        body: JSON.stringify({ name: name || '', with_inspection: withInspection ?? true }),
      })
      if (resp.inspection) {
        toast.success(`数字员工已上岗，并自动创建巡检「${resp.inspection.name}」`)
      } else {
        toast.success('数字员工已上岗')
      }
      return true
    } catch (e: unknown) {
      toast.error(`上岗失败: ${(e as Error).message}`)
      return false
    }
  },

  // ------------------------- 业务场景包 -------------------------

  loadSceneTemplates: async () => {
    try {
      const sceneTemplates = await apiList<SceneTemplate>('/ai/scene-templates')
      set({ sceneTemplates })
    } catch (e: unknown) {
      toast.error(`加载场景包失败: ${(e as Error).message}`)
    }
  },

  instantiateScene: async (id, name, autoCreateTickets) => {
    set(s => ({ sceneLaunching: [...s.sceneLaunching, id] }))
    try {
      const resp = await apiFetch<{
        agent: { id: string; name: string } | null
        inspection: { name: string } | null
        inspections: { name: string }[]
        catalog: SceneCatalogItem[]
        created_catalog: number
      }>(`/ai/scene-templates/${id}/instantiate`, {
        method: 'POST',
        body: JSON.stringify({ name: name || '', auto_create_tickets: autoCreateTickets ?? false }),
      })
      const inspCount = (resp.inspections?.length || 0) + (resp.inspection ? 1 : 0)
      toast.success(
        resp.agent
          ? `场景已启用: 数字员工「${resp.agent.name}」+ ${inspCount} 项巡检${resp.created_catalog ? ` + ${resp.created_catalog} 个工单类型` : ''}`
          : '场景已启用',
      )
      return true
    } catch (e: unknown) {
      toast.error(`启用场景失败: ${(e as Error).message}`)
      return false
    } finally {
      set(s => ({ sceneLaunching: s.sceneLaunching.filter(x => x !== id) }))
    }
  },

  // ------------------------- 主动巡检 -------------------------

  loadInspectionTemplates: async () => {
    try {
      const inspectionTemplates = await apiList<InspectionTemplate>('/ai/inspection-templates')
      set({ inspectionTemplates })
    } catch (e: unknown) {
      toast.error(`加载巡检模板失败: ${(e as Error).message}`)
    }
  },

  loadInspections: async () => {
    set({ inspectionsLoading: true })
    try {
      const inspections = await apiList<Inspection>('/ai/inspections')
      set({ inspections, inspectionsLoading: false })
    } catch (e: unknown) {
      set({ inspectionsLoading: false })
      toast.error(`加载巡检任务失败: ${(e as Error).message}`)
    }
  },

  createInspection: async (payload) => {
    try {
      await apiFetch('/ai/inspections', { method: 'POST', body: JSON.stringify(payload) })
      toast.success('巡检任务已创建')
      await get().loadInspections()
      return true
    } catch (e: unknown) {
      toast.error(`创建失败: ${(e as Error).message}`)
      return false
    }
  },

  updateInspection: async (id, payload) => {
    try {
      await apiFetch(`/ai/inspections/${id}`, { method: 'PUT', body: JSON.stringify(payload) })
      toast.success('巡检任务已更新')
      await get().loadInspections()
      return true
    } catch (e: unknown) {
      toast.error(`更新失败: ${(e as Error).message}`)
      return false
    }
  },

  deleteInspection: async (id) => {
    try {
      await apiFetch(`/ai/inspections/${id}`, { method: 'DELETE' })
      toast.success('巡检任务已删除')
      set(s => {
        const runs = { ...s.runs }
        delete runs[id]
        return { inspections: s.inspections.filter(i => i.id !== id), runs }
      })
      return true
    } catch (e: unknown) {
      toast.error(`删除失败: ${(e as Error).message}`)
      return false
    }
  },

  runInspection: async (id) => {
    set(s => ({ running: [...s.running, id] }))
    try {
      const run = await apiFetch<InspectionRun>(`/ai/inspections/${id}/run`, { method: 'POST' })
      if (run.status === 'failed') {
        toast.error(`巡检执行失败: ${run.error || '未知错误'}`)
      } else {
        toast.success('巡检完成，已生成报告')
      }
      // 刷新任务状态与该任务的运行历史
      await Promise.all([get().loadInspections(), get().loadRuns(id)])
      return run.status !== 'failed'
    } catch (e: unknown) {
      toast.error(`巡检执行失败: ${(e as Error).message}`)
      return false
    } finally {
      set(s => ({ running: s.running.filter(r => r !== id) }))
    }
  },

  runExperiment: async (id, meta) => {
    set(s => ({ running: [...s.running, id] }))
    try {
      const run = await apiFetch<InspectionRun>(`/ai/inspections/${id}/experiment`, {
        method: 'POST',
        body: JSON.stringify(meta),
      })
      if (run.status === 'failed') {
        toast.error(`实验执行失败: ${run.error || '未知错误'}`)
      } else {
        toast.success('实验运行完成，已与基线并列展示')
      }
      await Promise.all([get().loadInspections(), get().loadRuns(id)])
      return run.status === 'failed' ? null : run
    } catch (e: unknown) {
      toast.error(`实验执行失败: ${(e as Error).message}`)
      return null
    } finally {
      set(s => ({ running: s.running.filter(r => r !== id) }))
    }
  },

  loadRuns: async (id) => {
    try {
      const list = await apiList<InspectionRun>(`/ai/inspections/${id}/runs`)
      set(s => ({ runs: { ...s.runs, [id]: list } }))
    } catch (e: unknown) {
      toast.error(`加载巡检历史失败: ${(e as Error).message}`)
    }
  },

  escalateRun: async (inspectionId, runId, severity = 'medium') => {
    set(s => ({ escalating: [...s.escalating, runId] }))
    try {
      const run = await apiFetch<InspectionRun>(`/ai/inspections/${inspectionId}/runs/${runId}/escalate`, {
        method: 'POST',
        body: JSON.stringify({ severity }),
      })
      const n = (run.escalations || []).length
      toast.success(n > 0 ? `已转 ${n} 条发现为 ITSM 工单` : '已无新的可转发现')
      await get().loadRuns(inspectionId)
      return run
    } catch (e: unknown) {
      toast.error(`转工单失败: ${(e as Error).message}`)
      return null
    } finally {
      set(s => ({ escalating: s.escalating.filter(r => r !== runId) }))
    }
  },
}))

/** 岗位数字员工目录 */
export function useAgentTemplates() {
  const templates = useWorkforceStore(s => s.templates)
  const loading = useWorkforceStore(s => s.templatesLoading)
  const loadTemplates = useWorkforceStore(s => s.loadTemplates)
  const instantiateTemplate = useWorkforceStore(s => s.instantiateTemplate)
  return { templates, loading, loadTemplates, instantiateTemplate }
}

/** 业务场景包(岗位+巡检+推荐工单一键组合) */
export function useSceneTemplates() {
  const sceneTemplates = useWorkforceStore(s => s.sceneTemplates)
  const sceneLaunching = useWorkforceStore(s => s.sceneLaunching)
  const loadSceneTemplates = useWorkforceStore(s => s.loadSceneTemplates)
  const instantiateScene = useWorkforceStore(s => s.instantiateScene)
  return { sceneTemplates, sceneLaunching, loadSceneTemplates, instantiateScene }
}

/** 主动巡检 */
export function useInspections() {
  const inspectionTemplates = useWorkforceStore(s => s.inspectionTemplates)
  const inspections = useWorkforceStore(s => s.inspections)
  const loading = useWorkforceStore(s => s.inspectionsLoading)
  const runs = useWorkforceStore(s => s.runs)
  const running = useWorkforceStore(s => s.running)
  const escalating = useWorkforceStore(s => s.escalating)
  const loadInspectionTemplates = useWorkforceStore(s => s.loadInspectionTemplates)
  const loadInspections = useWorkforceStore(s => s.loadInspections)
  const createInspection = useWorkforceStore(s => s.createInspection)
  const updateInspection = useWorkforceStore(s => s.updateInspection)
  const deleteInspection = useWorkforceStore(s => s.deleteInspection)
  const runInspection = useWorkforceStore(s => s.runInspection)
  const runExperiment = useWorkforceStore(s => s.runExperiment)
  const loadRuns = useWorkforceStore(s => s.loadRuns)
  const escalateRun = useWorkforceStore(s => s.escalateRun)
  return {
    inspectionTemplates, inspections, loading, runs, running, escalating,
    loadInspectionTemplates, loadInspections,
    createInspection, updateInspection, deleteInspection, runInspection, runExperiment, loadRuns, escalateRun,
  }
}
