'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

export interface AgentHost {
  id: string; name: string; type: string; address: string; port: number
  os_type?: string; os_version?: string; hostname?: string
  version?: string; cpu?: string; memory?: string; disk?: string
  install_method?: string; auth_type?: string; ssh_user?: string
  status: string; capabilities?: string[]
  last_seen?: string; last_heartbeat?: string; first_seen?: string
  labels?: Record<string,string>; created_at: string; updated_at: string
  cmdb_data?: {
    hostname?: string; os?: string; os_version?: string
    cpu?: string; memory?: string; disk?: string
    ips?: string[]; timestamp?: number
  }
}

export interface ProxyInfo {
  id: string; name: string; address: string; port: number
  status: string; agent_count: number; version?: string; last_seen?: string
  created_at: string
}

export interface TaskInfo {
  task_id: string; action: string; script?: string; params?: Record<string, any>
  timeout?: number; status: string; response?: {
    task_id: string; success: boolean; output: string; exit_code: number
    duration_ms: number; error?: string
  }
  created_at: string; updated_at: string
}

interface AgentState {
  agents: AgentHost[]; proxies: ProxyInfo[]; loading: boolean; error: string | null
  fetch: (type?: string) => Promise<void>
  register: (data: any) => Promise<boolean>
  update: (id: string, data: any) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
  batchInstall: (data: { addresses: string[]; username: string; password?: string; ssh_key?: string; port?: number; use_key?: boolean; as_proxy?: boolean; proxy_id?: string }) => Promise<any>
  testSSH: (data: { address: string; port: number; username: string; password?: string; ssh_key?: string; use_key?: boolean }) => Promise<any>
  getInstallScript: (os: string) => Promise<string>
  fetchProxies: () => Promise<void>
  registerProxy: (data: { name: string; address: string; port: number }) => Promise<boolean>
  updateProxy: (id: string, data: any) => Promise<boolean>
  deleteProxy: (id: string) => Promise<boolean>
  getCircuitBreaker: (id: string) => Promise<any>
  setCircuitBreaker: (id: string, data: any) => Promise<boolean>
  restartAgent: (id: string) => Promise<boolean>
  upgradeAgent: (id: string, targetVersion?: string) => Promise<boolean>
  uninstallAgent: (id: string) => Promise<boolean>
  fetchTasks: (id: string) => Promise<TaskInfo[]>
  createTask: (id: string, data: { action: string; script?: string; params?: Record<string, any>; timeout?: number }) => Promise<boolean>
}

