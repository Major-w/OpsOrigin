
import { useEffect } from 'react'
import { Button, Result } from 'antd'
import { useNavigate } from 'react-router-dom'

export default function Error({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  const navigate = useNavigate()

  useEffect(() => { console.error('[PageError]', error) }, [error])

  return (
    <div className="flex items-center justify-center min-h-[80vh]">
      <Result
        status="error"
        title="页面加载失败"
        subTitle={<span className="text-muted-foreground">{error.message || '发生了一个意外错误，请稍后重试。'}</span>}
        extra={[
          <Button key="retry" type="primary" onClick={reset}>
            重新加载
          </Button>,
          <Button key="home" onClick={() => navigate('/')}>
            返回首页
          </Button>,
        ]}
      >
        {error.digest && (
          <div className="mt-2 text-xs text-muted-foreground/50 font-mono">
            Error ID: {error.digest}
          </div>
        )}
      </Result>
    </div>
  )
}
