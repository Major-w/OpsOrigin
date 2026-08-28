
export { useTicketStore, useProcessStore } from './itsm/index'
export type { Ticket, TicketEvent, ITSMStats, SLAStats, Process, ProcessStep, ProcessInstance, ProcessVersion, StepType, FormSchema, FormFieldOption } from './itsm/types'

import { useTicketStore } from './itsm/tickets'
import { useProcessStore } from './itsm/processes'

interface _TicketState {
  tickets: import('./itsm/types').Ticket[]
  stats: import('./itsm/types').ITSMStats | null
  loading: boolean
  error: string | null
  fetch: () => Promise<void>
  getTicket: (id: string) => Promise<import('./itsm/types').Ticket>
  getEvents: (id: string) => Promise<import('./itsm/types').TicketEvent[]>
  create: (data: Partial<import('./itsm/types').Ticket>) => Promise<boolean>
  approve: (id: string, comment?: string) => Promise<boolean>
  reject: (id: string, reason: string) => Promise<boolean>
  execute: (id: string) => Promise<boolean>
  complete: (id: string, comment?: string, assessment?: 'confirmed' | 'false_positive') => Promise<boolean>
  rollback: (id: string, reason?: string) => Promise<boolean>
  submit: (id: string) => Promise<boolean>
  assign: (id: string, assignee: string) => Promise<boolean>
  close: (id: string, comment?: string, satisfaction?: number) => Promise<boolean>
  withdraw: (id: string, reason?: string) => Promise<boolean>
  reopen: (id: string, reason?: string) => Promise<boolean>
  resubmit: (id: string, reason?: string) => Promise<boolean>
  hold: (id: string, reason?: string) => Promise<boolean>
  resume: (id: string) => Promise<boolean>
  remind: (id: string, message?: string) => Promise<boolean>
  relate: (id: string, relatedId: string) => Promise<boolean>
  fetchStats: () => Promise<void>
}

interface _ProcessState {
  processes: import('./itsm/types').Process[]
  loading: boolean
  error: string | null
  fetchProcesses: () => Promise<void>
  getProcess: (id: string) => Promise<import('./itsm/types').Process | null>
  createProcess: (data: Partial<import('./itsm/types').Process>) => Promise<string | null>
  updateProcess: (id: string, data: Partial<import('./itsm/types').Process>) => Promise<boolean>
  deleteProcess: (id: string) => Promise<boolean>
  listProcessVersions: (id: string) => Promise<import('./itsm/types').ProcessVersion[]>
  getProcessVersion: (id: string, version: number) => Promise<import('./itsm/types').ProcessVersion | null>
  getTicketProcess: (ticketId: string) => Promise<import('./itsm/types').ProcessInstance | null>
  getFormSchema: (scope: string) => Promise<import('./itsm/types').FormSchema | null>
  getFormScopes: () => Promise<string[]>
  upsertFormSchema: (scope: string, schema: import('./itsm/types').FormSchema) => Promise<boolean>
  getFormOptions: (source: string, keyword?: string, parent?: string) => Promise<import('./itsm/types').FormFieldOption[]>
  getFormOptionSources: () => Promise<string[]>
  updateTicketCustomFields: (ticketId: string, scope: string, fields: Record<string, any>) => Promise<boolean>
}

export type CombinedITSMState = _TicketState & _ProcessState

function getITSMCombined(): CombinedITSMState {
  const ticket = useTicketStore() as any
  const process = useProcessStore() as any
  return { ...ticket, ...process }
}

function itsmSelector<T>(selector: (state: CombinedITSMState) => T): T {
  return selector(getITSMCombined())
}

/** @deprecated 使用 useTicketStore 或 useProcessStore 替代 */
export const useITSMStore = Object.assign(
  (selector?: (state: CombinedITSMState) => any) => {
    return selector ? itsmSelector(selector) : getITSMCombined()
  },
  {
    getState: getITSMCombined,
    setState: () => {},
    subscribe: () => () => {},
  },
)
