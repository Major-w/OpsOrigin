
export { useChatStore, useTeamStore, useTeams, useAgentAIStore, useApprovalStore } from './ai/index'
export { useWorkforceStore, useAgentTemplates, useSceneTemplates, useInspections } from './ai/workforce'
export type { AgentTeam, TeamMember, TeamMode, TeamRole, ChatMessage, ModelInfo, CreateTeamPayload, UpdateTeamPayload, ApprovalPolicy, ApprovalMode, PendingApproval, TeamStreamEvent } from './ai/types'
export type { AgentSkill, AgentTemplate, InspectionSchedule, SceneCatalogItem, SceneTemplate, InspectionTemplate, Inspection, Escalation, ExperimentMeta, InspectionRun, InspectionPayload } from './ai/workforce'
export { isTeamStreamEvent } from './ai/types'

import { useChatStore } from './ai/chat'
import { useTeamStore } from './ai/teams'
import { useAgentAIStore } from './ai/agents'
import { useApprovalStore } from './ai/approval'

// 从子 store 的接口类型直接组合，避免 ReturnType 在 zustand v4 下的推断问题
interface _ChatState {
  models: import('./ai/types').ModelInfo[]
  assistantOpen: boolean
  setAssistantOpen: (open: boolean) => void
  toggleAssistant: () => void
  fetchModels: () => Promise<import('./ai/types').ModelInfo[]>
  chat: (prompt: string, history?: { role: string; content: string }[]) => Promise<string | null>
  streamChat: (
    prompt: string,
    onToken: (t: string) => void,
    onDone: (meta?: { message_id?: string; conversation_id?: string; citations?: string[] }) => void,
    history?: { role: string; content: string }[],
    agentId?: string,
    reasoning?: boolean,
    onThinking?: (t: string) => void,
    explore?: boolean,
    onAsk?: (approval: import('./ai/types').PendingApproval) => void,
    serverConvId?: string,
    signal?: AbortSignal,
    model?: string,
    onError?: (msg: string) => void,
    onToolCall?: (tc: { name: string; args: any; status: 'running' | 'done'; result?: string }) => void,
  ) => Promise<void>
  loadConversations: (agentId?: string) => Promise<any[]>
  saveConversation: (conv: any) => Promise<any | null>
  submitFeedback: (params: { conversation_id?: string; message_id?: string; rating: 'up' | 'down'; comment?: string }) => Promise<boolean>
}

interface _TeamState {
  teams: import('./ai/types').AgentTeam[]
  loading: boolean
  error: string | null
  loadTeams: () => Promise<void>
  createTeam: (payload: import('./ai/types').CreateTeamPayload) => Promise<import('./ai/types').AgentTeam | null>
  updateTeam: (id: string, payload: import('./ai/types').UpdateTeamPayload) => Promise<import('./ai/types').AgentTeam | null>
  deleteTeam: (id: string) => Promise<boolean>
  streamTeamChat: (
    teamId: string,
    message: string,
    history: import('./ai/types').ChatMessage[],
    onEvent: (ev: import('./ai/types').TeamStreamEvent) => void,
  ) => Promise<void>
}

interface _AgentState {
  agents: any[]
  tools: any[]
  plans: any[]
  loading: boolean
  error: string | null
  fetchAgents: () => Promise<void>
  fetchTools: () => Promise<void>
  fetchPlans: () => Promise<void>
  createAgent: (data: any) => Promise<boolean>
  executePlan: (data: any) => Promise<boolean>
}

interface _ApprovalState {
  getApprovalPolicy: (agentId?: string) => Promise<import('./ai/types').ApprovalPolicy | null>
  putApprovalPolicy: (data: { agent_id?: string; mode?: import('./ai/types').ApprovalMode; sensitive_tools?: string[]; ask_timeout_sec?: number }) => Promise<import('./ai/types').ApprovalPolicy | null>
  respondApproval: (id: string, action: 'approve' | 'reject', comment?: string) => Promise<boolean>
  listPendingApprovals: () => Promise<import('./ai/types').PendingApproval[]>
}

export type CombinedAIState = _ChatState & _TeamState & _AgentState & _ApprovalState & { teamsLoading: boolean; teamsError: string | null }

/** @deprecated 使用 useChatStore / useTeamStore / useAgentAIStore / useApprovalStore 替代 */
export function useAIStore(): CombinedAIState
export function useAIStore<T>(selector: (state: CombinedAIState) => T): T
export function useAIStore(selector?: (state: CombinedAIState) => any) {
  const chat = useChatStore() as any
  const team = useTeamStore() as any
  const agent = useAgentAIStore() as any
  const approval = useApprovalStore() as any
  const combined = {
    ...chat,
    ...team,
    ...agent,
    ...approval,
    teamsLoading: team.loading,
    teamsError: team.error,
  } as CombinedAIState
  return selector ? selector(combined) : combined
}
