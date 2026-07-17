'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

export interface Script {
  id: string; name: string; type: string; content: string; description?: string
  input_params?: { name: string; type: string; required: boolean; description?: string; default?: string }[]
  output_params?: { name: string; type: string; description?: string }[]
  timeout: number; run_as?: string; version: number
  labels?: Record<string,string>; created_at: string; updated_at: string
}

export interface ScriptVersion {
  id: string; script_id: string; version: number; name: string; type: string
  content: string; input_params: any[]; output_params: any[]
  timeout: number; run_as?: string; change_note?: string; created_by?: string
  created_at: string
}

export interface ExecutionResult {
  id: string; script_id: string; status: string; output: string; error?: string
  exit_code: number; duration_ms: number; params?: Record<string,string>
  run_as?: string; started_at: string; finished_at?: string
}

export interface DiffEntry {
  field: string; old_value: string; new_value: string
}

interface ScriptState {
  scripts: Script[]; loading: boolean
  fetch: () => Promise<void>
  create: (data: any) => Promise<boolean>
  update: (id: string, data: any, changeNote?: string) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
  duplicate: (id: string) => Promise<boolean>
  execute: (id: string, data: { params?: Record<string,string>; timeout?: number; run_as?: string }) => Promise<ExecutionResult | null>
  fetchExecutions: (id: string) => Promise<ExecutionResult[]>
  fetchVersions: (id: string) => Promise<ScriptVersion[]>
  diff: (id: string, from: number, to: number) => Promise<DiffEntry[]>
}

export const useScriptStore = create<ScriptState>((set, get) => ({
  scripts: [], loading: false,
  fetch: async () => {
    set({ loading: true })
    try {
      const scripts = await apiFetch<Script[]>('/scripts')
      set({ scripts, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载脚本列表失败: ${e.message}`)
    }
  },
  create: async (data) => {
    try {
      await apiFetch('/scripts', { method: 'POST', body: JSON.stringify(data) })
      toast.success('脚本创建成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`创建失败: ${e.message}`)
      return false
    }
  },
  update: async (id, data, changeNote) => {
    try {
      await apiFetch(`/scripts/${id}`, { method: 'PUT', body: JSON.stringify({ ...data, change_note: changeNote }) })
      toast.success('脚本更新成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`更新失败: ${e.message}`)
      return false
    }
  },
  remove: async (id) => {
    try {
      await apiFetch(`/scripts/${id}`, { method: 'DELETE' })
      set(s => ({ scripts: s.scripts.filter(sc => sc.id !== id) }))
      toast.success('脚本已删除')
      return true
    } catch (e: any) {
      toast.error(`删除失败: ${e.message}`)
      return false
    }
  },
  duplicate: async (id) => {
    try {
      const scripts = await apiFetch<Script[]>('/scripts')
      const source = scripts.find(s => s.id === id)
      if (!source) throw new Error('源脚本不存在')
      await apiFetch('/scripts', { method: 'POST', body: JSON.stringify({ ...source, name: `${source.name} (副本)`, id: undefined }) })
      toast.success('脚本已复制')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`复制失败: ${e.message}`)
      return false
    }
  },
  execute: async (id, data) => {
    try {
      const result = await apiFetch<ExecutionResult>(`/scripts/${id}/execute`, { method: 'POST', body: JSON.stringify(data) })
      toast.success(result.status === 'success' ? '脚本执行成功' : `执行状态: ${result.status}`)
      return result
    } catch (e: any) {
      toast.error(`执行失败: ${e.message}`)
      return null
    }
  },
  fetchExecutions: async (id) => {
    try {
      return await apiFetch<ExecutionResult[]>(`/scripts/${id}/executions`)
    } catch (e: any) {
      toast.error(`加载执行历史失败: ${e.message}`)
      return []
    }
  },
  fetchVersions: async (id) => {
    try {
      return await apiFetch<ScriptVersion[]>(`/scripts/${id}/versions`)
    } catch (e: any) {
      toast.error(`加载版本历史失败: ${e.message}`)
      return []
    }
  },
  diff: async (id, from, to) => {
    try {
      const result = await apiFetch<{ changes: DiffEntry[] }>(`/scripts/${id}/diff?from=${from}&to=${to}`)
      return result.changes || []
    } catch (e: any) {
      toast.error(`版本比对失败: ${e.message}`)
      return []
    }
  },
}))
