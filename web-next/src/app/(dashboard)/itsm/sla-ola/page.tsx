'use client'

import { Suspense, useEffect, useState } from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import { Clock, Timer } from 'lucide-react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import SLAPolicyPanel from '@/components/itsm/SLAPolicyPanel'
import OLAPolicyPanel from '@/components/itsm/OLAPolicyPanel'

function SLAOLAContent() {
  const router = useRouter()
  const searchParams = useSearchParams()
  const initial = searchParams.get('tab') === 'ola' ? 'ola' : 'sla'
  const [tab, setTab] = useState<'sla' | 'ola'>(initial)

  useEffect(() => { setTab(searchParams.get('tab') === 'ola' ? 'ola' : 'sla') }, [searchParams])

  const onChange = (v: string) => {
    const next = v === 'ola' ? 'ola' : 'sla'
    setTab(next)
    router.replace(`/itsm/sla-ola?tab=${next}`, { scroll: false })
  }

  return <div className="p-6 space-y-4">
    <div>
      <h1 className="text-xl font-bold">SLA / OLA</h1>
      <p className="text-xs text-muted-foreground">SLA 面向客户承诺服务级别，OLA 约束内部各环节处理时限，两者共同支撑时效达成</p>
    </div>

    <Tabs value={tab} onValueChange={onChange}>
      <TabsList>
        <TabsTrigger value="sla"><Clock className="h-3.5 w-3.5 mr-1.5" />SLA 管理</TabsTrigger>
        <TabsTrigger value="ola"><Timer className="h-3.5 w-3.5 mr-1.5" />OLA 管理</TabsTrigger>
      </TabsList>
      <TabsContent value="sla" className="mt-4"><SLAPolicyPanel /></TabsContent>
      <TabsContent value="ola" className="mt-4"><OLAPolicyPanel /></TabsContent>
    </Tabs>
  </div>
}

export default function ITSMSLAOLAPage() {
  return <Suspense fallback={<div className="p-6 text-sm text-muted-foreground">加载中…</div>}><SLAOLAContent /></Suspense>
}
