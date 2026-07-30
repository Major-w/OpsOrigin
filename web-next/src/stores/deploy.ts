'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

// ============ Types (matching Go backend) ============

export type AppType = 'java' | 'go' | 'python' | 'nodejs' | 'static' | 'container'
export type AppStatus = 'enabled' | 'disabled'
export type DeployEnv = 'dev' | 'test' | 'staging' | 'prod'
export type DeployMode = 'host' | 'container'
export type ReleaseStatus = 'draft' | 'pending_approval' | 'approved' | 'deploying' | 'succeeded' | 'failed' | 'rolled_back' | 'rejected'
export type ReleaseStrategy = 'blue_green' | 'canary' | 'rolling' | 'full'

export interface DeployApp {
  id: string; name: string; code: string; type: AppType; status: AppStatus
  repo_url: string; branch: string; build_cmd: string; artifact_path: string
  deploy_script: string; health_check_url: string; target_port: number
  // 容器模式专用字段（mode=container 的环境会使用）
  image_url: string; namespace: string; workload: string
  container_name: string; replicas: number; manifest: string
  envs: DeployEnv[]; current_versions: Record<DeployEnv, string>
  description: string; owner: string; labels: Record<string, string>
  created_at: string; updated_at: string
}

export interface DeployStep { id: string; name: string; status: string; output?: string }

export interface ReleaseTicket {
  id: string; app_id: string; app_name: string; app_code: string
  title: string; version: string; env: DeployEnv; strategy: ReleaseStrategy
  status: ReleaseStatus; commit_id: string; commit_msg: string; changelog: string
  approvers: string[]; approved_by: string; rejected_by: string; reject_reason: string
  operator: string; steps: DeployStep[]
  created_at: string; updated_at: string; approved_at?: string; deployed_at?: string
}

export interface DeployEnvironment {
  id: string; name: string; type: DeployEnv; mode: DeployMode
  hosts: string[]; agent_ids: string[]
  config: Record<string, string>; agent_count: number; status: string
  description: string; created_at: string; updated_at: string
}

export interface ReleaseHistory {
  id: string; release_id: string; app_id: string; app_name: string
  env: DeployEnv; version: string; status: ReleaseStatus
  operator: string; output: string; created_at: string
}

interface DeployState {
  apps: DeployApp[]; releases: ReleaseTicket[]
  environments: DeployEnvironment[]; history: ReleaseHistory[]
  loading: boolean
  fetchApps: () => Promise<void>
  createApp: (d: Partial<DeployApp>) => Promise<boolean>
  updateApp: (id: string, d: Partial<DeployApp>) => Promise<boolean>
  deleteApp: (id: string) => Promise<boolean>
  fetchReleases: () => Promise<void>
  createRelease: (d: Partial<ReleaseTicket>) => Promise<boolean>
  updateRelease: (id: string, d: Partial<ReleaseTicket>) => Promise<boolean>
  deleteRelease: (id: string) => Promise<boolean>
  approveRelease: (id: string) => Promise<boolean>
  rejectRelease: (id: string, reason: string) => Promise<boolean>
  deployRelease: (id: string) => Promise<boolean>
  rollbackRelease: (id: string) => Promise<boolean>
  fetchEnvs: () => Promise<void>
  createEnv: (d: Partial<DeployEnvironment>) => Promise<boolean>
  updateEnv: (id: string, d: Partial<DeployEnvironment>) => Promise<boolean>
  deleteEnv: (id: string) => Promise<boolean>
  fetchHistory: () => Promise<void>
}

