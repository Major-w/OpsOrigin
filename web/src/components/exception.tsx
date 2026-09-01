import { Button, Result } from 'antd'
import { useNavigate } from 'react-router-dom'

type ExceptionType = '403' | '404' | '500'

const config: Record<ExceptionType, { status: 403 | 404 | 500; title: string; subTitle: string }> = {
  '403': { status: 403, title: '无权限访问', subTitle: '抱歉，您没有权限访问此页面' },
  '404': { status: 404, title: '页面不存在', subTitle: '抱歉，您访问的页面不存在' },
  '500': { status: 500, title: '服务器错误', subTitle: '抱歉，服务器出了点问题' },
}

export default function ExceptionPage({ type = '404' }: { type?: ExceptionType }) {
  const navigate = useNavigate()
  const c = config[type]

  return (
    <div className="flex items-center justify-center min-h-[60vh]">
      <Result
        status={c.status}
        title={c.title}
        subTitle={c.subTitle}
        extra={
          <Button type="primary" onClick={() => navigate('/dashboard')}>
            返回首页
          </Button>
        }
      />
    </div>
  )
}
