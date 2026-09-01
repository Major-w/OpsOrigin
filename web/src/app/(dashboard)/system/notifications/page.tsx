
import { useEffect, useState, useMemo } from 'react'
import {
  useNotificationStore,
  EVENT_LABELS,
  CHANNEL_TYPE_LABELS,
  CHANNEL_TYPE_COLORS,
  type Channel,
  type Policy,
  type ChannelType,
  type EventType,
  type FieldSpec,
} from '@/stores/notification'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Modal, Input, Select, Button, Tag, Space, Tabs, Popconfirm, message } from 'antd'
import { UniversalForm } from '@/components/universal-form'
import { UniversalTable } from '@/components/universal-table'
import { PlusOutlined, SendOutlined, CheckCircleOutlined, CloseCircleOutlined, EyeOutlined } from '@ant-design/icons'
import {
  Bell, Mail, MessageSquare, Webhook, Send,
  Clock, Edit, Trash2,
} from 'lucide-react'
import { formatTime } from '@/lib/utils'
import { getMessage } from '@/lib/message'

export default function SystemNotificationsPage() {
  const store = useNotificationStore()
  const [tab, setTab] = useState('channels')

  useEffect(() => {
    store.fetchChannels()
    store.fetchPolicies()
    store.fetchChannelTypes()
    store.fetchEvents()
    store.fetchHistory()
  }, [])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">通知管理</h1>
          <p className="text-sm text-muted-foreground">管理通知渠道、订阅策略与发送历史</p>
        </div>
      </div>

      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          { key: 'channels', label: `通知渠道 (${store.channels.length})`, children: <ChannelsTab /> },
          { key: 'policies', label: `订阅策略 (${store.policies.length})`, children: <PoliciesTab /> },
          { key: 'history', label: `发送历史 (${store.notifications.length})`, children: <HistoryTab /> },
        ]}
      />
    </div>
  )
}

// =================== 渠道管理 ===================

