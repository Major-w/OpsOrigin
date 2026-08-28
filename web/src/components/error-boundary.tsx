
import { Component, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { AlertTriangle, RefreshCw } from 'lucide-react'

interface Props { children: ReactNode }
interface State { hasError: boolean; error?: Error }

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = { hasError: false }
  }
  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }
  render() {
    if (this.state.hasError) {
      return (
        <div className="flex items-center justify-center min-h-[60vh] p-6">
          <div className="text-center max-w-md space-y-4">
            <AlertTriangle className="h-16 w-16 mx-auto text-amber-500" />
            <h2 className="text-xl font-bold text-gray-900 dark:text-white">页面出现错误</h2>
            <p className="text-sm text-muted-foreground">
              非常抱歉，页面发生了意外错误。您可以尝试刷新页面或返回首页。
            </p>
            {this.state.error && (
              <details className="text-left text-xs text-muted-foreground bg-muted/30 rounded-md p-3 max-h-32 overflow-auto">
                <summary className="cursor-pointer font-medium mb-1">错误详情</summary>
                <p className="font-mono break-all">{this.state.error.message}</p>
              </details>
            )}
            <div className="flex gap-2 justify-center">
              <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
                <RefreshCw className="h-4 w-4 mr-1" />刷新页面
              </Button>
              <Button size="sm" onClick={() => window.location.href = '/'}>返回首页</Button>
            </div>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}

export function NotFound() {
  const navigate = useNavigate()
  return (
    <div className="flex items-center justify-center min-h-[60vh] p-6">
      <div className="text-center max-w-md space-y-4">
        <div className="text-8xl font-bold text-muted-foreground/20">404</div>
        <h2 className="text-xl font-bold text-gray-900 dark:text-white">页面未找到</h2>
        <p className="text-sm text-muted-foreground">您访问的页面不存在或已被移动。</p>
        <Button size="sm" onClick={() => navigate('/')}>返回首页</Button>
      </div>
    </div>
  )
}

export function ServerError() {
  const navigate = useNavigate()
  return (
    <div className="flex items-center justify-center min-h-[60vh] p-6">
      <div className="text-center max-w-md space-y-4">
        <div className="text-8xl font-bold text-muted-foreground/20">500</div>
        <h2 className="text-xl font-bold text-gray-900 dark:text-white">服务器错误</h2>
        <p className="text-sm text-muted-foreground">后端服务暂时不可用，请稍后重试或联系管理员。</p>
        <div className="flex gap-2 justify-center">
          <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
            <RefreshCw className="h-4 w-4 mr-1" />重试
          </Button>
          <Button size="sm" onClick={() => navigate('/')}>返回首页</Button>
        </div>
      </div>
    </div>
  )
}
