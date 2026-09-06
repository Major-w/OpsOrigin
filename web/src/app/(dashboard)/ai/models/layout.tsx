// 模型管理容器：顶部说明 + 子路由出口（模型列表为唯一定义入口，模型池概念已合并）。
import { Outlet } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Plus } from 'lucide-react'
import { useNavigate } from 'react-router-dom'

export default function AIModelsLayout() {
  const navigate = useNavigate()
  return (
    <div className="p-6 space-y-4">
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-lg font-semibold tracking-tight">模型管理</h1>
          <p className="text-xs text-muted-foreground mt-0.5">
            管理已接入的 LLM 模型：权重、类型、可见性与主 LLM 标记均在列表内配置。配置保存后即时热生效，无需重启 Core。
          </p>
        </div>
        <Button size="sm" onClick={() => navigate('/ai/models/new')}>
          <Plus className="h-3.5 w-3.5 mr-1" />新增模型
        </Button>
      </div>

      <Outlet />
    </div>
  )
}
