'use client'

import { useEffect, useState } from 'react'
import { useAgentStore, type AgentHost, type ProxyInfo, type TaskInfo } from '@/stores/agent'
import { useKeyStore } from '@/stores/keys'
import { useCloudStore } from '@/stores/cloud'
import { CLOUD_RESOURCE_TYPES, K8S_RESOURCE_TYPES, type CloudResource } from '@/lib/cloud'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ViewToggle, DataList, type ViewMode, type Column, type FilterOption, type BulkAction } from '@/components/ui/data-list'
import { formatTime, apiBase, apiFetch } from '@/lib/utils'
import { validateHostName, sanitizeHostName, HOST_NAME_RULE_TEXT } from '@/lib/validate'
import { Plus, Trash2, Terminal, Monitor, RefreshCw, Download, CheckCircle, XCircle, Clock, Eye, Cpu, HardDrive, MemoryStick, Copy, Server, Wifi, WifiOff, Cloud, Container, Pencil, RotateCcw, Zap, ListChecks, AlertTriangle, Save, Play, ArrowUp, BookOpen, GitMerge } from 'lucide-react'
import { toast } from '@/stores/toast'

function statusVariant(s: string): 'success' | 'destructive' | 'secondary' | 'outline' {
  if (s === 'online') return 'success'
  if (s === 'offline') return 'destructive'
  return 'secondary'
}

function statusLabel(s: string) {
  if (s === 'online') return '在线'
  if (s === 'offline') return '离线'
  return '未知'
}

function taskStatusVariant(s: string): 'success' | 'destructive' | 'secondary' | 'warning' {
  if (s === 'completed') return 'success'
  if (s === 'failed') return 'destructive'
  if (s === 'running') return 'warning'
  return 'secondary'
}

function taskStatusLabel(s: string) {
  if (s === 'pending') return '待执行'
  if (s === 'running') return '执行中'
  if (s === 'completed') return '已完成'
  if (s === 'failed') return '失败'
  return s
}

function labelsToString(labels?: Record<string, string>): string {
  if (!labels) return ''
  return Object.entries(labels).map(([k, v]) => `${k}=${v}`).join(',')
}

function stringToLabels(s: string): Record<string, string> {
  const result: Record<string, string> = {}
  s.split(',').forEach(pair => {
    const trimmed = pair.trim()
    if (!trimmed) return
    const idx = trimmed.indexOf('=')
    if (idx > 0) result[trimmed.slice(0, idx).trim()] = trimmed.slice(idx + 1).trim()
    else result[trimmed] = ''
  })
  return result
}

