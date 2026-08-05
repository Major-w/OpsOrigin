'use client'

import { CollectionPolicyCard } from '@/components/CollectionPolicyCard'
import { Database } from 'lucide-react'

export default function CollectionPolicyPage() {
  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2">
          <Database className="h-6 w-6 text-emerald-500" />采集策略
        </h1>
        <p className="text-sm text-muted-foreground">平台级全局默认采集策略，作为资源接入时的默认值，可逐资源覆盖。</p>
      </div>

      <CollectionPolicyCard />
    </div>
  )
}
