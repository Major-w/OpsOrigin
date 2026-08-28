import type { FormSchema, FormFieldOption } from '@/components/itsm/DynamicForm'

export interface Ticket {
  id: string
  title: string
  description?: string
  risk_level: string
  status: string
  change_plan?: string
  rollback_plan?: string
  created_by: string
  created_by_type: string
  approved_by?: string
  reject_reason?: string
  completed_by?: string
  rolled_back_by?: string
  rollback_reason?: string
  ci_ids?: string[]
  process_id?: string
  created_at: string
  updated_at: string
  approved_at?: string
  executed_at?: string
  completed_at?: string
  rolled_back_at?: string
  scheduled_at?: string
  deadline_at?: string
  labels?: Record<string, string>
  webhook_url?: string
  custom_fields?: Record<string, any>
  form_scope?: string
  assignee?: string
  assigned_at?: string
  satisfaction?: number
  on_hold?: boolean
  hold_reason?: string
  hold_until_at?: string
  hold_at?: string
  hold_seconds?: number
  sla_warning_notified?: boolean
  sla_breached?: boolean
  sla_breached_at?: string
  sla_escalation_level?: number
  approval_chain?: { mode: string; approvers: string[] }
  approval_status?: { current_index: number; votes: any[]; final_result: string }
  related_ticket_ids?: string[]
}

export interface TicketEvent {
  id: number
  ticket_id: string
  from_status: string
  to_status: string
  changed_by: string
  comment?: string
  created_at: string
}

export interface ITSMStats {
  total: number
  by_status: Record<string, number>
  by_risk: Record<string, number>
  approval_rate: number
  avg_approval_ms: number
  avg_execution_ms: number
  trends?: { date: string; created: number; closed: number }[]
  by_creator?: Record<string, number>
  by_channel?: Record<string, number>
}

export interface SLAStats {
  total_active: number
  sla_met: number
  sla_breached: number
  sla_warning: number
  compliance_rate: number
  avg_response_ms: number
  avg_resolution_ms: number
}

export interface ProcessStep {
  id: string; name: string; type: StepType
  description?: string
  approvers?: string[]; approval_msg?: string
  approval_type?: 'countersign' | 'orsign'
  approver_field?: string
  script_id?: string; inputs?: Record<string,any>
  condition?: string
  duration?: string; deadline?: string
  notify_channel?: string; notify_title?: string; notify_body?: string
  subflow_id?: string
  depends_on?: { nodeId: string; condition: string }[]
  position_x?: number; position_y?: number
  form_fields?: { key: string; visible: boolean; editable: boolean }[]
  form_scope?: string
  form_schema?: string
  actions?: string[]
}

export type StepType = 'start' | 'end' | 'user' | 'service' | 'gateway' | 'timer' | 'notify' | 'parallel' | 'subflow'

export interface Process {
  id: string; name: string; description?: string; category?: string
  steps: ProcessStep[]; labels?: Record<string,string>
  enabled: boolean; version: number
  form?: any
  sla_policy_id?: string
  notify_config?: any
  created_at: string; updated_at: string
}

export interface ProcessInstance {
  id: string
  process_id: string
  ticket_id: string
  current_step: string
  status: string
  step_results?: Record<string, any>
  started_at?: string
  finished_at?: string | null
  created_at?: string
  updated_at?: string
}

export interface ProcessVersion {
  id: string
  process_id: string
  version: number
  name: string
  description?: string
  category?: string
  steps: ProcessStep[]
  labels?: Record<string, string>
  enabled: boolean
  form?: any
  sla_policy_id?: string
  notify_config?: any
  created_by: string
  created_at: string
}

export type { FormSchema, FormFieldOption }
