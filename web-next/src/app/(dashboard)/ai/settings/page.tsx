'use client'

import { useEffect, useState } from 'react'
import { useAIStore } from '@/stores/ai'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { toast } from '@/stores/toast'
import { Bot, Plus, Trash2, Save, Wrench, Brain, Settings2, Terminal, Wand2, AlertCircle, CheckCircle2, Loader2, ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'

export default function AISettingsPage() {
  const { agents, fetchAgents, createAgent } = useAIStore()
  const { theme } = useThemeStore()
  const [activeTab, setActiveTab] = useState('agents')
  const [form, setForm] = useState({ name: '', model_name: '', system_prompt: '' })
  const [sysConfig, setSysConfig] = useState<any>(null)
  const [llmForm, setLlmForm] = useState({ api_key: '', model: '', base_url: '', timeout: 60 })
  const [editing, setEditing] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [editForm, setEditForm] = useState({ name: '', model_name: '', system_prompt: '' })
  const [tools, setTools] = useState<any[]>([])
  const [testing, setTesting] = useState(false)
  const [testStatus, setTestStatus] = useState<string | null>(null)

  useEffect(() => { fetchAgents(); loadTools(); loadSysConfig() }, [])
  
  const loadSysConfig = async () => {
    try {
      const r = await fetch('/api/v1/system/config')
      if (r.ok) { const d = await r.json(); setSysConfig(d?.config || d) }
    } catch {}
  }
  
  const loadTools = async () => {
    try {
      const r = await fetch('/api/v1/ai/tools')
      if (r.ok) { const d = await r.json(); setTools(Array.isArray(d) ? d : d?.tools || []) }
    } catch {}
  }

  const handleCreateAgent = async () => {
    if (!form.name.trim()) return toast.error('请输入 Agent 名称')
    setSaving(true)
    const ok = await createAgent({ name: form.name, model_name: form.model_name, system_prompt: form.system_prompt })
    setSaving(false)
    if (ok) { setForm({ name: '', model_name: '', system_prompt: '' }); fetchAgents() }
  }

  const handleDeleteAgent = async (id: string) => {
    if (!confirm('确定删除该 Agent？')) return
    await fetch(`/api/v1/ai/agents/${id}`, { method: 'DELETE' })
    toast.success('Agent 已删除')
    fetchAgents()
  }

  const handleTestConnection = async () => {
    setTesting(true); setTestStatus(null)
    try {
      const r = await fetch('/api/v1/ai/chat', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ prompt: 'ping', reasoning: false, stream: false }) })
      const d = await r.json()
      setTestStatus(d.content || d.thinking ? '✅ 连接成功！LLM 正常响应' : '❌ ' + (d.error || '未知错误'))
    } catch { setTestStatus('❌ 网络错误，请检查 Base URL') }
    setTesting(false)
  }

  const handleSaveEdit = async (id: string) => {
    setSaving(true)
    await fetch(`/api/v1/ai/agents/${id}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: editForm.name, model_name: editForm.model_name, system_prompt: editForm.system_prompt }),
    })
    toast.success('Agent 已更新')
    setSaving(false); setEditing(null); fetchAgents()
  }

  const handleUpdateLLM = async (key: string, value: string) => {
    setSaving(true)
    try {
      await fetch('/api/v1/system/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ updates: { [`OPSNEXUS_LLM_${key.toUpperCase()}`]: key === 'timeout' ? String(parseInt(value)||60) : value } }),
      })
      toast.success(`${key} 已更新，即时生效`)
      loadSysConfig()
    } catch { toast.error('保存失败') }
    setSaving(false)
  }

  return (
    <div className="p-6 h-[calc(100vh-120px)] flex flex-col">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Settings2 className="h-6 w-6 text-emerald-500" />
            AI 智能体配置
          </h1>
          <p className="text-sm text-muted-foreground">管理 Agent、提示词、模型和 MCP 工具</p>
        </div>
      </div>

      <Tabs value={activeTab} onValueChange={setActiveTab} className="flex-1 flex flex-col min-h-0">
        <TabsList className="mb-4">
          <TabsTrigger value="agents"><Bot className="h-4 w-4 mr-1.5" />智能体</TabsTrigger>
          <TabsTrigger value="prompts"><Wand2 className="h-4 w-4 mr-1.5" />提示词</TabsTrigger>
          <TabsTrigger value="model"><Brain className="h-4 w-4 mr-1.5" />模型</TabsTrigger>
          <TabsTrigger value="tools"><Wrench className="h-4 w-4 mr-1.5" />MCP 工具</TabsTrigger>
        </TabsList>

        {/* 智能体管理 */}
        <TabsContent value="agents" className="flex-1 overflow-auto space-y-4">
          <Card>
            <CardHeader><CardTitle className="text-base">创建智能体</CardTitle><CardDescription>定义 Agent 的名称、模型和系统提示词</CardDescription></CardHeader>
            <CardContent className="space-y-3">
              <Input placeholder="Agent 名称 (如: 运维助手)" value={form.name} onChange={e => setForm({...form, name: e.target.value})} />
              <Input placeholder="模型名 (如: deepseek-chat, 留空使用默认)" value={form.model_name} onChange={e => setForm({...form, model_name: e.target.value})} />
              <textarea placeholder="系统提示词, 定义 Agent 的行为和角色..." value={form.system_prompt} onChange={e => setForm({...form, system_prompt: e.target.value})} rows={4} className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 resize-none" />
              <Button onClick={handleCreateAgent} disabled={saving} className="w-full">
                {saving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Plus className="h-4 w-4 mr-1" />} 创建 Agent
              </Button>
            </CardContent>
          </Card>

          <div className="space-y-2">
            <h3 className="text-sm font-medium">已有智能体 ({agents.length})</h3>
            {agents.length === 0 ? (
              <p className="text-xs text-muted-foreground p-4 text-center">暂无 Agent，上方创建一个</p>
            ) : (
              agents.map((a: any) => (
                <Card key={a.id} className="hover:shadow-sm transition-shadow">
                  <CardContent className="p-4">
                    {editing === a.id ? (
                      <div className="space-y-2">
                        <Input value={editForm.name} onChange={e => setEditForm({...editForm, name: e.target.value})} placeholder="名称" />
                        <Input value={editForm.model_name} onChange={e => setEditForm({...editForm, model_name: e.target.value})} placeholder="模型名" />
                        <textarea rows={3} value={editForm.system_prompt} onChange={e => setEditForm({...editForm, system_prompt: e.target.value})} className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm resize-none" placeholder="系统提示词" />
                        <div className="flex gap-2">
                          <Button size="sm" onClick={() => handleSaveEdit(a.id)} disabled={saving}><Save className="h-3.5 w-3.5 mr-1" />保存</Button>
                          <Button size="sm" variant="outline" onClick={() => setEditing(null)}>取消</Button>
                        </div>
                      </div>
                    ) : (
                      <div className="flex items-center justify-between">
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <Bot className="h-4 w-4 text-emerald-500 shrink-0" />
                            <span className="font-medium text-sm truncate">{a.name}</span>
                            <Badge variant={a.status === 'active' ? 'default' : 'secondary'} className="text-[10px]">{a.status}</Badge>
                          </div>
                          {a.system_prompt && <p className="text-xs text-muted-foreground mt-1 truncate">{a.system_prompt.slice(0, 80)}</p>}
                          {a.model_name && <p className="text-[10px] text-muted-foreground mt-0.5">模型: {a.model_name}</p>}
                        </div>
                        <div className="flex gap-1 shrink-0">
                          <Button variant="ghost" size="sm" onClick={() => { setEditing(a.id); setEditForm({ name: a.name, model_name: a.model_name || '', system_prompt: a.system_prompt || '' }) }}><Save className="h-3.5 w-3.5" /></Button>
                          <Button variant="ghost" size="sm" onClick={() => handleDeleteAgent(a.id)}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                        </div>
                      </div>
                    )}
                  </CardContent>
                </Card>
              ))
            )}
          </div>
        </TabsContent>

        {/* 提示词模板 */}
        <TabsContent value="prompts" className="flex-1 overflow-auto space-y-4">
          <Card>
            <CardHeader><CardTitle className="text-base">默认系统提示词</CardTitle><CardDescription>当 Agent 未单独设置 system_prompt 时使用此默认值</CardDescription></CardHeader>
            <CardContent className="space-y-3">
              <textarea
                defaultValue="你是 OpsOrigin 智能运维助手,帮助用户完成主机巡检、故障排查、报告生成等运维任务。回答应简洁、专业、可操作。"
                rows={5}
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono ring-offset-background resize-none"
              />
              <p className="text-xs text-muted-foreground">
                提示：各 Agent 的独立提示词请在「智能体」Tab 中创建 Agent 时填写 system_prompt 字段，
                或在 Agent 卡片中点击编辑。此处显示的是引擎默认回退提示词（只读参考）。
              </p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle className="text-base">提示词最佳实践</CardTitle></CardHeader>
            <CardContent className="space-y-2 text-sm text-muted-foreground">
              <p>✅ 明确定义角色： "你是一位资深 Linux 运维工程师..."</p>
              <p>✅ 限制输出格式： "使用 Markdown 格式回答，代码块标注语言"</p>
              <p>✅ 指定行为边界： "只回答运维相关问题，拒绝无关请求"</p>
              <p>✅ 结合工具能力： "你可以使用 llm_chat 和 http_get 工具辅助回答"</p>
              <p>⚠️ system_prompt 变更后即时生效，无需重启 Core</p>
            </CardContent>
          </Card>
        </TabsContent>

        {/* 模型配置 */}
        <TabsContent value="model" className="flex-1 overflow-auto space-y-4">
          <Card>
            <CardHeader><CardTitle className="text-base">LLM 后端配置</CardTitle><CardDescription>修改后即时热生效，无需重启 Core</CardDescription></CardHeader>
            <CardContent className="space-y-4">
              <div>
                <label className="text-xs font-medium mb-1 block">API Key</label>
                <div className="flex gap-2">
                  <Input type="password" placeholder="sk-..." value={llmForm.api_key} onChange={e => setLlmForm({...llmForm, api_key: e.target.value})} className="font-mono text-xs" />
                  <Button size="sm" variant="outline" onClick={() => handleUpdateLLM('api_key', llmForm.api_key)} disabled={saving}>保存</Button>
                </div>
              </div>
              <div>
                <label className="text-xs font-medium mb-1 block">模型名称</label>
                <div className="flex gap-2">
                  <Input placeholder="deepseek-chat" value={llmForm.model} onChange={e => setLlmForm({...llmForm, model: e.target.value})} />
                  <Button size="sm" variant="outline" onClick={() => handleUpdateLLM('model', llmForm.model)} disabled={saving}>保存</Button>
                </div>
              </div>
              <div>
                <label className="text-xs font-medium mb-1 block">API Base URL</label>
                <div className="flex gap-2">
                  <Input placeholder="https://api.deepseek.com" value={llmForm.base_url} onChange={e => setLlmForm({...llmForm, base_url: e.target.value})} />
                  <Button size="sm" variant="outline" onClick={() => handleUpdateLLM('base_url', llmForm.base_url)} disabled={saving}>保存</Button>
                </div>
              </div>
              <div>
                <label className="text-xs font-medium mb-1 block">超时 (秒)</label>
                <div className="flex gap-2">
                  <Input type="number" placeholder="60" value={llmForm.timeout} onChange={e => setLlmForm({...llmForm, timeout: parseInt(e.target.value)||60})} />
                  <Button size="sm" variant="outline" onClick={() => handleUpdateLLM('timeout', String(llmForm.timeout))} disabled={saving}>保存</Button>
                </div>
              </div>
              <div className={cn("flex items-center gap-2 text-xs p-3 rounded-lg", theme === 'dark' ? 'bg-emerald-500/5 border border-emerald-500/10' : 'bg-emerald-50 border border-emerald-200')}>
                <CheckCircle2 className="h-4 w-4 text-emerald-500" />
                <span>修改后即时热生效，已连接的 Agent 无需重启</span>
              </div>
              <div className="flex items-center gap-2 pt-2">
                <Button variant="outline" size="sm" onClick={handleTestConnection} disabled={testing}>
                  {testing ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : null} 测试连接
                </Button>
                {testStatus && <span className="text-xs">{testStatus}</span>}
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        {/* MCP 工具 */}
        <TabsContent value="tools" className="flex-1 overflow-auto space-y-4">
          <Card>
            <CardHeader><CardTitle className="text-base">已注册 MCP 工具</CardTitle><CardDescription>Agent 引擎可调用的工具列表</CardDescription></CardHeader>
            <CardContent>
              {tools.length === 0 ? (
                <div className="text-center py-8 text-muted-foreground">
                  <Wrench className="h-8 w-8 mx-auto mb-2 opacity-30" />
                  <p className="text-sm">暂无注册工具</p>
                </div>
              ) : (
                <div className="space-y-2">
                  {tools.map((t: any, i: number) => (
                    <div key={i} className={cn("flex items-center justify-between p-3 rounded-lg", theme === 'dark' ? 'bg-white/[0.02] border border-white/[0.04]' : 'bg-gray-50 border border-gray-100')}>
                      <div>
                        <div className="flex items-center gap-2">
                          <Terminal className="h-4 w-4 text-emerald-500" />
                          <span className="font-medium text-sm">{t.name}</span>
                          {t.description && <span className="text-xs text-muted-foreground">— {t.description}</span>}
                        </div>
                      </div>
                      <Badge variant="outline" className="text-[10px]">MCP</Badge>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle className="text-base">添加业务工具</CardTitle><CardDescription>接入 CMDB/工单/脚本执行等业务能力</CardDescription></CardHeader>
            <CardContent className="space-y-3">
              <div className={cn("flex items-center gap-2 text-xs p-3 rounded-lg", theme === 'dark' ? 'bg-amber-500/5 border border-amber-500/10' : 'bg-amber-50 border border-amber-200')}>
                <AlertCircle className="h-4 w-4 text-amber-500" />
                <span>业务 MCP 工具注册功能已排期（#49），等待 CMDB/ITSM 等模块就位后实现</span>
              </div>
              <p className="text-xs text-muted-foreground">当前内置工具：</p>
              <ul className="text-xs text-muted-foreground space-y-1 ml-4 list-disc">
                <li><code className="text-emerald-600">llm_chat</code> — 纯 LLM 推理（无副作用）</li>
                <li><code className="text-emerald-600">http_get</code> — 只读 HTTP GET 查询</li>
              </ul>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
