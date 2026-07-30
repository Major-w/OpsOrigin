'use client'

import { useEffect, useState, useMemo } from 'react'
import { useParams, useRouter } from 'next/navigation'
import { useScriptStore, type Script, type ExecutionResult, type ScriptVersion, type DiffEntry, type CheckResult } from '@/stores/script'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { CodeEditor } from '@/components/ui/code-editor'
import { Plus, Trash2, Code2, FileText, Copy, Clock, Play, History, Pencil, Terminal, Hash, ChevronRight, GitBranch, GitCompare, CheckCircle, XCircle, Eye, Loader2, Star, ChevronLeft, Save, X, AlertCircle, Sparkles } from 'lucide-react'
import { cn, formatTime, apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { validateName, sanitizeName, NAME_RULE_TEXT } from '@/lib/validate'

const SCRIPT_TYPES = [
  { value: 'shell', label: 'Shell', color: 'text-emerald-500' },
  { value: 'python', label: 'Python', color: 'text-blue-500' },
  { value: 'bat', label: 'Batch', color: 'text-yellow-500' },
]

const TYPE_COLORS: Record<string, string> = {
  shell: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/20',
  python: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20',
  bat: 'bg-yellow-500/10 text-yellow-600 dark:text-yellow-400 border-yellow-500/20',
}

// LCS 行级 diff
function computeLineDiff(oldStr: string, newStr: string): { type: 'add' | 'del' | 'ctx'; text: string }[] {
  const oldLines = oldStr.split('\n')
  const newLines = newStr.split('\n')
  const m = oldLines.length, n = newLines.length
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0))
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      if (oldLines[i] === newLines[j]) dp[i][j] = dp[i + 1][j + 1] + 1
      else dp[i][j] = Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const result: { type: 'add' | 'del' | 'ctx'; text: string }[] = []
  let i = 0, j = 0
  while (i < m && j < n) {
    if (oldLines[i] === newLines[j]) {
      result.push({ type: 'ctx', text: oldLines[i] })
      i++; j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      result.push({ type: 'del', text: oldLines[i] })
      i++
    } else {
      result.push({ type: 'add', text: newLines[j] })
      j++
    }
  }
  while (i < m) { result.push({ type: 'del', text: oldLines[i] }); i++ }
  while (j < n) { result.push({ type: 'add', text: newLines[j] }); j++ }
  return result
}

// 参数字段级 diff
function computeParamDiff(oldStr: string, newStr: string): { type: 'add' | 'del' | 'mod'; name: string; detail?: string }[] | null {
  let oldArr: any[], newArr: any[]
  try {
    oldArr = JSON.parse(oldStr)
    newArr = JSON.parse(newStr)
    if (!Array.isArray(oldArr) || !Array.isArray(newArr)) return null
  } catch {
    return null
  }
  const oldMap = new Map<string, any>()
  oldArr.forEach((p: any, i: number) => oldMap.set(p.name || `#${i}`, p))
  const newMap = new Map<string, any>()
  newArr.forEach((p: any, i: number) => newMap.set(p.name || `#${i}`, p))

  const result: { type: 'add' | 'del' | 'mod'; name: string; detail?: string }[] = []
  for (const [name, oldP] of oldMap) {
    if (!newMap.has(name)) {
      result.push({ type: 'del', name })
    } else {
      const newP = newMap.get(name)
      const changes: string[] = []
      const allKeys = new Set([...Object.keys(oldP), ...Object.keys(newP)])
      for (const k of allKeys) {
        if (JSON.stringify(oldP[k]) !== JSON.stringify(newP[k])) {
          changes.push(`${k}: ${JSON.stringify(oldP[k])} → ${JSON.stringify(newP[k])}`)
        }
      }
      if (changes.length > 0) result.push({ type: 'mod', name, detail: changes.join('; ') })
    }
  }
  for (const [name] of newMap) {
    if (!oldMap.has(name)) result.push({ type: 'add', name })
  }
  return result
}

function StatusBadge({ status }: { status: string }) {
  if (status === 'success') return <Badge variant="success" className="text-[10px]"><CheckCircle className="h-3 w-3 mr-0.5" />成功</Badge>
  if (status === 'failed' || status === 'error') return <Badge variant="destructive" className="text-[10px]"><XCircle className="h-3 w-3 mr-0.5" />失败</Badge>
  if (status === 'timeout') return <Badge variant="warning" className="text-[10px]"><Clock className="h-3 w-3 mr-0.5" />超时</Badge>
  return <Badge variant="secondary" className="text-[10px]">{status}</Badge>
}

interface ParamForm {
  name: string; type: string; required: boolean; description: string; default_value: string
}

interface OutputParamForm {
  name: string; type: string; description: string
}

