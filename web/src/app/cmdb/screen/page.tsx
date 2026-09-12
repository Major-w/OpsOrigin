
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '@/stores/auth'
import CMDBBigScreen from '@/components/cmdb/big-screen'

// 独立投放大屏：脱离后台侧栏/顶栏，整屏固定、固定深色、自动刷新。
// 鉴权守卫沿用 DashboardLayout 逻辑（无 token 跳登录），但不渲染任何后台框架。
export default function CMDBScreenPage() {
  const navigate = useNavigate()
  const { token, init } = useAuthStore()
  const [ready, setReady] = useState(false)

  useEffect(() => {
    init()
    setReady(true)
  }, [init])

  useEffect(() => {
    if (ready && !token) navigate('/login', { replace: true })
  }, [ready, token, navigate])

  if (!ready || !token) return null

  return <CMDBBigScreen mode="standalone" />
}
