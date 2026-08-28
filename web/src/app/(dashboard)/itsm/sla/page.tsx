
import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'

// SLA 与 OLA 已合并为「SLA/OLA」单一入口，此路由保留用于旧链接跳转
export default function ITSMSLARedirectPage() {
  const navigate = useNavigate()
  useEffect(() => { navigate('/itsm/sla-ola?tab=sla', { replace: true }) }, [navigate])
  return <div className="p-6 text-sm text-muted-foreground">正在跳转至 SLA / OLA…</div>
}
