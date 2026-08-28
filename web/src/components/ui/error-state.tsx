
import { AlertTriangle, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface ErrorStateProps {
  message: string
  onRetry?: () => void
  className?: string
}

// 统一的数据加载错误态：提供「重试」入口与重载动作，避免仅靠 toast 文本而无恢复路径。
export function ErrorState({ message, onRetry, className }: ErrorStateProps) {
  return (
    <div className={`flex flex-col items-center justify-center gap-3 py-12 text-center ${className || ''}`}>
      <AlertTriangle className="h-10 w-10 text-destructive" />
      <p className="text-sm text-muted-foreground">{message}</p>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCw className="h-4 w-4 mr-1" />重试
        </Button>
      )}
    </div>
  )
}
