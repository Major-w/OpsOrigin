
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { message } from 'antd'
import type { AgentTeam, CreateTeamPayload, UpdateTeamPayload, ChatMessage, TeamStreamEvent } from './types'
import { isTeamStreamEvent } from './types'

interface TeamState {
  teams: AgentTeam[]
  loading: boolean
  error: string | null
  loadTeams: () => Promise<void>
  createTeam: (payload: CreateTeamPayload) => Promise<AgentTeam | null>
  updateTeam: (id: string, payload: UpdateTeamPayload) => Promise<AgentTeam | null>
  deleteTeam: (id: string) => Promise<boolean>
  streamTeamChat: (
    teamId: string,
    message: string,
    history: ChatMessage[],
    onEvent: (ev: TeamStreamEvent) => void,
  ) => Promise<void>
}

export const useTeamStore = create<TeamState>((set, get) => ({
  teams: [],
  loading: false,
  error: null,
  loadTeams: async () => {
    set({ loading: true, error: null })
    try {
      const teams = await apiList<AgentTeam>('/ai/teams')
      set({ teams: teams || [], loading: false })
    } catch (e: any) {
      set({ loading: false, error: e.message || '加载团队失败' })
      message.error(`加载团队失败: ${e.message}`)
    }
  },
  createTeam: async (payload) => {
    try {
      const team = await apiFetch<AgentTeam>('/ai/teams', {
        method: 'POST',
        body: JSON.stringify(payload),
      })
      message.success('创建团队成功')
      await get().loadTeams()
      return team
    } catch (e: any) {
      message.error(`创建团队失败: ${e.message}`)
      return null
    }
  },
  updateTeam: async (id, payload) => {
    try {
      const team = await apiFetch<AgentTeam>(`/ai/teams/${encodeURIComponent(id)}`, {
        method: 'PUT',
        body: JSON.stringify(payload),
      })
      message.success('更新团队成功')
      await get().loadTeams()
      return team
    } catch (e: any) {
      message.error(`更新团队失败: ${e.message}`)
      return null
    }
  },
  deleteTeam: async (id) => {
    try {
      await apiFetch<void>(`/ai/teams/${encodeURIComponent(id)}`, { method: 'DELETE' })
      message.success('删除团队成功')
      return true
    } catch (e: any) {
      message.error(`删除团队失败: ${e.message}`)
      return false
    }
  },
  streamTeamChat: async (teamId, msg, history, onEvent) => {
    try {
      const resp = await fetch(
        `${window.location.origin}/api/v1/ai/teams/${encodeURIComponent(teamId)}/chat`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ message: msg, history }),
        },
      )
      if (!resp.ok) {
        let errMsg = `HTTP ${resp.status}`
        try {
          const body = await resp.json()
          if (body && typeof body.error === 'string') errMsg = body.error
        } catch {}
        onEvent({ type: 'error', error: errMsg })
        return
      }
      const reader = resp.body?.getReader()
      if (!reader) {
        onEvent({ type: 'error', error: '响应流不可用' })
        return
      }
      const decoder = new TextDecoder()
      let buf = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const lines = buf.split('\n')
        buf = lines.pop() || ''
        for (const line of lines) {
          const trimmed = line.trim()
          if (!trimmed.startsWith('data:')) continue
          const data = trimmed.slice(trimmed.indexOf('data:') + 5).trim()
          if (!data) continue
          try {
            const parsed: unknown = JSON.parse(data)
            if (isTeamStreamEvent(parsed)) {
              onEvent(parsed)
            }
          } catch {}
        }
      }
    } catch (e: any) {
      onEvent({ type: 'error', error: e?.message || '连接异常' })
    }
  },
}))

export function useTeams() {
  const teams = useTeamStore(s => s.teams)
  const loading = useTeamStore(s => s.loading)
  const error = useTeamStore(s => s.error)
  const loadTeams = useTeamStore(s => s.loadTeams)
  const createTeam = useTeamStore(s => s.createTeam)
  const updateTeam = useTeamStore(s => s.updateTeam)
  const deleteTeam = useTeamStore(s => s.deleteTeam)
  const streamTeamChat = useTeamStore(s => s.streamTeamChat)
  return { teams, loading, error, loadTeams, createTeam, updateTeam, deleteTeam, streamTeamChat }
}
