// 模型列表：已定义的模型（providers），权重/类型/可见性/主LLM 均在此内联配置。
// 视角=「有哪些模型、怎么路由」：auto 模式按权重 + 健康 + 类型 + 可见性自动匹配。
import { useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { ProTable, type ProColumns } from '@ant-design/pro-components'
import { InputNumber, Input, Select, Tag } from 'antd'
import { Button } from '@/components/ui/button'
import { Plus, Pencil, Trash2, Loader2, Activity, Search } from 'lucide-react'
import { getMessage } from '@/lib/message'
import { ErrorState } from '@/components/ui/error-state'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { useModelRegistry, testModelConnection, type LLMProvider, type ModelRecord } from '@/lib/ai-models'
import { useAuthStore } from '@/stores/auth'

const TYPE_TAG: Record<string, { color: string; label: string }> = {
  text: { color: 'blue', label: '文字' },
  image: { color: 'purple', label: '图片' },
  video: { color: 'geekblue', label: '视频' },
}

export default function AIModelListPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const q = searchParams.get('q') || ''
  const type = searchParams.get('type') || 'all'

  const { loading, error, models, providers, reload, saveProviders } = useModelRegistry()
  const [deleting, setDeleting] = useState<ModelRecord | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [testingId, setTestingId] = useState<string | null>(null)
  // 内联权重编辑的暂存值：编辑中未提交的权重 {name: value}
  const [weightDraft, setWeightDraft] = useState<Record<string, number>>({})
  const [savingWeight, setSavingWeight] = useState(false)

  const me = useAuthStore(s => s.user)
  const myId = String(me?.user_id || '')

  const setParam = (patch: Record<string, string>) => {
    const next = new URLSearchParams(searchParams)
    for (const [k, v] of Object.entries(patch)) {
      if (!v || v === 'all') next.delete(k)
      else next.set(k, v)
    }
    setSearchParams(next, { replace: true })
  }

  const filtered = useMemo(() => models.filter(m => {
    if (type !== 'all' && ['text', 'image', 'video'].includes(type) && !m.types?.includes(type)) return false
    if (type === 'mine' && m.visibility === 'private' && m.created_by !== myId) return false
    if (!q.trim()) return true
    const k = q.trim().toLowerCase()
    return [m.name, m.model, m.base_url, m.providerLabel || ''].some(v => v.toLowerCase().includes(k))
  }), [models, q, type, myId])

  const handleDelete = async () => {
    if (!deleting) return
    setSubmitting(true)
    try {
      await saveProviders(providers.filter(p => p.name !== deleting.id))
      getMessage().success(`模型「${deleting.name}」已删除，即时生效`)
      setDeleting(null)
      reload()
    } catch (e: any) {
      getMessage().error(`删除失败: ${e.message}`)
    } finally {
      setSubmitting(false)
    }
  }

  /** 内联权重保存：按 name 更新对应 provider 的 weight 后整池覆盖 */
  const handleWeightChange = async (name: string, weight: number) => {
    setWeightDraft(d => { const n = { ...d }; delete n[name]; return n })
    if ((providers.find(p => p.name === name)?.weight ?? 0) === weight) return
    setSavingWeight(true)
    try {
      const next: LLMProvider[] = providers.map(p => (p.name === name ? { ...p, weight } : p))
      await saveProviders(next)
      getMessage().success(`「${name}」权重已更新为 ${weight}，即时生效`)
      reload()
    } catch (e: any) {
      getMessage().error(`权重保存失败: ${e.message}`)
    } finally {
      setSavingWeight(false)
    }
  }

  const handleTest = async (m: ModelRecord) => {
    setTestingId(m.id)
    const res = await testModelConnection()
    getMessage().info(`${m.name}：${res}`)
    setTestingId(null)
  }

  if (error) return <ErrorState message={error} onRetry={reload} />

  const columns: ProColumns<ModelRecord>[] = [
    {
      title: '名称',
      dataIndex: 'name',
      width: 200,
      render: (_, r) => (
        <div className="flex items-center gap-1.5 min-w-0">
          <span className="text-sm font-medium truncate">{r.name}</span>
          {r.primary && <Tag color="gold" className="text-[10px] m-0 shrink-0">主 LLM</Tag>}
        </div>
      ),
    },
    { title: '模型 ID', dataIndex: 'model', width: 170, render: (_, r) => <span className="font-mono text-xs">{r.model}</span> },
    { title: '服务商', dataIndex: 'providerLabel', width: 120, render: (_, r) => <span className="text-xs text-muted-foreground">{r.providerLabel || '自定义'}</span> },
    {
      title: '类型',
      dataIndex: 'types',
      width: 130,
      render: (_, r) => {
        const types = r.types?.length ? r.types : ['text']
        return (
          <div className="flex items-center gap-1 flex-wrap">
            {types.map(t => {
              const tag = TYPE_TAG[t] || TYPE_TAG.text
              return <Tag key={t} color={tag.color} className="text-[10px] m-0">{tag.label}</Tag>
            })}
          </div>
        )
      },
    },
    {
      title: '可见性',
      dataIndex: 'visibility',
      width: 90,
      render: (_, r) => r.visibility === 'private'
        ? <Tag color="orange" className="text-[10px] m-0">{r.created_by === myId ? '仅自己' : '私有'}</Tag>
        : <Tag color="green" className="text-[10px] m-0">所有人</Tag>,
    },
    { title: 'Base URL', dataIndex: 'base_url', render: (_, r) => <span className="font-mono text-xs text-muted-foreground truncate">{r.base_url || '—'}</span> },
    {
      title: '权重',
      dataIndex: 'weight',
      width: 110,
      render: (_, r) => (
        <InputNumber
          size="small"
          min={0}
          max={100}
          value={weightDraft[r.id] ?? r.weight ?? 0}
          onChange={v => setWeightDraft(d => ({ ...d, [r.id]: typeof v === 'number' ? v : 0 }))}
          onPressEnter={() => handleWeightChange(r.id, weightDraft[r.id] ?? r.weight ?? 0)}
          onBlur={() => { if (weightDraft[r.id] !== undefined) handleWeightChange(r.id, weightDraft[r.id]) }}
          disabled={savingWeight}
          className="w-20"
        />
      ),
    },
    {
      title: '健康',
      dataIndex: 'healthy',
      width: 80,
      render: (_, r) => r.healthy === null
        ? <span className="text-xs text-muted-foreground">—</span>
        : <Tag color={r.healthy ? 'green' : 'red'} className="text-[10px] m-0">{r.healthy ? '健康' : '降级'}</Tag>,
    },
    {
      title: '操作',
      key: 'op',
      width: 150,
      align: 'right',
      render: (_, r) => (
        <div className="flex items-center justify-end gap-1">
          <Button size="sm" variant="ghost" onClick={() => navigate(`/ai/models/${encodeURIComponent(r.id)}`)} title="查看/编辑">
            <Pencil className="h-3.5 w-3.5" />
          </Button>
          <Button size="sm" variant="ghost" onClick={() => handleTest(r)} disabled={testingId === r.id} title="测试连接">
            {testingId === r.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Activity className="h-3.5 w-3.5" />}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setDeleting(r)}
            title="删除"
            className="text-destructive"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      ),
    },
  ]

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 flex-wrap">
        <Input
          allowClear
          value={q}
          onChange={e => setParam({ q: e.target.value })}
          placeholder="搜索名称 / 模型 ID / Base URL"
          prefix={<Search className="h-3.5 w-3.5 text-muted-foreground" />}
          className="w-64"
        />
        <Select
          value={type}
          onChange={v => setParam({ type: v })}
          className="w-36"
          options={[
            { value: 'all', label: '全部类型' },
            { value: 'text', label: '文字模型' },
            { value: 'image', label: '图片模型' },
            { value: 'video', label: '视频模型' },
            { value: 'mine', label: '我可见的' },
          ]}
        />
        <div className="flex-1" />
        <Button size="sm" variant="primary" onClick={() => navigate('/ai/models/new')}>
          <Plus className="h-3.5 w-3.5 mr-1" />新增模型
        </Button>
      </div>

      <ProTable<ModelRecord>
        rowKey="id"
        size="small"
        search={false}
        options={{ reload: reload, density: false, setting: false }}
        loading={loading}
        dataSource={filtered}
        columns={columns}
        pagination={{ pageSize: 10, showSizeChanger: false, size: 'small' }}
        onRow={r => ({ onDoubleClick: () => navigate(`/ai/models/${encodeURIComponent(r.id)}`) })}
        locale={{ emptyText: q || type !== 'all' ? '没有匹配的模型，试试调整筛选条件' : '暂无模型，点击「新增模型」接入第一个模型；聊天输入框选「自动」即可按权重/健康/类型智能路由' }}
      />

      <ConfirmDialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        onConfirm={handleDelete}
        title={`删除模型「${deleting?.name || ''}」`}
        description="删除后该模型将从模型列表移除，引用它的智能体将回退到 auto 自动路由。此操作不可撤销。"
        confirmLabel="删除"
        loading={submitting}
        requireText={deleting?.name}
        requireLabel={deleting?.name}
      />
    </div>
  )
}
