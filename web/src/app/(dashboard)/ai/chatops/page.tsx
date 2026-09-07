// ChatOps 渠道管理(P3-2a 前端): 在飞书/Slack/企微等 IM 内完成运维问答与审批闭环。
// 本页负责「渠道接入配置」与「会话绑定查看/解绑」; IM 双向问答与审批卡片交互由后端 internal/chatops 处理。
//
// 后端接口:
//   GET    /api/v1/chatops/channels          渠道列表(含 callback_url, secret 已脱敏)
//   POST   /api/v1/chatops/channels          新建渠道 {platform,name,settings}
//   PUT    /api/v1/chatops/channels/{id}      更新 {name?,enabled?,settings?}
//   DELETE /api/v1/chatops/channels/{id}      删除
//   POST   /api/v1/chatops/channels/{id}/test 连通性测试 {chat_id}
//   GET    /api/v1/chatops/bindings           (platform,chat_id)→AI 会话 绑定列表
//   DELETE /api/v1/chatops/bindings/{p}/{c}   解绑
//
// 权限: 上述管理 API 走 system:admin(网关 AutoPermissionMiddleware 强制), 前端不做鉴权判断。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ProTable, type ProColumns } from '@ant-design/pro-components'
import { Modal, Switch, Input, Select, Tag } from 'antd'
import { Button } from '@/components/ui/button'
import { ErrorState } from '@/components/ui/error-state'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { getMessage } from '@/lib/message'
import { apiFetch, cn } from '@/lib/utils'
import {
  MessageSquare, Plus, Pencil, Trash2, Loader2, Webhook, Copy, CheckCircle2,
  Send, AlertCircle, Bot, Radio,
} from 'lucide-react'

// —— 平台元信息(前端表单的字段来源, 与后端 platform.go 口径一致) ——
// 飞书为 MVP 完整实现; Slack/企微为 P3-2b(后端适配器待接入), 前端置灰提示。
interface FieldMeta { key: string; label: string; secret: boolean; required: boolean; placeholder: string; desc?: string }
interface PlatformMeta { key: string; label: string; enabled: boolean; desc: string; fields: FieldMeta[] }

const PLATFORM_META: Record<string, PlatformMeta> = {
  feishu: {
    key: 'feishu',
    label: '飞书',
    enabled: true,
    desc: '飞书企业自建应用：支持群聊/单聊双向问答、审批卡片按钮交互。',
    fields: [
      { key: 'app_id', label: 'App ID', secret: false, required: true, placeholder: 'cli_xxxxxxxxxxxx' },
      { key: 'app_secret', label: 'App Secret', secret: true, required: true, placeholder: '飞书应用凭证 App Secret', desc: '应用凭证 → App Secret' },
      { key: 'verification_token', label: 'Verification Token', secret: true, required: true, placeholder: '事件订阅页的 Verification Token', desc: '事件订阅 → Verification Token' },
      { key: 'encrypt_key', label: 'Encrypt Key', secret: true, required: true, placeholder: '事件订阅页的 Encrypt Key', desc: '事件订阅 → Encrypt Key' },
      { key: 'default_agent_id', label: '默认 Agent ID（可选）', secret: false, required: false, placeholder: '留空使用系统默认 Agent', desc: '该渠道收到消息时默认路由到的 Agent；留空走全局默认' },
    ],
  },
  slack: { key: 'slack', label: 'Slack', enabled: false, desc: 'P3-2b 接入中：Slack 应用 + Socket Mode / Events API。', fields: [] },
  wecom: { key: 'wecom', label: '企业微信', enabled: false, desc: 'P3-2b 接入中：企业微信自建应用 + 回调配置。', fields: [] },
}

const PLATFORM_ORDER = ['feishu', 'slack', 'wecom']

interface Channel {
  id: string
  tenant_id: string
  platform: string
  name: string
  enabled: boolean
  settings: Record<string, any>
  created_at: string
  updated_at: string
  callback_url: string
}

interface Binding {
  platform: string
  chat_id: string
  tenant_id: string
  agent_id: string
  conversation_id: string
  updated_at: string
}

function platformLabel(p: string): string {
  return PLATFORM_META[p]?.label || p
}

