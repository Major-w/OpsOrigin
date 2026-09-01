
import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Mail, MessageSquare, Webhook, Save, CheckCircle2, Loader2 } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { Button, Input, Switch, Tag } from 'antd'
interface Channel {
  id: string
  type: string
  name: string
  enabled: boolean
  config: string
}

type FieldType = 'text' | 'password' | 'number'
interface FieldDef {
  key: string
  label: string
  placeholder?: string
  type?: FieldType
}

interface ChannelMeta {
  icon: LucideIcon
  label: string
  desc: string
  webhook?: string
  fields: FieldDef[]
}

const channelMeta: Record<string, ChannelMeta> = {
  email: {
    icon: Mail,
    label: '邮件入站 (IMAP)',
    desc: '通过 IMAP 轮询收件箱，自动建单或将回复追加到工单评论。',
    fields: [
      { key: 'host', label: 'IMAP 服务器' },
      { key: 'port', label: '端口', placeholder: '993', type: 'number' },
      { key: 'username', label: '用户名' },
      { key: 'password', label: '密码', type: 'password' },
      { key: 'mailbox', label: '收件箱', placeholder: 'INBOX' },
      { key: 'poll_secs', label: '轮询间隔(秒)', placeholder: '60', type: 'number' },
    ],
  },
  wecom: {
    icon: MessageSquare,
    label: '企业微信机器人',
    desc: '接收企业微信自建应用消息回调，命中工单 ID 回复、否则建单。',
    webhook: '/api/v1/inbound/wecom',
    fields: [
      { key: 'token', label: 'Token' },
      { key: 'aeskey', label: 'EncodingAESKey' },
      { key: 'corpid', label: '企业 ID (CorpID)' },
      { key: 'agentid', label: '应用 AgentId' },
      { key: 'process_id', label: '流程模板 ID' },
    ],
  },
  dingtalk: {
    icon: MessageSquare,
    label: '钉钉机器人',
    desc: '接收钉钉事件订阅回调（明文/加密模式），路由到工单。',
    webhook: '/api/v1/inbound/dingtalk',
    fields: [
      { key: 'token', label: 'Token' },
      { key: 'aeskey', label: 'EncodingAESKey (可选)' },
      { key: 'process_id', label: '流程模板 ID' },
    ],
  },
  feishu: {
    icon: Webhook,
    label: '飞书机器人',
    desc: '接收飞书事件订阅回调（HMAC-SHA256 验签 / 可选 AES 加密），路由到工单。',
    webhook: '/api/v1/inbound/feishu',
    fields: [
      { key: 'token', label: 'Verification Token' },
      { key: 'secret', label: 'App Secret', type: 'password' },
      { key: 'encrypt_key', label: 'EncodingAESKey (可选)' },
      { key: 'process_id', label: '流程模板 ID' },
    ],
  },
}

type CfgMap = Record<string, string | number>

interface Editor {
  enabled: boolean
  cfg: CfgMap
}

export default function InboundConfigPage() {
  const [channels, setChannels] = useState<Channel[]>([])
  const [editors, setEditors] = useState<Record<string, Editor>>({})
  const [savingId, setSavingId] = useState<string | null>(null)
  const [savedAt, setSavedAt] = useState<Record<string, number>>({})

  useEffect(() => {
    apiList<Channel>('/automation/inbound/channels')
      .then(list => {
        setChannels(list)
        const init: Record<string, Editor> = {}
        for (const c of list) {
          let cfg: CfgMap = {}
          if (c.config) {
            try { cfg = JSON.parse(c.config) } catch { cfg = {} }
          }
          init[c.id] = { enabled: c.enabled, cfg }
        }
        setEditors(init)
      })
      .catch(() => getMessage().error('加载入站渠道失败'))
  }, [])

  const toggleEnabled = (id: string, val: boolean) =>
    setEditors(prev => ({ ...prev, [id]: { ...prev[id], enabled: val } }))

  const setField = (id: string, key: string, value: string) =>
    setEditors(prev => ({ ...prev, [id]: { ...prev[id], cfg: { ...prev[id].cfg, [key]: value } } }))

  const save = async (c: Channel) => {
    const ed = editors[c.id]
    if (!ed) return
    setSavingId(c.id)
    try {
      const body = {
        id: c.id,
        type: c.type,
        name: c.name,
        enabled: ed.enabled,
        config: JSON.stringify(ed.cfg),
      }
      await apiFetch(`/automation/inbound/channels/${c.id}`, { method: 'PUT', body: JSON.stringify(body) })
      setChannels(prev => prev.map(x => x.id === c.id ? { ...x, enabled: ed.enabled, config: body.config } : x))
      setSavedAt(prev => ({ ...prev, [c.id]: Date.now() }))
      getMessage().success(`${c.name} 已保存`)
    } catch (e: any) {
      getMessage().error('保存失败: ' + e.message)
    } finally {
      setSavingId(null)
    }
  }

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2">
          <Webhook className="h-5 w-5" />入站配置
        </h1>
        <p className="text-xs text-muted-foreground">配置邮件与 IM 机器人（企业微信 / 钉钉 / 飞书）入站渠道，保存后实时生效，无需重启。</p>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {channels.map(c => {
          const meta = channelMeta[c.type]
          if (!meta) return null
          const ed = editors[c.id]
          const Icon = meta.icon
          const isSaved = savedAt[c.id] && Date.now() - savedAt[c.id] < 2500
          return (
            <Card key={c.id}>
              <CardHeader className="pb-3">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex items-center gap-2 min-w-0">
                    <div className="h-8 w-8 rounded-lg bg-blue-500/10 text-blue-500 flex items-center justify-center shrink-0">
                      <Icon className="h-4 w-4" />
                    </div>
                    <div className="min-w-0">
                      <CardTitle className="text-sm truncate">{meta.label}</CardTitle>
                      <p className="text-[11px] text-muted-foreground leading-snug mt-0.5">{meta.desc}</p>
                    </div>
                  </div>
                  <Switch
                    checked={!!ed?.enabled}
                    onChange={v => toggleEnabled(c.id, v)}
                  />
                </div>
                {meta.webhook && (
                  <Tag className="mt-2 w-fit font-mono text-[10px]">
                    {meta.webhook}
                  </Tag>
                )}
              </CardHeader>
              <CardContent className="space-y-2.5">
                <div className="grid grid-cols-2 gap-2.5">
                  {meta.fields.map(f => (
                    <div key={f.key} className={f.key === 'process_id' ? 'col-span-2' : ''}>
                      <label className="text-xs font-medium text-muted-foreground">{f.label}</label>
                      <Input
                        type={f.type ?? 'text'}
                        value={(ed?.cfg?.[f.key] ?? '') as string}
                        placeholder={f.placeholder}
                        onChange={e => setField(c.id, f.key, e.target.value)}
                        className="h-8 text-xs mt-1"
                      />
                    </div>
                  ))}
                </div>
                <div className="flex items-center justify-end pt-1">
                  <Button size="middle" onClick={() => save(c)} disabled={savingId === c.id}>
                    {savingId === c.id ? (
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : isSaved ? (
                      <CheckCircle2 className="h-3.5 w-3.5 text-blue-500" />
                    ) : (
                      <Save className="h-3.5 w-3.5" />
                    )}
                    {savingId === c.id ? '保存中' : isSaved ? '已保存' : '保存'}
                  </Button>
                </div>
              </CardContent>
            </Card>
          )
        })}
      </div>

      {channels.length === 0 && (
        <Card><CardContent className="p-6 text-center text-sm text-muted-foreground">暂无入站渠道</CardContent></Card>
      )}
    </div>
  )
}
