
import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Result } from 'antd'

interface ModuleErrorProps {
  error: Error & { digest?: string }
  reset: () => void
  moduleName?: string
}

export default function ModuleError({ error, reset, moduleName = '模块' }: ModuleErrorProps) {
  const navigate = useNavigate()
  useEffect(() => { console.error(`[${moduleName} Error]`, error) }, [error, moduleName])
  return (
    <div className="flex items-center justify-center min-h-[60vh]">
      <Result
        status="error"
        title={`${moduleName}加载失败`}
        subTitle={<span className="text-muted-foreground">{error.message || '该模块暂时无法加载，请稍后重试。'}</span>}
        extra={[
          <Button key="retry" type="primary" size="middle" onClick={reset}>
            重新加载
          </Button>,
          <Button key="home" size="middle" onClick={() => navigate('/')}>
            返回首页
          </Button>,
        ]}
      />
    </div>
  )
}
