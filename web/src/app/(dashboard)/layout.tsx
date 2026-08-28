
import { useEffect, useState } from 'react'
import { useNavigate, Outlet } from 'react-router-dom'
import { Layout } from '@/components/layout'
import { useAuthStore } from '@/stores/auth'
import { usePermissions } from '@/stores/permissions'

export default function DashboardLayout() {
  const navigate = useNavigate()
  const { token, init } = useAuthStore()
  const [ready, setReady] = useState(false)

  useEffect(() => {
    init()
    setReady(true)
  }, [init])

  useEffect(() => {
    if (ready && !token) {
      navigate('/login', { replace: true })
    }
  }, [ready, token, navigate])

  useEffect(() => {
    if (ready && token) {
      usePermissions.getState().fetchMyPermissions()
    }
  }, [ready, token])

  if (!ready || !token) return null

  return <Layout><Outlet /></Layout>
}
