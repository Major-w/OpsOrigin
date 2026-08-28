
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '@/stores/auth'

export default function HomePage() {
  const navigate = useNavigate()
  const { token, init } = useAuthStore()
  const [ready, setReady] = useState(false)

  useEffect(() => {
    init()
    setReady(true)
  }, [init])

  useEffect(() => {
    if (!ready) return
    if (token) navigate('/dashboard', { replace: true })
    else navigate('/login', { replace: true })
  }, [ready, token, navigate])

  return null
}
