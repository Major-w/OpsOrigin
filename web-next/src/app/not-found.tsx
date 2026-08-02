'use client'

import { Button } from '@/components/ui/button'

export default function NotFound() {
  return (
    <div className="flex items-center justify-center min-h-[60vh] p-6">
      <div className="text-center max-w-md space-y-4">
        <div className="text-8xl font-bold text-muted-foreground/20">404</div>
        <h2 className="text-xl font-bold">页面未找到</h2>
        <p className="text-sm text-muted-foreground">您访问的页面不存在或已被移动。</p>
        <Button size="sm" onClick={() => window.location.href = '/'}>返回首页</Button>
      </div>
    </div>
  )
}
