'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { Layout } from '@/components/layout'
import { useAuthStore } from '@/stores/auth'
import { usePermissions } from '@/stores/permissions'

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const { token, init } = useAuthStore()
  const [ready, setReady] = useState(false)

  useEffect(() => {
    init()
    setReady(true)
  }, [init])

  useEffect(() => {
    if (ready && !token) {
      router.replace('/login')
    }
  }, [ready, token, router])

  // token 就绪后拉取当前用户有效权限,供各页面做权限感知(隐藏越权按钮)
  useEffect(() => {
    if (ready && token) {
      usePermissions.getState().fetchMyPermissions()
    }
  }, [ready, token])

  if (!ready || !token) return null

  return <Layout>{children}</Layout>
}