export const useAgentStore = create<AgentState>((set, get) => ({
  agents: [], proxies: [], loading: false, error: null,
  fetch: async (type) => {
    set({ loading: true })
    try {
      const qs = type ? `?type=${type}` : ''
      const agents = await apiFetch<AgentHost[]>(`/agents${qs}`)
      set({ agents, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载代理列表失败: ${e.message}`)
    }
  },
  register: async (data) => {
    try {
      await apiFetch<AgentHost>('/agents', { method: 'POST', body: JSON.stringify(data) })
      toast.success('注册代理成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`注册代理失败: ${e.message}`)
      return false
    }
  },
  update: async (id, data) => {
    try {
      await apiFetch(`/agents/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      toast.success('更新代理成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`更新代理失败: ${e.message}`)
      return false
    }
  },
  remove: async (id) => {
    try {
      await apiFetch(`/agents/${id}`, { method: 'DELETE' })
      set(s => ({ agents: s.agents.filter(a => a.id !== id) }))
      toast.success('删除代理成功')
      return true
    } catch (e: any) {
      toast.error(`删除代理失败: ${e.message}`)
      return false
    }
  },
  batchInstall: async (data) => {
    try {
      const result = await apiFetch<any>('/agents/batch-install', { method: 'POST', body: JSON.stringify(data) })
      await get().fetch()
      return result
    } catch (e: any) {
      toast.error(`批量安装失败: ${e.message}`)
      throw e
    }
  },
  testSSH: async (data) => {
    try {
      return await apiFetch<any>('/agents/test-ssh', { method: 'POST', body: JSON.stringify(data) })
    } catch (e: any) {
      toast.error(`SSH 测试失败: ${e.message}`)
      throw e
    }
  },
  getInstallScript: async (os) => {
    try {
      const token = typeof window !== 'undefined' ? localStorage.getItem('opsnexus_token') : null
      const headers: Record<string, string> = {
        'Authorization': `Bearer ${token || ''}`,
        'X-Trace-ID': typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2),
      }
      if (typeof window !== 'undefined') {
        const userStr = localStorage.getItem('opsnexus_user')
        if (userStr) {
          try {
            const user = JSON.parse(userStr)
            headers['X-Principal-Type'] = 'HUMAN'
            headers['X-Principal-ID'] = user.user_id || user.id || user.username || 'unknown'
          } catch {}
        }
      }
      const resp = await fetch(`/api/v1/agents/install-script?os=${os}`, { headers })
      return await resp.text()
    } catch (e: any) {
      toast.error(`获取安装脚本失败: ${e.message}`)
      throw e
    }
  },
  fetchProxies: async () => {
    set({ loading: true })
    try {
      const proxies = await apiFetch<ProxyInfo[]>('/proxies')
      set({ proxies, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载代理网关失败: ${e.message}`)
    }
  },
  registerProxy: async (data) => {
    try {
      await apiFetch('/proxies', { method: 'POST', body: JSON.stringify(data) })
      toast.success('Proxy 注册成功')
      await get().fetchProxies()
      return true
    } catch (e: any) {
      toast.error(`Proxy 注册失败: ${e.message}`)
      return false
    }
  },
  updateProxy: async (id, data) => {
    try {
      await apiFetch(`/proxies/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      toast.success('Proxy 更新成功')
      await get().fetchProxies()
      return true
    } catch (e: any) {
      toast.error(`Proxy 更新失败: ${e.message}`)
      return false
    }
  },
  deleteProxy: async (id) => {
    try {
      await apiFetch(`/proxies/${id}`, { method: 'DELETE' })
      set(s => ({ proxies: s.proxies.filter(p => p.id !== id) }))
      toast.success('Proxy 已删除')
      return true
    } catch (e: any) {
      toast.error(`删除 Proxy 失败: ${e.message}`)
      return false
    }
  },
  getCircuitBreaker: async (id) => {
    try {
      return await apiFetch<any>(`/agents/${id}/circuit-breaker`)
    } catch (e: any) {
      toast.error(`加载熔断状态失败: ${e.message}`)
      throw e
    }
  },
  setCircuitBreaker: async (id, data) => {
    try {
      await apiFetch(`/agents/${id}/circuit-breaker`, { method: 'PUT', body: JSON.stringify(data) })
      toast.success('熔断配置已更新')
      return true
    } catch (e: any) {
      toast.error(`更新熔断配置失败: ${e.message}`)
      return false
    }
  },
  restartAgent: async (id) => {
    try {
      await apiFetch(`/agents/${id}/restart`, { method: 'POST' })
      toast.success('重启指令已下发')
      return true
    } catch (e: any) {
      toast.error(`重启代理失败: ${e.message}`)
      return false
    }
  },
  upgradeAgent: async (id, targetVersion) => {
    try {
      await apiFetch(`/agents/${id}/upgrade`, { method: 'POST', body: JSON.stringify({ target_version: targetVersion || '' }) })
      toast.success('升级指令已下发')
      return true
    } catch (e: any) {
      toast.error(`升级失败: ${e.message}`)
      return false
    }
  },
  uninstallAgent: async (id) => {
    try {
      await apiFetch(`/agents/${id}/uninstall`, { method: 'POST' })
      toast.success('卸载指令已下发')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`卸载失败: ${e.message}`)
      return false
    }
  },
  fetchTasks: async (id) => {
    try {
      return await apiFetch<TaskInfo[]>(`/agents/${id}/tasks`)
    } catch (e: any) {
      toast.error(`加载任务列表失败: ${e.message}`)
      return []
    }
  },
  createTask: async (id, data) => {
    try {
      await apiFetch(`/agents/${id}/tasks`, { method: 'POST', body: JSON.stringify(data) })
      toast.success('任务已下发')
      return true
    } catch (e: any) {
      toast.error(`任务下发失败: ${e.message}`)
      return false
    }
  },
}))
