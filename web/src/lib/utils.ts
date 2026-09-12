
import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatTime(iso?: string | null) {
  if (!iso) return ''
  return new Date(iso).toLocaleString('zh-CN')
}

export const apiBase = '/api/v1'

function getToken(): string | null {
  if (typeof window === 'undefined') return null
  return localStorage.getItem('opsorigin_token')
}

function getPrincipal(): { type: string; id: string } | null {
  if (typeof window === 'undefined') return null
  const userStr = localStorage.getItem('opsorigin_user')
  if (userStr) {
    try {
      const user = JSON.parse(userStr)
      return { type: 'HUMAN', id: user.user_id || user.id || user.username || 'unknown' }
    } catch {}
  }
  return null
}

// 当前用户权限集合（由 permissions store 在 fetchMyPermissions 成功后持久化）。
// 后端 CMDB 字段级规则权限（P2-4）据此判断对字段的可见/只读。
function getPermissions(): string[] | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = localStorage.getItem('opsorigin_permissions')
    if (!raw) return null
    const arr = JSON.parse(raw)
    return Array.isArray(arr) && arr.length > 0 ? arr : null
  } catch { return null }
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'X-Trace-ID': typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2),
  }
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`
  const principal = getPrincipal()
  if (principal) {
    headers['X-Principal-Type'] = principal.type
    headers['X-Principal-ID'] = principal.id
  }
  const perms = getPermissions()
  if (perms) headers['X-Permissions'] = perms.join(',')

  // 同 apiFetchRaw：调用方自带 headers 时与默认鉴权头合并，不能整体覆盖。
  const res = await fetch(`${apiBase}${path}`, {
    ...init,
    headers: { ...headers, ...((init?.headers as Record<string, string> | undefined) ?? {}) },
  })
  if (res.status === 401) {
    if (typeof window !== 'undefined') {
      localStorage.removeItem('opsorigin_token')
      localStorage.removeItem('opsorigin_user')
      window.location.href = '/login'
    }
    throw new Error('unauthorized')
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || `HTTP ${res.status}`)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

/**
 * 返回原始 Response 的底层请求（用于下载 blob / 上传文件等需要非 JSON 体的场景）。
 * 自动附带与 apiFetch 一致的鉴权头与 principal 头，并处理 401 跳转。
 */
export async function apiFetchRaw(
  path: string,
  init?: RequestInit,
  // redirectOn401=false: 401 时只抛错不跳转登录。用于导出/PPT 等"可失败"的旁路操作——
  // 全局跳转会把正在编辑的用户整页踢走, 这类操作失败只需提示, 交由用户自行决定。
  opts?: { redirectOn401?: boolean },
): Promise<Response> {
  const headers: Record<string, string> = {
    'X-Trace-ID': typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2),
  }
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`
  const principal = getPrincipal()
  if (principal) {
    headers['X-Principal-Type'] = principal.type
    headers['X-Principal-ID'] = principal.id
  }
  const perms = getPermissions()
  if (perms) headers['X-Permissions'] = perms.join(',')

  // init.headers 必须与默认鉴权头「合并」而非覆盖：调用方只要自带 Content-Type，
  // 旧写法 `{ headers, ...init }` 会把 Authorization/X-Trace-ID 整个顶掉 → 401。
  const res = await fetch(`${apiBase}${path}`, {
    ...init,
    headers: { ...headers, ...((init?.headers as Record<string, string> | undefined) ?? {}) },
  })
  if (res.status === 401) {
    if (typeof window !== 'undefined' && opts?.redirectOn401 !== false) {
      localStorage.removeItem('opsorigin_token')
      localStorage.removeItem('opsorigin_user')
      window.location.href = '/login'
    }
    throw new Error('unauthorized')
  }
  return res
}

/**
 * 列表接口专用:保证一定返回数组,永不返回 null/undefined。
 *
 * 后端存在两种"非数组"的列表响应形态,直接 apiFetch<T[]> 会让页面崩溃:
 *  1. Go 的 nil slice 序列化成 `null` → `list.map()` 抛
 *     "Cannot read properties of null"(应用管理/流程定义/工单列表都栽在这);
 *  2. 分页接口返回 `{ items, total, limit, offset }` 对象 → `list.find()`
 *     不是函数(低代码编排栽在这)。
 * 后端已统一规范为返回 [],这里再兜一层,保证接口异常也不白屏。
 */
export async function apiList<T>(path: string, init?: RequestInit): Promise<T[]> {
  const data = await apiFetch<unknown>(path, init)
  if (Array.isArray(data)) return data as T[]
  if (data && typeof data === 'object' && Array.isArray((data as { items?: unknown }).items)) {
    return (data as { items: T[] }).items
  }
  return []
}
