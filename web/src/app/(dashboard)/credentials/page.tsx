
import { useState, useEffect, useMemo } from 'react'
import { useKeyStore } from '@/stores/keys'
import { useScriptStore } from '@/stores/script'
import { useAgentStore } from '@/stores/agent'
import { formatTime, cn } from '@/lib/utils'
import { Card, CardContent } from '@/components/ui/card'
import { ViewToggle, type ViewMode } from '@/components/ui/data-list'
import type { KeyEntry } from '@/stores/keys'
import { Copy, Eye, EyeOff, Key, Lock, Database, Fingerprint, Plus, Trash2, Edit, CheckSquare, Square, Search, X, Wifi, CheckCircle, XCircle, Loader2 } from 'lucide-react'
import { getMessage } from '@/lib/message'
import { Button, Input, Modal, Popconfirm, Tag } from 'antd'

const credentialTypeLabels: Record<string, string> = {
  ssh_private: 'SSH私钥',
  ssh_public: 'SSH公钥',
  password: '密码',
  token: 'API Token',
  database: '数据库连接串',
}

const credentialTypeIcons: Record<string, React.FC<{ className?: string }>> = {
  ssh_private: Key,
  ssh_public: Key,
  password: Lock,
  token: Fingerprint,
  database: Database,
}

const typeOptions = [
  { value: '', label: '全部' },
  { value: 'ssh_private', label: 'SSH私钥' },
  { value: 'ssh_public', label: 'SSH公钥' },
  { value: 'password', label: '密码' },
  { value: 'token', label: 'API Token' },
  { value: 'database', label: '数据库' },
]

type CredentialForm = {
  name: string
  type: string
  content: string
  username: string
  host: string
  port: string
}

const emptyForm: CredentialForm = {
  name: '', type: 'ssh_private', content: '', username: '', host: '', port: '',
}