function ChannelsTab() {
  const { channels, channelTypes, fetchChannels, deleteChannel } = useNotificationStore()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [pendingChannel, setPendingChannel] = useState<Channel | null>(null)
  const [form, setForm] = useState<{ name: string; type: ChannelType; config: Record<string, string>; enabled: boolean }>({
    name: '', type: 'email', config: {}, enabled: true,
  })

  const fieldSpecs: FieldSpec[] = useMemo(() => {
    const meta = channelTypes.find(t => t.type === form.type)
    return meta?.required_fields || []
  }, [channelTypes, form.type])

  const changeType = (t: ChannelType) => {
    setForm(f => ({ ...f, type: t, config: {} }))
  }

  const openCreate = () => {
    setEditId(null)
    setForm({ name: '', type: 'email', config: {}, enabled: true })
    setDialogOpen(true)
  }

  const openEdit = (ch: Channel) => {
    setEditId(ch.id)
    setForm({ name: ch.name, type: ch.type, config: { ...ch.config }, enabled: ch.enabled })
    setDialogOpen(true)
  }

  const handleSave = async () => {
    const { createChannel, updateChannel } = useNotificationStore.getState()
    if (editId) {
      await updateChannel(editId, {
        name: form.name,
        config: form.config,
        enabled: form.enabled,
      })
    } else {
      await createChannel({
        name: form.name,
        type: form.type,
        config: form.config,
        enabled: form.enabled,
      })
    }
    setDialogOpen(false)
  }

  const handleDelete = async () => {
    if (!pendingChannel) return
    await deleteChannel(pendingChannel.id)
    setPendingChannel(null)
  }

  const handleTest = async (ch: Channel) => {
    if (!ch.enabled) {
      getMessage().warning('该渠道未启用,无法测试发送。请先编辑启用')
      return
    }
    await useNotificationStore.getState().testChannel(ch.id)
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="primary" size="middle" onClick={openCreate}><PlusOutlined className="mr-1" />添加渠道</Button>
      </div>

      {channels.length === 0 ? (
        <Card><CardContent className="py-12 text-center text-muted-foreground">
          <Bell className="h-10 w-10 mx-auto mb-3 opacity-30" />
          <p>暂无通知渠道</p>
          <p className="text-xs mt-1">支持邮件、钉钉、企业微信、飞书、通用 Webhook 五种类型</p>
        </CardContent></Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {channels.map(ch => (
            <ChannelCard
              key={ch.id}
              channel={ch}
              onEdit={() => openEdit(ch)}
              onDelete={() => setPendingChannel(ch)}
              onTest={() => handleTest(ch)}
            />
          ))}
        </div>
      )}

      {/* 创建/编辑渠道 */}
      <Modal
        open={dialogOpen}
        onCancel={() => setDialogOpen(false)}
        title={editId ? '编辑渠道' : '添加渠道'}
        footer={[
          <Button key="cancel" onClick={() => setDialogOpen(false)}>取消</Button>,
          <Button key="save" type="primary" onClick={handleSave} disabled={!form.name}>{editId ? '保存' : '创建'}</Button>,
        ]}
      >
        <div className="space-y-4 max-h-[70vh] overflow-y-auto">
          <div>
            <label className="text-sm font-medium">渠道名称 *</label>
            <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="如 运维告警邮箱" />
          </div>

          <div>
            <label className="text-sm font-medium">渠道类型</label>
            <Select
              value={form.type}
              onChange={(value) => changeType(value as ChannelType)}
              options={Object.entries(CHANNEL_TYPE_LABELS).map(([value, label]) => ({ value, label }))}
              disabled={!!editId}
              style={{ width: '100%' }}
            />
            {editId && <p className="text-[10px] text-muted-foreground mt-0.5">类型创建后不可修改</p>}
          </div>

          <div>
            <label className="text-sm font-medium">配置参数</label>
            <div className="mt-2 space-y-3 border rounded-md p-3">
              {fieldSpecs.length === 0 ? (
                <p className="text-xs text-muted-foreground text-center py-2">无可用字段规格</p>
              ) : fieldSpecs.map(field => (
                <div key={field.name}>
                  <label className="text-xs font-medium flex items-center gap-1">
                    {field.label}
                    {field.required && <span className="text-destructive">*</span>}
                    {field.secret && <Tag>敏感</Tag>}
                  </label>
                  <Input
                    className="h-8 text-xs mt-1"
                    type={field.secret ? 'password' : 'text'}
                    placeholder={field.placeholder}
                    value={form.config[field.name] || ''}
                    onChange={e => setForm(f => ({
                      ...f,
                      config: { ...f.config, [field.name]: e.target.value },
                    }))}
                  />
                  {editId && field.secret && (form.config[field.name] || '').includes('****') && (
                    <p className="text-[10px] text-amber-500 mt-0.5">已掩码,留空表示不修改原值</p>
                  )}
                </div>
              ))}
            </div>
          </div>

          <div>
            <label className="text-sm font-medium">状态</label>
            <Select
              value={form.enabled ? 'enabled' : 'disabled'}
              onChange={(value) => setForm(f => ({ ...f, enabled: value === 'enabled' }))}
              options={[
                { value: 'enabled', label: '启用' },
                { value: 'disabled', label: '禁用' },
              ]}
              style={{ width: '100%' }}
            />
          </div>
        </div>
      </Modal>

      <Popconfirm
        open={!!pendingChannel}
        title="删除渠道"
        description={`确定删除渠道「${pendingChannel?.name}」?`}
        onConfirm={handleDelete}
        onCancel={() => setPendingChannel(null)}
      >
        <span />
      </Popconfirm>
    </div>
  )
}

