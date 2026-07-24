'use client'

import { useEffect } from 'react'
import { Button } from '@/components/ui/button'
import { AlertTriangle, RefreshCw } from 'lucide-react'

export default function Error({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  useEffect(() => { console.error('page error:', error) }, [error])
  return (
    <div className="flex items-center justify-center min-h-[60vh] p-6">
      <div className="text-center max-w-md space-y-4">
        <AlertTriangle className="h-16 w-16 mx-auto text-amber-500" />
        <h2 className="text-xl font-bold">页面加载失败</h2>
        <p className="text-sm text-muted-foreground">{error.message || '发生了一个意外错误。'}</p>
        <div className="flex gap-2 justify-center">
          <Button variant="outline" size="sm" onClick={reset}><RefreshCw className="h-4 w-4 mr-1" />重试</Button>
          <Button size="sm" onClick={() => window.location.href = '/'}>返回首页</Button>
        </div>
      </div>
    </div>
  )
}
