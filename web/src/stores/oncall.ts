
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { message } from 'antd'
export interface Shift { id: string; user_name: string; role: string; start_time: string; end_time: string; phone?: string; email?: string }
export interface Rotation { id: string; name: string; members: string[]; cycle_days: number; labels?: Record<string, string> }

interface OnCallState {
  shifts: Shift[]; rotations: Rotation[]; loading: boolean
  /** rotation_id -> 当前当班成员(轮转解析) */
  currentOncall: Record<string, string>
  fetchShifts: () => Promise<void>
  addShift: (data: any) => Promise<boolean>
  fetchRotations: () => Promise<void>
  createRotation: (data: any) => Promise<boolean>
  deleteRotation: (id: string) => Promise<boolean>
  /** 查询轮值组当前当班成员并缓存 */
  fetchCurrentOncall: (id: string) => Promise<void>
}

export const useOnCallStore = create<OnCallState>((set, get) => ({
  shifts: [], rotations: [], loading: false, currentOncall: {},
  fetchShifts: async () => {
    set({ loading: true })
    try {
      const shifts = await apiList<Shift>('/oncall')
      set({ shifts, loading: false })
    } catch (e: any) {
      set({ loading: false })
      message.error(`加载值班排班失败: ${e.message}`)
    }
  },
  addShift: async (data) => {
    try {
      await apiFetch('/oncall', { method: 'POST', body: JSON.stringify(data) })
      message.success('添加值班成功')
      await get().fetchShifts()
      return true
    } catch (e: any) {
      message.error(`添加值班失败: ${e.message}`)
      return false
    }
  },
  fetchRotations: async () => {
    set({ loading: true })
    try {
      const rotations = await apiList<Rotation>('/oncall/rotations')
      set({ rotations, loading: false })
      // 顺带解析各轮值组当前当班成员
      rotations.forEach(r => get().fetchCurrentOncall(r.id))
    } catch (e: any) {
      set({ loading: false })
      message.error(`加载轮值列表失败: ${e.message}`)
    }
  },
  createRotation: async (data) => {
    try {
      await apiFetch('/oncall/rotations', { method: 'POST', body: JSON.stringify(data) })
      message.success('创建轮值成功')
      await get().fetchRotations()
      return true
    } catch (e: any) {
      message.error(`创建轮值失败: ${e.message}`)
      return false
    }
  },
  deleteRotation: async (id) => {
    try {
      await apiFetch(`/oncall/rotations/${id}`, { method: 'DELETE' })
      set(s => ({ rotations: s.rotations.filter(r => r.id !== id) }))
      message.success('删除轮值成功')
      return true
    } catch (e: any) {
      message.error(`删除轮值失败: ${e.message}`)
      return false
    }
  },
  fetchCurrentOncall: async (id) => {
    try {
      const d = await apiFetch<{ member: string }>(`/oncall/rotations/${id}/oncall`)
      if (d.member) set(s => ({ currentOncall: { ...s.currentOncall, [id]: d.member } }))
    } catch { /* 静默: 单成员组等边界无需提示 */ }
  },
}))