export default function ScriptDetailPage() {
  const params = useParams()
  const router = useRouter()
  const scriptId = String(params.id || '')
  const isNew = scriptId === 'new' || !scriptId

  const { scripts, fetch, create, update, execute, fetchExecutions, fetchVersions, diff, check } = useScriptStore()

  // 编辑/查看模式
  const [editing, setEditing] = useState(isNew)
  const [activeTab, setActiveTab] = useState('content')
  const [loading, setLoading] = useState(!isNew)

  // 表单状态
  const [form, setForm] = useState({ name: '', type: 'shell', content: '', description: '', timeout: 300, run_as: '' })
  const [paramForm, setParamForm] = useState<ParamForm[]>([])
  const [outputParamForm, setOutputParamForm] = useState<OutputParamForm[]>([])
  const [changeNote, setChangeNote] = useState('')
  const [nameError, setNameError] = useState<string | null>(null)

  // 当前脚本对象（只读视图）
  const [viewScript, setViewScript] = useState<Script | null>(null)

  // 执行对话框
  const [executeDialogOpen, setExecuteDialogOpen] = useState(false)
  const [executeParams, setExecuteParams] = useState<Record<string, string>>({})
  const [executeTimeout, setExecuteTimeout] = useState<number>(300)
  const [executeRunAs, setExecuteRunAs] = useState<string>('')
  const [executeHost, setExecuteHost] = useState<any>(null)
  const [executeChannel, setExecuteChannel] = useState<string>('')
  const [executeLocal, setExecuteLocal] = useState(false) // 默认不勾选=需要选择主机
  const [executeSandbox, setExecuteSandbox] = useState(false) // 沙箱环境执行(带沙箱标签的 Agent 上隔离运行)
  const [sandboxAgents, setSandboxAgents] = useState<any[]>([])
  const [executeSandboxAgentID, setExecuteSandboxAgentID] = useState('')
  const [executeRequirements, setExecuteRequirements] = useState('') // python 沙箱依赖,逗号分隔
  const [hosts, setHosts] = useState<any[]>([])
  const [executing, setExecuting] = useState(false)
  const [executeResult, setExecuteResult] = useState<ExecutionResult | null>(null)

  // 语法检查
  const [checking, setChecking] = useState(false)
  const [checkResult, setCheckResult] = useState<CheckResult | null>(null)
  const [aiGenerating, setAiGenerating] = useState(false)

  // 执行历史
  const [executions, setExecutions] = useState<ExecutionResult[]>([])
  const [execLoading, setExecLoading] = useState(false)
  const [expandedExecId, setExpandedExecId] = useState<string | null>(null)
  const [execStatusFilter, setExecStatusFilter] = useState<'all' | 'success' | 'failed' | 'timeout'>('all')
  const [autoRefreshing, setAutoRefreshing] = useState(false)

  // 版本历史
  const [versions, setVersions] = useState<ScriptVersion[]>([])
  const [versionLoading, setVersionLoading] = useState(false)
  const [viewVersion, setViewVersion] = useState<ScriptVersion | null>(null)

  // 版本比对
  const [diffMode, setDiffMode] = useState(false)
  const [diffFrom, setDiffFrom] = useState<number | ''>('')
  const [diffTo, setDiffTo] = useState<number | ''>('')
  const [diffResult, setDiffResult] = useState<DiffEntry[]>([])
  const [diffLoading, setDiffLoading] = useState(false)
  const [diffViewMode, setDiffViewMode] = useState<'unified' | 'side'>('unified')
  const [diffFromContent, setDiffFromContent] = useState('')
  const [diffToContent, setDiffToContent] = useState('')

  // 收藏
  const [starred, setStarred] = useState(false)

  useEffect(() => {
    if (isNew) {
      setEditing(true)
      setForm({ name: '', type: 'shell', content: '', description: '', timeout: 300, run_as: '' })
      setParamForm([])
      setOutputParamForm([])
      setLoading(false)
      return
    }
    // 加载脚本列表以查找当前脚本
    setLoading(true)
    fetch().then(() => setLoading(false))
  }, [scriptId, isNew])

  // 从 store 中找到当前脚本
  useEffect(() => {
    if (isNew || loading) return
    const sc = scripts.find(s => s.id === scriptId)
    if (sc) {
      setViewScript(sc)
      setForm({
        name: sc.name, type: sc.type, content: sc.content,
        description: sc.description || '', timeout: sc.timeout, run_as: sc.run_as || '',
      })
      setParamForm((sc.input_params || []).map(p => ({
        name: p.name, type: p.type, required: p.required,
        description: p.description || '', default_value: p.default || '',
      })))
      setOutputParamForm((sc.output_params || []).map(p => ({
        name: p.name, type: p.type, description: p.description || '',
      })))
      setExecuteTimeout(sc.timeout)
      setExecuteRunAs(sc.run_as || '')
    }
  }, [scripts, scriptId, isNew, loading])

  // 切换脚本时重置执行历史/版本数据
  useEffect(() => {
    setExecutions([])
    setExpandedExecId(null)
    setExecStatusFilter('all')
    setVersions([])
    setViewVersion(null)
    setDiffMode(false)
    setDiffResult([])
    setDiffFrom('')
    setDiffTo('')
  }, [scriptId])

  // 加载执行历史
  useEffect(() => {
    if (viewScript && activeTab === 'history' && !isNew) {
      setExecLoading(true)
      fetchExecutions(viewScript.id).then(r => { setExecutions(r); setExecLoading(false) })
    }
  }, [viewScript?.id, activeTab, isNew])

  // 执行历史自动刷新
  useEffect(() => {
    if (!viewScript || activeTab !== 'history' || isNew) return
    const timer = setInterval(async () => {
      setAutoRefreshing(true)
      try {
        const r = await fetchExecutions(viewScript.id)
        setExecutions(r)
      } finally {
        setAutoRefreshing(false)
      }
    }, 10000)
    return () => {
      clearInterval(timer)
      setAutoRefreshing(false)
    }
  }, [viewScript?.id, activeTab, isNew])

  // 加载版本历史
  useEffect(() => {
    if (viewScript && activeTab === 'versions' && !isNew) {
      setVersionLoading(true)
      fetchVersions(viewScript.id).then(r => { setVersions(r); setVersionLoading(false) })
    }
  }, [viewScript?.id, activeTab, isNew])

  // 收藏状态
  useEffect(() => {
    if (!scriptId || isNew) return
    try {
      const raw = localStorage.getItem('opsnexus_starred_scripts')
      if (!raw) return
      const arr = JSON.parse(raw)
      if (Array.isArray(arr)) setStarred(arr.includes(scriptId))
    } catch {}
  }, [scriptId, isNew])

  const filteredExecutions = useMemo(() => {
    if (execStatusFilter === 'all') return executions
    if (execStatusFilter === 'failed') {
      return executions.filter(h => h.status === 'failed' || h.status === 'error')
    }
    return executions.filter(h => h.status === execStatusFilter)
  }, [executions, execStatusFilter])

  const typeLabel = (t: string) => SCRIPT_TYPES.find(st => st.value === t)?.label || t

  const toggleStar = () => {
    if (!scriptId || isNew || !viewScript) return
    try {
      const raw = localStorage.getItem('opsnexus_starred_scripts') || '[]'
      const arr = JSON.parse(raw)
      const next = arr.includes(scriptId)
        ? arr.filter((x: string) => x !== scriptId)
        : [...arr, scriptId]
      localStorage.setItem('opsnexus_starred_scripts', JSON.stringify(next))
      setStarred(!starred)
    } catch {}
  }

  const handleCheck = async () => {
    if (!form.content.trim()) { toast.error('脚本内容不能为空'); return }
    setChecking(true); setCheckResult(null)
    try {
      const res = await check({ type: form.type, content: form.content })
      setCheckResult(res)
      if (res?.valid) {
        toast.success('语法检查通过')
      } else if (res?.errors?.length) {
        toast.warning(`发现 ${res.errors.length} 个语法错误`)
      }
    } finally {
      setChecking(false)
    }
  }

  const handleAIGenerate = async () => {
    const nameErr = validateName(form.name, '脚本名称')
    if (nameErr) { toast.error(nameErr); return }
    setAiGenerating(true)
    try {
      const lang = form.type === 'shell' ? 'bash' : form.type === 'python' ? 'python' : 'batch'
      const res = await apiFetch<{ content: string }>('/v1/ai/chat', {
        method: 'POST',
        body: JSON.stringify({ prompt: `写一个${lang}脚本,功能是"${form.name}${form.description?':'+form.description:''}"。只输出完整可执行代码,不要加解释。`, stream: false }),
      })
      if (res.content) {
        let code = res.content.replace(/```[\s\S]*?\n/g, '').replace(/```/g, '').trim()
        setForm(f => ({ ...f, content: code }))
        toast.success('AI 已生成脚本代码')
      }
    } catch (e: any) { toast.error('AI 生成失败: ' + e.message) }
    setAiGenerating(false)
  }

  const handleSave = async () => {
    const nameErr = validateName(form.name, '脚本名称')
    if (nameErr) {
      toast.error(nameErr)
      return
    }
    if (!form.content.trim()) {
      toast.error('脚本内容不能为空')
      return
    }
    const data = {
      ...form,
      input_params: paramForm,
      output_params: outputParamForm,
    }
    if (isNew) {
      const ok = await create(data)
      if (ok) {
        toast.success('脚本创建成功')
        router.push('/scripts')
      }
    } else {
      const ok = await update(scriptId, data, changeNote || undefined)
      if (ok) {
        toast.success('脚本保存成功')
        setChangeNote('')
        setEditing(false)
        // 刷新 viewScript
        const updated = useScriptStore.getState().scripts.find(s => s.id === scriptId)
        if (updated) setViewScript(updated)
      }
    }
  }

  const handleCancel = () => {
    if (isNew) {
      router.push('/scripts')
      return
    }
    setEditing(false)
    // 恢复表单
    if (viewScript) {
      setForm({
        name: viewScript.name, type: viewScript.type, content: viewScript.content,
        description: viewScript.description || '', timeout: viewScript.timeout, run_as: viewScript.run_as || '',
      })
      setParamForm((viewScript.input_params || []).map(p => ({
        name: p.name, type: p.type, required: p.required,
        description: p.description || '', default_value: p.default || '',
      })))
      setOutputParamForm((viewScript.output_params || []).map(p => ({
        name: p.name, type: p.type, description: p.description || '',
      })))
    }
  }

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text)
    toast.success('已复制到剪贴板')
  }

  const openExecute = () => {
    if (!viewScript) return
    const params: Record<string, string> = {}
    viewScript.input_params?.forEach(p => { params[p.name] = p.default || '' })
    setExecuteParams(params)
    setExecuteTimeout(viewScript.timeout)
    setExecuteRunAs(viewScript.run_as || '')
    setExecuteHost(null)
    setExecuteChannel('')
    setExecuteLocal(false)
    setExecuteDialogOpen(true)
    // 加载主机列表
    apiFetch<any[]>('/agents').then(arr => {
      setHosts((arr || []).filter((h: any) => h.type !== 'sandbox'))
    }).catch(() => {})
    // 加载沙箱 Agent 列表(带沙箱标签的真实 Agent)
    apiFetch<any>('/agents/sandbox').then(res => {
      setSandboxAgents((res?.agents as any[]) || [])
    }).catch(() => { setSandboxAgents([]) })
  }

  const handleExecute = async () => {
    if (!viewScript) return
    setExecuting(true)
    const data: any = {
      params: executeParams,
      timeout: executeTimeout,
      run_as: executeRunAs || undefined,
    }
    if (executeSandbox) {
      data.sandbox = true
      data.requirements = executeRequirements.split(',').map((s: string) => s.trim()).filter(Boolean)
      data.agent_id = executeSandboxAgentID || undefined
    } else if (!executeLocal) {
      data.agent_id = executeHost?.id || undefined
      data.channel = executeChannel || undefined
    }
    const result = await execute(viewScript.id, data)
    setExecuting(false)
    if (result) {
      setExecuteResult(result)
      setExecuteDialogOpen(false)
      // 刷新执行历史
      if (activeTab === 'history') {
        setExecLoading(true)
        fetchExecutions(viewScript.id).then(r => { setExecutions(r); setExecLoading(false) })
      }
    }
  }

  const handleDiff = async () => {
    if (!viewScript || diffFrom === '' || diffTo === '') return
    setDiffLoading(true)
    const result = await diff(viewScript.id, Number(diffFrom), Number(diffTo))
    setDiffResult(result)
    const allVersions = await fetchVersions(viewScript.id)
    const fromV = allVersions.find(v => v.version === Number(diffFrom))
    const toV = allVersions.find(v => v.version === Number(diffTo))
    setDiffFromContent(fromV?.content || '')
    setDiffToContent(toV?.content || '')
    setDiffLoading(false)
  }

  if (loading) {
    return (
      <div className="p-6 flex items-center justify-center min-h-[400px]">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      {/* 顶部导航栏 */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3 min-w-0">
          <Button variant="ghost" size="icon" onClick={() => router.push('/scripts')} title="返回脚本列表">
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <Code2 className={cn("h-5 w-5 shrink-0", SCRIPT_TYPES.find(t => t.value === form.type)?.color || 'text-muted-foreground')} />
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h1 className="text-xl font-bold truncate">{isNew ? '新建脚本' : (form.name || '未命名脚本')}</h1>
              {!isNew && (
                <button onClick={toggleStar} title={starred ? '取消收藏' : '收藏'} className="p-1 rounded hover:bg-gray-100 dark:hover:bg-[rgba(255,255,255,0.06)]">
                  <Star className={cn("h-4 w-4", starred ? "fill-amber-400 text-amber-400" : "text-muted-foreground/40")} />
                </button>
              )}
              {!isNew && !editing && viewScript && (
                <Badge variant="outline" className={cn("text-[10px]", TYPE_COLORS[viewScript.type])}>{typeLabel(viewScript.type)}</Badge>
              )}
            </div>
            <p className="text-xs text-muted-foreground">{isNew ? '创建新的运维脚本' : (form.description || '无描述')}</p>
          </div>
        </div>
        <div className="flex gap-2 shrink-0">
          {!isNew && !editing && viewScript && (
            <>
              <Button variant="outline" size="sm" onClick={() => copyToClipboard(viewScript.content)}>
                <Copy className="h-3.5 w-3.5 mr-1" />复制
              </Button>
              <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
                <Pencil className="h-3.5 w-3.5 mr-1" />编辑
              </Button>
              <Button size="sm" onClick={openExecute}>
                <Play className="h-3.5 w-3.5 mr-1" />执行
              </Button>
            </>
          )}
          {editing && (
            <>
              <Button variant="outline" size="sm" onClick={handleCancel}>
                <X className="h-3.5 w-3.5 mr-1" />取消
              </Button>
              <Button variant="outline" size="sm" onClick={handleCheck} disabled={!form.content.trim() || checking}>
                {checking ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <AlertCircle className="h-3.5 w-3.5 mr-1" />}
                语法检查
              </Button>
              <Button variant="outline" size="sm" onClick={handleAIGenerate} disabled={!form.name.trim() || aiGenerating}>
                {aiGenerating ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Sparkles className="h-3.5 w-3.5 mr-1" />}
                AI 生成
              </Button>
              <Button size="sm" onClick={handleSave} disabled={!form.name.trim() || !form.content.trim()}>
                <Save className="h-3.5 w-3.5 mr-1" />{isNew ? '创建' : '保存'}
              </Button>
            </>
          )}
        </div>
      </div>

      <Card>
        <CardContent className="p-4">
          {editing ? (
            <div className="space-y-4">
              {/* 基本信息 */}
              <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
                <div className="md:col-span-1">
                  <label className="text-sm font-medium">脚本名称 *</label>
                  <Input
                    value={form.name}
                    onChange={e => {
                      const v = sanitizeName(e.target.value)
                      setForm(f => ({ ...f, name: v }))
                      setNameError(validateName(v, '脚本名称'))
                    }}
                    placeholder="例:restart_nginx 或 restart-nginx"
                    className="mt-1"
                  />
                  {nameError ? (
                    <p className="text-[11px] text-destructive mt-1">{nameError}</p>
                  ) : (
                    <p className="text-[11px] text-muted-foreground mt-1">{NAME_RULE_TEXT}</p>
                  )}
                </div>
                <div>
                  <label className="text-sm font-medium">类型</label>
                  <select
                    className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1"
                    value={form.type}
                    onChange={e => setForm(f => ({ ...f, type: e.target.value }))}
                  >
                    {SCRIPT_TYPES.map(t => <option key={t.value} value={t.value}>{t.label}</option>)}
                  </select>
                </div>
                <div>
                  <label className="text-sm font-medium">超时(秒)</label>
                  <Input
                    type="number"
                    value={form.timeout}
                    onChange={e => setForm(f => ({ ...f, timeout: parseInt(e.target.value) || 300 }))}
                    className="mt-1"
                  />
                </div>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                <div>
                  <label className="text-sm font-medium">描述</label>
                  <Input
                    value={form.description}
                    onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
                    placeholder="脚本用途说明（可选）"
                    className="mt-1"
                  />
                </div>
                <div>
                  <div className="flex items-center gap-1.5 mb-1">
                    <label className="text-sm font-medium">执行用户 (run_as)</label>
                    <span className="relative group cursor-help">
                      <span className="text-[11px] text-muted-foreground border rounded-full w-4 h-4 inline-flex items-center justify-center hover:bg-muted">?</span>
                      <span className="absolute left-0 bottom-full mb-1 w-56 p-2 rounded-md bg-gray-900 dark:bg-gray-800 text-white dark:text-gray-200 text-[10px] leading-relaxed opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity z-50 shadow-lg">
                        通过 <code className="bg-emerald-500/20 px-1 rounded">sudo -u 用户</code> 切换身份<br/>
                        留空 = 当前运行 Core 的用户<br/>
                        需配置无密码 sudo
                      </span>
                    </span>
                  </div>
                  <Input
                    value={form.run_as}
                    onChange={e => setForm(f => ({ ...f, run_as: e.target.value }))}
                    placeholder="留空使用当前用户"
                    className="mt-1"
                  />
                </div>
              </div>

              {!isNew && (
                <div>
                  <label className="text-sm font-medium">变更说明（可选）</label>
                  <Input
                    value={changeNote}
                    onChange={e => setChangeNote(e.target.value)}
                    placeholder="本次更新的变更说明，将记录到版本历史"
                    className="mt-1"
                  />
                </div>
              )}

              {/* 脚本内容编辑器 */}
              <div>
                <label className="text-sm font-medium mb-2 block">脚本内容 *</label>
                <CodeEditor
                  value={form.content}
                  onChange={v => setForm(f => ({ ...f, content: v }))}
                  language={form.type}
                  height="320px"
                  placeholder="#!/bin/bash\necho hello world"
                />
                {checkResult && !checkResult.valid && (
                  <div className="mt-2 p-3 rounded-md border border-destructive/30 bg-destructive/5">
                    <div className="flex items-center gap-1.5 mb-2">
                      <AlertCircle className="h-4 w-4 text-destructive" />
                      <span className="text-sm font-medium text-destructive">语法检查发现 {checkResult.errors.length} 个错误</span>
                    </div>
                    <ul className="space-y-1 pl-6 list-disc">
                      {checkResult.errors.map((e, i) => (
                        <li key={i} className="text-xs text-destructive/80">
                          {e.line ? <span className="font-mono mr-2">[行 {e.line}]</span> : null}
                          {e.column ? <span className="font-mono mr-2 text-[10px]">列 {e.column}</span> : null}
                          {e.message}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
                {checkResult?.valid && (
                  <div className="mt-2 p-2 rounded-md border border-emerald-500/30 bg-emerald-50 dark:bg-emerald-950/20 flex items-center gap-1.5">
                    <CheckCircle className="h-3.5 w-3.5 text-emerald-600" />
                    <span className="text-xs font-medium text-emerald-700 dark:text-emerald-400">语法检查通过</span>
                  </div>
                )}
              </div>

              {/* 入参定义 */}
              <div>
                <div className="flex items-center justify-between mb-2">
                  <div className="flex items-center gap-1.5">
                    <label className="text-sm font-medium">入参定义</label>
                    <span className="relative group cursor-help">
                      <span className="text-[11px] text-muted-foreground border rounded-full w-4 h-4 inline-flex items-center justify-center hover:bg-muted">?</span>
                      <span className="absolute left-0 bottom-full mb-1 w-64 p-2 rounded-md bg-gray-900 dark:bg-gray-800 text-white dark:text-gray-200 text-[10px] leading-relaxed opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity z-50 shadow-lg">
                        参数通过环境变量注入脚本进程：<br/>
                        <code className="bg-emerald-500/20 px-1 rounded">PARAM_大写参数名</code><br/>
                        Shell: <code className="bg-emerald-500/20 px-1 rounded">$PARAM_HOST</code><br/>
                        Python: <code className="bg-emerald-500/20 px-1 rounded">os.environ['PARAM_HOST']</code><br/>
                        Batch: <code className="bg-emerald-500/20 px-1 rounded">%PARAM_HOST%</code>
                      </span>
                    </span>
                  </div>
                  <Button variant="outline" size="sm" className="h-7 text-xs" onClick={() => setParamForm([...paramForm, { name: '', type: 'string', required: false, description: '', default_value: '' }])}>
                    <Plus className="h-3 w-3 mr-1" />添加参数
                  </Button>
                </div>
                {paramForm.length > 0 && (
                  <div className="space-y-1.5">
                    {paramForm.map((p, i) => (
                      <div key={i} className="flex items-center gap-1.5 p-1.5 rounded-md border border-gray-200 dark:border-[rgba(255,255,255,0.06)] bg-gray-50/50 dark:bg-white/[0.01]">
                        <Input className="text-xs h-7 w-24 shrink-0" value={p.name} onChange={e => { const pf = [...paramForm]; pf[i].name = e.target.value; setParamForm(pf) }} placeholder="参数名" />
                        <select className="text-xs h-7 w-18 rounded-md border border-input bg-background shrink-0" value={p.type} onChange={e => { const pf = [...paramForm]; pf[i].type = e.target.value; setParamForm(pf) }}>
                          <option value="string">string</option><option value="number">number</option><option value="boolean">bool</option>
                        </select>
                        <label className="text-[10px] flex items-center gap-0.5 shrink-0"><input type="checkbox" checked={p.required} onChange={e => { const pf = [...paramForm]; pf[i].required = e.target.checked; setParamForm(pf) }} />必填</label>
                        <Input className="text-xs h-7 w-20 shrink-0" value={p.default_value} onChange={e => { const pf = [...paramForm]; pf[i].default_value = e.target.value; setParamForm(pf) }} placeholder="默认值" />
                        <Input className="text-xs h-7 flex-1 min-w-0" value={p.description} onChange={e => { const pf = [...paramForm]; pf[i].description = e.target.value; setParamForm(pf) }} placeholder="描述（可选）" />
                        <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0" onClick={() => setParamForm(paramForm.filter((_, pi) => pi !== i))}><Trash2 className="h-3 w-3 text-red-400" /></Button>
                      </div>
                    ))}
                  </div>
                )}
              </div>

              {/* 出参定义 */}
              <div>
                <div className="flex items-center justify-between mb-2">
                  <div className="flex items-center gap-1.5">
                    <label className="text-sm font-medium">出参定义</label>
                    <span className="relative group cursor-help">
                      <span className="text-[11px] text-muted-foreground border rounded-full w-4 h-4 inline-flex items-center justify-center hover:bg-muted">?</span>
                      <span className="absolute left-0 bottom-full mb-1 w-64 p-2 rounded-md bg-gray-900 dark:bg-gray-800 text-white dark:text-gray-200 text-[10px] leading-relaxed opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity z-50 shadow-lg">
                        用 <code className="bg-emerald-500/20 px-1 rounded">OUT_参数名=动态值</code> 输出<br/>
                        Shell:   <code className="bg-emerald-500/20 px-1 rounded">OUT_hostname=$(hostname)</code><br/>
                        Python: <code className="bg-emerald-500/20 px-1 rounded">print(f'OUT_hostname={'{hostname}'}')</code><br/>
                        Batch:  <code className="bg-emerald-500/20 px-1 rounded">echo OUT_hostname=%COMPUTERNAME%</code><br/>
                        或末行 JSON: <code className="bg-emerald-500/20 px-1 rounded">{'{"status":"ok"}'}</code>
                      </span>
                    </span>
                  </div>
                  <Button variant="outline" size="sm" className="h-7 text-xs" onClick={() => setOutputParamForm([...outputParamForm, { name: '', type: 'string', description: '' }])}>
                    <Plus className="h-3 w-3 mr-1" />添加参数
                  </Button>
                </div>
                <p className="text-[10px] text-muted-foreground mb-2">脚本中用 <code className="bg-muted px-1 rounded">OUT_变量名=值</code> 输出动态内容，如 <code className="bg-muted px-1 rounded">OUT_hostname=$(hostname)</code>，系统自动提取并格式化</p>
                {outputParamForm.length > 0 && (
                  <div className="space-y-2">
                    {outputParamForm.map((p, i) => (
                      <div key={i} className="flex gap-1 items-center">
                        <Input className="text-xs flex-1" value={p.name} onChange={e => { const pf = [...outputParamForm]; pf[i].name = e.target.value; setOutputParamForm(pf) }} placeholder="参数名" />
                        <select className="text-xs h-8 w-24 rounded-md border border-input bg-background" value={p.type} onChange={e => { const pf = [...outputParamForm]; pf[i].type = e.target.value; setOutputParamForm(pf) }}>
                          <option value="string">string</option>
                          <option value="number">number</option>
                          <option value="boolean">bool</option>
                          <option value="json">json</option>
                        </select>
                        <Input className="text-xs flex-1" value={p.description} onChange={e => { const pf = [...outputParamForm]; pf[i].description = e.target.value; setOutputParamForm(pf) }} placeholder="描述" />
                        <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" onClick={() => setOutputParamForm(outputParamForm.filter((_, pi) => pi !== i))}><Trash2 className="h-3 w-3" /></Button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          ) : viewScript ? (
            <Tabs value={activeTab} onValueChange={setActiveTab}>
              <TabsList>
                <TabsTrigger value="content"><FileText className="h-3 w-3 mr-1" />脚本内容</TabsTrigger>
                <TabsTrigger value="input_params">入参 ({viewScript.input_params?.length || 0})</TabsTrigger>
                <TabsTrigger value="output_params">出参 ({viewScript.output_params?.length || 0})</TabsTrigger>
                <TabsTrigger value="history"><History className="h-3 w-3 mr-1" />执行历史</TabsTrigger>
                <TabsTrigger value="versions"><GitBranch className="h-3 w-3 mr-1" />版本历史</TabsTrigger>
              </TabsList>

              {/* 脚本内容 */}
              <TabsContent value="content" className="mt-3">
                {viewScript.description && (
                  <div className="mb-3 p-3 bg-muted/30 rounded-md">
                    <p className="text-xs text-muted-foreground">{viewScript.description}</p>
                  </div>
                )}
                <div className="flex items-center gap-2 mb-2">
                  <Badge variant="outline" className={cn("text-[10px]", TYPE_COLORS[viewScript.type])}>{typeLabel(viewScript.type)}</Badge>
                  <span className="text-xs text-muted-foreground flex items-center gap-0.5"><Clock className="h-2.5 w-2.5" />{viewScript.timeout}s 超时</span>
                  {viewScript.version > 0 && (
                    <span className="text-xs text-muted-foreground flex items-center gap-0.5"><GitBranch className="h-2.5 w-2.5" />v{viewScript.version}</span>
                  )}
                  {viewScript.run_as && (
                    <span className="text-xs text-muted-foreground">· {viewScript.run_as}</span>
                  )}
                </div>
                <div className="relative group">
                  <div className="absolute top-2 right-2 z-10 opacity-0 group-hover:opacity-100 transition-opacity">
                    <Button variant="outline" size="sm" className="h-7 text-xs" onClick={() => copyToClipboard(viewScript.content)}>
                      <Copy className="h-3 w-3 mr-1" />复制脚本
                    </Button>
                  </div>
                  <CodeEditor
                    value={viewScript.content}
                    language={viewScript.type}
                    readOnly
                    height="380px"
                  />
                </div>
              </TabsContent>

              {/* 入参 */}
              <TabsContent value="input_params" className="mt-3">
                {(!viewScript.input_params || viewScript.input_params.length === 0) ? (
                  <div className="py-8 text-center text-muted-foreground">
                    <Hash className="h-8 w-8 mx-auto mb-2 opacity-30" />
                    <p className="text-sm">无入参定义</p>
                    <p className="text-xs mt-1">脚本无需输入参数，点击「编辑」可添加</p>
                  </div>
                ) : (
                  <div className="space-y-2">
                    {viewScript.input_params.map((p, i) => (
                      <div key={i} className="flex items-center gap-3 p-3 rounded-lg border border-gray-100 dark:border-[rgba(255,255,255,0.04)] bg-gray-50/50 dark:bg-transparent">
                        <div className="flex items-center gap-2 min-w-0 flex-1">
                          <code className="text-xs font-mono bg-muted px-1.5 py-0.5 rounded">{p.name}</code>
                          <Badge variant="outline" className="text-[10px]">{p.type}</Badge>
                          {p.required && <Badge variant="destructive" className="text-[10px]">必填</Badge>}
                          {p.default && <Badge variant="secondary" className="text-[10px]">默认: {p.default}</Badge>}
                        </div>
                        {p.description && <span className="text-xs text-muted-foreground truncate max-w-[240px]">{p.description}</span>}
                        <ChevronRight className="h-3 w-3 text-muted-foreground/30" />
                      </div>
                    ))}
                  </div>
                )}
              </TabsContent>

              {/* 出参 */}
              <TabsContent value="output_params" className="mt-3">
                {(!viewScript.output_params || viewScript.output_params.length === 0) ? (
                  <div className="py-8 text-center text-muted-foreground">
                    <Terminal className="h-8 w-8 mx-auto mb-2 opacity-30" />
                    <p className="text-sm">无出参定义</p>
                    <p className="text-xs mt-1">脚本无结构化输出定义</p>
                  </div>
                ) : (
                  <div className="space-y-2">
                    {viewScript.output_params.map((p, i) => (
                      <div key={i} className="flex items-center gap-3 p-3 rounded-lg border border-gray-100 dark:border-[rgba(255,255,255,0.04)] bg-gray-50/50 dark:bg-transparent">
                        <code className="text-xs font-mono bg-muted px-1.5 py-0.5 rounded">{p.name}</code>
                        <Badge variant="outline" className="text-[10px]">{p.type}</Badge>
                        {p.description && <span className="text-xs text-muted-foreground">{p.description}</span>}
                      </div>
                    ))}
                  </div>
                )}
              </TabsContent>

              {/* 执行历史 */}
              <TabsContent value="history" className="mt-3">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-1.5">
                    {(['all', 'success', 'failed', 'timeout'] as const).map(st => {
                      const active = execStatusFilter === st
                      const labelMap = { all: '全部', success: '成功', failed: '失败', timeout: '超时' }
                      const variantMap: Record<string, any> = {
                        all: active ? 'default' : 'outline',
                        success: active ? 'success' : 'outline',
                        failed: active ? 'destructive' : 'outline',
                        timeout: active ? 'warning' : 'outline',
                      }
                      const count = st === 'all'
                        ? executions.length
                        : st === 'failed'
                          ? executions.filter(h => h.status === 'failed' || h.status === 'error').length
                          : executions.filter(h => h.status === st).length
                      return (
                        <button key={st} onClick={() => setExecStatusFilter(st)} className="p-0">
                          <Badge variant={variantMap[st]} className="text-[10px] cursor-pointer">
                            {labelMap[st]} ({count})
                          </Badge>
                        </button>
                      )
                    })}
                  </div>
                  {autoRefreshing && (
                    <span className="text-[10px] text-muted-foreground flex items-center gap-1">
                      <Loader2 className="h-2.5 w-2.5 animate-spin" />自动刷新中
                    </span>
                  )}
                </div>

                {execLoading ? (
                  <div className="py-8 text-center text-muted-foreground">
                    <Loader2 className="h-6 w-6 mx-auto mb-2 animate-spin opacity-50" />
                    <p className="text-xs">加载执行历史...</p>
                  </div>
                ) : filteredExecutions.length === 0 ? (
                  <div className="py-8 text-center text-muted-foreground">
                    <History className="h-8 w-8 mx-auto mb-2 opacity-30" />
                    <p className="text-sm">{executions.length === 0 ? '暂无执行记录' : '当前筛选无记录'}</p>
                    <p className="text-xs mt-1">执行脚本后记录将显示在此处</p>
                  </div>
                ) : (
                  <div className="space-y-2">
                    {filteredExecutions.map(h => (
                      <div key={h.id} className="rounded-lg border border-gray-100 dark:border-[rgba(255,255,255,0.04)] overflow-hidden">
                        <div
                          className="flex items-center gap-3 p-3 cursor-pointer hover:bg-gray-50/50 dark:hover:bg-transparent"
                          onClick={() => setExpandedExecId(expandedExecId === h.id ? null : h.id)}
                        >
                          <StatusBadge status={h.status} />
                          <div className="flex-1 min-w-0 flex items-center gap-2">
                            <span className="text-xs text-muted-foreground">退出码 <code className="font-mono">{h.exit_code}</code></span>
                            <span className="text-xs text-muted-foreground">·</span>
                            <span className="text-xs text-muted-foreground flex items-center gap-0.5"><Clock className="h-2.5 w-2.5" />{h.duration_ms}ms</span>
                            {h.run_as && (
                              <>
                                <span className="text-xs text-muted-foreground">·</span>
                                <span className="text-xs text-muted-foreground">{h.run_as}</span>
                              </>
                            )}
                          </div>
                          <span className="text-[10px] text-muted-foreground/70 shrink-0">{formatTime(h.started_at)}</span>
                          <ChevronRight className={cn("h-3 w-3 text-muted-foreground/50 transition-transform", expandedExecId === h.id && "rotate-90")} />
                        </div>
                        {expandedExecId === h.id && (
                          <div className="border-t border-gray-100 dark:border-[rgba(255,255,255,0.04)] p-3 space-y-2">
                            {h.params && Object.keys(h.params).length > 0 && (
                              <div className="text-xs">
                                <span className="text-muted-foreground">入参：</span>
                                <code className="font-mono bg-muted px-1.5 py-0.5 rounded ml-1">{JSON.stringify(h.params)}</code>
                              </div>
                            )}
                            {h.output && (
                              <div>
                                <div className="text-[10px] text-muted-foreground mb-1">输出</div>
                                <pre className="text-[11px] font-mono bg-black text-green-400 dark:text-green-300 rounded p-2 overflow-x-auto whitespace-pre-wrap break-all max-h-60 overflow-auto">{h.output}</pre>
                              </div>
                            )}
                            {h.error && (
                              <div>
                                <div className="text-[10px] text-muted-foreground mb-1">错误</div>
                                <pre className="text-[11px] font-mono bg-red-950/40 text-red-400 rounded p-2 overflow-x-auto whitespace-pre-wrap break-all max-h-60 overflow-auto">{h.error}</pre>
                              </div>
                            )}
                            {h.finished_at && (
                              <div className="text-[10px] text-muted-foreground">完成于 {formatTime(h.finished_at)}</div>
                            )}
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </TabsContent>

              {/* 版本历史 */}
              <TabsContent value="versions" className="mt-3">
                <div className="flex items-center justify-between mb-3">
                  <div className="text-xs text-muted-foreground">
                    {versionLoading ? '加载版本历史...' : `共 ${versions.length} 个版本`}
                  </div>
                  <Button
                    variant={diffMode ? "default" : "outline"}
                    size="sm"
                    className="h-7 text-xs"
                    onClick={() => {
                      setDiffMode(!diffMode)
                      setDiffResult([])
                      setDiffFrom('')
                      setDiffTo('')
                    }}
                  >
                    <GitCompare className="h-3 w-3 mr-1" />{diffMode ? '退出比对' : '版本比对'}
                  </Button>
                </div>

                {versionLoading ? (
                  <div className="py-8 text-center text-muted-foreground">
                    <Loader2 className="h-6 w-6 mx-auto mb-2 animate-spin opacity-50" />
                    <p className="text-xs">加载版本历史...</p>
                  </div>
                ) : versions.length === 0 ? (
                  <div className="py-8 text-center text-muted-foreground">
                    <GitBranch className="h-8 w-8 mx-auto mb-2 opacity-30" />
                    <p className="text-sm">暂无版本记录</p>
                    <p className="text-xs mt-1">脚本更新后将自动记录版本</p>
                  </div>
                ) : diffMode ? (
                  <div className="space-y-3">
                    <div className="p-3 rounded-lg border bg-muted/30 space-y-2">
                      <div className="text-xs font-medium">选择比对版本</div>
                      <div className="flex items-center gap-2">
                        <div className="flex-1">
                          <label className="text-[10px] text-muted-foreground">From (旧版本)</label>
                          <select
                            className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs"
                            value={diffFrom}
                            onChange={e => setDiffFrom(e.target.value === '' ? '' : Number(e.target.value))}
                          >
                            <option value="">选择版本</option>
                            {versions.map(v => <option key={v.id} value={v.version}>v{v.version}</option>)}
                          </select>
                        </div>
                        <ChevronRight className="h-4 w-4 text-muted-foreground mt-4" />
                        <div className="flex-1">
                          <label className="text-[10px] text-muted-foreground">To (新版本)</label>
                          <select
                            className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs"
                            value={diffTo}
                            onChange={e => setDiffTo(e.target.value === '' ? '' : Number(e.target.value))}
                          >
                            <option value="">选择版本</option>
                            {versions.map(v => <option key={v.id} value={v.version}>v{v.version}</option>)}
                          </select>
                        </div>
                        <Button
                          size="sm"
                          className="h-8 mt-4"
                          disabled={diffLoading || diffFrom === '' || diffTo === '' || diffFrom === diffTo}
                          onClick={handleDiff}
                        >
                          {diffLoading ? <Loader2 className="h-3 w-3 mr-1 animate-spin" /> : <GitCompare className="h-3 w-3 mr-1" />}
                          比对
                        </Button>
                      </div>
                    </div>

                    {diffResult.length > 0 && (
                      <div className="space-y-2">
                        <div className="flex items-center justify-between">
                          <div className="text-xs font-medium">差异结果 ({diffResult.length} 项变更)</div>
                          <div className="flex gap-1 border rounded-md p-0.5">
                            <button className={cn("px-2 py-0.5 text-xs rounded-sm", diffViewMode === 'unified' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted')} onClick={() => setDiffViewMode('unified')}>逐行差异</button>
                            <button className={cn("px-2 py-0.5 text-xs rounded-sm", diffViewMode === 'side' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted')} onClick={() => setDiffViewMode('side')}>左右对比</button>
                          </div>
                        </div>
                        {diffViewMode === 'side' && (
                          <div className="flex border rounded-md divide-x overflow-hidden" style={{ maxHeight: '500px' }}>
                            <div className="flex-1 min-w-0 overflow-auto">
                              <div className="px-3 py-1.5 bg-red-50 dark:bg-red-950/30 text-xs font-medium border-b">v{diffFrom} (旧)</div>
                              <pre className="text-[11px] font-mono p-2 leading-relaxed whitespace-pre-wrap">{diffFromContent || '(空)'}</pre>
                            </div>
                            <div className="flex-1 min-w-0 overflow-auto">
                              <div className="px-3 py-1.5 bg-emerald-50 dark:bg-emerald-950/30 text-xs font-medium border-b">v{diffTo} (新)</div>
                              <pre className="text-[11px] font-mono p-2 leading-relaxed whitespace-pre-wrap">{diffToContent || '(空)'}</pre>
                            </div>
                          </div>
                        )}
                        {diffViewMode === 'unified' && diffResult.map((d, i) => {
                          if (d.field === 'content') {
                            const lineDiff = computeLineDiff(d.old_value || '', d.new_value || '')
                            return (
                              <div key={i} className="border rounded-md overflow-hidden">
                                <div className="px-3 py-1.5 bg-muted/50 text-xs font-medium border-b flex items-center gap-2">
                                  <Code2 className="h-3 w-3" />content
                                </div>
                                <pre className="text-[11px] font-mono overflow-x-auto bg-black/5 dark:bg-black/30 max-h-80 overflow-auto">
                                  {lineDiff.map((line, li) => (
                                    <div
                                      key={li}
                                      className={cn(
                                        "px-3 whitespace-pre",
                                        line.type === 'add' && "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
                                        line.type === 'del' && "bg-red-500/15 text-red-700 dark:text-red-300 line-through",
                                      )}
                                    >
                                      <span className="select-none mr-2 opacity-60">
                                        {line.type === 'add' ? '+' : line.type === 'del' ? '-' : ' '}
                                      </span>
                                      {line.text || ' '}
                                    </div>
                                  ))}
                                </pre>
                              </div>
                            )
                          }
                          if (d.field === 'input_params' || d.field === 'output_params') {
                            const paramDiff = computeParamDiff(d.old_value || '', d.new_value || '')
                            if (paramDiff) {
                              return (
                                <div key={i} className="border rounded-md overflow-hidden">
                                  <div className="px-3 py-1.5 bg-muted/50 text-xs font-medium border-b flex items-center gap-2">
                                    <Hash className="h-3 w-3" />{d.field}
                                  </div>
                                  <div className="p-2 space-y-1">
                                    {paramDiff.length === 0 ? (
                                      <div className="text-[11px] text-muted-foreground px-2 py-1">参数无实质变化</div>
                                    ) : paramDiff.map((p, pi) => (
                                      <div
                                        key={pi}
                                        className={cn(
                                          "flex items-center gap-2 px-2 py-1 rounded text-[11px] font-mono",
                                          p.type === 'add' && "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
                                          p.type === 'del' && "bg-red-500/15 text-red-700 dark:text-red-300 line-through",
                                          p.type === 'mod' && "bg-amber-500/15 text-amber-700 dark:text-amber-300",
                                        )}
                                      >
                                        <span className="select-none opacity-70 shrink-0">
                                          {p.type === 'add' ? '+ 新增' : p.type === 'del' ? '- 删除' : '~ 修改'}
                                        </span>
                                        <code className="bg-muted/60 px-1 rounded shrink-0">{p.name}</code>
                                        {p.detail && <span className="truncate text-[10px] opacity-80">{p.detail}</span>}
                                      </div>
                                    ))}
                                  </div>
                                </div>
                              )
                            }
                          }
                          return (
                            <div key={i} className="flex items-center gap-2 p-2 border rounded-md text-xs">
                              <code className="font-mono bg-muted px-1.5 py-0.5 rounded shrink-0">{d.field}</code>
                              <span className="text-red-600 dark:text-red-400 line-through truncate flex-1">{d.old_value || '∅'}</span>
                              <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
                              <span className="text-emerald-600 dark:text-emerald-400 truncate flex-1">{d.new_value || '∅'}</span>
                            </div>
                          )
                        })}
                      </div>
                    )}

                    {!diffLoading && diffResult.length === 0 && diffFrom !== '' && diffTo !== '' && diffFrom !== diffTo && (
                      <div className="py-6 text-center text-muted-foreground text-xs">点击"比对"按钮查看差异</div>
                    )}
                  </div>
                ) : (
                  <div className="space-y-2">
                    {versions.map(v => (
                      <div
                        key={v.id}
                        className={cn(
                          "flex items-center gap-3 p-3 rounded-lg border",
                          v.version === viewScript.version
                            ? "border-emerald-500/40 bg-emerald-50/40 dark:bg-emerald-500/5"
                            : "border-gray-100 dark:border-[rgba(255,255,255,0.04)]"
                        )}
                      >
                        <div className="flex items-center gap-2 shrink-0">
                          <Badge variant="outline" className="text-[10px] font-mono">v{v.version}</Badge>
                          {v.version === viewScript.version && (
                            <Badge variant="success" className="text-[10px]"><CheckCircle className="h-2.5 w-2.5 mr-0.5" />当前</Badge>
                          )}
                        </div>
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            {v.change_note ? (
                              <span className="text-xs truncate">{v.change_note}</span>
                            ) : (
                              <span className="text-xs text-muted-foreground italic">无变更说明</span>
                            )}
                          </div>
                          <div className="flex items-center gap-2 mt-0.5">
                            {v.created_by && <span className="text-[10px] text-muted-foreground">{v.created_by}</span>}
                            <span className="text-[10px] text-muted-foreground">{formatTime(v.created_at)}</span>
                          </div>
                        </div>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 text-xs shrink-0"
                          onClick={() => setViewVersion(v)}
                        >
                          <Eye className="h-3 w-3 mr-1" />查看
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
              </TabsContent>
            </Tabs>
          ) : (
            <div className="py-12 text-center text-muted-foreground">
              <Code2 className="h-10 w-10 mx-auto mb-3 opacity-30" />
              <p className="text-sm">未找到该脚本</p>
              <Button variant="outline" size="sm" className="mt-3" onClick={() => router.push('/scripts')}>返回脚本列表</Button>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 执行对话框 */}
      {executeDialogOpen && viewScript && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => !executing && setExecuteDialogOpen(false)} />
          <div className="relative z-50 w-full max-w-lg bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg max-h-[85vh] overflow-y-auto">
            <h3 className="text-lg font-semibold mb-4 flex items-center gap-2">
              <Play className="h-5 w-5 text-emerald-500" />执行脚本
            </h3>
            <div className="space-y-4">
              <div className="p-3 rounded-md bg-muted/30">
                <div className="flex items-center gap-2">
                  <Code2 className={cn("h-4 w-4", SCRIPT_TYPES.find(t => t.value === viewScript.type)?.color || 'text-muted-foreground')} />
                  <span className="text-sm font-medium">{viewScript.name}</span>
                  <Badge variant="outline" className={cn("text-[10px]", TYPE_COLORS[viewScript.type])}>{typeLabel(viewScript.type)}</Badge>
                </div>
              </div>

              {/* 执行模式选择 */}
              <div className="space-y-2">
                <label className="flex items-center gap-2 cursor-pointer p-3 rounded-md bg-muted/30">
                  <input
                    type="checkbox"
                    checked={executeLocal}
                    onChange={e => {
                      setExecuteLocal(e.target.checked)
                      if (e.target.checked) { setExecuteHost(null); setExecuteChannel(''); setExecuteSandbox(false) }
                    }}
                    className="h-4 w-4 rounded border-gray-300 text-emerald-600 focus:ring-emerald-500"
                  />
                  <span className="text-sm font-medium">在 Core 本机执行（无 Agent）</span>
                  <span className="text-[10px] text-muted-foreground">不指定目标主机，直接在 Core 进程所在机器运行</span>
                </label>
                <label className="flex items-center gap-2 cursor-pointer p-3 rounded-md bg-muted/30">
                  <input
                    type="checkbox"
                    checked={executeSandbox}
                    onChange={e => {
                      setExecuteSandbox(e.target.checked)
                      if (e.target.checked) { setExecuteLocal(false); setExecuteHost(null); setExecuteChannel('') }
                    }}
                    className="h-4 w-4 rounded border-gray-300 text-emerald-600 focus:ring-emerald-500"
                  />
                  <span className="text-sm font-medium">沙箱环境执行（隔离环境）</span>
                  <span className="text-[10px] text-muted-foreground">下发到带"沙箱"标签的 Agent，在目标主机隔离环境中运行（如 Python venv）</span>
                </label>
              </div>

              {/* 沙箱环境:选择沙箱 Agent + 依赖 */}
              {executeSandbox && (
                <div className="space-y-2">
                  <label className="text-sm font-medium">沙箱环境</label>
                  {sandboxAgents.length === 0 ? (
                    <p className="text-[11px] text-muted-foreground">暂无可用的沙箱 Agent，请先在「资源纳管」给主机 Agent 打上"沙箱环境"标签。</p>
                  ) : (
                    <select
                      className="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm"
                      value={executeSandboxAgentID}
                      onChange={e => setExecuteSandboxAgentID(e.target.value)}
                    >
                      <option value="">自动挑选在线沙箱 Agent</option>
                      {sandboxAgents.map((a: any) => (
                        <option key={a.id} value={a.id}>{a.name}（{a.address}）· {a.status === 'online' ? '在线' : '离线'}</option>
                      ))}
                    </select>
                  )}
                  {viewScript.type === 'python' && (
                    <div>
                      <label className="text-xs font-medium">Python 依赖（可选，逗号分隔）</label>
                      <Input
                        value={executeRequirements}
                        onChange={e => setExecuteRequirements(e.target.value)}
                        placeholder="如 requests,flask"
                        className="mt-1 h-8 text-xs"
                      />
                    </div>
                  )}
                </div>
              )}

              {/* 目标主机选择 — 未勾选本机/沙箱时必需 */}
              {!executeLocal && !executeSandbox && (
                <div>
                  <label className="text-sm font-medium mb-1 block">
                    目标主机 <span className="text-red-500">*</span>
                  </label>
                  <select
                    className="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm"
                    value={executeHost?.id || ''}
                    onChange={e => {
                      const h = hosts.find(h => h.id === e.target.value)
                      setExecuteHost(h || null)
                      setExecuteChannel('')
                    }}
                  >
                    <option value="">请选择目标主机</option>
                    {hosts.map((h: any) => (
                      <option key={h.id} value={h.id}>{h.name} ({h.address}{h.channels?.length ? ' — ' + h.channels.map((c:string) => c==='agent'?'Agent':c==='ssh'?'SSH':c).join('/') : ''})</option>
                    ))}
                  </select>
                  {hosts.length === 0 && (
                    <p className="text-[10px] text-muted-foreground mt-1">暂无已注册主机，请先在资源纳管中注册，或勾选"在 Core 本机执行"/"沙箱环境执行"</p>
                  )}
                  {executeHost && executeHost.channels?.length > 0 && (
                    <div className="mt-2 flex items-center gap-1.5">
                      <span className="text-[10px] text-muted-foreground shrink-0">通道:</span>
                      {executeHost.channels.map((ch: string) => (
                        <button
                          key={ch}
                          onClick={() => setExecuteChannel(executeChannel === ch ? '' : ch)}
                          className={cn(
                            "text-[10px] px-2 py-0.5 rounded border transition-colors",
                            executeChannel === ch
                              ? "bg-emerald-50 border-emerald-300 text-emerald-700 dark:bg-emerald-500/10 dark:border-emerald-500/30 dark:text-emerald-400"
                              : "border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700"
                          )}
                        >
                          {ch === 'agent' ? '🤖 Agent' : ch === 'ssh' ? '🔑 SSH' : ch}
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              )}

              {viewScript.input_params && viewScript.input_params.length > 0 && (
                <div className="space-y-2">
                  <label className="text-sm font-medium">输入参数</label>
                  {viewScript.input_params.map((p, i) => (
                    <div key={i} className="space-y-1">
                      <div className="flex items-center gap-2">
                        <label className="text-xs font-mono flex-1">
                          {p.name}
                          {p.required && <span className="text-red-500 ml-1">*</span>}
                          <span className="text-muted-foreground ml-1 text-[10px]">({p.type})</span>
                        </label>
                      </div>
                      <Input
                        className="text-xs h-8"
                        value={executeParams[p.name] || ''}
                        onChange={e => setExecuteParams(prev => ({ ...prev, [p.name]: e.target.value }))}
                        placeholder={p.description || `输入 ${p.name}`}
                      />
                    </div>
                  ))}
                </div>
              )}

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="text-sm font-medium">超时(秒)</label>
                  <Input
                    type="number"
                    value={executeTimeout}
                    onChange={e => setExecuteTimeout(parseInt(e.target.value) || 300)}
                  />
                </div>
                <div>
                  <div className="flex items-center gap-1.5 mb-1">
                    <label className="text-sm font-medium">执行用户 (run_as)</label>
                    <span className="relative group cursor-help">
                      <span className="text-[11px] text-muted-foreground border rounded-full w-4 h-4 inline-flex items-center justify-center hover:bg-muted">?</span>
                      <span className="absolute right-0 bottom-full mb-1 w-56 p-2 rounded-md bg-gray-900 dark:bg-gray-800 text-white dark:text-gray-200 text-[10px] leading-relaxed opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity z-50 shadow-lg">
                        <code className="bg-emerald-500/20 px-1 rounded">sudo -u 用户</code> 切换身份<br/>
                        留空 = 当前 Core 用户<br/>
                        需无密码 sudo
                      </span>
                    </span>
                  </div>
                  <Input
                    value={executeRunAs}
                    onChange={e => setExecuteRunAs(e.target.value)}
                    placeholder="留空使用当前用户"
                  />
                </div>
              </div>
            </div>
            <div className="flex justify-end gap-2 mt-6">
              <Button variant="outline" onClick={() => setExecuteDialogOpen(false)} disabled={executing}>取消</Button>
              <Button onClick={handleExecute} disabled={executing || (!executeLocal && !executeSandbox && !executeHost?.id)}>
                {executing ? <><Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />执行中</> : <><Play className="h-3.5 w-3.5 mr-1" />执行</>}
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* 执行结果对话框 */}
      {executeResult && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setExecuteResult(null)} />
          <div className="relative z-50 w-full max-w-2xl bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg max-h-[85vh] overflow-y-auto">
            <h3 className="text-lg font-semibold mb-4 flex items-center gap-2">
              <CheckCircle className="h-5 w-5 text-emerald-500" />执行结果
            </h3>
            <div className="space-y-3">
              <div className="flex items-center gap-2 flex-wrap">
                <StatusBadge status={executeResult.status} />
                <span className="text-xs text-muted-foreground">退出码 <code className="font-mono">{executeResult.exit_code}</code></span>
                <span className="text-xs text-muted-foreground">·</span>
                <span className="text-xs text-muted-foreground flex items-center gap-0.5"><Clock className="h-2.5 w-2.5" />{executeResult.duration_ms}ms</span>
                {executeResult.run_as && (
                  <>
                    <span className="text-xs text-muted-foreground">·</span>
                    <span className="text-xs text-muted-foreground">{executeResult.run_as}</span>
                  </>
                )}
              </div>

              {/* stdin — 展示参数如何传入 */}
              {executeResult.params && Object.keys(executeResult.params).length > 0 && (
                <div>
                  <div className="text-[10px] uppercase tracking-wider text-amber-500 font-medium mb-1 flex items-center gap-1">📥 stdin</div>
                  <pre className="text-[11px] font-mono bg-amber-50 dark:bg-amber-500/5 text-amber-800 dark:text-amber-300 rounded p-3 overflow-x-auto max-h-32 overflow-auto whitespace-pre-wrap break-all">{JSON.stringify(executeResult.params, null, 2)}</pre>
                </div>
              )}

              {/* stdout — 绿色终端风格 */}
              {executeResult.output && (
                <div>
                  <div className="text-[10px] uppercase tracking-wider text-green-500 font-medium mb-1 flex items-center gap-1">📤 stdout</div>
                  <pre className="text-[11px] font-mono bg-black text-green-400 dark:text-green-300 rounded p-3 overflow-x-auto whitespace-pre-wrap break-all max-h-96 overflow-auto">{executeResult.output}</pre>
                </div>
              )}

              {/* 解析后的出参 — 带定义元数据 */}
              {(executeResult.parsed_output && Object.keys(executeResult.parsed_output).length > 0) && (
                <div>
                  <div className="text-[10px] uppercase tracking-wider text-blue-500 font-medium mb-1 flex items-center gap-1">📋 出参结果</div>
                  <div className="space-y-1">
                    {(viewScript?.output_params || []).map(op => {
                      const val = executeResult.parsed_output?.[op.name]
                      const found = val !== undefined
                      return (
                        <div key={op.name} className={cn(
                          "flex items-center gap-3 p-2 rounded text-xs border",
                          found
                            ? "bg-blue-50 dark:bg-blue-500/5 border-blue-100 dark:border-blue-500/10"
                            : "bg-gray-50 dark:bg-white/[0.02] border-gray-100 dark:border-white/[0.03] opacity-50"
                        )}>
                          <span className="font-mono font-medium w-24 shrink-0">{op.name}</span>
                          <Badge variant="outline" className="text-[9px] h-4">{op.type}</Badge>
                          {op.description && <span className="text-[10px] text-muted-foreground flex-1 truncate">{op.description}</span>}
                          <code className={cn("font-mono text-xs shrink-0 max-w-[200px] truncate", found ? "text-emerald-600 dark:text-emerald-400" : "text-muted-foreground")}>
                            {found ? JSON.stringify(val) : '—'}
                          </code>
                        </div>
                      )
                    })}
                    {/* 只显示已定义出参中没匹配到的额外字段 */}
                    {Object.keys(executeResult.parsed_output).filter(k => !(viewScript?.output_params || []).some(op => op.name === k)).map(k => (
                      <div key={k} className="flex items-center gap-3 p-2 rounded text-xs border bg-amber-50 dark:bg-amber-500/5 border-amber-100 dark:border-amber-500/10">
                        <span className="font-mono font-medium w-24 shrink-0">{k}</span>
                        <Badge variant="outline" className="text-[9px] h-4">额外</Badge>
                        <span className="text-[10px] text-muted-foreground flex-1">未在出参中定义</span>
                        <code className="font-mono text-xs text-amber-600 dark:text-amber-400 shrink-0 max-w-[200px] truncate">{JSON.stringify(executeResult.parsed_output?.[k])}</code>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* stderr — 红色终端风格 */}
              {executeResult.error && (
                <div>
                  <div className="text-[10px] uppercase tracking-wider text-red-500 font-medium mb-1 flex items-center gap-1">⚠️ stderr</div>
                  <pre className="text-[11px] font-mono bg-red-950/30 text-red-400 dark:text-red-300 rounded p-3 overflow-x-auto whitespace-pre-wrap break-all max-h-96 overflow-auto">{executeResult.error}</pre>
                </div>
              )}

              {executeResult.finished_at && (
                <div className="text-[10px] text-muted-foreground">完成于 {formatTime(executeResult.finished_at)}</div>
              )}
            </div>
            <div className="flex justify-end gap-2 mt-6">
              <Button variant="outline" onClick={() => setExecuteResult(null)}>关闭</Button>
              {viewScript && activeTab !== 'history' && (
                <Button onClick={() => { setActiveTab('history'); setExecuteResult(null) }}>
                  <History className="h-3 w-3 mr-1" />查看执行历史
                </Button>
              )}
            </div>
          </div>
        </div>
      )}

      {/* 版本查看对话框 */}
      {viewVersion && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setViewVersion(null)} />
          <div className="relative z-50 w-full max-w-3xl bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg max-h-[85vh] overflow-y-auto">
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-lg font-semibold flex items-center gap-2">
                <GitBranch className="h-5 w-5 text-emerald-500" />版本 v{viewVersion.version}
              </h3>
              <Button variant="ghost" size="icon" onClick={() => setViewVersion(null)}><X className="h-4 w-4" /></Button>
            </div>
            <div className="space-y-3">
              {viewVersion.change_note && (
                <div className="p-3 bg-muted/30 rounded-md">
                  <p className="text-xs text-muted-foreground">变更说明</p>
                  <p className="text-sm mt-1">{viewVersion.change_note}</p>
                </div>
              )}
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                {viewVersion.created_by && <span>作者: {viewVersion.created_by}</span>}
                <span>·</span>
                <span>创建于 {formatTime(viewVersion.created_at)}</span>
              </div>
              <div>
                <div className="text-xs font-medium mb-2">脚本内容</div>
                <CodeEditor
                  value={viewVersion.content}
                  language={viewVersion.type}
                  readOnly
                  height="320px"
                />
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
