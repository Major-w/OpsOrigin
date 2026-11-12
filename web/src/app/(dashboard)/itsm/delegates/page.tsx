// 审批委托（代理人）管理页（对标 ServiceNow Delegate）。
// 追加式语义：委托人生效期内，其代理人会与本人一同进入流程人工节点的表决名册，
// 原审批人仍可投票。后端 internal/itsm/process/delegation.go。
import { useCallback, useEffect, useState } from 'react'
import { Plus, RefreshCw, Trash2 } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { Button, DatePicker, Input, Modal, Select, Switch, Table, Tag } from 'antd'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'

interface ApprovalDelegate {
  id: string
  principal: string
  delegate: string
  start_at?: string
  end_at?: string
  enabled: boolean
  note?: string
  created_at?: string
}

interface UserOption {
  value: string
  label: string
}

const fmtTime = (s?: string) => {
  if (!s) return '长期'
  const d = new Date(s)
  return isNaN(d.getTime()) ? '长期' : d.toLocaleString('zh-CN', { hour12: false })
}

export default function DelegatesPage() {
  const [items, setItems] = useState<ApprovalDelegate[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [users, setUsers] = useState<UserOption[]>([])

  const [modalOpen, setModalOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [principal, setPrincipal] = useState('')
  const [delegate, setDelegate] = useState('')
  const [range, setRange] = useState<{ start?: string; end?: string }>({})
  const [enabled, setEnabled] = useState(true)
  const [note, setNote] = useState('')
  const [confirmDelete, setConfirmDelete] = useState<ApprovalDelegate | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const list = await apiList<ApprovalDelegate>('/itsm/approval-delegates')
      setItems(list || [])
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
    apiFetch<UserOption[]>('/itsm/form-options?source=iam:user')
      .then(opts => setUsers(opts || []))
      .catch(() => setUsers([]))
  }, [load])

  const openCreate = () => {
    setPrincipal(''); setDelegate(''); setRange({}); setEnabled(true); setNote('')
    setModalOpen(true)
  }

  const submit = async () => {
    if (!principal || !delegate) { getMessage().warning('请选择委托人与代理人'); return }
    if (principal === delegate) { getMessage().warning('不能把审批权委托给自己'); return }
    setSubmitting(true)
    try {
      const res = await apiFetch<ApprovalDelegate>('/itsm/approval-delegates', {
        method: 'POST',
        body: JSON.stringify({
          principal, delegate, enabled,
          note: note || undefined,
          start_at: range.start || new Date().toISOString(),
          end_at: range.end || undefined,
        }),
      })
      if (res?.id) {
        getMessage().success('委托已创建')
        setModalOpen(false)
        load()
      }
    } catch (e: unknown) {
      getMessage().error(e instanceof Error ? e.message : '创建失败')
    } finally {
      setSubmitting(false)
    }
  }

  const doDelete = async () => {
    if (!confirmDelete) return
    try {
      await apiFetch(`/itsm/approval-delegates/${confirmDelete.id}`, { method: 'DELETE' })
      getMessage().success('委托已删除')
      setConfirmDelete(null)
      load()
    } catch (e: unknown) {
      getMessage().error(e instanceof Error ? e.message : '删除失败')
    }
  }

  const columns = [
    { title: '委托人', dataIndex: 'principal', key: 'principal' },
    { title: '代理人', dataIndex: 'delegate', key: 'delegate' },
    {
      title: '生效期', key: 'period',
      render: (_: unknown, r: ApprovalDelegate) => `${fmtTime(r.start_at)} ~ ${r.end_at ? fmtTime(r.end_at) : '长期'}`,
    },
    {
      title: '状态', dataIndex: 'enabled', key: 'enabled', width: 90,
      render: (v: boolean) => (v ? <Tag color="green">启用</Tag> : <Tag>停用</Tag>),
    },
    { title: '备注', dataIndex: 'note', key: 'note', ellipsis: true },
    {
      title: '', key: 'op', width: 60,
      render: (_: unknown, r: ApprovalDelegate) => (
        <button type="button" className="text-muted-foreground hover:text-destructive" title="删除" onClick={() => setConfirmDelete(r)}>
          <Trash2 className="h-4 w-4" />
        </button>
      ),
    },
  ]

  if (error) {
    return <ErrorState message={`委托列表加载失败：${error}`} onRetry={load} />
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">审批委托</h1>
          <p className="text-xs text-muted-foreground mt-0.5">
            委托人生效期内，代理人将与其一同进入审批名册（原审批人仍可投票，代理人通过同样计入表决）。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button icon={<RefreshCw className="h-3.5 w-3.5" />} onClick={load} loading={loading}>刷新</Button>
          <Button type="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={openCreate}>新建委托</Button>
        </div>
      </div>

      <div className="rounded-lg border bg-card">
        <Table
          rowKey="id"
          size="small"
          columns={columns as never}
          dataSource={items}
          loading={loading}
          pagination={items.length > 10 ? { pageSize: 10 } : false}
          locale={{ emptyText: '暂无委托关系。外出/休假前可把审批权委托给同事。' }}
        />
      </div>

      <Modal
        title="新建审批委托"
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={submit}
        okText="创建"
        confirmLoading={submitting}
        destroyOnHidden
      >
        <div className="space-y-3 pt-2">
          <div>
            <label className="text-xs font-medium">委托人（原审批人）</label>
            <Select className="w-full mt-1" showSearch optionFilterProp="label" placeholder="选择用户"
              value={principal || undefined} options={users}
              onChange={v => setPrincipal(String(v))} />
          </div>
          <div>
            <label className="text-xs font-medium">代理人</label>
            <Select className="w-full mt-1" showSearch optionFilterProp="label" placeholder="选择用户"
              value={delegate || undefined} options={users}
              onChange={v => setDelegate(String(v))} />
          </div>
          <div>
            <label className="text-xs font-medium">生效区间（不选结束时间=长期有效）</label>
            <DatePicker.RangePicker
              className="w-full mt-1" showTime
              value={(range.start || range.end) ? [range.start, range.end] as never : null}
              onChange={(_, strs) => setRange({ start: strs?.[0] || undefined, end: strs?.[1] || undefined })}
            />
          </div>
          <div className="flex items-center gap-2">
            <Switch checked={enabled} onChange={setEnabled} />
            <span className="text-xs">{enabled ? '启用中' : '已停用'}</span>
          </div>
          <div>
            <label className="text-xs font-medium">备注（可选）</label>
            <Input className="mt-1" value={note} onChange={e => setNote(e.target.value)} placeholder="如：休假期间由张三代审" />
          </div>
        </div>
      </Modal>

      <ConfirmDialog
        open={!!confirmDelete}
        title="删除委托"
        description={confirmDelete ? `确定删除「${confirmDelete.principal} → ${confirmDelete.delegate}」的委托关系？删除后代理人立即退出表决名册。` : ''}
        onClose={() => setConfirmDelete(null)}
        onConfirm={doDelete}
      />
    </div>
  )
}
