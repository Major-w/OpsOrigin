
import { useCallback, useEffect, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Form, Table, Select, Empty, Alert, Popconfirm } from 'antd'
import { apiFetch, formatTime } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { useAuthStore } from '@/stores/auth'
import { getMessage } from '@/lib/message'
import { ErrorState } from '@/components/ui/error-state'
import { Key, Plus, RefreshCw, Trash2, Copy, RotateCw, ShieldCheck, Eye, Info } from 'lucide-react'

// ——— API Key 管理 ———
// 设计要点（与后端 internal/apikey 一致）：
//  * Key 绑定到用户；鉴权命中后以该用户身份通过，权限 = 用户当前「角色 + 用户组」聚合，
//    因此用户角色变更后名下所有 Key 的权限自动跟随（不需要重建 Key，也不在 Key 上冗余权限）。
//  * 明文仅在创建/轮换响应中返回一次，服务端只存 SHA-256 摘要 —— 关闭弹窗后无法再查看。
//  * 任何登录用户可管理自己的 Key；管理员可切换到「全租户」视角做密钥治理。

interface APIKey {
  id: string
  tenant_id: string
  user_id: string
  username?: string
  name: string
  key_prefix: string
  expires_at?: string | null
  last_used_at?: string | null
  use_count: number
  revoked_at?: string | null
  created_at: string
}

type KeyStatus = 'active' | 'expired' | 'revoked'

function keyStatus(k: APIKey): KeyStatus {
  if (k.revoked_at) return 'revoked'
  if (k.expires_at && new Date(k.expires_at).getTime() < Date.now()) return 'expired'
  return 'active'
}

const STATUS_META: Record<KeyStatus, { label: string; color: string }> = {
  active: { label: '有效', color: 'green' },
  expired: { label: '已过期', color: 'orange' },
  revoked: { label: '已吊销', color: 'red' },
}

const EXPIRY_OPTIONS = [
  { value: 0, label: '永不过期' },
  { value: 30, label: '30 天' },
  { value: 90, label: '90 天' },
  { value: 180, label: '180 天' },
  { value: 365, label: '365 天' },
]

