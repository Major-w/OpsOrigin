// 模型详情 / 编辑：基本信息 + 页签（基本配置 / 训练数据集 / 微调任务）。
// 默认模型(source=default)与池内模型(source=provider)共用此页，保存逻辑按来源分流。
import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Tabs, Tag } from 'antd'
import { ArrowLeft } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { ModelForm } from '@/components/ai/model-form'
import { ModelDatasetTab, ModelFinetuneTab } from '@/components/ai/model-training'
import { useModelRegistry, type LLMProvider } from '@/lib/ai-models'

export default function AIModelDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { loading, error, models, providers, presets, defaultLLM, reload, saveProviders, saveDefaultField } = useModelRegistry()
  const [tab, setTab] = useState('config')

  const record = models.find(m => m.id === decodeURIComponent(id || ''))

  if (error) return <ErrorState message={error} onRetry={reload} />
  if (loading && !models.length) return <Skeleton className="h-64 w-full" />
  if (!record) {
    return (
      <ErrorState
        message={`未找到模型「${decodeURIComponent(id || '')}」，它可能已被删除。`}
        onRetry={() => navigate('/ai/models/list')}
      />
    )
  }

  const saveProvider = async (p: LLMProvider, originalName?: string) => {
    const next = providers.filter(x => x.name !== originalName)
    if (next.some(x => x.name === p.name && p.name !== originalName)) {
      throw new Error(`模型名称「${p.name}」已存在`)
    }
    await saveProviders([...next, p])
  }

  const saveDefault = async (patch: Record<string, string>) => {
    for (const [k, v] of Object.entries(patch)) await saveDefaultField(k, v)
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 flex-wrap">
        <Button variant="ghost" size="sm" onClick={() => navigate('/ai/models/list')}>
          <ArrowLeft className="h-3.5 w-3.5 mr-1" />返回模型列表
        </Button>
        <h2 className="text-sm font-medium">{record.name}</h2>
        {record.primary && <Tag color="gold" className="text-[10px] m-0">主 LLM · 兜底</Tag>}
        {(record.types?.length ? record.types : ['text']).map(t => (
          <Tag key={t} color="blue" className="text-[10px] m-0">
            {t === 'image' ? '图片' : t === 'video' ? '视频' : '文字'}
          </Tag>
        ))}
        <Tag color={record.visibility === 'private' ? 'orange' : 'green'} className="text-[10px] m-0">
          {record.visibility === 'private' ? '仅创建者可用' : '所有人可用'}
        </Tag>
        <Tag className="text-[10px] m-0">权重 {record.weight ?? 0}</Tag>
        {record.healthy !== null && (
          <Tag color={record.healthy ? 'green' : 'red'} className="text-[10px] m-0">{record.healthy ? '健康' : '降级'}</Tag>
        )}
      </div>

      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          {
            key: 'config',
            label: '基本配置',
            children: (
              <ModelForm
                mode="edit"
                record={record}
                presets={presets}
                defaultLLM={defaultLLM}
                onSaveProvider={saveProvider}
                onSaveDefault={saveDefault}
                onSaved={reload}
                onCancel={() => navigate('/ai/models/list')}
              />
            ),
          },
          { key: 'dataset', label: '训练数据集', children: <ModelDatasetTab model={record} /> },
          { key: 'finetune', label: '微调任务', children: <ModelFinetuneTab model={record} /> },
        ]}
      />
    </div>
  )
}