export default function CredentialsPage() {
  const { keys, loading, fetch, create, update, remove } = useKeyStore()
  const { scripts, fetch: fetchScripts } = useScriptStore()
  const { agents, fetch: fetchAgents, testSSH } = useAgentStore()

  const [searchQuery, setSearchQuery] = useState('')
  const [typeFilter, setTypeFilter] = useState('')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [showCreateDialog, setShowCreateDialog] = useState(false)
  const [showEditDialog, setShowEditDialog] = useState(false)
  const [showDeleteDialog, setShowDeleteDialog] = useState(false)
  const [showBatchDeleteDialog, setShowBatchDeleteDialog] = useState(false)
  const [currentKey, setCurrentKey] = useState<KeyEntry | null>(null)
  const [showContent, setShowContent] = useState<Record<string, boolean>>({})
  const [createForm, setCreateForm] = useState<CredentialForm>(emptyForm)
  const [editForm, setEditForm] = useState<CredentialForm>(emptyForm)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ reachable: boolean; detail?: string } | null>(null)

  useEffect(() => {
    fetch()
    fetchScripts()
    fetchAgents()
  }, [fetch, fetchScripts, fetchAgents])

  const filteredKeys = keys.filter(key => {
    const matchesSearch = key.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
                         key.fingerprint?.toLowerCase().includes(searchQuery.toLowerCase())
    const matchesType = !typeFilter || key.type === typeFilter
    return matchesSearch && matchesType
  })

  const usageMap = useMemo(() => {
    const map: Record<string, { hosts: number; scripts: number }> = {}
    for (const key of keys) {
      map[key.id] = { hosts: 0, scripts: 0 }
    }
    for (const agent of agents) {
      const aid = (agent as any).credential_id
      if (aid && map[aid]) {
        map[aid].hosts++
      }
    }
    for (const script of scripts) {
      for (const key of keys) {
        let matched = false
        if (script.content && (script.content.includes(key.id) || script.content.includes(key.name))) {
          matched = true
        }
        if (!matched && script.input_params) {
          for (const p of script.input_params) {
            if (p.name === 'credential_id' && p.default === key.id) {
              matched = true
              break
            }
          }
        }
        if (matched) {
          map[key.id].scripts++
        }
      }
    }
    return map
  }, [keys, agents, scripts])

  const handleCreate = async () => {
    await create({
      name: createForm.name,
      type: createForm.type as any,
      content: createForm.content,
      username: createForm.username,
      host: createForm.host,
      port: createForm.port,
    })
    setShowCreateDialog(false)
    setCreateForm(emptyForm)
    setTestResult(null)
  }

  const handleEdit = async () => {
    if (!currentKey) return
    await update(currentKey.id, {
      name: editForm.name,
      type: editForm.type as any,
      content: editForm.content,
      username: editForm.username,
      host: editForm.host,
      port: editForm.port,
    })
    setShowEditDialog(false)
    setCurrentKey(null)
    setTestResult(null)
  }

  const handleDelete = async () => {
    if (!currentKey) return
    await remove(currentKey.id)
    setShowDeleteDialog(false)
    setCurrentKey(null)
  }

  const handleBatchDelete = async () => {
    if (selectedIds.size === 0) return
    const ids = Array.from(selectedIds)
    for (const id of ids) {
      await remove(id)
    }
    setSelectedIds(new Set())
    setShowBatchDeleteDialog(false)
  }

  const handleCopy = (content: string) => {
    navigator.clipboard.writeText(content)
    getMessage().success('已复制到剪贴板')
  }

  const handleTestSSH = async (form: CredentialForm) => {
    setTesting(true)
    setTestResult(null)
    try {
      const isSSH = form.type === 'ssh_private' || form.type === 'ssh_public'
      const payload = isSSH
        ? { address: form.host, port: Number(form.port) || 22, username: form.username, ssh_key: form.content, use_key: true }
        : { address: form.host, port: Number(form.port) || 22, username: form.username, password: form.content }
      const result = await testSSH(payload)
      setTestResult({ reachable: !!result?.reachable, detail: result?.detail })
      if (result?.reachable) {
        getMessage().success('SSH 连接测试成功')
      } else {
        getMessage().error(`SSH 连接失败: ${result?.detail || '不可达'}`)
      }
    } catch (e: any) {
      setTestResult({ reachable: false, detail: e.message })
    } finally {
      setTesting(false)
    }
  }

  const canSave = (form: CredentialForm) => {
    const needsTest = (form.type === 'ssh_private' || form.type === 'ssh_public' || form.type === 'password')
      && !!form.host && !!form.port && !!form.username
    if (!needsTest) return true
    return testResult?.reachable === true
  }

  const toggleSelectAll = () => {
    if (selectedIds.size === filteredKeys.length && filteredKeys.length > 0) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(filteredKeys.map(k => k.id)))
    }
  }

  const toggleSelect = (id: string) => {
    const next = new Set(selectedIds)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    setSelectedIds(next)
  }

  const toggleContentVisibility = (id: string) => {
    setShowContent(prev => ({ ...prev, [id]: !prev[id] }))
  }

  const openCreateDialog = () => {
    setCreateForm(emptyForm)
    setTestResult(null)
    setShowCreateDialog(true)
  }

  const openEditDialog = (key: KeyEntry) => {
    setCurrentKey(key)
    setEditForm({
      name: key.name,
      type: key.type,
      content: key.content,
      username: key.username || '',
      host: key.host || '',
      port: key.port || '',
    })
    setTestResult(null)
    setShowEditDialog(true)
  }

  const openDeleteDialog = (key: KeyEntry) => {
    setCurrentKey(key)
    setShowDeleteDialog(true)
  }

  const renderContentField = (key: KeyEntry) => {
    const isVisible = showContent[key.id]
    return (
      <div className="flex items-center gap-1">
        <code className="text-xs text-muted-foreground font-mono truncate max-w-[200px]">
          {isVisible ? key.content : '••••••••••••••••••••••••'}
        </code>
        <Button
          type="text"
         
          className="h-6 w-6"
          onClick={() => toggleContentVisibility(key.id)}
        >
          {isVisible ? <EyeOff className="h-3 w-3" /> : <Eye className="h-3 w-3" />}
        </Button>
        <Button
          type="text"
         
          className="h-6 w-6"
          onClick={() => handleCopy(key.content)}
        >
          <Copy className="h-3 w-3" />
        </Button>
      </div>
    )
  }

  const renderTime = (key: KeyEntry) => {
    const updated = key.updated_at && key.updated_at !== key.created_at
    return (
      <span className="text-xs text-muted-foreground">
        {updated ? `更新于 ${formatTime(key.updated_at)}` : `创建于 ${formatTime(key.created_at)}`}
      </span>
    )
  }

  const renderUsage = (key: KeyEntry) => {
    const usage = usageMap[key.id] || { hosts: 0, scripts: 0 }
    return (
      <Tag>
        引用: {usage.hosts}主机 / {usage.scripts}脚本
      </Tag>
    )
  }

  const renderFormFields = (
    form: CredentialForm,
    setForm: React.Dispatch<React.SetStateAction<CredentialForm>>
  ) => {
    const isSSH = form.type === 'ssh_private' || form.type === 'ssh_public'
    const showConnection = form.type !== 'token'
    const canTest = (isSSH || form.type === 'password') && !!form.host && !!form.username
    const contentLabel = isSSH ? '密钥内容'
      : form.type === 'password' ? '密码'
      : form.type === 'token' ? 'Token' : '连接串'
    const contentPlaceholder = form.type === 'ssh_private' ? '-----BEGIN OPENSSH PRIVATE KEY-----'
      : form.type === 'ssh_public' ? 'ssh-rsa AAAA...'
      : form.type === 'password' ? '输入密码'
      : form.type === 'token' ? '输入API Token'
      : '输入数据库连接串'

    return (
      <div className="space-y-4">
        <div>
          <label className="text-sm font-medium">凭证名称</label>
          <Input
            value={form.name}
            onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
            placeholder="输入凭证名称"
          />
        </div>
        <div>
          <label className="text-sm font-medium">凭证类型</label>
          <select
            value={form.type}
            onChange={e => { setForm(f => ({ ...f, type: e.target.value })); setTestResult(null) }}
            className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          >
            <option value="ssh_private">SSH私钥</option>
            <option value="ssh_public">SSH公钥</option>
            <option value="password">密码</option>
            <option value="token">API Token</option>
            <option value="database">数据库连接串</option>
          </select>
        </div>
        <div>
          <label className="text-sm font-medium">{contentLabel}</label>
          <textarea
            value={form.content}
            onChange={e => setForm(f => ({ ...f, content: e.target.value }))}
            className="flex h-24 w-full rounded-md border border-input bg-background px-3 py-2 text-xs font-mono"
            placeholder={contentPlaceholder}
          />
        </div>
        {showConnection && (
          <>
            {isSSH && (
              <p className="text-xs text-muted-foreground">可选：用户名/主机/端口（用于测试连接）</p>
            )}
            <div>
              <label className="text-sm font-medium">用户名</label>
              <Input
                value={form.username}
                onChange={e => setForm(f => ({ ...f, username: e.target.value }))}
                placeholder="输入用户名"
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="text-sm font-medium">主机</label>
                <Input
                  value={form.host}
                  onChange={e => setForm(f => ({ ...f, host: e.target.value }))}
                  placeholder="输入主机地址"
                />
              </div>
              <div>
                <label className="text-sm font-medium">端口</label>
                <Input
                  value={form.port}
                  onChange={e => setForm(f => ({ ...f, port: e.target.value }))}
                  placeholder="输入端口"
                />
              </div>
            </div>
          </>
        )}
        {canTest && (
          <div className="space-y-2">
            <Button
              type="default"
              size="middle"
              onClick={() => handleTestSSH(form)}
              disabled={testing}
            >
              {testing ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Wifi className="h-3.5 w-3.5 mr-1" />}
              {testing ? '测试中...' : '测试连接'}
            </Button>
            {testResult && (
              <div className={cn("flex items-center gap-2 text-xs", testResult.reachable ? "text-blue-600" : "text-red-600")}>
                {testResult.reachable ? <CheckCircle className="h-3.5 w-3.5" /> : <XCircle className="h-3.5 w-3.5" />}
                <span>{testResult.reachable ? '连接成功' : '连接失败'}</span>
                {testResult.detail && <span className="text-muted-foreground">({testResult.detail})</span>}
              </div>
            )}
          </div>
        )}
      </div>
    )
  }

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">凭证管理</h1>
          <p className="text-sm text-muted-foreground mt-1">管理SSH密钥、密码、API Token等凭证</p>
        </div>
        <div className="flex items-center gap-2">
          <Button type="default" size="middle" onClick={openCreateDialog}>
            <Plus className="h-4 w-4 mr-1" />新增凭证
          </Button>
          {selectedIds.size > 0 && (
            <Button danger size="middle" onClick={() => setShowBatchDeleteDialog(true)}>
              <Trash2 className="h-4 w-4 mr-1" />批量删除 ({selectedIds.size})
            </Button>
          )}
        </div>
      </div>

      <div className="flex items-center gap-4 flex-wrap">
        <div className="relative flex-1 max-w-md min-w-[200px]">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder="搜索凭证..."
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            className="pl-9"
          />
          {searchQuery && (
            <button className="absolute right-3 top-1/2 -translate-y-1/2" onClick={() => setSearchQuery('')}>
              <X className="h-3 w-3 text-muted-foreground hover:text-foreground" />
            </button>
          )}
        </div>
        <div className="flex items-center gap-2 flex-wrap">
          {typeOptions.map(opt => (
            <Tag
              key={opt.value}
              color={typeFilter === opt.value ? 'blue' : 'default'}
              className="cursor-pointer"
              onClick={() => setTypeFilter(opt.value)}
            >
              {opt.label}
            </Tag>
          ))}
        </div>
        <ViewToggle value={viewMode} onChange={setViewMode} />
      </div>

      {loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-400"></div>
        </div>
      ) : filteredKeys.length === 0 ? (
        <Card>
          <CardContent className="py-12 text-center text-muted-foreground">
            暂无凭证。请点击右上角"新增凭证"按钮添加。
          </CardContent>
        </Card>
      ) : viewMode === 'grid' ? (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {filteredKeys.map(key => {
            const Icon = credentialTypeIcons[key.type] || Key
            const isVisible = showContent[key.id]
            return (
              <Card key={key.id}>
                <CardContent className="p-4 space-y-3">
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex items-center gap-3 min-w-0">
                      <Button
                        type="text"
                       
                        className="h-6 w-6 -ml-1 flex-shrink-0"
                        onClick={() => toggleSelect(key.id)}
                      >
                        {selectedIds.has(key.id) ? <CheckSquare className="h-4 w-4" /> : <Square className="h-4 w-4" />}
                      </Button>
                      <div className="flex items-center justify-center w-10 h-10 rounded-lg bg-muted flex-shrink-0">
                        <Icon className="h-5 w-5 text-muted-foreground" />
                      </div>
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="font-medium truncate">{key.name}</span>
                          <Tag>
                            {credentialTypeLabels[key.type] || key.type}
                          </Tag>
                        </div>
                        {key.fingerprint && (
                          <div className="text-xs text-muted-foreground font-mono truncate mt-1">
                            {key.fingerprint}
                          </div>
                        )}
                      </div>
                    </div>
                    <div className="flex items-center gap-1 flex-shrink-0">
                      <Button type="text" className="h-7 w-7" onClick={() => openEditDialog(key)}>
                        <Edit className="h-3.5 w-3.5" />
                      </Button>
                      <Button type="text" className="h-7 w-7 text-destructive hover:text-destructive" onClick={() => openDeleteDialog(key)}>
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </div>
                  {(key.username || key.host) && (
                    <div className="text-xs text-muted-foreground">
                      {key.username && <span>用户: {key.username}</span>}
                      {key.host && <span className="ml-2">主机: {key.host}{key.port ? `:${key.port}` : ''}</span>}
                    </div>
                  )}
                  <div className="flex items-center gap-1">
                    <code className="text-xs text-muted-foreground font-mono truncate flex-1">
                      {isVisible ? key.content : '••••••••••••••••••••••••'}
                    </code>
                    <Button type="text" className="h-6 w-6" onClick={() => toggleContentVisibility(key.id)}>
                      {isVisible ? <EyeOff className="h-3 w-3" /> : <Eye className="h-3 w-3" />}
                    </Button>
                    <Button type="text" className="h-6 w-6" onClick={() => handleCopy(key.content)}>
                      <Copy className="h-3 w-3" />
                    </Button>
                  </div>
                  <div className="flex items-center justify-between gap-2 flex-wrap">
                    {renderUsage(key)}
                    {renderTime(key)}
                  </div>
                </CardContent>
              </Card>
            )
          })}
        </div>
      ) : (
        <Card>
          <CardContent className="p-0">
            <div className="flex items-center justify-between px-4 py-3 border-b">
              <div className="flex items-center gap-2">
                <Button type="text" className="h-6 w-6" onClick={toggleSelectAll}>
                  {selectedIds.size === filteredKeys.length && filteredKeys.length > 0 ? (
                    <CheckSquare className="h-4 w-4" />
                  ) : (
                    <Square className="h-4 w-4" />
                  )}
                </Button>
                <span className="text-sm text-muted-foreground">
                  已选择 {selectedIds.size} / {filteredKeys.length} 项
                </span>
              </div>
            </div>
            <div className="divide-y">
              {filteredKeys.map(key => {
                const Icon = credentialTypeIcons[key.type] || Key
                return (
                  <div
                    key={key.id}
                    className="flex items-center gap-4 px-4 py-3 hover:bg-muted/50 transition-colors"
                  >
                    <Button type="text" className="h-6 w-6" onClick={() => toggleSelect(key.id)}>
                      {selectedIds.has(key.id) ? <CheckSquare className="h-4 w-4" /> : <Square className="h-4 w-4" />}
                    </Button>
                    <div className="flex items-center justify-center w-10 h-10 rounded-lg bg-muted flex-shrink-0">
                      <Icon className="h-5 w-5 text-muted-foreground" />
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-medium truncate">{key.name}</span>
                        <Tag className="text-xs">
                          {credentialTypeLabels[key.type] || key.type}
                        </Tag>
                        {renderUsage(key)}
                      </div>
                      <div className="text-xs text-muted-foreground mt-1">
                        {key.fingerprint && <span className="font-mono">{key.fingerprint}</span>}
                        {key.username && <span className="ml-2">用户: {key.username}</span>}
                        {key.host && <span className="ml-2">主机: {key.host}{key.port ? `:${key.port}` : ''}</span>}
                      </div>
                    </div>
                    <div className="flex-1 min-w-0">
                      {renderContentField(key)}
                    </div>
                    <div className="flex flex-col items-end gap-1 flex-shrink-0">
                      {renderTime(key)}
                    </div>
                    <div className="flex items-center gap-1 flex-shrink-0">
                      <Button type="text" className="h-8 w-8" onClick={() => openEditDialog(key)}>
                        <Edit className="h-4 w-4" />
                      </Button>
                      <Button type="text" className="h-8 w-8 text-destructive hover:text-destructive" onClick={() => openDeleteDialog(key)}>
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </div>
                )
              })}
            </div>
          </CardContent>
        </Card>
      )}

      <Modal title="新增凭证" open={showCreateDialog} onCancel={() => setShowCreateDialog(false)} footer={<><Button type="default" onClick={() => setShowCreateDialog(false)}>取消</Button><Button onClick={handleCreate} disabled={!canSave(createForm)}>创建</Button></>}>
        {renderFormFields(createForm, setCreateForm)}
      </Modal>

      <Modal title="编辑凭证" open={showEditDialog} onCancel={() => setShowEditDialog(false)} footer={<><Button type="default" onClick={() => setShowEditDialog(false)}>取消</Button><Button onClick={handleEdit} disabled={!canSave(editForm)}>保存</Button></>}>
        {renderFormFields(editForm, setEditForm)}
      </Modal>

      <Modal title="确认删除" open={showDeleteDialog} onCancel={() => setShowDeleteDialog(false)} footer={<><Button type="default" onClick={() => setShowDeleteDialog(false)}>取消</Button><Button danger onClick={handleDelete}>删除</Button></>}>
        <p className="text-sm text-muted-foreground">
          确定要删除凭证 "{currentKey?.name}" 吗？此操作不可撤销。
        </p>
      </Modal>

      <Modal title="确认批量删除" open={showBatchDeleteDialog} onCancel={() => setShowBatchDeleteDialog(false)} footer={<><Button type="default" onClick={() => setShowBatchDeleteDialog(false)}>取消</Button><Button danger onClick={handleBatchDelete}>删除</Button></>}>
        <p className="text-sm text-muted-foreground">
          确定要删除选中的 {selectedIds.size} 个凭证吗？此操作不可撤销。
        </p>
      </Modal>
    </div>
  )
}