export default function ResourcesPage() {
  const { agents, proxies, fetch, register, update, remove, batchInstall, testSSH, getInstallScript, fetchProxies, registerProxy, updateProxy, deleteProxy, getCircuitBreaker, setCircuitBreaker, restartAgent, upgradeAgent, uninstallAgent, fetchTasks, createTask } = useAgentStore()
  const { keys, fetch: fetchKeys, create: createKey } = useKeyStore()
  const { providers, resources, fetchProviders, listCredentials, saveCredential, listResources, syncToCMDB } = useCloudStore()

  // 云资源 tab 表单状态
  const [cloudProvider, setCloudProvider] = useState('')
  const [cloudRegion, setCloudRegion] = useState('')
  const [cloudAccessKey, setCloudAccessKey] = useState('')
  const [cloudSecretKey, setCloudSecretKey] = useState('')
  const [cloudExtra, setCloudExtra] = useState('')
  const [cloudProxyId, setCloudProxyId] = useState('')
  const [cloudTypes, setCloudTypes] = useState<string[]>(['vm', 'database', 'load_balancer'])
  const [cloudDiscovering, setCloudDiscovering] = useState(false)
  const [cloudSyncing, setCloudSyncing] = useState(false)

  // K8s 容器资源纳管 tab 表单状态（经 Agent 通道发现，无需 AK/SK）
  const [k8sAgent, setK8sAgent] = useState('')
  const [k8sTypes, setK8sTypes] = useState<string[]>(K8S_RESOURCE_TYPES.map(t => t.value))
  const [k8sResources, setK8sResources] = useState<CloudResource[]>([])
  const [k8sDiscovering, setK8sDiscovering] = useState(false)
  const [k8sSyncing, setK8sSyncing] = useState(false)

  const serverBase = typeof window !== 'undefined' ? `${window.location.protocol}//${window.location.host}` : ''
  const [mainTab, setMainTab] = useState('host')
  const [hostTab, setHostTab] = useState<string>('list')
  const [agentViewMode, setAgentViewMode] = useState<ViewMode>('list')
  const [proxyViewMode, setProxyViewMode] = useState<ViewMode>('list')

  const [regMethod, setRegMethod] = useState<'auto' | 'manual' | 'ssh'>('auto')
  const [autoForm, setAutoForm] = useState({ addresses: '', username: 'root', password: '', port: 22, credentialType: 'password', credentialId: '', keyName: '', keyContent: '', proxyId: '' })
  const [sshForm, setSSHForm] = useState({ name: '', address: '', port: 22, username: 'root', password: '', credentialType: 'password', credentialId: '', keyName: '', keyContent: '', proxyId: '' })
  const [sshResult, setSSHResult] = useState<{ reachable: boolean; address: string; detail?: string } | null>(null)
  const [sshTestedAddress, setSSHTestedAddress] = useState<string>('')
  const [scriptOS, setScriptOS] = useState('linux')
  const [scriptContent, setScriptContent] = useState('')
  const [scriptDialog, setScriptDialog] = useState(false)
  const [detailDialog, setDetailDialog] = useState(false)
  const [detailHost, setDetailHost] = useState<AgentHost | null>(null)
  const [sandboxChecked, setSandboxChecked] = useState(false)
  const isHostSandbox = (h: AgentHost | null) => !!h && (h.labels?.sandbox === 'true' || h.labels?.sandbox === '1' || h.labels?.sandbox === 'yes')
  const [autoResult, setAutoResult] = useState<any>(null)
  const [autoDialog, setAutoDialog] = useState(false)

  // 详情 dialog 扩展状态
  const [detailTab, setDetailTab] = useState('detail')
  const [editForm, setEditForm] = useState({ name: '', address: '', port: 22, ssh_user: '', labels: '' })
  const [cbForm, setCbForm] = useState({ max_heartbeats_per_min: 60, max_req_per_min: 60, auto_restart: false, max_consecutive_fails: 5, breaker_open: false, fail_count: 0 })
  const [cbLoading, setCbLoading] = useState(false)
  const [tasks, setTasks] = useState<TaskInfo[]>([])
  const [tasksLoading, setTasksLoading] = useState(false)
  const [newTaskForm, setNewTaskForm] = useState({ action: '', script: '', timeout: 60 })

  // Proxy 管理 dialog 状态
  const [proxyDialog, setProxyDialog] = useState(false)
  const [proxyForm, setProxyForm] = useState({ name: '', address: '', port: 8080 })
  const [editProxyDialog, setEditProxyDialog] = useState(false)
  const [editProxyTarget, setEditProxyTarget] = useState<ProxyInfo | null>(null)
  const [editProxyForm, setEditProxyForm] = useState({ name: '', address: '', port: 8080 })

  // 二次确认状态
  const [confirmDeleteHost, setConfirmDeleteHost] = useState<AgentHost | null>(null)
  const [mergingId, setMergingId] = useState<string | null>(null)
  const [confirmDeleteProxy, setConfirmDeleteProxy] = useState<ProxyInfo | null>(null)
  const [confirmRestart, setConfirmRestart] = useState<AgentHost | null>(null)
  const [confirmUninstall, setConfirmUninstall] = useState<AgentHost | null>(null)

  // IP 校验：仅支持 IPv4(192.168.1.1)、范围(192.168.1.1-10)、CIDR(192.168.1.0/24)，不支持主机名
  const validateIPs = (ips: string) => {
    if (!ips.trim()) return false
    const ipList = ips.split(',').map(ip => ip.trim()).filter(Boolean)
    if (ipList.length === 0) return false
    const validOctet = (n: string) => { const v = parseInt(n, 10); return v >= 0 && v <= 255 }
    const ipv4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/
    const range = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})-(\d{1,3})$/
    const cidr = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/
    return ipList.every(ip => {
      if (ipv4.test(ip)) {
        return ip.split('.').every(validOctet)
      }
      if (range.test(ip)) {
        const m = ip.match(range)!
        const start = parseInt(m[4], 10); const end = parseInt(m[5], 10)
        return [m[1], m[2], m[3]].every(validOctet) && validOctet(m[4]) && end >= start && end <= 255
      }
      if (cidr.test(ip)) {
        const m = ip.match(cidr)!
        const prefix = parseInt(m[5], 10)
        return [m[1], m[2], m[3], m[4]].every(validOctet) && prefix >= 0 && prefix <= 32
      }
      return false
    })
  }

  useEffect(() => {
    fetch('')
    fetchKeys('ssh_private')
    fetchProxies()
    fetchProviders()
    listCredentials()
  }, [])

  // 打开主机详情时加载编辑表单、熔断器、任务列表
  useEffect(() => {
    if (!detailHost || !detailDialog) return
    setDetailTab('detail')
    setEditForm({
      name: detailHost.name,
      address: detailHost.address,
      port: detailHost.port,
      ssh_user: detailHost.ssh_user || '',
      labels: labelsToString(detailHost.labels)
    })
    setCbLoading(true)
    getCircuitBreaker(detailHost.id).then(cb => {
      setCbForm({
        max_heartbeats_per_min: cb?.max_heartbeats_per_min ?? 60,
        max_req_per_min: cb?.max_req_per_min ?? 60,
        auto_restart: cb?.auto_restart ?? false,
        max_consecutive_fails: cb?.max_consecutive_fails ?? 5,
        breaker_open: cb?.breaker_open ?? false,
        fail_count: cb?.fail_count ?? 0
      })
    }).catch(() => {}).finally(() => setCbLoading(false))
    setTasksLoading(true)
    fetchTasks(detailHost.id).then(t => setTasks(t || [])).catch(() => setTasks([])).finally(() => setTasksLoading(false))
  }, [detailHost, detailDialog])

  const refresh = () => {
    fetch('')
    fetchKeys('ssh_private')
    fetchProxies()
  }

  const openDetailHost = (a: AgentHost) => {
    setDetailHost(a)
    setSandboxChecked(isHostSandbox(a))
    setDetailDialog(true)
  }

  const toggleSandbox = async (enabled: boolean) => {
    if (!detailHost) return
    try {
      await apiFetch(`/agents/${detailHost.id}/sandbox`, { method: 'PUT', body: JSON.stringify({ enabled }) })
      setSandboxChecked(enabled)
      const labels = { ...(detailHost.labels || {}) }
      if (enabled) labels.sandbox = 'true'; else delete labels.sandbox
      setDetailHost({ ...detailHost, labels })
      toast.success(enabled ? '已标记为沙箱环境' : '已取消沙箱环境标记')
      refresh()
    } catch (e: any) { toast.error('操作失败: ' + e.message) }
  }

  const handleAutoInstall = async () => {
    const addrList = autoForm.addresses.split(',').map(s => s.trim()).filter(Boolean)
    if (addrList.length === 0) return
    let password = '', sshKey = '', useKey = false
    if (autoForm.credentialType === 'password') { password = autoForm.password }
    else { useKey = true; if (autoForm.credentialId) { const key = keys.find(k => k.id === autoForm.credentialId); if (key) sshKey = key.content } else { sshKey = autoForm.keyContent } }
    const result = await batchInstall({ addresses: addrList, username: autoForm.username, password, ssh_key: sshKey, port: autoForm.port, use_key: useKey, proxy_id: autoForm.proxyId || undefined })
    setAutoResult(result); setAutoDialog(true); refresh()
  }

  const handleTestSSH = async () => {
    let password = '', sshKey = '', useKey = false
    if (sshForm.credentialType === 'password') { password = sshForm.password }
    else { useKey = true; if (sshForm.credentialId) { const key = keys.find(k => k.id === sshForm.credentialId); if (key) sshKey = key.content } else { sshKey = sshForm.keyContent } }
    // 对批量地址取第一个进行连接测试(后端 test-ssh 是单地址)
    const firstAddr = sshForm.address.split(',').map(s => s.trim()).filter(Boolean)[0] || ''
    const result = await testSSH({ address: firstAddr, port: sshForm.port, username: sshForm.username, password, ssh_key: sshKey, use_key: useKey })
    setSSHResult(result)
    setSSHTestedAddress(firstAddr)
  }

  const handleRegisterSSH = async () => {
    let password = '', sshKey = '', useKey = false
    if (sshForm.credentialType === 'password') { password = sshForm.password }
    else { useKey = true; if (sshForm.credentialId) { const key = keys.find(k => k.id === sshForm.credentialId); if (key) sshKey = key.content } else { sshKey = sshForm.keyContent } }
    // 批量注册:支持逗号分隔多个地址
    const addrList = sshForm.address.split(',').map(s => s.trim()).filter(Boolean)
    for (const addr of addrList) {
      await register({
        name: sshForm.name || addr,
        type: 'agentless',
        address: addr,
        port: sshForm.port,
        auth_type: useKey ? 'ssh_key' : 'ssh',
        ssh_user: sshForm.username,
        ssh_password: password,
        ssh_key: sshKey,
        install_method: 'manual',
        proxy_id: sshForm.proxyId || undefined,
      })
    }
    setSSHForm({ name: '', address: '', port: 22, username: 'root', password: '', credentialType: 'password', credentialId: '', keyName: '', keyContent: '', proxyId: '' })
    setSSHResult(null); setSSHTestedAddress(''); refresh()
  }

  const handleSaveEdit = async () => {
    if (!detailHost) return
    const nameErr = validateHostName(editForm.name, '主机名称')
    if (nameErr) { toast.error(nameErr); return }
    const ok = await update(detailHost.id, {
      name: editForm.name,
      address: editForm.address,
      port: editForm.port,
      ssh_user: editForm.ssh_user,
      labels: stringToLabels(editForm.labels)
    })
    if (ok) {
      setDetailHost({ ...detailHost, name: editForm.name, address: editForm.address, port: editForm.port, ssh_user: editForm.ssh_user, labels: stringToLabels(editForm.labels) })
    }
  }

  const handleSaveCB = async () => {
    if (!detailHost) return
    await setCircuitBreaker(detailHost.id, {
      max_heartbeats_per_min: cbForm.max_heartbeats_per_min,
      max_req_per_min: cbForm.max_req_per_min,
      auto_restart: cbForm.auto_restart,
      max_consecutive_fails: cbForm.max_consecutive_fails
    })
    const cb = await getCircuitBreaker(detailHost.id).catch(() => null)
    if (cb) setCbForm(f => ({ ...f, breaker_open: cb.breaker_open ?? f.breaker_open, fail_count: cb.fail_count ?? f.fail_count }))
  }

  const handleCreateTask = async () => {
    if (!detailHost || !newTaskForm.action.trim()) return
    const ok = await createTask(detailHost.id, {
      action: newTaskForm.action,
      script: newTaskForm.script || undefined,
      timeout: newTaskForm.timeout || undefined
    })
    if (ok) {
      setNewTaskForm({ action: '', script: '', timeout: 60 })
      setTasksLoading(true)
      fetchTasks(detailHost.id).then(t => setTasks(t || [])).catch(() => setTasks([])).finally(() => setTasksLoading(false))
    }
  }

  const handleRestart = async () => {
    if (!confirmRestart) return
    const ok = await restartAgent(confirmRestart.id)
    if (ok) setConfirmRestart(null)
  }

  const handleDeleteHost = async () => {
    if (!confirmDeleteHost) return
    const ok = await remove(confirmDeleteHost.id)
    if (ok) { setConfirmDeleteHost(null); refresh() }
  }

  // 合并疑似重复记录:将 a(重复/旧记录)合并进其 duplicate_of 指向的规范记录。
  const handleMergeAgent = async (a: AgentHost) => {
    if (!a.duplicate_of) return
    if (!confirm(`确认将主机「${a.name}」合并到规范记录「${a.duplicate_of}」？\n合并后本条记录(含沙箱等标签)将并入规范记录并删除。`)) return
    setMergingId(a.id)
    try {
      await apiFetch(`/agents/${a.id}/merge`, { method: 'POST', body: JSON.stringify({ target_id: a.duplicate_of }) })
      toast.success('已合并重复记录')
      refresh()
    } catch (e: any) {
      toast.error('合并失败: ' + (e?.message || e))
    } finally {
      setMergingId(null)
    }
  }

  const handleRegisterProxy = async () => {
    const nameErr = validateHostName(proxyForm.name, 'Proxy 名称')
    if (nameErr) { toast.error(nameErr); return }
    const ok = await registerProxy({ name: proxyForm.name, address: proxyForm.address, port: proxyForm.port })
    if (ok) {
      setProxyForm({ name: '', address: '', port: 8080 })
      setProxyDialog(false)
    }
  }

  const openEditProxy = (p: ProxyInfo) => {
    setEditProxyTarget(p)
    setEditProxyForm({ name: p.name, address: p.address, port: p.port })
    setEditProxyDialog(true)
  }

  const handleSaveEditProxy = async () => {
    if (!editProxyTarget) return
    const nameErr = validateHostName(editProxyForm.name, 'Proxy 名称')
    if (nameErr) { toast.error(nameErr); return }
    const ok = await updateProxy(editProxyTarget.id, {
      name: editProxyForm.name,
      address: editProxyForm.address,
      port: editProxyForm.port
    })
    if (ok) { setEditProxyDialog(false); setEditProxyTarget(null) }
  }

  const handleDeleteProxy = async () => {
    if (!confirmDeleteProxy) return
    const ok = await deleteProxy(confirmDeleteProxy.id)
    if (ok) setConfirmDeleteProxy(null)
  }

  const handleGetScript = async (arch?: string) => {
    try {
      const script = await getInstallScript(scriptOS + (arch ? `&arch=${arch}` : ''))
      setScriptContent(script); setScriptDialog(true)
    } catch {}
  }
  const copyToClipboard = (text: string) => { navigator.clipboard.writeText(text) }

  // 解析云商 Extra（JSON，可选）
  const parseCloudExtra = (): Record<string, string> | undefined => {
    if (!cloudExtra.trim()) return undefined
    try {
      return JSON.parse(cloudExtra)
    } catch {
      toast.error('Extra 不是合法 JSON')
      return undefined
    }
  }

  const handleCloudSaveCredential = async () => {
    if (!cloudProvider.trim()) { toast.error('请选择云服务商'); return }
    const extra = parseCloudExtra()
    if (cloudExtra.trim() && !extra) return
    const ok = await saveCredential({
      provider: cloudProvider,
      region: cloudRegion,
      access_key: cloudAccessKey,
      secret_key: cloudSecretKey,
      extra,
    })
    if (ok) { setCloudSecretKey(''); setCloudExtra('') }
  }

  const handleCloudDiscover = async () => {
    if (!cloudProvider.trim() || !cloudAccessKey.trim() || !cloudSecretKey.trim()) {
      toast.error('请填写云商、AccessKey 与 SecretKey')
      return
    }
    const extra = parseCloudExtra()
    if (cloudExtra.trim() && !extra) return
    setCloudDiscovering(true)
    try {
      await listResources({
        provider: cloudProvider,
        region: cloudRegion,
        types: cloudTypes,
        credential: { access_key: cloudAccessKey, secret_key: cloudSecretKey, region: cloudRegion, extra },
        proxy_id: cloudProxyId || undefined,
      })
    } finally {
      setCloudDiscovering(false)
    }
  }

  const handleCloudSync = async () => {
    if (resources.length === 0) return
    const extra = parseCloudExtra()
    if (cloudExtra.trim() && !extra) return
    setCloudSyncing(true)
    try {
      const res = await syncToCMDB({
        provider: cloudProvider,
        region: cloudRegion,
        types: cloudTypes,
        credential: { access_key: cloudAccessKey, secret_key: cloudSecretKey, region: cloudRegion, extra },
        resources,
      })
      if (res) {
        const parts = [`新建 ${res.created}`, `更新 ${res.updated}`, `跳过 ${res.skipped}`]
        if (res.failed > 0) parts.push(`失败 ${res.failed}`)
        toast.success(`已同步到 CMDB：${parts.join('，')}`)
        if (res.failed > 0 && res.errors?.length) console.warn('cloud sync errors', res.errors)
      }
    } finally {
      setCloudSyncing(false)
    }
  }

  // 确保 Agent 列表已加载（容器 tab 需要选择目标集群网关 Agent）。
  useEffect(() => {
    if (agents.length === 0) fetch()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const handleK8sDiscover = async () => {
    if (!k8sAgent) { toast.error('请选择目标集群网关 Agent'); return }
    setK8sDiscovering(true)
    try {
      const rs = await listResources({
        provider: 'k8s',
        agent_id: k8sAgent,
        types: k8sTypes,
      })
      setK8sResources(rs)
    } finally {
      setK8sDiscovering(false)
    }
  }

  const handleK8sSync = async () => {
    if (k8sResources.length === 0) { toast.error('请先发现 K8s 资源'); return }
    if (!k8sAgent) { toast.error('请选择目标集群网关 Agent'); return }
    setK8sSyncing(true)
    try {
      const res = await syncToCMDB({
        provider: 'k8s',
        agent_id: k8sAgent,
        types: k8sTypes,
        resources: k8sResources,
      })
      if (res) {
        const parts = [`新建 ${res.created}`, `更新 ${res.updated}`, `跳过 ${res.skipped}`]
        if (res.failed > 0) parts.push(`失败 ${res.failed}`)
        toast.success(`已同步到 CMDB：${parts.join('，')}`)
        if (res.failed > 0 && res.errors?.length) console.warn('k8s sync errors', res.errors)
      }
    } finally {
      setK8sSyncing(false)
    }
  }

  const onlineCount = agents.filter(a => a.status === 'online').length
  const offlineCount = agents.filter(a => a.status === 'offline').length
  const agentCount = agents.filter(a => a.type === 'agent').length
  const agentlessCount = agents.filter(a => a.type === 'agentless').length

  // 主机列表列定义
  const agentColumns: Column<AgentHost>[] = [
    { key: 'name', label: '名称', sortable: true, filterable: true },
    { key: 'status', label: '状态', sortable: true, filterable: true },
    { key: 'type', label: '类型', sortable: true, filterable: true },
    { key: 'address', label: '地址', sortable: true, filterable: true },
    { key: 'os_type', label: '操作系统', sortable: true },
    { key: 'cpu', label: 'CPU' },
    { key: 'memory', label: '内存' },
    { key: 'last_heartbeat', label: '最后心跳', sortable: true },
  ]

  // 主机列表筛选
  const agentFilters: FilterOption[] = [
    { key: 'status', label: '状态', type: 'select', options: [{ label: '在线', value: 'online' }, { label: '离线', value: 'offline' }] },
    { key: 'type', label: '类型', type: 'select', options: [{ label: 'Agent', value: 'agent' }, { label: 'Agentless', value: 'agentless' }] },
  ]

  // 批量操作
  const agentBulkActions: BulkAction[] = [
    {
      key: 'delete',
      label: '批量删除',
      icon: <Trash2 className="h-3 w-3 mr-1" />,
      variant: 'destructive',
      confirmMessage: '确认删除选中的主机？此操作不可恢复。',
      onClick: async (items) => {
        for (const item of items) { await remove(item.id) }
        refresh()
      }
    },
  ]

  const renderAgentListItem = (a: AgentHost) => (
    <>
      <div className="flex items-center gap-2 min-w-0 flex-1">
        {a.type === 'agent' ? <Terminal className="h-4 w-4 text-emerald-400 shrink-0" /> : <Monitor className="h-4 w-4 text-blue-400 shrink-0" />}
        <span className="text-sm font-medium truncate min-w-0">{a.name}</span>
      </div>
      <div className="w-16 shrink-0"><Badge variant={statusVariant(a.status)} className="text-xs">{statusLabel(a.status)}</Badge></div>
      <div className="w-20 shrink-0"><Badge variant="outline" className="text-xs">{a.type === 'agent' ? 'Agent' : 'Agentless'}</Badge></div>
      <div className="flex-1 min-w-0 truncate text-xs text-muted-foreground">{a.address}{a.port && a.port !== 22 ? `:${a.port}` : ''}</div>
      <div className="w-20 shrink-0 hidden lg:block text-xs text-muted-foreground truncate">{a.os_type || '-'}</div>
      <div className="w-16 shrink-0 hidden xl:block text-xs text-muted-foreground truncate">{a.cpu || '-'}</div>
      <div className="w-16 shrink-0 hidden xl:block text-xs text-muted-foreground truncate">{a.memory || '-'}</div>
      <div className="w-24 shrink-0 text-xs text-muted-foreground/50 hidden md:block"><Clock className="h-3 w-3 inline mr-0.5" />{a.last_heartbeat ? formatTime(a.last_heartbeat) : '无心跳'}</div>
      <div className="w-24 shrink-0 flex justify-end gap-1">
        <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => openDetailHost(a)}><Eye className="h-3 w-3" /></Button>
        {a.duplicate_of && (
          <Button variant="ghost" size="icon" className="h-6 w-6 text-amber-500" title="合并重复记录" disabled={mergingId === a.id} onClick={() => handleMergeAgent(a)}><GitMerge className="h-3 w-3" /></Button>
        )}
        <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => setConfirmDeleteHost(a)}><Trash2 className="h-3 w-3" /></Button>
      </div>
    </>
  )

  const renderAgentGridItem = (a: AgentHost) => (
    <Card key={a.id} className="hover:border-emerald-500/20 transition-colors">
      <CardHeader className="py-3 pb-2">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 min-w-0">
            {a.type === 'agent' ? <Terminal className="h-4 w-4 text-emerald-400 shrink-0" /> : <Monitor className="h-4 w-4 text-blue-400 shrink-0" />}
            <span className="text-sm font-medium truncate">{a.name}</span>
          </div>
          <div className="flex gap-1 shrink-0">
            <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => openDetailHost(a)}><Eye className="h-3 w-3" /></Button>
            <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => setConfirmDeleteHost(a)}><Trash2 className="h-3 w-3" /></Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-2 pt-0">
        {a.duplicate_of && (
          <div className="flex items-center justify-between gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-2 py-1.5">
            <span className="text-xs text-amber-600 dark:text-amber-400">疑似重复记录（同主机名/IP），建议合并</span>
            <Button size="sm" variant="outline" className="h-6 shrink-0 text-xs" disabled={mergingId === a.id} onClick={() => handleMergeAgent(a)}>
              {mergingId === a.id ? '合并中…' : '合并'}
            </Button>
          </div>
        )}
        <div className="flex items-center gap-2">
          <Badge variant={statusVariant(a.status)} className="text-xs">{statusLabel(a.status)}</Badge>
          <Badge variant="outline" className="text-xs">{a.type === 'agent' ? 'Agent' : 'Agentless'}</Badge>
          {a.install_method && <Badge variant="outline" className="text-xs">{a.install_method === 'auto' ? '自动安装' : '手动安装'}</Badge>}
        </div>
        <p className="text-xs text-muted-foreground">{a.address}{a.port && a.port !== 22 ? `:${a.port}` : ''}</p>
        <div className="grid grid-cols-2 gap-1 text-xs">
          {a.os_type && <span className="text-muted-foreground">OS: {a.os_type}{a.os_version ? ` ${a.os_version}` : ''}</span>}
          {a.version && <span className="text-muted-foreground">Agent: v{a.version}</span>}
          {a.cpu && <span className="text-muted-foreground"><Cpu className="h-3 w-3 inline mr-0.5" />{a.cpu}</span>}
          {a.memory && <span className="text-muted-foreground"><MemoryStick className="h-3 w-3 inline mr-0.5" />{a.memory}</span>}
          {a.disk && <span className="text-muted-foreground"><HardDrive className="h-3 w-3 inline mr-0.5" />{a.disk}</span>}
        </div>
        <div className="flex items-center justify-between text-xs text-muted-foreground pt-1 border-t">
          <span className="flex items-center gap-1"><Clock className="h-3 w-3" />{a.last_heartbeat ? formatTime(a.last_heartbeat) : '无心跳'}</span>
          {a.auth_type && <span>{a.auth_type.toUpperCase()}</span>}
        </div>
      </CardContent>
    </Card>
  )

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">资源纳管</h1>
          <p className="text-sm text-muted-foreground">主机、云资源、容器统一纳管</p>
        </div>
        <a href="/guide" className="inline-flex items-center justify-center rounded-md text-sm font-medium border border-input bg-background hover:bg-accent hover:text-accent-foreground h-9 px-4 py-2">
          <BookOpen className="h-4 w-4 mr-1" />接入指引
        </a>
      </div>

      <Tabs value={mainTab} onValueChange={setMainTab}>
        <TabsList>
          <TabsTrigger value="host"><Server className="h-3 w-3 mr-1" />主机资源纳管</TabsTrigger>
          <TabsTrigger value="cloud"><Cloud className="h-3 w-3 mr-1" />云资源纳管</TabsTrigger>
          <TabsTrigger value="container"><Container className="h-3 w-3 mr-1" />容器资源纳管</TabsTrigger>
        </TabsList>

        <TabsContent value="host" className="mt-4 space-y-4">
          <div className="grid grid-cols-5 gap-3">
            <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground">主机总数</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{agents.length}</span></CardContent></Card>
            <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Wifi className="h-3 w-3 inline mr-1" />在线</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{onlineCount}</span></CardContent></Card>
            <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><WifiOff className="h-3 w-3 inline mr-1" />离线</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-destructive">{offlineCount}</span></CardContent></Card>
            <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Terminal className="h-3 w-3 inline mr-1" />Agent</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{agentCount}</span></CardContent></Card>
            <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Monitor className="h-3 w-3 inline mr-1" />Agentless</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{agentlessCount}</span></CardContent></Card>
          </div>

          <Tabs value={hostTab} onValueChange={setHostTab}>
            <TabsList>
              <TabsTrigger value="list">主机列表</TabsTrigger>
              <TabsTrigger value="proxies">Proxy 管理 ({proxies.length})</TabsTrigger>
            </TabsList>

            <TabsContent value="list" className="mt-4">
              <DataList
                data={agents}
                columns={agentColumns}
                viewMode={agentViewMode}
                filters={agentFilters}
                bulkActions={agentBulkActions}
                listView={renderAgentListItem}
                gridView={renderAgentGridItem}
                emptyMessage="暂无主机数据。"
                searchPlaceholder="搜索主机名/IP..."
                onRefresh={refresh}
                customHeader={
                  <Button size="sm" onClick={() => setHostTab('register')}><Plus className="h-4 w-4 mr-1" />注册主机</Button>
                }
              />
            </TabsContent>

            <TabsContent value="register" className="mt-4">
              <Card>
                <CardHeader className="py-3">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm">注册新主机</CardTitle>
                    <Button variant="outline" size="sm" onClick={() => setHostTab('list')}><Server className="h-3 w-3 mr-1" />返回主机列表</Button>
                  </div>
                </CardHeader>
                <CardContent>
                  <Tabs defaultValue={regMethod}>
                    <TabsList>
                      <TabsTrigger value="auto" onClick={() => setRegMethod('auto')}><Download className="h-3 w-3 mr-1" />自动安装</TabsTrigger>
                      <TabsTrigger value="manual" onClick={() => setRegMethod('manual')}><Terminal className="h-3 w-3 mr-1" />手动安装</TabsTrigger>
                      <TabsTrigger value="ssh" onClick={() => setRegMethod('ssh')}><Monitor className="h-3 w-3 mr-1" />SSH 接入</TabsTrigger>
                    </TabsList>
                    <TabsContent value="auto" className="mt-4 space-y-4">
                      <div className="bg-muted/30 rounded-md p-4 text-sm"><p className="font-medium mb-2">自动安装说明</p><p className="text-muted-foreground">输入目标主机的 IP 地址（逗号分隔）、SSH 用户名和凭证，系统将自动 SSH 连接到目标主机并安装 Agent。支持 IP 范围(192.168.1.1-10)和 CIDR(192.168.1.0/24)。</p></div>
                      <div className="space-y-4">
                        <div><label className="text-sm font-medium">目标 IP 地址（逗号分隔）*</label><Input value={autoForm.addresses} onChange={e => setAutoForm(f => ({ ...f, addresses: e.target.value }))} placeholder="192.168.1.10, 192.168.1.1-10 或 192.168.1.0/24" className={!validateIPs(autoForm.addresses) && autoForm.addresses ? 'border-red-500' : ''} /></div>
                        <div><label className="text-sm font-medium">用户名 *</label><Input value={autoForm.username} onChange={e => setAutoForm(f => ({ ...f, username: e.target.value }))} placeholder="root" /></div>
                        <div>
                          <label className="text-sm font-medium">凭证 *</label>
                          <div className="space-y-2">
                            <select className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={autoForm.credentialType} onChange={e => setAutoForm(f => ({ ...f, credentialType: e.target.value, credentialId: '' }))}>
                              <option value="password">密码</option><option value="ssh_key">SSH密钥</option>
                            </select>
                            {autoForm.credentialType === 'password' ? (
                              <Input type="password" value={autoForm.password} onChange={e => setAutoForm(f => ({ ...f, password: e.target.value }))} placeholder="••••" />
                            ) : (
                              <div className="space-y-2">
                                <select className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={autoForm.credentialId} onChange={e => setAutoForm(f => ({ ...f, credentialId: e.target.value }))}>
                                  <option value="">选择已有密钥</option>{keys.filter(k => k.type === 'ssh_private').map(k => (<option key={k.id} value={k.id}>{k.name}</option>))}
                                </select>
                                {!autoForm.credentialId && (
                                  <div className="space-y-2">
                                    <Input value={autoForm.keyName} onChange={e => setAutoForm(f => ({ ...f, keyName: e.target.value }))} placeholder="密钥名称（可选）" />
                                    <textarea className="flex h-24 w-full rounded-md border border-input bg-background px-3 py-2 text-xs font-mono" value={autoForm.keyContent} onChange={e => setAutoForm(f => ({ ...f, keyContent: e.target.value }))} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
                                    <Button variant="outline" size="sm" onClick={() => { if (!autoForm.keyContent.trim()) return; createKey({ name: autoForm.keyName || `key-${Date.now()}`, type: 'ssh_private', content: autoForm.keyContent }); setAutoForm(f => ({ ...f, keyName: '', keyContent: '' })); fetchKeys('ssh_private') }}>保存到凭证管理</Button>
                                  </div>
                                )}
                              </div>
                            )}
                          </div>
                        </div>
                        <div><label className="text-sm font-medium">SSH 端口</label><Input type="number" value={autoForm.port} onChange={e => setAutoForm(f => ({ ...f, port: parseInt(e.target.value) || 22 }))} /></div>
                        <div className="space-y-2">
                          <label className="flex items-center gap-2 cursor-pointer select-none">
                            <input type="checkbox" checked={!!autoForm.proxyId} onChange={e => setAutoForm(f => ({ ...f, proxyId: e.target.checked ? (proxies[0]?.id || '') : '' }))} className="h-4 w-4 rounded border-input" disabled={proxies.length === 0} />
                            <span className="text-sm">通过 Proxy 连接 Server{proxies.length === 0 && '（需先注册 Proxy）'}</span>
                          </label>
                          {autoForm.proxyId && proxies.length > 0 && (
                            <select className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={autoForm.proxyId} onChange={e => setAutoForm(f => ({ ...f, proxyId: e.target.value }))}>
                              {proxies.map(p => <option key={p.id} value={p.id}>{p.name} ({p.address}:{p.port})</option>)}
                            </select>
                          )}
                        </div>
                        <Button onClick={handleAutoInstall} disabled={!validateIPs(autoForm.addresses) || !autoForm.addresses.trim() || (!autoForm.password && !autoForm.credentialId)}>
                          <Download className="h-4 w-4 mr-1" />批量安装 Agent
                        </Button>
                      </div>
                    </TabsContent>
                    <TabsContent value="manual" className="mt-4 space-y-4">
                      <div className="bg-muted/30 rounded-md p-4 text-sm"><p className="font-medium mb-2">手动安装说明</p><p className="text-muted-foreground">在目标主机上执行对应的一键安装命令，命令会从 Server 下载安装脚本并自动运行，Agent 安装后将自动向 Server 注册并上报心跳。</p></div>
                      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                        <Card>
                          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Download className="h-4 w-4" />Linux x86 (amd64)</CardTitle></CardHeader>
                          <CardContent className="space-y-3">
                            <div className="bg-black rounded-md p-3 text-green-400 text-xs font-mono whitespace-pre-wrap break-all">curl -sSL 'http://YOUR_SERVER/api/v1/agents/install-script?os=linux&arch=amd64' | sudo bash</div>
                            <p className="text-xs text-muted-foreground">将 YOUR_SERVER 替换为平台部署地址。</p>
                            <div className="flex gap-2">
                              <Button variant="outline" size="sm" onClick={() => { setScriptOS('linux'); handleGetScript('amd64') }}><Eye className="h-3 w-3 mr-1" />查看脚本</Button>
                              <Button variant="outline" size="sm" onClick={() => copyToClipboard(`curl -sSL 'http://${window.location.host}/api/v1/agents/install-script?os=linux&arch=amd64' | sudo bash`)}><Copy className="h-3 w-3 mr-1" />复制命令</Button>
                            </div>
                          </CardContent>
                        </Card>
                        <Card>
                          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Download className="h-4 w-4" />Linux ARM (arm64)</CardTitle></CardHeader>
                          <CardContent className="space-y-3">
                            <div className="bg-black rounded-md p-3 text-green-400 text-xs font-mono whitespace-pre-wrap break-all">curl -sSL 'http://YOUR_SERVER/api/v1/agents/install-script?os=linux&arch=arm64' | sudo bash</div>
                            <p className="text-xs text-muted-foreground">适用于树莓派、ARM 服务器等 ARM 架构设备。</p>
                            <div className="flex gap-2">
                              <Button variant="outline" size="sm" onClick={() => { setScriptOS('linux'); handleGetScript('arm64') }}><Eye className="h-3 w-3 mr-1" />查看脚本</Button>
                              <Button variant="outline" size="sm" onClick={() => copyToClipboard(`curl -sSL 'http://${window.location.host}/api/v1/agents/install-script?os=linux&arch=arm64' | sudo bash`)}><Copy className="h-3 w-3 mr-1" />复制命令</Button>
                            </div>
                          </CardContent>
                        </Card>
                        <Card>
                          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Download className="h-4 w-4" />Windows</CardTitle></CardHeader>
                          <CardContent className="space-y-3">
                            <div className="bg-black rounded-md p-3 text-green-400 text-xs font-mono whitespace-pre-wrap break-all">Invoke-Expression (Invoke-WebRequest -Uri "http://YOUR_SERVER/api/v1/agents/install-script?os=windows").Content</div>
                            <p className="text-xs text-muted-foreground">以管理员身份运行 PowerShell，执行上述命令。</p>
                            <div className="flex gap-2">
                              <Button variant="outline" size="sm" onClick={() => { setScriptOS('windows'); handleGetScript() }}><Eye className="h-3 w-3 mr-1" />查看脚本</Button>
                              <Button variant="outline" size="sm" onClick={() => copyToClipboard(`Invoke-Expression (Invoke-WebRequest -Uri "http://${window.location.host}/api/v1/agents/install-script?os=windows").Content`)}><Copy className="h-3 w-3 mr-1" />复制命令</Button>
                            </div>
                          </CardContent>
                        </Card>
                        <Card>
                          <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-2"><Container className="h-4 w-4" />容器 (Docker)</CardTitle></CardHeader>
                          <CardContent className="space-y-3">
                            <div className="bg-black rounded-md p-3 text-green-400 text-xs font-mono whitespace-pre-wrap break-all">curl -sSL 'http://YOUR_SERVER/api/v1/agents/install-script?os=container' | sudo bash</div>
                            <p className="text-xs text-muted-foreground">目标主机需已安装 Docker，将自动拉取并运行 Agent 容器。</p>
                            <div className="flex gap-2">
                              <Button variant="outline" size="sm" onClick={() => { setScriptOS('container'); handleGetScript() }}><Eye className="h-3 w-3 mr-1" />查看脚本</Button>
                              <Button variant="outline" size="sm" onClick={() => copyToClipboard(`curl -sSL 'http://${window.location.host}/api/v1/agents/install-script?os=container' | sudo bash`)}><Copy className="h-3 w-3 mr-1" />复制命令</Button>
                            </div>
                          </CardContent>
                        </Card>
                      </div>
                    </TabsContent>
                    <TabsContent value="ssh" className="mt-4 space-y-4">
                      <div className="bg-muted/30 rounded-md p-4 text-sm"><p className="font-medium mb-2">SSH Agentless 接入</p><p className="text-muted-foreground">通过 SSH 协议无代理接入主机。支持批量 IP（逗号分隔）或 IP 范围（192.168.1.1-10）。必须先通过连接测试，才能保存到主机列表。</p></div>
                      <div className="space-y-4">
                        <div><label className="text-sm font-medium">主机名</label><Input value={sshForm.name} onChange={e => setSSHForm(f => ({ ...f, name: sanitizeHostName(e.target.value) }))} placeholder="可选（批量注册时以 IP 命名）" /></div>
                        <div><label className="text-sm font-medium">IP 地址 *（支持批量：192.168.1.10,192.168.1.11 或 192.168.1.1-10）</label><Input value={sshForm.address} onChange={e => { setSSHForm(f => ({ ...f, address: e.target.value })); setSSHResult(null); setSSHTestedAddress('') }} placeholder="192.168.1.100,192.168.1.101 或 192.168.1.1-10" className={!validateIPs(sshForm.address) && sshForm.address ? 'border-red-500' : ''} /></div>
                        <div><label className="text-sm font-medium">SSH 端口</label><Input type="number" value={sshForm.port} onChange={e => setSSHForm(f => ({ ...f, port: parseInt(e.target.value) || 22 }))} /></div>
                        <div><label className="text-sm font-medium">用户名 *</label><Input value={sshForm.username} onChange={e => setSSHForm(f => ({ ...f, username: e.target.value }))} placeholder="root" /></div>
                        <div>
                          <label className="text-sm font-medium">凭证 *</label>
                          <div className="space-y-2">
                            <select className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={sshForm.credentialType} onChange={e => setSSHForm(f => ({ ...f, credentialType: e.target.value, credentialId: '' }))}>
                              <option value="password">密码</option><option value="ssh_key">SSH密钥</option>
                            </select>
                            {sshForm.credentialType === 'password' ? (
                              <Input type="password" value={sshForm.password} onChange={e => setSSHForm(f => ({ ...f, password: e.target.value }))} placeholder="••••" />
                            ) : (
                              <div className="space-y-2">
                                <select className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={sshForm.credentialId} onChange={e => setSSHForm(f => ({ ...f, credentialId: e.target.value }))}>
                                  <option value="">选择已有密钥</option>{keys.filter(k => k.type === 'ssh_private').map(k => (<option key={k.id} value={k.id}>{k.name}</option>))}
                                </select>
                                {!sshForm.credentialId && (
                                  <div className="space-y-2">
                                    <Input value={sshForm.keyName} onChange={e => setSSHForm(f => ({ ...f, keyName: e.target.value }))} placeholder="密钥名称（可选）" />
                                    <textarea className="flex h-24 w-full rounded-md border border-input bg-background px-3 py-2 text-xs font-mono" value={sshForm.keyContent} onChange={e => setSSHForm(f => ({ ...f, keyContent: e.target.value }))} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" />
                                    <Button variant="outline" size="sm" onClick={() => { if (!sshForm.keyContent.trim()) return; createKey({ name: sshForm.keyName || `key-${Date.now()}`, type: 'ssh_private', content: sshForm.keyContent }); setSSHForm(f => ({ ...f, keyName: '', keyContent: '' })); fetchKeys('ssh_private') }}>保存到凭证管理</Button>
                                  </div>
                                )}
                              </div>
                            )}
                          </div>
                        </div>
                        <div className="space-y-2">
                          <label className="flex items-center gap-2 cursor-pointer select-none">
                            <input type="checkbox" checked={!!sshForm.proxyId} onChange={e => setSSHForm(f => ({ ...f, proxyId: e.target.checked ? (proxies[0]?.id || '') : '' }))} className="h-4 w-4 rounded border-input" disabled={proxies.length === 0} />
                            <span className="text-sm">通过 Proxy 连接 Server{proxies.length === 0 && '（需先注册 Proxy）'}</span>
                          </label>
                          {sshForm.proxyId && proxies.length > 0 && (
                            <select className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={sshForm.proxyId} onChange={e => setSSHForm(f => ({ ...f, proxyId: e.target.value }))}>
                              {proxies.map(p => <option key={p.id} value={p.id}>{p.name} ({p.address}:{p.port})</option>)}
                            </select>
                          )}
                        </div>
                        <div className="flex gap-2">
                          <Button variant="outline" onClick={handleTestSSH} disabled={!validateIPs(sshForm.address) || (!sshForm.password && !sshForm.credentialId)}><Wifi className="h-4 w-4 mr-1" />连接测试</Button>
                          <Button onClick={handleRegisterSSH} disabled={!sshResult?.reachable || !sshResult || sshTestedAddress !== sshForm.address.split(',')[0].trim()}><CheckCircle className="h-4 w-4 mr-1" />保存到主机列表</Button>
                        </div>
                        {sshResult && (
                          <div className={`flex items-center gap-2 p-3 rounded-md text-sm ${sshResult.reachable ? 'bg-emerald-500/10 text-emerald-500' : 'bg-destructive/10 text-destructive'}`}>
                            {sshResult.reachable ? <><CheckCircle className="h-4 w-4" />连接测试通过 - {sshResult.address} 可达</> : <><XCircle className="h-4 w-4" />连接测试失败 - {sshResult.address} 不可达：{sshResult.detail}</>}
                          </div>
                        )}
                        {!sshResult && sshForm.address && (
                          <p className="text-xs text-amber-500 flex items-center gap-1"><AlertTriangle className="h-3 w-3" />请先通过连接测试，才能保存到主机列表</p>
                        )}
                      </div>
                    </TabsContent>
                  </Tabs>
                </CardContent>
              </Card>
            </TabsContent>

            <TabsContent value="proxies" className="mt-4 space-y-4">
              {/* Proxy 安装命令 */}
              <Card className="mb-4">
                <CardHeader className="py-3">
                  <CardTitle className="text-sm flex items-center gap-2">
                    <Terminal className="h-4 w-4" /> 一键安装 Proxy
                  </CardTitle>
                </CardHeader>
                <CardContent className="space-y-3">
                  <p className="text-xs text-muted-foreground">在目标主机上执行以下命令,自动下载并安装 Proxy,启动后自动连接 Core 并暴露端口给 Agent</p>
                  {[
                    { label: 'Linux x86', cmd: `curl -fsSL ${serverBase}/api/v1/agents/install-script?os=proxy&arch=amd64 | bash` },
                    { label: 'Linux ARM', cmd: `curl -fsSL ${serverBase}/api/v1/agents/install-script?os=proxy&arch=arm64 | bash` },
                    { label: 'Windows', cmd: `Invoke-WebRequest -Uri "${serverBase}/api/v1/agents/install-script?os=proxy" -OutFile install-proxy.ps1; powershell -ExecutionPolicy Bypass -File install-proxy.ps1` },
                    { label: 'Docker', cmd: `docker run -d --name opsorigin-proxy -p 9090:9090 opsorigin/proxy:latest --core-addr=${serverBase}` },
                  ].map(item => (
                    <div key={item.label} className="space-y-1">
                      <div className="flex items-center justify-between">
                        <span className="text-xs font-medium">{item.label}</span>
                        <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={() => { navigator.clipboard.writeText(item.cmd); toast.success('已复制') }}>
                          <Copy className="h-3 w-3 mr-1" />复制
                        </Button>
                      </div>
                      <pre className="text-xs bg-muted/50 rounded p-2 overflow-x-auto">{item.cmd}</pre>
                    </div>
                  ))}
                </CardContent>
              </Card>
              <div className="flex items-center justify-between">
                <ViewToggle value={proxyViewMode} onChange={setProxyViewMode} />
                <Button size="sm" onClick={() => setProxyDialog(true)}><Plus className="h-4 w-4 mr-1" />注册 Proxy</Button>
              </div>
              {proxies.length === 0 ? (
                <Card><CardContent className="py-8 text-center text-muted-foreground">
                  <Server className="h-10 w-10 mx-auto mb-3 opacity-30" />
                  <p>暂无 Proxy 节点。</p>
                  <p className="text-xs mt-1">Proxy 部署后会自动注册到 Server。</p>
                </CardContent></Card>
              ) : proxyViewMode === 'list' ? (
                <div className="space-y-1">
                  {proxies.map(p => (
                    <div key={p.id} className="flex items-center justify-between p-3 rounded-lg border border-gray-100 dark:border-[rgba(255,255,255,0.04)] bg-white dark:bg-transparent">
                      <div className="flex items-center gap-3 min-w-0">
                        <Server className="h-4 w-4 text-emerald-400 shrink-0" />
                        <span className="text-sm font-medium truncate">{p.name || p.address}</span>
                        <Badge variant={statusVariant(p.status)} className="text-xs">{p.status}</Badge>
                        <span className="text-xs text-muted-foreground truncate">{p.address}:{p.port}</span>
                        <span className="text-xs text-muted-foreground hidden md:inline">管理 {p.agent_count} 个 Agent</span>
                        <span className="text-xs text-muted-foreground hidden lg:inline">v{p.version || '未知'}</span>
                        <span className="text-xs text-muted-foreground/50 hidden md:inline"><Clock className="h-3 w-3 inline mr-0.5" />{p.last_seen ? formatTime(p.last_seen) : '未知'}</span>
                      </div>
                      <div className="flex gap-1 shrink-0">
                        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => openEditProxy(p)}><Pencil className="h-3 w-3" /></Button>
                        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setConfirmDeleteProxy(p)}><Trash2 className="h-3 w-3" /></Button>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  {proxies.map(p => (
                    <Card key={p.id}>
                      <CardHeader className="py-3">
                        <div className="flex items-center justify-between">
                          <CardTitle className="text-sm"><Server className="h-4 w-4 inline mr-1 text-emerald-400" />{p.name || p.address}</CardTitle>
                          <div className="flex gap-1">
                            <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => openEditProxy(p)}><Pencil className="h-3 w-3" /></Button>
                            <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setConfirmDeleteProxy(p)}><Trash2 className="h-3 w-3" /></Button>
                          </div>
                        </div>
                      </CardHeader>
                      <CardContent>
                        <div className="grid grid-cols-2 gap-2 text-xs">
                          <div><span className="text-muted-foreground">地址:</span> {p.address}:{p.port}</div>
                          <div><span className="text-muted-foreground">状态:</span> <Badge variant={statusVariant(p.status)} className="text-xs">{p.status}</Badge></div>
                          <div><span className="text-muted-foreground">管理 Agent:</span> {p.agent_count}</div>
                          <div><span className="text-muted-foreground">版本:</span> {p.version || '未知'}</div>
                          <div className="col-span-2"><span className="text-muted-foreground">最后在线:</span> {p.last_seen ? formatTime(p.last_seen) : '未知'}</div>
                        </div>
                      </CardContent>
                    </Card>
                  ))}
                </div>
              )}
            </TabsContent>
          </Tabs>
        </TabsContent>

        <TabsContent value="cloud" className="mt-4">
          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm flex items-center gap-2">
                <Cloud className="h-4 w-4" />云资源发现与纳管
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                {/* 凭证区 */}
                <div className="space-y-3">
                  <p className="text-xs text-muted-foreground">填写云商访问凭证（临时使用，不强制持久化）。</p>
                  <div>
                    <label className="text-sm font-medium">云服务商 *</label>
                    <select className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={cloudProvider} onChange={e => setCloudProvider(e.target.value)}>
                      <option value="">选择云商</option>
                      {providers.map(p => <option key={p.name} value={p.name}>{p.name}</option>)}
                    </select>
                  </div>
                  <div>
                    <label className="text-sm font-medium">Region</label>
                    <Input value={cloudRegion} onChange={e => setCloudRegion(e.target.value)} placeholder="如 cn-hangzhou / us-east-1" />
                  </div>
                  <div>
                    <label className="text-sm font-medium">AccessKey</label>
                    <Input value={cloudAccessKey} onChange={e => setCloudAccessKey(e.target.value)} placeholder="AKIA..." />
                  </div>
                  <div>
                    <label className="text-sm font-medium">SecretKey</label>
                    <Input type="password" value={cloudSecretKey} onChange={e => setCloudSecretKey(e.target.value)} placeholder="••••" />
                  </div>
                  <div>
                    <label className="text-sm font-medium">Extra（JSON，可选，如华为云 project_id=xxx）</label>
                    <textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-xs font-mono" value={cloudExtra} onChange={e => setCloudExtra(e.target.value)} placeholder='{"project_id":"xxx"}' />
                  </div>
                  <div>
                    <label className="text-sm font-medium">代理（Proxy，可选，用于私有云/隔离网络场景）</label>
                    <select className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={cloudProxyId} onChange={e => setCloudProxyId(e.target.value)}>
                      <option value="">直连（不使用 Proxy）</option>
                      {proxies.map(p => <option key={p.id} value={p.id}>{p.name} · {p.address}</option>)}
                    </select>
                  </div>
                  <div className="flex gap-2">
                    <Button size="sm" onClick={handleCloudSaveCredential}><Save className="h-3 w-3 mr-1" />保存凭证</Button>
                    <Button size="sm" variant="outline" onClick={handleCloudDiscover} disabled={!cloudProvider || !cloudAccessKey || !cloudSecretKey || cloudDiscovering}>
                      <Play className="h-3 w-3 mr-1" />{cloudDiscovering ? '发现中...' : '发现资源'}
                    </Button>
                  </div>
                </div>

                {/* 资源类型 + 结果 */}
                <div className="space-y-3">
                  <div>
                    <label className="text-sm font-medium">资源类型（多选）</label>
                    <div className="flex gap-2 flex-wrap mt-1">
                      {CLOUD_RESOURCE_TYPES.map(t => (
                        <label key={t.value} className="flex items-center gap-1 text-sm border rounded-md px-2 py-1 cursor-pointer">
                          <input type="checkbox" checked={cloudTypes.includes(t.value)} onChange={e => {
                            if (e.target.checked) setCloudTypes(ts => Array.from(new Set([...ts, t.value])))
                            else setCloudTypes(ts => ts.filter(v => v !== t.value))
                          }} /> {t.label}
                        </label>
                      ))}
                    </div>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-sm text-muted-foreground">已发现 {resources.length} 个资源</span>
                    <Button size="sm" onClick={handleCloudSync} disabled={resources.length === 0 || cloudSyncing}>
                      <ArrowUp className="h-3 w-3 mr-1" />{cloudSyncing ? '纳管中...' : '纳管到 CMDB'}
                    </Button>
                  </div>
                  {resources.length === 0 ? (
                    <p className="text-xs text-muted-foreground">填写凭证后点击「发现资源」即可列出云上资源。</p>
                  ) : (
                    <div className="border rounded-md max-h-80 overflow-auto">
                      <table className="w-full text-xs">
                        <thead className="bg-muted/40 sticky top-0">
                          <tr>
                            <th className="text-left p-2 font-medium">云商</th>
                            <th className="text-left p-2 font-medium">类型</th>
                            <th className="text-left p-2 font-medium">名称</th>
                            <th className="text-left p-2 font-medium">状态</th>
                            <th className="text-left p-2 font-medium">地域</th>
                            <th className="text-left p-2 font-medium">公网IP</th>
                            <th className="text-left p-2 font-medium">私网IP</th>
                          </tr>
                        </thead>
                        <tbody>
                          {resources.map((r: CloudResource, i: number) => (
                            <tr key={i} className="border-t">
                              <td className="p-2">{r.provider}</td>
                              <td className="p-2"><Badge variant="outline" className="text-[10px]">{r.type}</Badge></td>
                              <td className="p-2">{r.name}</td>
                              <td className="p-2">{r.status || '-'}</td>
                              <td className="p-2">{r.region}</td>
                              <td className="p-2">{r.public_ip || '-'}</td>
                              <td className="p-2">{r.private_ip || '-'}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                </div>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="container" className="mt-4">
          <Card>
            <CardHeader className="py-3">
              <CardTitle className="text-sm flex items-center gap-2">
                <Container className="h-4 w-4" />Kubernetes 容器资源纳管（经网关 Agent 通道）
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-4">
              {/* K8s Agent 安装指引 */}
              <details className="border rounded-md">
                <summary className="px-4 py-2 text-sm font-medium cursor-pointer hover:bg-muted/30 flex items-center gap-2">
                  <Download className="h-4 w-4" />K8s Agent 一键安装指引
                </summary>
                <div className="px-4 pb-4 space-y-3 border-t pt-3">
                  <p className="text-xs text-muted-foreground">在目标 K8s 集群中执行以下命令部署网关 Agent（DaemonSet），部署完成后 Agent 将自动注册到 Core。</p>
                  <div>
                    <p className="text-xs font-medium mb-1">▸ 直连 Core（Agent→Core 网络可达）</p>
                    <div className="bg-black rounded-md p-3 text-green-400 text-xs font-mono whitespace-pre-wrap break-all">kubectl apply -f https://raw.githubusercontent.com/Major-w/OpsOrigin/dev/deploy/k8s/opsorigin-k8s-gateway.yaml</div>
                  </div>
                  <div>
                    <p className="text-xs font-medium mb-1">▸ 经 Proxy 连接（Agent 在隔离网络，通过 Proxy 中转）</p>
                    <div className="bg-black rounded-md p-3 text-green-400 text-xs font-mono whitespace-pre-wrap break-all"># 1. 先部署 Proxy（参照主机纳管 → Proxy 管理）
# 2. 部署 K8s Agent 并指定 Proxy 地址：
kubectl apply -f https://raw.githubusercontent.com/Major-w/OpsOrigin/dev/deploy/k8s/opsorigin-k8s-gateway.yaml
# 3. 编辑 Deployment 设置 Proxy 环境变量：
kubectl -n opsorigin set env deployment/opsorigin-k8s-gateway OPSORIGIN_PROXY_ADDR=&lt;proxy_ip:port&gt;</div>
                  </div>
                  <p className="text-xs text-muted-foreground">部署后 Agent 类型自动标记为 <code className="text-[11px]">k8s</code>，与主机 Agent 独立管理。版管理在 Agent 详情对话框中查看。</p>
                </div>
              </details>

              {agents.filter(a => a.type === 'k8s').length === 0 ? (
                <p className="text-xs text-muted-foreground">未检测到 K8s Agent。请先按上方指引部署 K8s 网关 Agent。</p>
              ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                  {/* 网关 Agent 选择 + 资源类型 */}
                  <div className="space-y-3">
                    <p className="text-xs text-muted-foreground">选择已部署的 K8s Agent 作为集群网关，经其隧道下发 <code className="text-[11px]">kubectl</code> 只读命令发现资源。</p>
                    <div>
                      <label className="text-sm font-medium">K8s 网关 Agent *</label>
                      <select className="h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={k8sAgent} onChange={e => setK8sAgent(e.target.value)}>
                        <option value="">选择 K8s Agent</option>
                        {agents.filter(a => a.type === 'k8s').map(a => (
                          <option key={a.id} value={a.id}>{a.name || a.id} · {a.address}{a.status === 'online' ? '（在线）' : '（离线）'}</option>
                        ))}
                      </select>
                    </div>
                    <div>
                      <label className="text-sm font-medium">资源类型（多选）</label>
                      <div className="flex gap-2 flex-wrap mt-1">
                        {K8S_RESOURCE_TYPES.map(t => (
                          <label key={t.value} className="flex items-center gap-1 text-sm border rounded-md px-2 py-1 cursor-pointer">
                            <input type="checkbox" checked={k8sTypes.includes(t.value)} onChange={e => {
                              if (e.target.checked) setK8sTypes(ts => Array.from(new Set([...ts, t.value])))
                              else setK8sTypes(ts => ts.filter(v => v !== t.value))
                            }} /> {t.label}
                          </label>
                        ))}
                      </div>
                    </div>
                    <div className="flex gap-2">
                      <Button size="sm" variant="outline" onClick={handleK8sDiscover} disabled={!k8sAgent || k8sDiscovering}>
                        <Play className="h-3 w-3 mr-1" />{k8sDiscovering ? '发现中...' : '发现资源'}
                      </Button>
                    </div>
                  </div>

                  {/* 结果 */}
                  <div className="space-y-3">
                    <div className="flex items-center justify-between">
                      <span className="text-sm text-muted-foreground">已发现 {k8sResources.length} 个资源</span>
                      <Button size="sm" onClick={handleK8sSync} disabled={k8sResources.length === 0 || k8sSyncing}>
                        <ArrowUp className="h-3 w-3 mr-1" />{k8sSyncing ? '纳管中...' : '纳管到 CMDB'}
                      </Button>
                    </div>
                    {k8sResources.length === 0 ? (
                      <p className="text-xs text-muted-foreground">选择网关 Agent 后点击「发现资源」即可列出集群内的命名空间 / 工作负载 / Pod / 容器。</p>
                    ) : (
                      <div className="border rounded-md max-h-80 overflow-auto">
                        <table className="w-full text-xs">
                          <thead className="bg-muted/40 sticky top-0">
                            <tr>
                              <th className="text-left p-2 font-medium">类型</th>
                              <th className="text-left p-2 font-medium">名称</th>
                              <th className="text-left p-2 font-medium">状态</th>
                              <th className="text-left p-2 font-medium">集群</th>
                              <th className="text-left p-2 font-medium">命名空间</th>
                              <th className="text-left p-2 font-medium">镜像</th>
                            </tr>
                          </thead>
                          <tbody>
                            {k8sResources.map((r: CloudResource, i: number) => (
                              <tr key={i} className="border-t">
                                <td className="p-2"><Badge variant="outline" className="text-[10px]">{r.type}</Badge></td>
                                <td className="p-2">{r.name}</td>
                                <td className="p-2">{r.status || '-'}</td>
                                <td className="p-2">{typeof r.extra?.cluster === 'string' ? r.extra.cluster : '-'}</td>
                                <td className="p-2">{typeof r.extra?.namespace === 'string' ? r.extra.namespace : '-'}</td>
                                <td className="p-2">{typeof r.extra?.image === 'string' ? r.extra.image : '-'}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      {/* Dialogs */}
      <Dialog open={scriptDialog} onClose={() => setScriptDialog(false)} title={`${scriptOS === 'linux' ? 'Linux' : scriptOS === 'windows' ? 'Windows' : scriptOS === 'container' ? '容器' : 'Unknown'} 安装脚本`}>
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">将以下脚本在目标主机上执行即可完成 Agent 安装。</p>
          <pre className="bg-black rounded-md p-4 text-green-400 text-xs font-mono whitespace-pre-wrap max-h-96 overflow-auto">{scriptContent}</pre>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => copyToClipboard(scriptContent)}><Copy className="h-3 w-3 mr-1" />复制到剪贴板</Button>
          <Button onClick={() => setScriptDialog(false)}>关闭</Button>
        </DialogFooter>
      </Dialog>

      <Dialog open={autoDialog} onClose={() => setAutoDialog(false)} title="批量安装结果">
        <div className="space-y-2 max-h-80 overflow-auto">
          {(autoResult?.results || []).map((r: any, i: number) => (
            <div key={i} className="flex items-center gap-2 border-b pb-2 text-sm">
              {r.status === 'created' ? <CheckCircle className="h-4 w-4 text-emerald-500" /> : <XCircle className="h-4 w-4 text-destructive" />}
              <span className="font-medium">{r.address}</span>
              <span className="text-muted-foreground">{r.status === 'created' ? '已创建' : `失败: ${r.error}`}</span>
              {r.agent_id && <Badge variant="outline" className="text-xs">{r.agent_id.slice(0, 8)}</Badge>}
            </div>
          ))}
        </div>
        <DialogFooter><Button onClick={() => setAutoDialog(false)}>关闭</Button></DialogFooter>
      </Dialog>

      {/* 主机详情 Tab 式 Dialog */}
      <Dialog open={detailDialog} onClose={() => setDetailDialog(false)} title={`主机详情${detailHost ? ' - ' + detailHost.name : ''}`}>
        {detailHost && (
          <div className="space-y-3">
            <Tabs value={detailTab} onValueChange={setDetailTab}>
              <TabsList>
                <TabsTrigger value="detail"><Eye className="h-3 w-3 mr-1" />详情</TabsTrigger>
                <TabsTrigger value="edit"><Pencil className="h-3 w-3 mr-1" />编辑</TabsTrigger>
                <TabsTrigger value="breaker"><Zap className="h-3 w-3 mr-1" />熔断器</TabsTrigger>
                <TabsTrigger value="tasks"><ListChecks className="h-3 w-3 mr-1" />任务</TabsTrigger>
              </TabsList>

              {/* 详情 Tab（只读） */}
              <TabsContent value="detail" className="mt-4 max-h-[55vh] overflow-auto">
                <div className="grid grid-cols-2 gap-2 text-sm">
                  <div><span className="text-muted-foreground">名称:</span> {detailHost.name}</div>
                  <div><span className="text-muted-foreground">类型:</span> <Badge variant="outline" className="text-xs">{detailHost.type}</Badge></div>
                  <div><span className="text-muted-foreground">地址:</span> {detailHost.address}{detailHost.port ? `:${detailHost.port}` : ''}</div>
                  <div><span className="text-muted-foreground">状态:</span> <Badge variant={statusVariant(detailHost.status)} className="text-xs">{statusLabel(detailHost.status)}</Badge></div>
                  <div><span className="text-muted-foreground">安装方式:</span> {detailHost.install_method === 'auto' ? '自动' : '手动'}</div>
                  <div><span className="text-muted-foreground">认证方式:</span> {detailHost.auth_type?.toUpperCase() || '-'}</div>
                  <div><span className="text-muted-foreground">操作系统:</span> {detailHost.os_type || '-'}{detailHost.os_version ? ` ${detailHost.os_version}` : ''}</div>
                  <div><span className="text-muted-foreground">主机名:</span> {detailHost.hostname || '-'}</div>
                  <div><span className="text-muted-foreground">Agent 版本:</span> {detailHost.version ? `v${detailHost.version}` : '-'}</div>
                  <div><span className="text-muted-foreground">CPU:</span> {detailHost.cpu || '-'}</div>
                  <div><span className="text-muted-foreground">内存:</span> {detailHost.memory || '-'}</div>
                  <div><span className="text-muted-foreground">磁盘:</span> {detailHost.disk || '-'}</div>
                  <div><span className="text-muted-foreground">首次上线:</span> {detailHost.first_seen ? formatTime(detailHost.first_seen) : '-'}</div>
                  <div><span className="text-muted-foreground">最后心跳:</span> {detailHost.last_heartbeat ? formatTime(detailHost.last_heartbeat) : '-'}</div>
                  {detailHost.labels && Object.keys(detailHost.labels).length > 0 && (
                    <div className="col-span-2"><span className="text-muted-foreground">标签:</span>
                      <div className="flex gap-1 mt-1 flex-wrap">{Object.entries(detailHost.labels).map(([k, v]) => <Badge key={k} variant="secondary" className="text-xs">{k}={v}</Badge>)}</div>
                    </div>
                  )}
                  {detailHost.capabilities && detailHost.capabilities.length > 0 && (
                    <div className="col-span-2"><span className="text-muted-foreground">能力:</span>
                      <div className="flex gap-1 mt-1 flex-wrap">{detailHost.capabilities.map(c => <Badge key={c} variant="secondary" className="text-xs">{c}</Badge>)}</div>
                    </div>
                  )}
                  {detailHost.type !== 'agentless' && (
                    <div className="col-span-2 flex items-center justify-between p-3 rounded-md bg-muted/30">
                      <div>
                        <div className="text-sm font-medium">沙箱环境</div>
                        <div className="text-[11px] text-muted-foreground">标记为沙箱后，脚本可在此主机的隔离环境中执行（如 Python venv）</div>
                      </div>
                      <label className="flex items-center gap-2 cursor-pointer">
                        <input type="checkbox" checked={sandboxChecked} onChange={e => toggleSandbox(e.target.checked)} className="h-4 w-4 rounded border-gray-300 text-emerald-600 focus:ring-emerald-500" />
                        <span className="text-sm font-medium">{sandboxChecked ? '已启用' : '未启用'}</span>
                      </label>
                    </div>
                  )}
                </div>
              </TabsContent>

              {/* 编辑 Tab */}
              <TabsContent value="edit" className="mt-4 space-y-3 max-h-[55vh] overflow-auto">
                <div><label className="text-sm font-medium">名称</label><Input value={editForm.name} onChange={e => setEditForm(f => ({ ...f, name: sanitizeHostName(e.target.value) }))} placeholder="host-web-01 (字母/数字/点/横杠/下划线)" /></div>
                <div><label className="text-sm font-medium">地址</label><Input value={editForm.address} onChange={e => setEditForm(f => ({ ...f, address: e.target.value }))} /></div>
                <div><label className="text-sm font-medium">端口</label><Input type="number" value={editForm.port} onChange={e => setEditForm(f => ({ ...f, port: parseInt(e.target.value) || 0 }))} /></div>
                <div><label className="text-sm font-medium">SSH 用户</label><Input value={editForm.ssh_user} onChange={e => setEditForm(f => ({ ...f, ssh_user: e.target.value }))} /></div>
                <div><label className="text-sm font-medium">标签 (key=value,逗号分隔)</label><Input value={editForm.labels} onChange={e => setEditForm(f => ({ ...f, labels: e.target.value }))} placeholder="env=prod,role=web" /></div>
                <Button onClick={handleSaveEdit}><Save className="h-4 w-4 mr-1" />保存修改</Button>
              </TabsContent>

              {/* 熔断器 Tab */}
              <TabsContent value="breaker" className="mt-4 space-y-3 max-h-[55vh] overflow-auto">
                {cbLoading ? (
                  <p className="text-sm text-muted-foreground">加载中...</p>
                ) : (
                  <>
                    <div className="grid grid-cols-2 gap-3">
                      <div><label className="text-sm font-medium">心跳限速/分</label><Input type="number" value={cbForm.max_heartbeats_per_min} onChange={e => setCbForm(f => ({ ...f, max_heartbeats_per_min: parseInt(e.target.value) || 0 }))} /></div>
                      <div><label className="text-sm font-medium">请求限速/分</label><Input type="number" value={cbForm.max_req_per_min} onChange={e => setCbForm(f => ({ ...f, max_req_per_min: parseInt(e.target.value) || 0 }))} /></div>
                      <div><label className="text-sm font-medium">最大连续失败</label><Input type="number" value={cbForm.max_consecutive_fails} onChange={e => setCbForm(f => ({ ...f, max_consecutive_fails: parseInt(e.target.value) || 0 }))} /></div>
                      <div className="flex items-center gap-2 pt-6">
                        <input type="checkbox" id="auto_restart" checked={cbForm.auto_restart} onChange={e => setCbForm(f => ({ ...f, auto_restart: e.target.checked }))} />
                        <label htmlFor="auto_restart" className="text-sm cursor-pointer">自动重启</label>
                      </div>
                    </div>
                    <div className="flex items-center gap-3 p-3 rounded-md bg-muted/30 text-sm">
                      <span className="text-muted-foreground">当前状态:</span>
                      <Badge variant={cbForm.breaker_open ? 'destructive' : 'success'} className="text-xs">{cbForm.breaker_open ? '熔断开启' : '正常'}</Badge>
                      <span className="text-muted-foreground">失败计数: {cbForm.fail_count}</span>
                    </div>
                    <Button onClick={handleSaveCB}><Save className="h-4 w-4 mr-1" />保存熔断配置</Button>
                  </>
                )}
              </TabsContent>

              {/* 任务 Tab */}
              <TabsContent value="tasks" className="mt-4 space-y-3 max-h-[55vh] overflow-auto">
                <div className="space-y-2 border rounded-md p-3">
                  <p className="text-sm font-medium">下发新任务</p>
                  <div><label className="text-xs text-muted-foreground">动作 (action)</label><Input value={newTaskForm.action} onChange={e => setNewTaskForm(f => ({ ...f, action: e.target.value }))} placeholder="script" /></div>
                  <div><label className="text-xs text-muted-foreground">脚本内容</label><textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-xs font-mono" value={newTaskForm.script} onChange={e => setNewTaskForm(f => ({ ...f, script: e.target.value }))} placeholder="#!/bin/bash&#10;uptime" /></div>
                  <div><label className="text-xs text-muted-foreground">超时(秒)</label><Input type="number" value={newTaskForm.timeout} onChange={e => setNewTaskForm(f => ({ ...f, timeout: parseInt(e.target.value) || 60 }))} /></div>
                  <Button size="sm" onClick={handleCreateTask} disabled={!newTaskForm.action.trim()}><Play className="h-3 w-3 mr-1" />下发任务</Button>
                </div>
                <div className="space-y-1">
                  <p className="text-sm font-medium">任务历史</p>
                  {tasksLoading ? <p className="text-xs text-muted-foreground">加载中...</p> : tasks.length === 0 ? <p className="text-xs text-muted-foreground">暂无任务</p> : tasks.map(t => (
                    <div key={t.task_id} className="flex items-center gap-2 p-2 border rounded text-xs">
                      <Badge variant={taskStatusVariant(t.status)} className="text-xs">{taskStatusLabel(t.status)}</Badge>
                      <span className="font-mono">{t.action}</span>
                      <span className="text-muted-foreground">{formatTime(t.created_at)}</span>
                      {t.response && <span className="text-muted-foreground">exit={t.response.exit_code}</span>}
                      {t.response?.error && <span className="text-destructive truncate">{t.response.error}</span>}
                    </div>
                  ))}
                </div>
              </TabsContent>
            </Tabs>
          </div>
        )}
        <DialogFooter>
          <Button variant="destructive" onClick={() => setConfirmUninstall(detailHost)} disabled={!detailHost || detailHost.type !== 'agent'}><Trash2 className="h-3 w-3 mr-1" />卸载 Agent</Button>
          <Button variant="outline" onClick={() => upgradeAgent(detailHost?.id || '')} disabled={!detailHost || detailHost.type !== 'agent'}><ArrowUp className="h-3 w-3 mr-1" />升级 Agent</Button>
          <Button variant="destructive" onClick={() => setConfirmRestart(detailHost)} disabled={!detailHost || detailHost.type !== 'agent'}><RotateCcw className="h-3 w-3 mr-1" />重启 Agent</Button>
          <Button variant="outline" onClick={() => setDetailDialog(false)}>关闭</Button>
        </DialogFooter>
      </Dialog>

      {/* 注册 Proxy Dialog */}
      <Dialog open={proxyDialog} onClose={() => setProxyDialog(false)} title="注册 Proxy">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">名称</label><Input value={proxyForm.name} onChange={e => setProxyForm(f => ({ ...f, name: sanitizeHostName(e.target.value) }))} placeholder="proxy-beijing" /></div>
          <div><label className="text-sm font-medium">地址 *</label><Input value={proxyForm.address} onChange={e => setProxyForm(f => ({ ...f, address: e.target.value }))} placeholder="10.0.0.5" /></div>
          <div><label className="text-sm font-medium">端口 *</label><Input type="number" value={proxyForm.port} onChange={e => setProxyForm(f => ({ ...f, port: parseInt(e.target.value) || 8080 }))} /></div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setProxyDialog(false)}>取消</Button>
          <Button onClick={handleRegisterProxy} disabled={!proxyForm.address.trim() || !proxyForm.port}><Plus className="h-3 w-3 mr-1" />注册</Button>
        </DialogFooter>
      </Dialog>

      {/* 编辑 Proxy Dialog */}
      <Dialog open={editProxyDialog} onClose={() => { setEditProxyDialog(false); setEditProxyTarget(null) }} title="编辑 Proxy">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">名称</label><Input value={editProxyForm.name} onChange={e => setEditProxyForm(f => ({ ...f, name: sanitizeHostName(e.target.value) }))} /></div>
          <div><label className="text-sm font-medium">地址</label><Input value={editProxyForm.address} onChange={e => setEditProxyForm(f => ({ ...f, address: e.target.value }))} /></div>
          <div><label className="text-sm font-medium">端口</label><Input type="number" value={editProxyForm.port} onChange={e => setEditProxyForm(f => ({ ...f, port: parseInt(e.target.value) || 8080 }))} /></div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => { setEditProxyDialog(false); setEditProxyTarget(null) }}>取消</Button>
          <Button onClick={handleSaveEditProxy}><Save className="h-3 w-3 mr-1" />保存</Button>
        </DialogFooter>
      </Dialog>

      {/* 确认删除主机 Dialog */}
      <Dialog open={!!confirmDeleteHost} onClose={() => setConfirmDeleteHost(null)} title="确认删除主机">
        <div className="space-y-2">
          <div className="flex items-start gap-2">
            <AlertTriangle className="h-5 w-5 text-destructive shrink-0 mt-0.5" />
            <p className="text-sm">确认删除主机 <span className="font-medium">{confirmDeleteHost?.name}</span> ({confirmDeleteHost?.address})? 此操作不可恢复。</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setConfirmDeleteHost(null)}>取消</Button>
          <Button variant="destructive" onClick={handleDeleteHost}><Trash2 className="h-3 w-3 mr-1" />确认删除</Button>
        </DialogFooter>
      </Dialog>

      {/* 确认删除 Proxy Dialog */}
      <Dialog open={!!confirmDeleteProxy} onClose={() => setConfirmDeleteProxy(null)} title="确认删除 Proxy">
        <div className="space-y-2">
          <div className="flex items-start gap-2">
            <AlertTriangle className="h-5 w-5 text-destructive shrink-0 mt-0.5" />
            <p className="text-sm">确认删除 Proxy <span className="font-medium">{confirmDeleteProxy?.name || confirmDeleteProxy?.address}</span> ({confirmDeleteProxy?.address}:{confirmDeleteProxy?.port})? 该 Proxy 管理的 {confirmDeleteProxy?.agent_count} 个 Agent 将失去代理。</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setConfirmDeleteProxy(null)}>取消</Button>
          <Button variant="destructive" onClick={handleDeleteProxy}><Trash2 className="h-3 w-3 mr-1" />确认删除</Button>
        </DialogFooter>
      </Dialog>

      {/* 确认重启 Agent Dialog */}
      <Dialog open={!!confirmRestart} onClose={() => setConfirmRestart(null)} title="确认重启 Agent">
        <div className="space-y-2">
          <div className="flex items-start gap-2">
            <AlertTriangle className="h-5 w-5 text-amber-500 shrink-0 mt-0.5" />
            <p className="text-sm">确认重启主机 <span className="font-medium">{confirmRestart?.name}</span> ({confirmRestart?.address}) 上的 Agent? 重启期间该主机将短暂离线。</p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setConfirmRestart(null)}>取消</Button>
          <Button onClick={handleRestart}><RotateCcw className="h-3 w-3 mr-1" />确认重启</Button>
        </DialogFooter>
      </Dialog>

      {/* 确认卸载 Agent Dialog */}
      <Dialog open={!!confirmUninstall} onClose={() => setConfirmUninstall(null)} title="确认卸载 Agent">
        <div className="space-y-4">
          <p className="text-sm">确认卸载主机 <span className="font-medium">{confirmUninstall?.name}</span> ({confirmUninstall?.address}) 上的 Agent?</p>
          <p className="text-xs text-destructive">卸载操作会停止 Agent 服务、删除二进制文件和配置,该主机将从列表移除。此操作不可恢复。</p>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setConfirmUninstall(null)}>取消</Button>
            <Button variant="destructive" onClick={async () => {
              if (!confirmUninstall) return
              const ok = await uninstallAgent(confirmUninstall.id)
              if (ok) { setConfirmUninstall(null); setDetailDialog(false) }
            }}><Trash2 className="h-3 w-3 mr-1" />确认卸载</Button>
          </div>
        </div>
      </Dialog>
    </div>
  )
}
