import { usePermissions } from '@/stores/permissions'

export interface Access {
  canAdmin: boolean
  canUser: boolean
  canCmdb: boolean
  canDeploy: boolean
  canMonitor: boolean
  canWorkflow: boolean
  canScript: boolean
  canKnowledge: boolean
  canSystem: boolean
  canAudit: boolean
  canAi: boolean
  canItsm: boolean
  canLowcode: boolean
  canAuto: boolean
  canReport: boolean
  canSchedule: boolean
  canCredential: boolean
  canResource: boolean
}

export function useAccess(): Access {
  const permissions = usePermissions((s) => s.permissions)
  const loaded = usePermissions((s) => s.loaded)

  const has = (p: string) => !loaded || permissions.includes(p)

  return {
    canAdmin: has('admin') || has('system:manage'),
    canUser: has('user:read'),
    canCmdb: has('cmdb:read') || has('cmdb:manage'),
    canDeploy: has('deploy:read') || has('deploy:manage'),
    canMonitor: has('monitor:read') || has('monitor:manage'),
    canWorkflow: has('workflow:read') || has('workflow:manage'),
    canScript: has('script:read') || has('script:manage'),
    canKnowledge: has('knowledge:read') || has('knowledge:manage'),
    canSystem: has('system:read') || has('system:manage'),
    canAudit: has('audit:read'),
    canAi: has('ai:read') || has('ai:manage'),
    canItsm: has('itsm:read') || has('itsm:manage'),
    canLowcode: has('lowcode:read') || has('lowcode:manage'),
    canAuto: has('auto:read') || has('auto:manage'),
    canReport: has('report:read'),
    canSchedule: has('schedule:read') || has('schedule:manage'),
    canCredential: has('credential:read') || has('credential:manage'),
    canResource: has('resource:read') || has('resource:manage'),
  }
}
