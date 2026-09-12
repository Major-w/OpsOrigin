export type TeamMode = 'supervisor' | 'parallel' | 'sequential' | 'debate'
export type TeamRole = 'supervisor' | 'member' | 'finalizer'

export interface TeamMember {
  agent_id: string
  agent_name: string
  role: TeamRole
  can_finalize: boolean
  persona: string
  system_prompt: string
  enable_tools: boolean
  model_name: string
  weight: number
  id?: string
}

export interface AgentTeam {
  id: string
  name: string
  description: string
  mode: TeamMode
  system_prompt: string
  members: TeamMember[]
  enable_tools: boolean
  member_timeout_sec: number
  member_retries: number
  debate_rounds: number
  supervisor_verify: boolean
  created_at?: string
  updated_at?: string
}

export interface ChatMessage {
  role: string
  content: string
}

export interface ModelInfo {
  name: string
  model: string
  base_url?: string
  weight?: number
  healthy?: boolean
  /** 是否为推理模型(先深度思考再作答, 首答较慢但质量更高); 后端 /ai/models 已返回 */
  reasoning?: boolean
}

export interface CreateTeamPayload {
  name: string
  description: string
  mode: TeamMode
  system_prompt: string
  enable_tools: boolean
  member_timeout_sec: number
  member_retries: number
  debate_rounds: number
  supervisor_verify: boolean
  members: Array<{
    agent_id: string
    agent_name: string
    role: TeamRole
    can_finalize: boolean
    persona: string
    system_prompt: string
    enable_tools: boolean
    model_name: string
  }>
}

export type UpdateTeamPayload = CreateTeamPayload

export type ApprovalMode = 'always_ask' | 'always_allow' | 'sensitive_only'

export interface ApprovalPolicy {
  mode: ApprovalMode
  sensitive_tools: string[]
  ask_timeout_sec: number
}

export interface PendingApproval {
  id: string
  tenant_id?: string
  agent_id?: string
  tool: string
  summary: string
  args: string
  created_at?: string
  deadline: string
}

export type TeamStreamEvent =
  | { type: 'agent_start'; agent_id: string; agent_name: string; role: string }
  | { type: 'token'; agent_id: string; agent_name: string; role: string; content: string }
  | { type: 'agent_end'; agent_id: string; agent_name: string; role: string; content: string }
  | { type: 'tool_call'; agent_id: string; agent_name: string; role: string; tool_name: string; tool_args: string }
  | { type: 'tool_result'; agent_id: string; agent_name: string; role: string; tool_name: string; tool_result: string }
  | { type: 'supervisor_review'; agent_id: string; agent_name: string; role: string; content: string }
  | { type: 'final'; content: string; done: boolean }
  | { type: 'error'; agent_id?: string; agent_name?: string; role?: string; error: string }

export function isTeamStreamEvent(value: unknown): value is TeamStreamEvent {
  if (typeof value !== 'object' || value === null) return false
  const v = value as Record<string, unknown>
  if (typeof v.type !== 'string') return false
  switch (v.type) {
    case 'agent_start':
    case 'token':
    case 'agent_end': {
      const baseOk =
        typeof v.agent_id === 'string' &&
        typeof v.agent_name === 'string' &&
        typeof v.role === 'string'
      if (!baseOk) return false
      if (v.type === 'token' || v.type === 'agent_end') {
        return typeof v.content === 'string'
      }
      return true
    }
    case 'tool_call': {
      const baseOk =
        typeof v.agent_id === 'string' &&
        typeof v.agent_name === 'string' &&
        typeof v.role === 'string'
      if (!baseOk) return false
      return typeof v.tool_name === 'string' && typeof v.tool_args === 'string'
    }
    case 'tool_result': {
      const baseOk =
        typeof v.agent_id === 'string' &&
        typeof v.agent_name === 'string' &&
        typeof v.role === 'string'
      if (!baseOk) return false
      return typeof v.tool_name === 'string' && typeof v.tool_result === 'string'
    }
    case 'supervisor_review':
      return typeof v.agent_id === 'string' &&
        typeof v.agent_name === 'string' &&
        typeof v.role === 'string' &&
        typeof v.content === 'string'
    case 'final':
      return typeof v.content === 'string' && typeof v.done === 'boolean'
    case 'error':
      return typeof v.error === 'string'
    default:
      return false
  }
}
