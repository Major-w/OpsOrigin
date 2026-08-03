'use client'

import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'

export interface Shift { id: string; user_name: string; role: string; start_time: string; end_time: string; phone?: string; email?: string }
export interface Rotation { id: string; name: string; members: string[]; cycle_days: number }

interface OnCallState {
  shifts: Shift[]; rotations: Rotation[]; loading: boolean
  fetchShifts: () => Promise<void>
  addShift: (data: any) => Promise<boolean>
  fetchRotations: () => Promise<void>
  createRotation: (data: any) => Promise<boolean>
  deleteRotation: (id: string) => Promise<boolean>
}

export const useOnCallStore = create<OnCallState>((set, get) => ({
  shifts: [], rotations: [], loading: false,
  fetchShifts: async () => {
    set({ loading: true })
    try {
      const shifts = await apiList<Shift>('/oncall')
      set({ shifts, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载值班排班失败: ${e.message}`)
    }
  },
  addShift: async (data) => {
    try {
      await apiFetch('/oncall', { method: 'POST', body: JSON.stringify(data) })
      toast.success('添加值班成功')
      await get().fetchShifts()
      return true
    } catch (e: any) {
      toast.error(`添加值班失败: ${e.message}`)
      return false
    }
  },
  fetchRotations: async () => {
    set({ loading: true })
    try {
      const rotations = await apiList<Rotation>('/oncall/rotations')
      set({ rotations, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载轮值列表失败: ${e.message}`)
    }
  },
  createRotation: async (data) => {
    try {
      await apiFetch('/oncall/rotations', { method: 'POST', body: JSON.stringify(data) })
      toast.success('创建轮值成功')
      await get().fetchRotations()
      return true
    } catch (e: any) {
      toast.error(`创建轮值失败: ${e.message}`)
      return false
    }
  },
  deleteRotation: async (id) => {
    try {
      await apiFetch(`/oncall/rotations/${id}`, { method: 'DELETE' })
      set(s => ({ rotations: s.rotations.filter(r => r.id !== id) }))
      toast.success('删除轮值成功')
      return true
    } catch (e: any) {
      toast.error(`删除轮值失败: ${e.message}`)
      return false
    }
  },
}))
