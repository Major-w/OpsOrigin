
import { Suspense, useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Clock, Timer } from 'lucide-react'
import { Tabs } from 'antd'
import SLAPolicyPanel from '@/components/itsm/SLAPolicyPanel'
import OLAPolicyPanel from '@/components/itsm/OLAPolicyPanel'

function SLAOLAContent() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const initial = searchParams.get('tab') === 'ola' ? 'ola' : 'sla'
  const [tab, setTab] = useState<'sla' | 'ola'>(initial)

  useEffect(() => { setTab(searchParams.get('tab') === 'ola' ? 'ola' : 'sla') }, [searchParams])

  const onChange = (v: string) => {
    const next = v === 'ola' ? 'ola' : 'sla'
    setTab(next)
    navigate(`/itsm/sla-ola?tab=${next}`, { replace: true })
  }

  return <div className="p-6 space-y-4">
    <div>
      <h1 className="text-2xl font-bold">SLA / OLA</h1>
      <p className="text-xs text-muted-foreground">SLA 面向客户承诺服务级别，OLA 约束内部各环节处理时限，两者共同支撑时效达成</p>
    </div>

    <Tabs activeKey={tab} onChange={onChange} items={[
      { key: 'sla', label: <><Clock className="h-3.5 w-3.5 mr-1.5" />SLA 管理</>, children: <div className="mt-4"><SLAPolicyPanel /></div> },
      { key: 'ola', label: <><Timer className="h-3.5 w-3.5 mr-1.5" />OLA 管理</>, children: <div className="mt-4"><OLAPolicyPanel /></div> },
    ]} />
  </div>
}

export default function ITSMSLAOLAPage() {
  return <Suspense fallback={<div className="p-6 text-sm text-muted-foreground">加载中…</div>}><SLAOLAContent /></Suspense>
}
