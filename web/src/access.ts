import { usePermissions } from '@/stores/permissions'

/**
 * 前端访问位与后端权限点的映射。
 *
 * 约定：后端 `pkg/auth/rbac.go` 的权限常量是唯一真源（词表 `module:read|write|manage|exec`），
 * 本文件只做「权限点 → 界面能力」的投影，不得引入后端不存在的权限名，
 * 否则该访问位恒为 false（权限加载完成后），菜单会被静默隐藏。
 */
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
  // 知识库检索可观测/测试台（manage 级）：insights/retrieval 菜单门控
  canKnowledgeManage: boolean
}

export function useAccess(): Access {
  const permissions = usePermissions((s) => s.permissions)
  const loaded = usePermissions((s) => s.loaded)

  // 权限尚未加载完成时按「放行」处理，避免首屏菜单闪烁；加载完成后以真实权限判定。
  const has = (p: string) => !loaded || permissions.includes(p)

  return {
    // 系统级管理：后端不返回 "admin" 字样，统一用 system:admin（admin 角色默认持有）
    canAdmin: has('system:admin'),
    canUser: has('user:read'),
    canCmdb: has('cmdb:read'),
    canDeploy: has('deploy:read'),
    canMonitor: has('monitor:read'),
    canWorkflow: has('workflow:read'),
    canScript: has('script:read'),
    canKnowledge: has('knowledge:read'),
    canSystem: has('system:admin'),
    canAudit: has('audit:read'),
    canAi: has('ai:read'),
    canItsm: has('itsm:read'),
    canLowcode: has('lowcode:read'),
    // 自动化入站渠道配置（企微/钉钉/飞书/邮件）
    canAuto: has('automation:read'),
    canReport: has('report:read'),
    canSchedule: has('schedule:read'),
    canCredential: has('credential:read'),
    // 资源视图读的是 CMDB 资源，沿用 CMDB 读权限
    canResource: has('cmdb:read'),
    // 知识库检索可观测/测试台（manage 级）：insights/retrieval 菜单门控
    canKnowledgeManage: has('knowledge:manage'),
  }
}