export default function ChatOpsPage() {
  const [activeTab, setActiveTab] = useState<'channels' | 'bindings'>('channels')

  // —— 渠道 ——
  const [channels, setChannels] = useState<Channel[]>([])
  const [chanLoading, setChanLoading] = useState(false)
  const [chanError, setChanError] = useState<string | null>(null)
  const [togglingId, setTogglingId] = useState<string | null>(null)

  // —— 会话绑定 ——
  const [bindings, setBindings] = useState<Binding[]>([])
  const [bindLoading, setBindLoading] = useState(false)
  const [bindError, setBindError] = useState<string | null>(null)

  // —— 新建/编辑弹窗 ——
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<Channel | null>(null) // null = 新建
  const [step, setStep] = useState<'form' | 'done'>('form')
  const [saving, setSaving] = useState(false)
  const [formPlatform, setFormPlatform] = useState('feishu')
  const [formName, setFormName] = useState('')
  const [cred, setCred] = useState<Record<string, string>>({})
  const [createdChannel, setCreatedChannel] = useState<Channel | null>(null)
  const [copied, setCopied] = useState(false)

  // —— 删除确认 ——
  const [delTarget, setDelTarget] = useState<Channel | null>(null)
  const [delLoading, setDelLoading] = useState(false)

  // —— 测试弹窗 ——
  const [testTarget, setTestTarget] = useState<Channel | null>(null)
  const [testChatId, setTestChatId] = useState('')
  const [testLoading, setTestLoading] = useState(false)

  // —— 解绑确认 ——
  const [unbindTarget, setUnbindTarget] = useState<Binding | null>(null)
  const [unbindLoading, setUnbindLoading] = useState(false)

  const loadChannels = useCallback(async () => {
    setChanLoading(true)
    setChanError(null)
    try {
      const list = await apiFetch<Channel[]>('/api/v1/chatops/channels')
      setChannels(Array.isArray(list) ? list : [])
    } catch (e: any) {
      setChanError(e?.message || '加载渠道失败')
      setChannels([])
    } finally {
      setChanLoading(false)
    }
  }, [])

  const loadBindings = useCallback(async () => {
    setBindLoading(true)
    setBindError(null)
    try {
      const list = await apiFetch<Binding[]>('/api/v1/chatops/bindings')
      setBindings(Array.isArray(list) ? list : [])
    } catch (e: any) {
      setBindError(e?.message || '加载绑定失败')
      setBindings([])
    } finally {
      setBindLoading(false)
    }
  }, [])

  useEffect(() => {
    if (activeTab === 'channels') loadChannels()
    else loadBindings()
  }, [activeTab, loadChannels, loadBindings])

  // —— 开关启停 ——
  const toggleEnabled = async (ch: Channel, next: boolean) => {
    setTogglingId(ch.id)
    try {
      await apiFetch(`/api/v1/chatops/channels/${ch.id}`, {
        method: 'PUT',
        body: JSON.stringify({ enabled: next }),
      })
      getMessage().success(next ? '渠道已启用' : '渠道已停用')
      loadChannels()
    } catch (e: any) {
      getMessage().error('操作失败: ' + (e?.message || ''))
    } finally {
      setTogglingId(null)
    }
  }

  // —— 打开新建/编辑 ——
  const openCreate = () => {
    setEditing(null)
    setStep('form')
    setFormPlatform('feishu')
    setFormName('')
    setCred({})
    setCreatedChannel(null)
    setCopied(false)
    setModalOpen(true)
  }
  const openEdit = (ch: Channel) => {
    setEditing(ch)
    setStep('form')
    setFormPlatform(ch.platform)
    setFormName(ch.name)
    // settings 已脱敏, 凭据不回填(避免把 ****** 写回); 编辑时留空=不修改
    setCred({})
    setCreatedChannel(null)
    setCopied(false)
    setModalOpen(true)
  }

  const validateForm = (): string | null => {
    if (!formName.trim()) return '请填写渠道名称'
    const meta = PLATFORM_META[formPlatform]
    if (!meta?.enabled) return `${meta?.label || formPlatform} 暂未接入`
    const required = meta.fields.filter(f => f.required)
    const filled = required.filter(f => (cred[f.key] || '').trim())
    if (editing) {
      // 编辑: 凭据全部留空=仅改名称/启停; 部分填写=要求补全
      if (filled.length > 0 && filled.length < required.length) {
        return '凭据需完整填写或保持留空（留空=不修改）'
      }
    } else {
      if (filled.length < required.length) return '请填写全部必填凭据'
    }
    return null
  }

  const submitForm = async () => {
    const err = validateForm()
    if (err) return getMessage().error(err)
    setSaving(true)
    try {
      const meta = PLATFORM_META[formPlatform]
      const settings: Record<string, string> = {}
      for (const f of meta.fields) {
        const v = (cred[f.key] || '').trim()
        if (v) settings[f.key] = v
      }
      if (editing) {
        const body: any = { name: formName.trim() }
        if (Object.keys(settings).length > 0) body.settings = settings
        await apiFetch(`/api/v1/chatops/channels/${editing.id}`, {
          method: 'PUT',
          body: JSON.stringify(body),
        })
        getMessage().success('渠道已更新')
        setModalOpen(false)
        loadChannels()
      } else {
        const ch = await apiFetch<Channel>('/api/v1/chatops/channels', {
          method: 'POST',
          body: JSON.stringify({ platform: formPlatform, name: formName.trim(), settings }),
        })
        setCreatedChannel(ch)
        setStep('done')
        loadChannels()
      }
    } catch (e: any) {
      getMessage().error('保存失败: ' + (e?.message || ''))
    } finally {
      setSaving(false)
    }
  }

  const copyCallback = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      getMessage().success('回调地址已复制')
      setTimeout(() => setCopied(false), 2000)
    } catch {
      getMessage().error('复制失败，请手动选择')
    }
  }

  // —— 删除 ——
  const handleDelete = async () => {
    if (!delTarget) return
    setDelLoading(true)
    try {
      await apiFetch(`/api/v1/chatops/channels/${delTarget.id}`, { method: 'DELETE' })
      getMessage().success(`渠道「${delTarget.name}」已删除`)
      setDelTarget(null)
      loadChannels()
    } catch (e: any) {
      getMessage().error('删除失败: ' + (e?.message || ''))
    } finally {
      setDelLoading(false)
    }
  }

  // —— 测试 ——
  const openTest = (ch: Channel) => { setTestTarget(ch); setTestChatId(''); setTestLoading(false); }
  const runTest = async () => {
    if (!testTarget) return
    if (!testChatId.trim()) return getMessage().error('请填写 chat_id（在 IM 群里 @机器人 发任意消息后，从「会话绑定」页获取）')
    setTestLoading(true)
    try {
      await apiFetch(`/api/v1/chatops/channels/${testTarget.id}/test`, {
        method: 'POST',
        body: JSON.stringify({ chat_id: testChatId.trim() }),
      })
      getMessage().success('测试消息已发送，请到对应 IM 会话查看')
      setTestTarget(null)
    } catch (e: any) {
      getMessage().error('测试失败: ' + (e?.message || ''))
    } finally {
      setTestLoading(false)
    }
  }

  // —— 解绑 ——
  const handleUnbind = async () => {
    if (!unbindTarget) return
    setUnbindLoading(true)
    try {
      await apiFetch(`/api/v1/chatops/bindings/${encodeURIComponent(unbindTarget.platform)}/${encodeURIComponent(unbindTarget.chat_id)}`, { method: 'DELETE' })
      getMessage().success('已解绑该会话')
      setUnbindTarget(null)
      loadBindings()
    } catch (e: any) {
      getMessage().error('解绑失败: ' + (e?.message || ''))
    } finally {
      setUnbindLoading(false)
    }
  }

  const channelColumns: ProColumns<Channel>[] = [
    {
      title: '渠道名称',
      dataIndex: 'name',
      render: (_, r) => (
        <div className="flex items-center gap-2 min-w-0">
          <MessageSquare className="h-4 w-4 text-blue-500 shrink-0" />
          <div className="min-w-0">
            <div className="font-medium text-sm truncate">{r.name}</div>
            <div className="text-[11px] text-muted-foreground font-mono truncate">{r.id}</div>
          </div>
        </div>
      ),
    },
    {
      title: '平台',
      dataIndex: 'platform',
      render: (_, r) => <Tag color="blue" className="text-[11px]">{platformLabel(r.platform)}</Tag>,
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      width: 90,
      render: (_, r) => (
        <Switch
          checked={r.enabled}
          loading={togglingId === r.id}
          disabled={togglingId === r.id}
          onChange={(v) => toggleEnabled(r, v)}
        />
      ),
    },
    {
      title: '回调地址',
      dataIndex: 'callback_url',
      render: (_, r) => (
        <div className="flex items-center gap-1.5 max-w-[320px]">
          <code className="text-[11px] font-mono text-muted-foreground truncate flex-1">{r.callback_url}</code>
          <Button variant="ghost" size="sm" className="h-6 px-1.5 shrink-0" onClick={() => copyCallback(r.callback_url)} title="复制回调地址">
            <Copy className="h-3 w-3" />
          </Button>
        </div>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      render: (_, r) => <span className="text-xs text-muted-foreground">{r.created_at ? new Date(r.created_at).toLocaleString('zh-CN') : '-'}</span>,
    },
    {
      title: '操作',
      key: 'op',
      width: 200,
      render: (_, r) => (
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => openTest(r)} disabled={!r.enabled}><Send className="h-3 w-3 mr-1" />测试</Button>
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => openEdit(r)}><Pencil className="h-3 w-3 mr-1" />编辑</Button>
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs text-red-500 hover:text-red-600" onClick={() => setDelTarget(r)}><Trash2 className="h-3 w-3 mr-1" />删除</Button>
        </div>
      ),
    },
  ]

  const bindingColumns: ProColumns<Binding>[] = [
    {
      title: '平台',
      dataIndex: 'platform',
      width: 100,
      render: (_, r) => <Tag color="blue" className="text-[11px]">{platformLabel(r.platform)}</Tag>,
    },
    {
      title: '会话 ID (chat_id)',
      dataIndex: 'chat_id',
      render: (_, r) => <code className="text-xs font-mono text-muted-foreground">{r.chat_id}</code>,
    },
    {
      title: 'Agent',
      dataIndex: 'agent_id',
      render: (_, r) => r.agent_id ? <code className="text-xs font-mono">{r.agent_id}</code> : <span className="text-xs text-muted-foreground">默认</span>,
    },
    {
      title: '会话 ID (conversation)',
      dataIndex: 'conversation_id',
      render: (_, r) => <code className="text-[11px] font-mono text-muted-foreground truncate block max-w-[200px]">{r.conversation_id}</code>,
    },
    {
      title: '最近活跃',
      dataIndex: 'updated_at',
      width: 160,
      render: (_, r) => <span className="text-xs text-muted-foreground">{r.updated_at ? new Date(r.updated_at).toLocaleString('zh-CN') : '-'}</span>,
    },
    {
      title: '操作',
      key: 'op',
      width: 100,
      render: (_, r) => (
        <Button variant="ghost" size="sm" className="h-7 px-2 text-xs text-red-500 hover:text-red-600" onClick={() => setUnbindTarget(r)}><Trash2 className="h-3 w-3 mr-1" />解绑</Button>
      ),
    },
  ]

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-5">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Webhook className="h-6 w-6 text-blue-500" />
            ChatOps 渠道管理
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            将飞书/Slack/企微接入 AI 运维助手：在 IM 内直接问答、处理审批，无需切换到控制台。
          </p>
        </div>
      </div>

      <div className="flex gap-1 border-b border-border mb-4">
        {([
          { k: 'channels', label: '渠道接入', icon: MessageSquare },
          { k: 'bindings', label: '会话绑定', icon: Radio },
        ] as const).map(t => {
          const Icon = t.icon
          const active = activeTab === t.k
          return (
            <button
              key={t.k}
              onClick={() => setActiveTab(t.k)}
              className={cn(
                'flex items-center gap-1.5 px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors',
                active
                  ? 'border-blue-500 text-blue-600 dark:text-blue-400'
                  : 'border-transparent text-muted-foreground hover:text-foreground',
              )}
            >
              <Icon className="h-4 w-4" />{t.label}
              {t.k === 'bindings' && bindings.length > 0 && <span className="text-[11px]">({bindings.length})</span>}
            </button>
          )
        })}
      </div>

      {activeTab === 'channels' ? (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">接入一个 IM 渠道后，把回调地址填到对应平台，即可在群里 @机器人 开始对话。</p>
            <Button variant="primary" size="sm" onClick={openCreate}>
              <Plus className="h-3.5 w-3.5 mr-1" />新建渠道
            </Button>
          </div>

          {chanError ? (
            <ErrorState message={chanError} onRetry={loadChannels} />
          ) : (
            <ProTable<Channel>
              rowKey="id"
              size="small"
              search={false}
              options={{ reload: loadChannels, density: false, setting: false }}
              loading={chanLoading}
              dataSource={channels}
              columns={channelColumns}
              pagination={{ pageSize: 10, showSizeChanger: false, size: 'small' }}
              locale={{ emptyText: '暂无渠道，点击「新建渠道」接入第一个 IM 平台' }}
            />
          )}
        </div>
      ) : (
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            用户在 IM 内首次 @机器人 会自动建立「会话 → AI 会话」绑定，续接上下文。解绑后该会话回到无状态问答。
          </p>
          {bindError ? (
            <ErrorState message={bindError} onRetry={loadBindings} />
          ) : (
            <ProTable<Binding>
              rowKey={(r) => `${r.platform}:${r.chat_id}`}
              size="small"
              search={false}
              options={{ reload: loadBindings, density: false, setting: false }}
              loading={bindLoading}
              dataSource={bindings}
              columns={bindingColumns}
              pagination={{ pageSize: 10, showSizeChanger: false, size: 'small' }}
              locale={{ emptyText: '暂无绑定会话。在 IM 群里 @机器人 发任意消息即可建立绑定。' }}
            />
          )}
        </div>
      )}

      {/* —— 新建/编辑渠道弹窗 —— */}
      <Modal
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        title={editing ? `编辑渠道「${editing.name}」` : '新建 ChatOps 渠道'}
        width={640}
        footer={null}
      >
        {step === 'done' && createdChannel ? (
          <div className="space-y-4">
            <div className="flex items-center gap-2 text-green-600 dark:text-green-400">
              <CheckCircle2 className="h-5 w-5" />
              <span className="font-medium">渠道已创建，最后一步：配置平台回调</span>
            </div>
            <div className="rounded-lg border border-blue-200 dark:border-blue-500/25 bg-blue-50/50 dark:bg-blue-500/5 p-3 space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium text-muted-foreground">回调地址（填到平台事件订阅/回调配置）</span>
                <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => copyCallback(createdChannel.callback_url)}>
                  {copied ? <CheckCircle2 className="h-3 w-3 mr-1" /> : <Copy className="h-3 w-3 mr-1" />}{copied ? '已复制' : '复制'}
                </Button>
              </div>
              <code className="block text-[11px] font-mono break-all bg-white dark:bg-[rgba(255,255,255,0.04)] rounded p-2 border border-border">{createdChannel.callback_url}</code>
            </div>
            <ol className="text-xs text-muted-foreground space-y-1.5 list-decimal pl-5">
              <li>前往飞书开放平台，创建<strong className="text-foreground">企业自建应用</strong>。</li>
              <li>「事件订阅」页：将上方<strong className="text-foreground">回调地址</strong>填入请求地址；Verification Token / Encrypt Key 即你刚填的凭据。</li>
              <li>开通权限：<code className="font-mono">im:message</code>、<code className="font-mono">im:message:send_as_bot</code>、<code className="font-mono">im:resource</code>。</li>
              <li>把应用添加到目标群，在群里 <strong className="text-foreground">@机器人</strong> 发送任意消息即完成绑定并开始问答。</li>
            </ol>
            <div className="flex justify-end">
              <Button variant="primary" size="sm" onClick={() => setModalOpen(false)}>完成</Button>
            </div>
          </div>
        ) : (
          <div className="space-y-3">
            <div className="flex items-start gap-2 text-xs p-2.5 rounded-lg bg-blue-50 dark:bg-blue-500/5 border border-blue-200 dark:border-blue-500/10">
              <AlertCircle className="h-4 w-4 text-blue-500 shrink-0 mt-px" />
              <span className="text-muted-foreground">{PLATFORM_META[formPlatform]?.desc}</span>
            </div>

            <div className="grid gap-3 md:grid-cols-2">
              <div className="space-y-1">
                <label className="text-xs font-medium">平台 <span className="text-red-400">*</span></label>
                <Select
                  value={formPlatform}
                  disabled={!!editing}
                  onChange={(v) => { setFormPlatform(v); setCred({}) }}
                  className="w-full"
                  options={PLATFORM_ORDER.map(k => ({
                    value: k,
                    label: PLATFORM_META[k].label,
                    disabled: !PLATFORM_META[k].enabled,
                  }))}
                />
                {!PLATFORM_META[formPlatform]?.enabled && (
                  <p className="text-[11px] text-amber-600 dark:text-amber-400">该平台将于 P3-2b 接入，敬请期待。</p>
                )}
              </div>
              <div className="space-y-1">
                <label className="text-xs font-medium">渠道名称 <span className="text-red-400">*</span></label>
                <Input placeholder="如: 运维飞书群机器人" value={formName} onChange={e => setFormName(e.target.value)} />
              </div>
            </div>

            {PLATFORM_META[formPlatform]?.enabled && (
              <div className="space-y-2.5 rounded-lg border border-border p-3">
                <div className="flex items-center gap-1.5 text-xs font-medium">
                  <Bot className="h-3.5 w-3.5 text-blue-500" />平台凭据
                  {editing && <span className="text-[11px] font-normal text-muted-foreground">（留空 = 不修改；要改请完整重填）</span>}
                </div>
                {PLATFORM_META[formPlatform].fields.map(f => (
                  <div key={f.key} className="space-y-1">
                    <label className="text-xs text-muted-foreground">
                      {f.label}{f.required && <span className="text-red-400"> *</span>}
                    </label>
                    <Input
                      type={f.secret ? 'password' : 'text'}
                      placeholder={f.placeholder}
                      value={cred[f.key] || ''}
                      onChange={e => setCred(prev => ({ ...prev, [f.key]: e.target.value }))}
                    />
                    {f.desc && <p className="text-[10px] text-muted-foreground">{f.desc}</p>}
                  </div>
                ))}
              </div>
            )}

            <div className="flex items-center justify-end gap-2 pt-1">
              <Button variant="outline" size="sm" onClick={() => setModalOpen(false)} disabled={saving}>取消</Button>
              <Button variant="primary" size="sm" onClick={submitForm} disabled={saving}>
                {saving ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <CheckCircle2 className="h-3.5 w-3.5 mr-1" />}
                {editing ? '保存' : '创建并生成回调地址'}
              </Button>
            </div>
          </div>
        )}
      </Modal>

      {/* —— 测试弹窗 —— */}
      <Modal
        open={!!testTarget}
        onCancel={() => setTestTarget(null)}
        title="连通性测试"
        width={480}
        footer={null}
      >
        {testTarget && (
          <div className="space-y-3">
            <p className="text-xs text-muted-foreground">
              向指定会话发送一条测试消息。需先在 IM 内 @机器人 建立绑定以获取 chat_id（见「会话绑定」页）。
            </p>
            <div className="space-y-1">
              <label className="text-xs font-medium">chat_id</label>
              <Input
                placeholder="从「会话绑定」页复制 chat_id"
                value={testChatId}
                onChange={e => setTestChatId(e.target.value)}
                className="font-mono text-xs"
              />
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setTestTarget(null)} disabled={testLoading}>取消</Button>
              <Button variant="primary" size="sm" onClick={runTest} disabled={testLoading}>
                {testLoading ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Send className="h-3.5 w-3.5 mr-1" />}发送测试消息
              </Button>
            </div>
          </div>
        )}
      </Modal>

      <ConfirmDialog
        open={!!delTarget}
        onClose={() => setDelTarget(null)}
        onConfirm={handleDelete}
        title={`删除渠道「${delTarget?.name || ''}」`}
        description="删除后该 IM 渠道立即停用，已建立的会话绑定也随之失效。此操作不可撤销。"
        confirmLabel="删除"
        loading={delLoading}
        requireText={delTarget?.name}
        requireLabel={delTarget?.name}
      />

      <ConfirmDialog
        open={!!unbindTarget}
        onClose={() => setUnbindTarget(null)}
        onConfirm={handleUnbind}
        title="解绑会话"
        description={`解除 ${unbindTarget ? platformLabel(unbindTarget.platform) : ''} 会话「${unbindTarget?.chat_id || ''}」与 AI 的绑定？解绑后该会话回到无状态问答（历史上下文不删除）。`}
        confirmLabel="解绑"
        loading={unbindLoading}
      />
    </div>
  )
}
