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
  skills?: AgentSkill[]
  tags?: string[]
}

/** 巡检周期 */
export type InspectionSchedule = '@hourly' | '@every_6h' | '@daily' | '@weekly'

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

/** 巡检运行记录 */
export interface InspectionRun {
  id: string
  inspection_id: string
  tenant_id: string
  triggered_by: 'scheduled' | 'manual'
  status: 'running' | 'success' | 'failed'
  summary: string
  findings?: string
  error?: string
  started_at: string
  finished_at?: string
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

  loadTemplates: () => Promise<void>
  instantiateTemplate: (id: string, name?: string) => Promise<boolean>

  loadInspectionTemplates: () => Promise<void>
  loadInspections: () => Promise<void>
  createInspection: (payload: InspectionPayload) => Promise<boolean>
  updateInspection: (id: string, payload: InspectionPayload) => Promise<boolean>
  deleteInspection: (id: string) => Promise<boolean>
  runInspection: (id: string) => Promise<boolean>
  loadRuns: (id: string) => Promise<void>
}

export const useWorkforceStore = create<WorkforceState>((set, get) => ({
  templates: [],
  templatesLoading: false,
  inspectionTemplates: [],
  inspections: [],
  inspectionsLoading: false,
  runs: {},
  running: [],

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

  instantiateTemplate: async (id, name) => {
    try {
      await apiFetch(`/ai/agent-templates/${id}/instantiate`, {
        method: 'POST',
        body: JSON.stringify({ name: name || '' }),
      })
      toast.success('数字员工已上岗')
      return true
    } catch (e: unknown) {
      toast.error(`上岗失败: ${(e as Error).message}`)
      return false
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

  loadRuns: async (id) => {
    try {
      const list = await apiList<InspectionRun>(`/ai/inspections/${id}/runs`)
      set(s => ({ runs: { ...s.runs, [id]: list } }))
    } catch (e: unknown) {
      toast.error(`加载巡检历史失败: ${(e as Error).message}`)
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

/** 主动巡检 */
export function useInspections() {
  const inspectionTemplates = useWorkforceStore(s => s.inspectionTemplates)
  const inspections = useWorkforceStore(s => s.inspections)
  const loading = useWorkforceStore(s => s.inspectionsLoading)
  const runs = useWorkforceStore(s => s.runs)
  const running = useWorkforceStore(s => s.running)
  const loadInspectionTemplates = useWorkforceStore(s => s.loadInspectionTemplates)
  const loadInspections = useWorkforceStore(s => s.loadInspections)
  const createInspection = useWorkforceStore(s => s.createInspection)
  const updateInspection = useWorkforceStore(s => s.updateInspection)
  const deleteInspection = useWorkforceStore(s => s.deleteInspection)
  const runInspection = useWorkforceStore(s => s.runInspection)
  const loadRuns = useWorkforceStore(s => s.loadRuns)
  return {
    inspectionTemplates, inspections, loading, runs, running,
    loadInspectionTemplates, loadInspections,
    createInspection, updateInspection, deleteInspection, runInspection, loadRuns,
  }
}
