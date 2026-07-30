/**
 * 全平台统一的名称校验规则，与后端 pkg/validate 保持一致。
 *
 * 名称类字段（脚本名/工作流名/任务名/角色名/模型名等）：
 *   仅允许 字母、数字、横杠(-)、下划线(_)，不允许空格及其他特殊字符，长度 1-64。
 * 主机类名称（Agent/Proxy）在此基础上额外放行点号(.)，以兼容 FQDN。
 */

export const MAX_NAME_LEN = 64

export const NAME_RE = /^[A-Za-z0-9_-]+$/
export const HOST_NAME_RE = /^[A-Za-z0-9._-]+$/

export const NAME_RULE_TEXT =
  '仅允许字母、数字、横杠(-)和下划线(_)，不允许空格及其他特殊字符'
export const HOST_NAME_RULE_TEXT =
  '仅允许字母、数字、点(.)、横杠(-)和下划线(_)，不允许空格及其他特殊字符'

/**
 * 校验名称，合法返回 null，非法返回中文错误信息。
 */
export function validateName(value: string, field = '名称'): string | null {
  if (!value) return `${field}不能为空`
  if (value.length > MAX_NAME_LEN) return `${field}过长（最多 ${MAX_NAME_LEN} 字符）`
  if (!NAME_RE.test(value)) return `${field}格式非法：${NAME_RULE_TEXT}`
  return null
}

/**
 * 校验主机类名称（允许点号），合法返回 null，非法返回中文错误信息。
 */
export function validateHostName(value: string, field = '主机名称'): string | null {
  if (!value) return `${field}不能为空`
  if (value.length > MAX_NAME_LEN) return `${field}过长（最多 ${MAX_NAME_LEN} 字符）`
  if (!HOST_NAME_RE.test(value)) return `${field}格式非法：${HOST_NAME_RULE_TEXT}`
  return null
}

/**
 * 实时输入过滤：剔除所有不允许的字符（含空格），用于 onChange 时即时净化输入。
 */
export function sanitizeName(value: string): string {
  return value.replace(/[^A-Za-z0-9_-]/g, '').slice(0, MAX_NAME_LEN)
}

export function sanitizeHostName(value: string): string {
  return value.replace(/[^A-Za-z0-9._-]/g, '').slice(0, MAX_NAME_LEN)
}