function ChannelCard({ channel, onEdit, onDelete, onTest }: {
  channel: Channel
  onEdit: () => void
  onDelete: () => void
  onTest: () => void
}) {
  const Icon = channel.type === 'email' ? Mail
    : channel.type === 'webhook' ? Webhook
    : MessageSquare
  const color = CHANNEL_TYPE_COLORS[channel.type]

  const configPreview = (() => {
    const c = channel.config || {}
    return c.webhook_url || c.url || c.smtp_host || JSON.stringify(c).slice(0, 50)
  })()

  return (
    <Card className="hover:border-blue-500/30 transition-colors">
      <CardHeader className="py-3 pb-2">
        <div className="flex items-start justify-between">
          <div className="flex items-center gap-2 min-w-0">
            <Icon className={`h-4 w-4 shrink-0 ${color}`} />
            <div className="min-w-0">
              <p className="text-sm font-medium truncate">{channel.name}</p>
              <p className="text-[10px] text-muted-foreground">{CHANNEL_TYPE_LABELS[channel.type]}</p>
            </div>
          </div>
          <Tag color={channel.enabled ? 'success' : 'default'}>
            {channel.enabled ? '启用' : '禁用'}
          </Tag>
        </div>
      </CardHeader>
      <CardContent className="space-y-2">
        <p className="text-xs text-muted-foreground truncate font-mono" title={configPreview}>
          {configPreview}
        </p>
        <div className="flex items-center gap-1 pt-1 border-t">
          <Button type="text" size="middle" onClick={onTest} title="测试发送">
            <Send className="h-3 w-3 mr-1" />测试
          </Button>
          <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={onEdit} title="编辑">
            <Edit className="h-3 w-3" />
          </Button>
          <Button type="text" size="middle" danger className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={onDelete} title="删除">
            <Trash2 className="h-3 w-3" />
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

// =================== 策略管理 ===================

function PoliciesTab() {
  const { policies, channels, events } = useNotificationStore()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [sendDialogOpen, setSendDialogOpen] = useState(false)
  const [pendingPolicy, setPendingPolicy] = useState<Policy | null>(null)
  const [sendForm, setSendForm] = useState<{ event: EventType; title: string; content: string }>({
    event: 'alert.triggered', title: '', content: '',
  })

  const [form, setForm] = useState<{
    name: string
    event: EventType
    channel_ids: string[]
    template: string
    quiet_hours: string
    enabled: boolean
  }>({
    name: '', event: 'alert.triggered', channel_ids: [], template: '', quiet_hours: '', enabled: true,
  })

  const channelMap = useMemo(() => new Map(channels.map(c => [c.id, c])), [channels])

  const openCreate = () => {
    setEditId(null)
    setForm({ name: '', event: 'alert.triggered', channel_ids: [], template: '', quiet_hours: '', enabled: true })
    setDialogOpen(true)
  }

  const openEdit = (p: Policy) => {
    setEditId(p.id)
    setForm({
      name: p.name,
      event: p.event,
      channel_ids: [...(p.channel_ids || [])],
      template: p.template || '',
      quiet_hours: p.quiet_hours || '',
      enabled: p.enabled,
    })
    setDialogOpen(true)
  }

  const handleSave = async () => {
    const { createPolicy, updatePolicy } = useNotificationStore.getState()
    if (editId) {
      await updatePolicy(editId, form)
    } else {
      await createPolicy(form)
    }
    setDialogOpen(false)
  }

  const handleDelete = async () => {
    if (!pendingPolicy) return
    await useNotificationStore.getState().deletePolicy(pendingPolicy.id)
    setPendingPolicy(null)
  }

  const toggleChannel = (id: string) => {
    setForm(f => ({
      ...f,
      channel_ids: f.channel_ids.includes(id)
        ? f.channel_ids.filter(c => c !== id)
        : [...f.channel_ids, id],
    }))
  }

  const handleSend = async () => {
    await useNotificationStore.getState().sendEvent(sendForm.event, sendForm.title, sendForm.content)
    setSendDialogOpen(false)
    setSendForm({ event: 'alert.triggered', title: '', content: '' })
  }

  return (
    <div className="space-y-4">
      <div className="flex justify-end gap-2">
        <Button size="middle" onClick={() => setSendDialogOpen(true)}>
          <SendOutlined className="mr-1" />触发事件
        </Button>
        <Button type="primary" size="middle" onClick={openCreate}><PlusOutlined className="mr-1" />添加策略</Button>
      </div>

      {policies.length === 0 ? (
        <Card><CardContent className="py-12 text-center text-muted-foreground">
          <Bell className="h-10 w-10 mx-auto mb-3 opacity-30" />
          <p>暂无订阅策略</p>
          <p className="text-xs mt-1">策略决定事件触发时,向哪些渠道分发通知</p>
        </CardContent></Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {policies.map(p => (
            <PolicyCard
              key={p.id}
              policy={p}
              channelMap={channelMap}
              onEdit={() => openEdit(p)}
              onDelete={() => setPendingPolicy(p)}
            />
          ))}
        </div>
      )}

      {/* 创建/编辑策略 */}
      <Modal
        open={dialogOpen}
        onCancel={() => setDialogOpen(false)}
        title={editId ? '编辑策略' : '添加策略'}
        footer={[
          <Button key="cancel" onClick={() => setDialogOpen(false)}>取消</Button>,
          <Button key="save" type="primary" onClick={handleSave} disabled={!form.name}>{editId ? '保存' : '创建'}</Button>,
        ]}
      >
        <div className="space-y-4 max-h-[70vh] overflow-y-auto">
          <div>
            <label className="text-sm font-medium">策略名称 *</label>
            <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="如 告警通知运维组" />
          </div>

          <div>
            <label className="text-sm font-medium">订阅事件 *</label>
            <Select
              value={form.event}
              onChange={(value) => setForm(f => ({ ...f, event: value as EventType }))}
              options={events.length > 0
                ? events.map(ev => ({ value: ev, label: `${EVENT_LABELS[ev]} (${ev})` }))
                : Object.entries(EVENT_LABELS).map(([value, label]) => ({ value, label: `${label} (${value})` }))
              }
              style={{ width: '100%' }}
            />
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <label className="text-sm font-medium">通知渠道</label>
              <span className="text-xs text-muted-foreground">已选 {form.channel_ids.length}/{channels.length}</span>
            </div>
            <div className="space-y-1.5 border rounded-md p-3 max-h-48 overflow-y-auto">
              {channels.length === 0 ? (
                <p className="text-xs text-muted-foreground text-center py-2">
                  暂无渠道,请先到「通知渠道」Tab 添加
                </p>
              ) : channels.map(c => (
                <label key={c.id} className="flex items-center gap-2 p-1.5 rounded hover:bg-muted/50 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={form.channel_ids.includes(c.id)}
                    onChange={() => toggleChannel(c.id)}
                    className="rounded"
                  />
                  <span className="text-sm flex-1">{c.name}</span>
                  <Tag>{CHANNEL_TYPE_LABELS[c.type]}</Tag>
                  {!c.enabled && <Tag>禁用</Tag>}
                </label>
              ))}
            </div>
          </div>

          <div>
            <label className="text-sm font-medium">消息模板 (可选)</label>
            <Input
              className="font-mono text-xs"
              value={form.template}
              onChange={e => setForm(f => ({ ...f, template: e.target.value }))}
              placeholder="如 [告警] ${title} - ${content}"
            />
            <p className="text-[10px] text-muted-foreground mt-1">支持变量:{'${title}'} {'${content}'}。留空则使用默认内容</p>
          </div>

          <div>
            <label className="text-sm font-medium">免打扰时段 (可选)</label>
            <Input
              className="font-mono text-xs"
              value={form.quiet_hours}
              onChange={e => setForm(f => ({ ...f, quiet_hours: e.target.value }))}
              placeholder="如 22:00-08:00 (支持跨天)"
            />
            <p className="text-[10px] text-muted-foreground mt-1">该时段内不发送通知,留空则全天可发送</p>
          </div>

          <div>
            <label className="text-sm font-medium">状态</label>
            <Select
              value={form.enabled ? 'enabled' : 'disabled'}
              onChange={(value) => setForm(f => ({ ...f, enabled: value === 'enabled' }))}
              options={[
                { value: 'enabled', label: '启用' },
                { value: 'disabled', label: '禁用' },
              ]}
              style={{ width: '100%' }}
            />
          </div>
        </div>
      </Modal>

      {/* 触发事件(整链路测试) */}
      <UniversalForm
        visible={sendDialogOpen}
        title="触发事件通知"
        form={sendForm}
        submitLabel="触发"
        onCancel={() => setSendDialogOpen(false)}
        onSubmit={handleSend}
        fields={[
          {
            name: 'event',
            label: '事件类型 *',
            type: 'select',
            options: Object.entries(EVENT_LABELS).map(([value, label]) => ({ value, label: `${label} (${value})` })),
            rules: [{ required: true }],
          },
          { name: 'title', label: '标题', type: 'text', placeholder: '如 [告警] CPU 使用率超过 90%', rules: [{ required: true }] },
          { name: 'content', label: '内容', type: 'textarea', placeholder: '事件详情...' },
        ]}
        description="该操作会查找匹配该事件的所有启用策略,向其绑定的渠道分发通知,并写入发送历史。"
      />

      <Popconfirm
        open={!!pendingPolicy}
        title="删除策略"
        description={`确定删除策略「${pendingPolicy?.name}」?`}
        onConfirm={handleDelete}
        onCancel={() => setPendingPolicy(null)}
      >
        <span />
      </Popconfirm>
    </div>
  )
}

function PolicyCard({ policy, channelMap, onEdit, onDelete }: {
  policy: Policy
  channelMap: Map<string, Channel>
  onEdit: () => void
  onDelete: () => void
}) {
  const channels = (policy.channel_ids || [])
    .map(id => channelMap.get(id))
    .filter(Boolean) as Channel[]

  return (
    <Card className={`hover:border-blue-500/30 transition-colors ${!policy.enabled ? 'opacity-60' : ''}`}>
      <CardHeader className="py-3 pb-2">
        <div className="flex items-start justify-between">
          <div className="flex items-center gap-2 min-w-0">
            <Bell className="h-4 w-4 shrink-0 text-blue-500" />
            <div className="min-w-0">
              <p className="text-sm font-medium truncate">{policy.name}</p>
              <Tag color="blue" className="text-[10px] mt-0.5">
                {EVENT_LABELS[policy.event] || policy.event}
              </Tag>
            </div>
          </div>
          <div className="flex gap-0.5 shrink-0">
            <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={onEdit} title="编辑">
              <Edit className="h-3 w-3" />
            </Button>
            <Button type="text" size="middle" danger className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={onDelete} title="删除">
              <Trash2 className="h-3 w-3" />
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-2">
        <div className="flex items-center justify-between text-xs">
          <span className="text-muted-foreground">渠道数:</span>
          <span className="font-medium">{channels.length}</span>
        </div>
        <div className="flex flex-wrap gap-1">
          {channels.length === 0 ? (
            <span className="text-xs text-muted-foreground/50">未绑定渠道</span>
          ) : channels.slice(0, 4).map(c => (
            <Tag key={c.id}>{c.name}</Tag>
          ))}
          {channels.length > 4 && (
            <Tag>+{channels.length - 4}</Tag>
          )}
        </div>
        {policy.template && (
          <p className="text-[10px] font-mono text-muted-foreground truncate border-t pt-1.5" title={policy.template}>
            模板: {policy.template}
          </p>
        )}
        {policy.quiet_hours && (
          <div className="flex items-center gap-1 text-[10px] text-muted-foreground">
            <Clock className="h-2.5 w-2.5" />免打扰: {policy.quiet_hours}
          </div>
        )}
        <p className="text-[10px] text-muted-foreground/60 pt-1 border-t">
          更新于 {formatTime(policy.updated_at)}
        </p>
      </CardContent>
    </Card>
  )
}

// =================== 发送历史 ===================

function HistoryTab() {
  const { notifications, channels, fetchHistory } = useNotificationStore()
  const channelMap = useMemo(() => new Map(channels.map(c => [c.id, c])), [channels])

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button size="middle" onClick={() => fetchHistory()}>
          <EyeOutlined className="mr-1" />刷新
        </Button>
      </div>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">最近发送记录 ({notifications.length})</CardTitle>
        </CardHeader>
        <CardContent>
          <UniversalTable
            data={notifications}
            pageSize={20}
            scroll={{ x: 800 }}
            emptyText="暂无发送记录"
            fields={[
              { key: 'status', title: '状态', width: 100, render: (status: string) => status === 'sent'
                ? <Tag color="success"><CheckCircleOutlined /> 成功</Tag>
                : <Tag color="error"><CloseCircleOutlined /> 失败</Tag> },
              { key: 'event', title: '事件', width: 150, render: (event: string) => event
                ? <Tag>{EVENT_LABELS[event as EventType] || event}</Tag>
                : <span className="text-xs text-muted-foreground">测试</span> },
              { key: 'channel_id', title: '渠道', width: 150, render: (channelId: string) => channelMap.get(channelId)?.name || (
                <span className="text-muted-foreground font-mono text-xs">{channelId.slice(0, 8)}</span>
              ) },
              { key: 'title', title: '标题', dataIndex: 'title', },
              { key: 'error', title: '错误', width: 200, render: (error: string) => <span className="text-destructive">{error || '-'}</span> },
              { key: 'created_at', title: '时间', width: 160, render: (t: string) => formatTime(t) },
            ]}
          />
        </CardContent>
      </Card>
    </div>
  )
}
