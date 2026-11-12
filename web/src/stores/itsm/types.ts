import type { FormSchema, FormFieldOption } from '@/components/itsm/DynamicForm'
import type { StepFieldPerm } from '@/lib/itsm-constants'

export interface Ticket {
  id: string
  number?: string
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

export interface TicketTemplateContent {
  risk_level?: string
  title?: string
  description?: string
  change_plan?: string
  rollback_plan?: string
  ci_ids?: string[]
  labels?: Record<string, string>
  custom_fields?: Record<string, any>
}

export interface TicketTemplate {
  id: string
  name: string
  remark?: string
  category?: string
  form_scope?: string
  process_id?: string
  catalog_id?: string
  content: TicketTemplateContent
  created_by?: string
  created_at?: string
  updated_at?: string
}

export interface RuleCondition {
  field: string
  op: 'equals' | 'not_equals' | 'contains'
  value: string
}

export interface RuleAction {
  type: 'notify' | 'assign' | 'webhook' | 'start_process'
  target: string
  message?: string
  /** 仅 notify：显式指定通知渠道 ID（留空=按通知策略 Policy 分发） */
  channels?: string[]
}

export interface AutomationRule {
  id: string
  name: string
  event: string
  enabled: boolean
  conditions?: RuleCondition[]
  actions: RuleAction[]
  /** 定时调度配置：event='schedule' 时生效；cron(5 段)与 interval_minutes 二选一 */
  schedule?: { cron?: string; interval_minutes?: number }
  created_by?: string
  created_at?: string
  updated_at?: string
}

export interface TicketButton {
  id: string
  name: string
  action_type: string
  action_params?: Record<string, any>
  visible_statuses?: string[]
  roles?: string[]
  confirm_text?: string
  sort_order: number
  enabled: boolean
  created_by?: string
  created_at?: string
  updated_at?: string
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
  approval_type?: 'countersign' | 'orsign' | 'responded_any' | 'percent' | 'count'
  /** percent/count 表决的阈值（percent: 0-100；count: 人数）；其余语义忽略 */
  approval_threshold?: number
  approver_field?: string
  /** 审批时间语义：时限(如 24h/3d) + 到期处置 + 周期催办间隔 */
  timeout_duration?: string
  timeout_action?: 'auto_approve' | 'auto_reject' | 'notify_only'
  reminder_interval?: string
  /** 工作日历（服务时间窗口名称/ID）：配置后催办/超时按业务时间计算，空=自然时间 */
  calendar?: string
  script_id?: string; inputs?: Record<string,any>
  /** 流程引擎动作库类型：非空时 service 步骤执行预置动作(http/notify/create_ticket/script/cmdb_update)，
   *  参数复用 inputs；留空=走 script_id 脚本旧路径。对齐 internal/itsm/process Step.ActionType。 */
  action_type?: string
  /** 动作模板运行期引用：非空时模板为权威来源(按已发布版本解析)，inputs 中同名键覆盖模板参数。
   *  version=0/省略=跟随最新已发布版本；与「复制为快照」二选一。对齐 process.ActionTemplateRef。 */
  template_ref?: { template_id: string; version?: number }
  /** 服务任务失败重试策略：次数(0=不重试,后端上限5) + 间隔(如 10s/1m，空=立即重试) */
  retry_count?: number; retry_interval?: string
  condition?: string
  join_mode?: 'all' | 'any'
  assignee?: string; assignee_var?: string
  dispatch_mode?: 'auto' | 'manual'
  duration?: string; deadline?: string
  notify_channel?: string; notify_title?: string; notify_body?: string
  subflow_id?: string
  /** 子流程显式传参：key=参数名(子流程内以 ${params.key} 引用)，value=变量表达式(如 ${custom_fields.owner}) */
  subflow_params?: Record<string,string>
  /** For Each 循环：对表达式求值出的列表逐项执行子流程(仅全自动子流程)，子流程内用 ${item}/${item_index} 引用当前项 */
  for_each?: string
  depends_on?: { nodeId: string; condition: string }[]
  position_x?: number; position_y?: number
  form_fields?: StepFieldPerm[]
  form_scope?: string
  form_schema?: string
  actions?: string[]
  /** 所属阶段（阶段管理）：同阶段名节点聚合为阶段进度；空=不参与阶段视图 */
  stage?: string
}

export type StepType = 'start' | 'end' | 'user' | 'service' | 'gateway' | 'timer' | 'notify' | 'parallel' | 'join' | 'dispatch' | 'subflow' | 'wait'

export interface TicketNumberRule {
  enabled?: boolean
  prefix?: string
  date_format?: '' | 'YYYY' | 'YYYYMM' | 'YYYYMMDD'
  separator?: string
  seq_width?: number
  seq_scope?: 'year' | 'month' | 'global'
}

/** 流程测试运行（dry-run）报告 —— 纯模拟，不建实例不执行动作 */
export interface DryRunStep {
  step_id: string; name: string; type: string
  status: 'would_run' | 'would_pause' | 'branch_end' | 'skipped'
  detail: string
  branches?: string
}

export interface DryRunResult {
  process_id: string
  paused_at?: string
  paused_name?: string
  summary: string
  warnings?: string[]
  steps: DryRunStep[]
}

/** 流程级决策表资产绑定：运行期按已发布版本求值，输出注入 decision.<alias> 变量命名空间 */
export interface DecisionBinding {
  asset_id: string
  version?: number              // 0/省略 = 跟随最新已发布版本
  alias: string                 // decision.<alias>.<输出列>
  inputs?: Record<string,string> // 条件列 key -> ${...} 变量表达式；未映射按同名字段取值
}

export interface Process {
  id: string; name: string; description?: string; category?: string
  steps: ProcessStep[]; labels?: Record<string,string>
  enabled: boolean; version: number
  form?: any
  decision_bindings?: DecisionBinding[]
  sla_policy_id?: string
  notify_config?: any
  number_rule?: TicketNumberRule
  created_at: string; updated_at: string
}

export interface ProcessInstance {
  id: string
  process_id: string
  ticket_id: string
  current_step: string
  status: string
  step_results?: Record<string, any>
  /** 阶段进度视图（阶段管理：由后端按节点 stage 归属推导，与实例字段平铺返回） */
  stages?: StageView[]
  started_at?: string
  finished_at?: string | null
  created_at?: string
  updated_at?: string
}

/** 阶段进度视图（对齐后端 process.StageView） */
export interface StageView {
  stage: string
  status: 'done' | 'current' | 'pending'
  steps: { id: string; name: string; type: string; status: 'done' | 'failed' | 'current' | 'pending' }[]
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
