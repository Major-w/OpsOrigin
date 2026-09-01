
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import type { ApprovalPolicy, ApprovalMode, PendingApproval } from './types'
import { getMessage } from '@/lib/message'

interface ApprovalState {
  getApprovalPolicy: (agentId?: string) => Promise<ApprovalPolicy | null>
  putApprovalPolicy: (data: { agent_id?: string; mode?: ApprovalMode; sensitive_tools?: string[]; ask_timeout_sec?: number }) => Promise<ApprovalPolicy | null>
  respondApproval: (id: string, action: 'approve' | 'reject', comment?: string) => Promise<boolean>
  listPendingApprovals: () => Promise<PendingApproval[]>
}

export const useApprovalStore = create<ApprovalState>(() => ({
  getApprovalPolicy: async (agentId) => {
    try {
      const q = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''
      const d = await apiFetch<any>(`/ai/approval-policy${q}`)
      return d?.policy || null
    } catch (e: any) {
      getMessage().error(`加载审批策略失败: ${e.message}`)
      return null
    }
  },
  putApprovalPolicy: async (data) => {
    try {
      const d = await apiFetch<any>('/ai/approval-policy', {
        method: 'PUT',
        body: JSON.stringify(data),
      })
      getMessage().success('审批策略已保存，即时生效')
      return d?.policy || null
    } catch (e: any) {
      getMessage().error(`保存审批策略失败: ${e.message}`)
      return null
    }
  },
  respondApproval: async (id, action, comment) => {
    try {
      await apiFetch<void>(`/ai/approvals/${encodeURIComponent(id)}/respond`, {
        method: 'POST',
        body: JSON.stringify({ action, comment }),
      })
      return true
    } catch (e: any) {
      getMessage().error(`响应审批失败: ${e.message}`)
      return false
    }
  },
  listPendingApprovals: async () => {
    try {
      return await apiList<PendingApproval>('/ai/approvals/pending')
    } catch {
      return []
    }
  },
}))
