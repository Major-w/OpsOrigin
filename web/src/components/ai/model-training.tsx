// 模型详情页的训练相关页签：训练数据集 + 微调任务。
// 数据集为平台全量反馈样本（后端按全量导出，未做单模型拆分），此处以「当前模型作为基座」的视角呈现；
// 微调任务按 base_model 过滤，只展示以当前模型为基座的任务。
import { useEffect, useState } from 'react'
import { Button, Input, Tag } from 'antd'
import { Database, Download, RefreshCw, Sparkles, Loader2 } from 'lucide-react'
import { apiFetch, apiFetchRaw } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { ErrorState } from '@/components/ui/error-state'
import { fetchFinetuneTasks, type ModelRecord } from '@/lib/ai-models'

interface DatasetStats { total: number; preference_up: number; preference_down: number; disposal_cases: number }

interface FinetuneTaskItem {
  id: string
  name: string
  base_model: string
  method: string
  status: string
  result_model?: string
  result_provider_name?: string
  error?: string
  created_at: string
}

/** 「训练数据集」页签 */
export function ModelDatasetTab({ model }: { model: ModelRecord }) {
  const [stats, setStats] = useState<DatasetStats | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const d = await apiFetch<any>('/ai/dataset?format=json')
      setStats(d?.summary || null)
    } catch (e: any) {
      setError(`获取数据集统计失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  const download = async () => {
    setLoading(true)
    try {
      const r = await apiFetchRaw('/ai/dataset?format=jsonl')
      if (!r.ok) throw new Error(`导出失败: ${r.status}`)
      const blob = await r.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'ai-dataset.ndjson'
      a.click()
      URL.revokeObjectURL(url)
      getMessage().success('数据集已导出')
    } catch (e: any) {
      getMessage().error(`导出失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  return (
    <section className="space-y-2">
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <div>
          <h3 className="text-sm font-medium flex items-center gap-1.5"><Database className="h-4 w-4" />训练数据集</h3>
          <p className="text-xs text-muted-foreground mt-0.5">
            以「{model.name}」为基座视角的训练样本：点赞偏好 / 点踩偏好 / 处置案例（知识库）。样本为平台全量反馈，导出后可用于该模型的领域微调。
          </p>
        </div>
        <div className="flex gap-2 shrink-0">
          <Button size="middle" onClick={load} disabled={loading}>
            <RefreshCw className={loading ? 'h-3.5 w-3.5 mr-1 animate-spin' : 'h-3.5 w-3.5 mr-1'} />查看统计
          </Button>
          <Button size="middle" type="primary" onClick={download} disabled={loading || !stats?.total}>
            <Download className="h-3.5 w-3.5 mr-1" />导出 JSONL
          </Button>
        </div>
      </div>

      {error ? <ErrorState message={error} onRetry={load} className="py-6" /> : stats && stats.total > 0 ? (
        <div className="grid grid-cols-4 gap-2">
          {[
            { label: '总样本', value: stats.total, cls: '' },
            { label: '点赞偏好', value: stats.preference_up, cls: 'text-blue-500' },
            { label: '点踩偏好', value: stats.preference_down, cls: 'text-destructive' },
            { label: '处置案例', value: stats.disposal_cases, cls: 'text-blue-500' },
          ].map(c => (
            <div key={c.label} className="rounded-lg bg-muted/50 px-3 py-2">
              <div className="text-[11px] text-muted-foreground">{c.label}</div>
              <div className={`text-lg font-semibold ${c.cls}`}>{c.value}</div>
            </div>
          ))}
        </div>
      ) : !loading ? (
        <p className="text-xs text-muted-foreground py-3">暂无训练样本。用户在对话中的点赞/点踩与知识库处置案例会自动累积到这里。</p>
      ) : null}
    </section>
  )
}

/** 「微调任务」页签 */
export function ModelFinetuneTab({ model }: { model: ModelRecord }) {
  const [tasks, setTasks] = useState<FinetuneTaskItem[]>([])
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState({ name: '', base_model: model.model, method: 'loRA' })

  const load = async () => {
    setLoading(true)
    try {
      const all = await fetchFinetuneTasks()
      setTasks(all.filter(t => t.base_model === model.model))
    } catch (e: any) {
      getMessage().error(`加载微调任务失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { setForm(f => ({ ...f, base_model: model.model })); load() }, [model.model])

  const create = async () => {
    if (!form.name.trim()) return getMessage().error('请填写任务名')
    setCreating(true)
    try {
      await apiFetch('/ai/finetune/tasks', {
        method: 'POST',
        body: JSON.stringify({ name: form.name.trim(), base_model: form.base_model, method: form.method }),
      })
      getMessage().success('微调任务已创建，产物将自动注册进模型池')
      setForm(f => ({ ...f, name: '' }))
      load()
    } catch (e: any) {
      getMessage().error(`创建失败: ${e.message}`)
    } finally {
      setCreating(false)
    }
  }

  const statusTag = (s: string) => (
    <Tag color={s === 'succeeded' ? 'green' : s === 'failed' ? 'red' : s === 'training' ? 'orange' : 'default'} className="text-[10px] m-0">
      {s === 'succeeded' ? '成功' : s === 'failed' ? '失败' : s === 'training' ? '训练中' : '排队'}
    </Tag>
  )

  return (
    <section className="space-y-2">
      <div>
        <h3 className="text-sm font-medium flex items-center gap-1.5"><Sparkles className="h-4 w-4" />微调任务</h3>
        <p className="text-xs text-muted-foreground mt-0.5">
          以「{model.name}」为基座（{model.model}）执行领域微调，产物注册进模型池供智能体选用。
        </p>
      </div>

      <div className="flex gap-2 items-end flex-wrap">
        <div className="flex-1 min-w-[160px]">
          <label className="text-xs text-muted-foreground">任务名</label>
          <Input className="mt-1" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="如: 容量预测微调" />
        </div>
        <div className="w-32">
          <label className="text-xs text-muted-foreground">方式</label>
          <select className="mt-1 h-8 w-full rounded-md border border-input bg-background px-2 text-xs" value={form.method} onChange={e => setForm(f => ({ ...f, method: e.target.value }))}>
            <option value="loRA">loRA</option>
            <option value="full">full</option>
            <option value="api">api</option>
          </select>
        </div>
        <Button size="middle" type="primary" onClick={create} disabled={creating}>
          {creating ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Sparkles className="h-3.5 w-3.5 mr-1" />}创建任务
        </Button>
        <Button size="middle" onClick={load} disabled={loading}>
          <RefreshCw className={loading ? 'h-3 w-3 mr-1 animate-spin' : 'h-3 w-3 mr-1'} />刷新
        </Button>
      </div>

      {tasks.length === 0 ? (
        <p className="text-xs text-muted-foreground py-3">
          {loading ? '加载中…' : `暂无以 ${model.model} 为基座的微调任务。`}
        </p>
      ) : (
        <div className="space-y-2">
          {tasks.map(t => (
            <div key={t.id} className="flex items-center justify-between rounded-lg bg-muted/50 px-3 py-2">
              <div className="min-w-0">
                <div className="text-sm font-medium truncate">{t.name} <span className="text-xs text-muted-foreground ml-1">{t.base_model} · {t.method}</span></div>
                <div className="text-[11px] text-muted-foreground truncate">
                  {t.result_provider_name ? `已注册: ${t.result_provider_name}（${t.result_model}）` : t.error || new Date(t.created_at).toLocaleString()}
                </div>
              </div>
              {statusTag(t.status)}
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
