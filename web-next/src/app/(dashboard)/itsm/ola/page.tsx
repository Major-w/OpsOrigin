'use client'

import { useEffect } from 'react'
import { useRouter } from 'next/navigation'

// SLA 与 OLA 已合并为「SLA/OLA」单一入口，此路由保留用于旧链接跳转
export default function ITSMOLARedirectPage() {
  const router = useRouter()
  useEffect(() => { router.replace('/itsm/sla-ola?tab=ola') }, [router])
  return <div className="p-6 text-sm text-muted-foreground">正在跳转至 SLA / OLA…</div>
}