export default function APIKeysPage() {
  const canSelf = useCan('apikey:self')
  const role = useAuthStore(s => s.user?.role)
  const isAdmin = role === 'admin'

  const [keys, setKeys] = useState<APIKey[]>([])
  const [scope, setScope] = useState<'self' | 'tenant'>('self')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [createOpen, setCreateOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [form] = Form.useForm()

  // 创建/轮换后的一次性明文弹窗。
  const [revealed, setRevealed] = useState<{ raw: string; name: string; notice: string } | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const q = isAdmin && scope === 'tenant' ? '?all=true' : ''
      const data = await apiFetch<{ keys: APIKey[] }>('/api-keys' + q)
      setKeys(data.keys || [])
    } catch (e: any) {
      setError(e?.message || '加载 API Key 失败')
    } finally {
      setLoading(false)
    }
  }, [isAdmin, scope])

  useEffect(() => { if (canSelf) load() }, [canSelf, load])

  const doCreate = async () => {
    try {
      const values = await form.validateFields()
      setCreating(true)
      const data = await apiFetch<{ key: APIKey; raw_key: string; notice: string }>('/api-keys', {
        method: 'POST',
        body: JSON.stringify({
          name: values.name,
          expires_in_days: values.expires_in_days || 0,
          user_id: values.user_id || '',
        }),
      })
      setCreateOpen(false)
      form.resetFields()
      setRevealed({ raw: data.raw_key, name: data.key.name, notice: data.notice })
      await load()
    } catch (e: any) {
      if (e?.errorFields) return // 表单校验失败，交给 antd 展示
      getMessage().error('创建失败: ' + (e?.message || e))
    } finally {
      setCreating(false)
    }
  }

  const doRevoke = async (k: APIKey) => {
    try {
      await apiFetch('/api-keys/' + k.id, { method: 'DELETE' })
      getMessage().success('已吊销密钥：' + k.name)
      await load()
    } catch (e: any) {
      getMessage().error('吊销失败: ' + (e?.message || e))
    }
  }

  const doRotate = async (k: APIKey) => {
    try {
      const data = await apiFetch<{ key: APIKey; raw_key: string; notice: string }>(
        `/api-keys/${k.id}/rotate`, { method: 'POST', body: JSON.stringify({ name: k.name }) })
      setRevealed({ raw: data.raw_key, name: data.key.name, notice: data.notice })
      getMessage().success('已轮换密钥，旧密钥已吊销')
      await load()
    } catch (e: any) {
      getMessage().error('轮换失败: ' + (e?.message || e))
    }
  }

  const copyRaw = async () => {
    if (!revealed) return
    try {
      await navigator.clipboard.writeText(revealed.raw)
      getMessage().success('已复制到剪贴板')
    } catch {
      getMessage().warning('复制失败，请手动选中复制')
    }
  }

  if (!canSelf) {
    return (
      <div className="p-6">
        <Card><CardContent className="flex flex-col items-center justify-center gap-2 py-16 text-center">
          <ShieldCheck className="h-8 w-8 text-muted-foreground" />
          <p className="text-sm font-medium">无访问权限</p>
          <p className="text-xs text-muted-foreground">当前账号缺少「管理自己的 API Key（apikey:self）」权限。</p>
        </CardContent></Card>
      </div>
    )
  }

  const columns = [
    {
      title: '名称', dataIndex: 'name', key: 'name',
      render: (v: string, row: APIKey) => (
        <div className="min-w-0">
          <div className="font-medium truncate">{v}</div>
          <div className="text-[11px] text-muted-foreground font-mono">{row.key_prefix}••••••••</div>
        </div>
      ),
    },
    ...(scope === 'tenant' ? [{
      title: '绑定用户', dataIndex: 'username', key: 'username', width: 160,
      render: (v: string, row: APIKey) => (
        <div className="min-w-0">
          <div className="text-xs truncate">{v || '(已删除用户)'}</div>
          <div className="text-[11px] text-muted-foreground font-mono truncate">{row.user_id}</div>
        </div>
      ),
    }] : []),
    {
      title: '状态', key: 'status', width: 92,
      render: (_: any, row: APIKey) => {
        const st = keyStatus(row)
        return <Tag color={STATUS_META[st].color}>{STATUS_META[st].label}</Tag>
      },
    },
    {
      title: '调用次数', dataIndex: 'use_count', key: 'use_count', width: 100,
      render: (v: number) => <Tag>{v ?? 0}</Tag>,
    },
    {
      title: '最近使用', dataIndex: 'last_used_at', key: 'last_used_at', width: 165,
      render: (v: string | null) => <span className="text-xs text-muted-foreground">{v ? formatTime(v) : '从未使用'}</span>,
    },
    {
      title: '过期时间', dataIndex: 'expires_at', key: 'expires_at', width: 165,
      render: (v: string | null) => <span className="text-xs text-muted-foreground">{v ? formatTime(v) : '永不过期'}</span>,
    },
    {
      title: '创建时间', dataIndex: 'created_at', key: 'created_at', width: 165,
      render: (v: string) => <span className="text-xs text-muted-foreground">{formatTime(v)}</span>,
    },
    {
      title: '操作', key: 'op', width: 168, fixed: 'right' as const,
      render: (_: any, row: APIKey) => {
        const disabled = keyStatus(row) === 'revoked'
        return (
          <div className="flex items-center gap-1">
            <Popconfirm
              title="轮换该密钥？"
              description="将立即吊销当前密钥并生成一把新密钥（明文仅展示一次）。"
              okText="轮换" cancelText="取消" disabled={disabled}
              onConfirm={() => doRotate(row)}
            >
              <Button size="small" type="text" disabled={disabled}>
                <RotateCw className="h-3.5 w-3.5 mr-1" />轮换
              </Button>
            </Popconfirm>
            <Popconfirm
              title="确认吊销该密钥？"
              description="吊销后使用该密钥的调用将立即返回 401，且不可恢复。"
              okText="吊销" okButtonProps={{ danger: true }} cancelText="取消" disabled={disabled}
              onConfirm={() => doRevoke(row)}
            >
              <Button size="small" type="text" danger disabled={disabled}>
                <Trash2 className="h-3.5 w-3.5 mr-1" />吊销
              </Button>
            </Popconfirm>
          </div>
        )
      },
    },
  ]

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-bold">API Key 管理</h1>
          <p className="text-sm text-muted-foreground">
            为自动化脚本 / 外部系统签发长期凭证。密钥绑定到用户，调用时的权限等于该用户当前的角色与用户组聚合权限
          </p>
        </div>
        <div className="flex items-center gap-2">
          {isAdmin && (
            <Select
              size="middle" value={scope} style={{ width: 140 }}
              onChange={v => setScope(v)}
              options={[{ value: 'self', label: '仅我的密钥' }, { value: 'tenant', label: '全租户密钥' }]}
            />
          )}
          <Button type="default" onClick={load} loading={loading}>
            <RefreshCw className="h-3.5 w-3.5 mr-1" />刷新
          </Button>
          <Button type="primary" onClick={() => { form.resetFields(); setCreateOpen(true) }}>
            <Plus className="h-3.5 w-3.5 mr-1" />新建 API Key
          </Button>
        </div>
      </div>

      <Alert
        type="info" showIcon icon={<Info className="h-4 w-4" />}
        message="权限继承说明"
        description={
          <span className="text-xs">
            请求携带 <code className="font-mono">X-API-Key: &lt;key&gt;</code>（或 <code className="font-mono">Authorization: ApiKey &lt;key&gt;</code>）即可通过鉴权。
            权限由绑定用户实时派生 —— 用户被停用或角色被降级后，其名下所有密钥立即跟随失效/降权，无需逐个回收。
            密钥明文仅在创建/轮换时展示一次，服务端只存摘要，请妥善保管。
          </span>
        }
      />

      <Card>
        <CardContent className="p-0">
          {error ? (
            <ErrorState message={error} onRetry={load} />
          ) : (
            <Table
              rowKey="id"
              loading={loading}
              columns={columns}
              dataSource={keys}
              scroll={{ x: scope === 'tenant' ? 1200 : 1050 }}
              pagination={{ pageSize: 10, hideOnSinglePage: true }}
              locale={{
                emptyText: (
                  <Empty description={
                    scope === 'tenant' ? '租户下暂无 API Key' : '还没有 API Key，点击右上角「新建 API Key」为你的自动化任务签发凭证'
                  } />
                ),
              }}
            />
          )}
        </CardContent>
      </Card>

      {/* 新建 */}
      <Modal
        open={createOpen}
        title="新建 API Key"
        onCancel={() => setCreateOpen(false)}
        onOk={doCreate}
        confirmLoading={creating}
        okText="创建"
        cancelText="取消"
        destroyOnHidden
        width={520}
      >
        <Form form={form} layout="vertical" initialValues={{ expires_in_days: 90 }}>
          <Form.Item
            name="name" label="名称" rules={[{ required: true, message: '请填写名称，便于后续识别用途' }]}
          >
            <Input placeholder="例如：CI 流水线 / 巡检脚本 / 第三方监控对接" maxLength={60} />
          </Form.Item>
          <Form.Item name="expires_in_days" label="有效期">
            <Select options={EXPIRY_OPTIONS} />
          </Form.Item>
          {isAdmin && (
            <Form.Item
              name="user_id" label="绑定用户（可选）"
              extra="留空则绑定到你自己。填写其他用户 ID 可为该用户签发密钥（仅管理员可用），其权限继承该用户。"
            >
              <Input placeholder="用户 ID，留空为本人" />
            </Form.Item>
          )}
          <p className="text-xs text-muted-foreground">
            创建成功后会一次性展示密钥明文，请立即保存 —— 关闭弹窗后无法再次查看。
          </p>
        </Form>
      </Modal>

      {/* 一次性明文展示 */}
      <Modal
        open={!!revealed}
        title={<span className="flex items-center gap-2"><Key className="h-4 w-4 text-blue-500" />请保存密钥明文 · {revealed?.name}</span>}
        onCancel={() => setRevealed(null)}
        footer={[
          <Button key="copy" type="primary" onClick={copyRaw}>
            <Copy className="h-3.5 w-3.5 mr-1" />复制密钥
          </Button>,
          <Button key="done" onClick={() => setRevealed(null)}>我已保存</Button>,
        ]}
        width={560}
      >
        <Alert
          type="warning" showIcon className="mb-3"
          message="此密钥仅展示这一次"
          description={<span className="text-xs">{revealed?.notice}</span>}
        />
        <div className="rounded-md border bg-muted/40 p-3 font-mono text-xs break-all select-all">
          {revealed?.raw}
        </div>
        <p className="text-xs text-muted-foreground mt-3 flex items-center gap-1.5">
          <Eye className="h-3.5 w-3.5" />
          使用方式：<code className="font-mono">curl -H "X-API-Key: &lt;key&gt;" https://&lt;core&gt;/api/v1/cmdb/ci</code>
        </p>
      </Modal>
    </div>
  )
}