export const useDeployStore = create<DeployState>((set, get) => ({
  apps: [], releases: [], environments: [], history: [], loading: false,

  fetchApps: async () => {
    set({ loading: true })
    try { set({ apps: await apiFetch<DeployApp[]>('/deploy/apps'), loading: false }) }
    catch (e: any) { set({ loading: false }); toast.error('加载应用失败: ' + e.message) }
  },
  createApp: async (d) => {
    try { await apiFetch('/deploy/apps', { method: 'POST', body: JSON.stringify(d) }); toast.success('应用已创建'); await get().fetchApps(); return true }
    catch (e: any) { toast.error('创建失败: ' + e.message); return false }
  },
  updateApp: async (id, d) => {
    try { await apiFetch(`/deploy/apps/${id}`, { method: 'PUT', body: JSON.stringify(d) }); toast.success('应用已更新'); await get().fetchApps(); return true }
    catch (e: any) { toast.error('更新失败: ' + e.message); return false }
  },
  deleteApp: async (id) => {
    try { await apiFetch(`/deploy/apps/${id}`, { method: 'DELETE' }); set(s => ({ apps: s.apps.filter(a => a.id !== id) })); toast.success('已删除'); return true }
    catch (e: any) { toast.error('删除失败: ' + e.message); return false }
  },

  fetchReleases: async () => {
    set({ loading: true })
    try { set({ releases: await apiFetch<ReleaseTicket[]>('/deploy/releases'), loading: false }) }
    catch (e: any) { set({ loading: false }); toast.error('加载发布单失败: ' + e.message) }
  },
  createRelease: async (d) => {
    try { await apiFetch('/deploy/releases', { method: 'POST', body: JSON.stringify(d) }); toast.success('发布单已创建'); await get().fetchReleases(); return true }
    catch (e: any) { toast.error('创建失败: ' + e.message); return false }
  },
  updateRelease: async (id, d) => {
    try { await apiFetch(`/deploy/releases/${id}`, { method: 'PUT', body: JSON.stringify(d) }); toast.success('已更新'); await get().fetchReleases(); return true }
    catch (e: any) { toast.error('更新失败: ' + e.message); return false }
  },
  deleteRelease: async (id) => {
    try { await apiFetch(`/deploy/releases/${id}`, { method: 'DELETE' }); set(s => ({ releases: s.releases.filter(r => r.id !== id) })); toast.success('已删除'); return true }
    catch (e: any) { toast.error('删除失败: ' + e.message); return false }
  },
  approveRelease: async (id) => {
    try { await apiFetch(`/deploy/releases/${id}/approve`, { method: 'POST' }); toast.success('已审批'); await get().fetchReleases(); return true }
    catch (e: any) { toast.error('审批失败: ' + e.message); return false }
  },
  rejectRelease: async (id, reason) => {
    try { await apiFetch(`/deploy/releases/${id}/reject`, { method: 'POST', body: JSON.stringify({ reason }) }); toast.success('已驳回'); await get().fetchReleases(); return true }
    catch (e: any) { toast.error('驳回失败: ' + e.message); return false }
  },
  deployRelease: async (id) => {
    try { await apiFetch(`/deploy/releases/${id}/deploy`, { method: 'POST' }); toast.success('部署已启动'); await get().fetchReleases(); await get().fetchHistory(); return true }
    catch (e: any) { toast.error('部署失败: ' + e.message); return false }
  },
  rollbackRelease: async (id) => {
    try { await apiFetch(`/deploy/releases/${id}/rollback`, { method: 'POST' }); toast.success('已回滚'); await get().fetchReleases(); await get().fetchHistory(); return true }
    catch (e: any) { toast.error('回滚失败: ' + e.message); return false }
  },

  fetchEnvs: async () => {
    try { set({ environments: await apiFetch<DeployEnvironment[]>('/deploy/environments') }) }
    catch (e: any) { toast.error('加载环境失败: ' + e.message) }
  },
  createEnv: async (d) => {
    try { await apiFetch('/deploy/environments', { method: 'POST', body: JSON.stringify(d) }); toast.success('环境已创建'); await get().fetchEnvs(); return true }
    catch (e: any) { toast.error('创建失败: ' + e.message); return false }
  },
  updateEnv: async (id, d) => {
    try { await apiFetch(`/deploy/environments/${id}`, { method: 'PUT', body: JSON.stringify(d) }); toast.success('已更新'); await get().fetchEnvs(); return true }
    catch (e: any) { toast.error('更新失败: ' + e.message); return false }
  },
  deleteEnv: async (id) => {
    try { await apiFetch(`/deploy/environments/${id}`, { method: 'DELETE' }); set(s => ({ environments: s.environments.filter(e => e.id !== id) })); toast.success('已删除'); return true }
    catch (e: any) { toast.error('删除失败: ' + e.message); return false }
  },

  fetchHistory: async () => {
    try { set({ history: await apiFetch<ReleaseHistory[]>('/deploy/history') }) }
    catch (e: any) { toast.error('加载历史失败: ' + e.message) }
  },
}))
