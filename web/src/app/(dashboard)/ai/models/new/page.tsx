// 新增模型：与模型列表/模型池共用同一入口（两处「新增模型」都跳到这里）。
import { useNavigate } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/ui/error-state'
import { ModelForm } from '@/components/ai/model-form'
import { useModelRegistry } from '@/lib/ai-models'

export default function AIModelNewPage() {
  const navigate = useNavigate()
  const { loading, error, providers, presets, defaultLLM, reload, saveProviders } = useModelRegistry()

  if (error) return <ErrorState message={error} onRetry={reload} />

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" onClick={() => navigate('/ai/models/list')}>
          <ArrowLeft className="h-3.5 w-3.5 mr-1" />返回模型列表
        </Button>
        <h2 className="text-sm font-medium">新增模型</h2>
      </div>

      {loading && !presets.length ? (
        <p className="text-xs text-muted-foreground py-6">加载服务商预设…</p>
      ) : (
        <ModelForm
          mode="create"
          presets={presets}
          defaultLLM={defaultLLM}
          onSaveProvider={async p => {
            if (providers.some(x => x.name === p.name)) throw new Error(`模型名称「${p.name}」已存在`)
            await saveProviders([...providers, p])
          }}
          onSaveDefault={async () => { /* 新增只落模型池 */ }}
          onSaved={() => navigate('/ai/models/list')}
          onCancel={() => navigate('/ai/models/list')}
        />
      )}
    </div>
  )
}
