'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

export type StepType = 'script' | 'condition' | 'parallel' | 'wait' | 'approval'

export interface WorkflowStep {
  id: string
  name: string
  type: StepType
  script_id?: string
  inputs?: Record<string, any>
  condition?: string
  on_success?: string
  on_failure?: string
  parallel_steps?: WorkflowStep[]
  wait_seconds?: number
  timeout?: number
  approvers?: string[]
  approval_msg?: string
  position?: { x: number; y: number }
}

export interface ParamDef {
  name: string
  type: string
  required: boolean
  description?: string
  default?: string
}

export interface Workflow {
  id: string
  name: string
  description?: string
  steps: WorkflowStep[]
  input_params?: ParamDef[]
  labels?: Record<string, string>
  created_at: string
  updated_at: string
}

export interface StepResult {
  step_id: string
  step_name: string
  status: string
  output?: Record<string, any>
  error?: string
  duration_ms: number
}

export interface WorkflowExecution {
  id: string
  workflow_id: string
  status: string
  agent_ids: string[]
  inputs?: Record<string, any>
  step_results?: Record<string, StepResult>
  started_at?: string
  finished_at?: string
  created_at: string
  pending_step_id?: string
}

export interface StepApproval {
  id: string
  execution_id: string
  step_id: string
  step_name: string
  approvers: string[]
  message: string
  status: 'pending' | 'approved' | 'rejected'
  decided_by?: string
  comment?: string
  created_at: string
  decided_at?: string
}

interface WorkflowState {
  workflows: Workflow[]
  executions: WorkflowExecution[]
  approvals: StepApproval[]
  loading: boolean
  fetch: () => Promise<void>
  create: (data: any) => Promise<string | null>
  update: (id: string, data: any) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
  execute: (id: string, agent_ids: string[], inputs?: Record<string, any>) => Promise<WorkflowExecution | null>
  fetchExecutions: (id: string) => Promise<void>
  fetchApprovals: () => Promise<void>
  approve: (id: string, comment?: string) => Promise<boolean>
  reject: (id: string, comment?: string) => Promise<boolean>
}

export const useWorkflowStore = create<WorkflowState>((set, get) => ({
  workflows: [],
  executions: [],
  approvals: [],
  loading: false,

  fetch: async () => {
    set({ loading: true })
    try {
      const workflows = await apiFetch<Workflow[]>('/workflows')
      set({ workflows, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载工作流失败: ${e.message}`)
    }
  },

  create: async (data) => {
    try {
      const wf = await apiFetch<Workflow>('/workflows', { method: 'POST', body: JSON.stringify(data) })
      toast.success('创建工作流成功')
      await get().fetch()
      return wf.id
    } catch (e: any) {
      toast.error(`创建工作流失败: ${e.message}`)
      return null
    }
  },

  update: async (id, data) => {
    try {
      await apiFetch(`/workflows/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      toast.success('保存工作流成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`保存工作流失败: ${e.message}`)
      return false
    }
  },

  remove: async (id) => {
    try {
      await apiFetch(`/workflows/${id}`, { method: 'DELETE' })
      set(s => ({ workflows: s.workflows.filter(w => w.id !== id) }))
      toast.success('删除工作流成功')
      return true
    } catch (e: any) {
      toast.error(`删除工作流失败: ${e.message}`)
      return false
    }
  },

  execute: async (id, agent_ids, inputs) => {
    try {
      const exec = await apiFetch<WorkflowExecution>(`/workflows/${id}/execute`, {
        method: 'POST',
        body: JSON.stringify({ agent_ids, inputs }),
      })
      if (exec.status === 'waiting') {
        toast.success('流程已暂停，等待审批')
      } else if (exec.status === 'succeeded') {
        toast.success('流程执行成功')
      } else if (exec.status === 'failed') {
        toast.error('流程执行失败')
      }
      return exec
    } catch (e: any) {
      toast.error(`执行工作流失败: ${e.message}`)
      return null
    }
  },

  fetchExecutions: async (id) => {
    set({ loading: true })
    try {
      const executions = await apiFetch<WorkflowExecution[]>(`/workflows/${id}/executions`)
      set({ executions, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载执行记录失败: ${e.message}`)
    }
  },

  fetchApprovals: async () => {
    try {
      const approvals = await apiFetch<StepApproval[]>('/workflows/approvals')
      set({ approvals })
    } catch (e: any) {
      toast.error(`加载审批列表失败: ${e.message}`)
    }
  },

  approve: async (id, comment) => {
    try {
      await apiFetch(`/workflows/approvals/${id}/approve`, {
        method: 'POST',
        body: JSON.stringify({ comment }),
      })
      toast.success('审批通过')
      await get().fetchApprovals()
      return true
    } catch (e: any) {
      toast.error(`审批操作失败: ${e.message}`)
      return false
    }
  },

  reject: async (id, comment) => {
    try {
      await apiFetch(`/workflows/approvals/${id}/reject`, {
        method: 'POST',
        body: JSON.stringify({ comment }),
      })
      toast.success('审批已驳回')
      await get().fetchApprovals()
      return true
    } catch (e: any) {
      toast.error(`驳回操作失败: ${e.message}`)
      return false
    }
  },
}))
