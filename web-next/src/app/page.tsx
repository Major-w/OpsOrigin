'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { useAuthStore } from '@/stores/auth'

export default function HomePage() {
  const router = useRouter()
  const { token, init } = useAuthStore()
  const [ready, setReady] = useState(false)

  useEffect(() => {
    init()
    setReady(true)
  }, [init])

  useEffect(() => {
    if (!ready) return
    if (token) router.replace('/dashboard')
    else router.replace('/login')
  }, [ready, token, router])

  return null
}
