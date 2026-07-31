'use client'

import { useEffect, useState, useCallback } from 'react'
import type { TextareaHTMLAttributes } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { useTeams, type AgentTeam, type TeamMode, type TeamRole, type CreateTeamPayload, type TeamMember } from '@/stores/ai'
import { Users, Plus, RefreshCw, Trash2, Edit, Bot, Settings2 } from 'lucide-react'

// ----------------------------- 标签 / 样式映射 -----------------------------

const modeLabels: Record<TeamMode, string> = {
  supervisor: '主管调度',
  parallel: '并行协作',
  sequential: '顺序流转',
  debate: '辩论裁决',
}
const modeBadge: Record<TeamMode, 'default' | 'info' | 'warning' | 'destructive'> = {
  supervisor: 'default',
  parallel: 'info',
  sequential: 'warning',
  debate: 'destructive',
}
const roleLabels: Record<TeamRole, string> = {
  supervisor: '主管',
  member: '成员',
  finalizer: '终结者',
}
const roleBadge: Record<TeamRole, 'info' | 'secondary' | 'success'> = {
  supervisor: 'info',
  member: 'secondary',
  finalizer: 'success',
}

// ----------------------------- 表单类型 -----------------------------

interface MemberForm {
  agent_id: string
  agent_name: string
  role: TeamRole
  can_finalize: boolean
  persona: string
  system_prompt: string
  enable_tools: boolean
  model_name: string
}

interface TeamForm {
  id?: string
  name: string
  description: string
  mode: TeamMode
  system_prompt: string
  enable_tools: boolean
  member_timeout_sec: number
  member_retries: number
  debate_rounds: number
  supervisor_verify: boolean
  members: MemberForm[]
}

function emptyForm(): TeamForm {
  return {
    name: '',
    description: '',
    mode: 'supervisor',
    system_prompt: '',
    enable_tools: false,
    member_timeout_sec: 90,
    member_retries: 1,
    debate_rounds: 2,
    supervisor_verify: false,
    members: [
      { agent_id: '', agent_name: '', role: 'member', can_finalize: false, persona: '', system_prompt: '', enable_tools: false, model_name: '' },
    ],
  }
}

function teamToForm(team: AgentTeam): TeamForm {
  return {
    id: team.id,
    name: team.name,
    description: team.description,
    mode: team.mode,
    system_prompt: team.system_prompt,
    enable_tools: team.enable_tools,
    member_timeout_sec: team.member_timeout_sec || 90,
    member_retries: team.member_retries || 1,
    debate_rounds: team.debate_rounds || 2,
    supervisor_verify: team.supervisor_verify || false,
    members: team.members.length > 0
      ? team.members.map((m: TeamMember) => ({
          agent_id: m.agent_id,
          agent_name: m.agent_name,
          role: m.role,
          can_finalize: m.can_finalize,
          persona: m.persona,
          system_prompt: m.system_prompt,
          enable_tools: m.enable_tools,
          model_name: m.model_name || '',
        }))
      : emptyForm().members,
  }
}

// ----------------------------- 多行输入组件 -----------------------------
// 项目暂无 Textarea 组件，按 Input 的 className 风格派生一个轻量多行控件。

function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={cn(
        'flex w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 min-h-[72px] resize-y',
        className,
      )}
      {...props}
    />
  )
}

// ----------------------------- 页面 -----------------------------

export default function TeamsPage() {
  const { teams, loading, loadTeams, createTeam, updateTeam, deleteTeam } = useTeams()

  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<TeamForm>(emptyForm())

  useEffect(() => {
    loadTeams()
  }, [loadTeams])

  const openCreate = useCallback(() => {
    setForm(emptyForm())
    setDialogOpen(true)
  }, [])

  const openEdit = useCallback((team: AgentTeam) => {
    setForm(teamToForm(team))
    setDialogOpen(true)
  }, [])

  const updateMember = (i: number, patch: Partial<MemberForm>) => {
    setForm(f => ({
      ...f,
      members: f.members.map((m, idx) => (idx === i ? { ...m, ...patch } : m)),
    }))
  }
  const addMember = () => {
    setForm(f => ({
      ...f,
      members: [
        ...f.members,
        { agent_id: '', agent_name: '', role: 'member', can_finalize: false, persona: '', system_prompt: '', enable_tools: false, model_name: '' },
      ],
    }))
  }
  const removeMember = (i: number) => {
    setForm(f => ({ ...f, members: f.members.filter((_, idx) => idx !== i) }))
  }

  const handleSave = async () => {
    if (!form.name.trim()) return
    const payload: CreateTeamPayload = {
      name: form.name.trim(),
      description: form.description.trim(),
      mode: form.mode,
      system_prompt: form.system_prompt,
      enable_tools: form.enable_tools,
      member_timeout_sec: form.member_timeout_sec,
      member_retries: form.member_retries,
      debate_rounds: form.debate_rounds,
      supervisor_verify: form.supervisor_verify,
      members: form.members.map(m => ({
        agent_id: m.agent_id.trim(),
        agent_name: m.agent_name.trim(),
        role: m.role,
        can_finalize: m.can_finalize,
        persona: m.persona,
        system_prompt: m.system_prompt,
        enable_tools: m.enable_tools,
        model_name: m.model_name.trim(),
      })),
    }
    if (form.id) {
      await updateTeam(form.id, payload)
    } else {
      await createTeam(payload)
    }
    setDialogOpen(false)
    setForm(emptyForm())
  }

  const handleDelete = async (id: string) => {
    if (typeof window !== 'undefined' && !window.confirm('确认删除该团队？此操作不可撤销。')) return
    await deleteTeam(id)
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Users className="h-6 w-6 text-emerald-500" />
            多智能体团队
          </h1>
          <p className="text-sm text-muted-foreground">编排多个 AI 代理协同完成复杂运维任务（主管调度 / 并行 / 顺序 / 辩论）</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => loadTeams()} disabled={loading}>
            <RefreshCw className={`h-4 w-4 mr-1 ${loading ? 'animate-spin' : ''}`} />刷新
          </Button>
          <Button size="sm" onClick={openCreate}><Plus className="h-4 w-4 mr-1" />新建团队</Button>
        </div>
      </div>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">团队列表（{teams.length}）</CardTitle>
          <CardDescription>点击「编辑」修改团队配置，包含模式与成员编排</CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          {loading ? (
            <p className="px-4 py-6 text-sm text-muted-foreground">加载中...</p>
          ) : teams.length === 0 ? (
            <p className="px-4 py-6 text-sm text-muted-foreground">暂无团队，点击「新建团队」开始创建。</p>
          ) : (
            <div className="flex flex-col">
              {teams.map(team => (
                <div
                  key={team.id}
                  className="border-b last:border-0 px-4 py-3 hover:bg-accent transition-colors flex items-center justify-between gap-3"
                >
                  <div className="flex items-center gap-3 min-w-0">
                    <Bot className="h-4 w-4 text-emerald-500 shrink-0" />
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium truncate">{team.name}</span>
                        <Badge variant={modeBadge[team.mode]} className="text-[10px]">{modeLabels[team.mode]}</Badge>
                      </div>
                      <p className="text-xs text-muted-foreground mt-0.5 truncate">{team.description || '无描述'}</p>
                      <div className="flex gap-1.5 mt-1 flex-wrap">
                        {team.members.slice(0, 6).map((m, i) => (
                          <Badge key={`${m.agent_id || i}-${m.agent_name}`} variant={roleBadge[m.role]} className="text-[10px]">
                            {m.agent_name || roleLabels[m.role]}
                          </Badge>
                        ))}
                        {team.members.length > 6 && (
                          <Badge variant="secondary" className="text-[10px]">+{team.members.length - 6}</Badge>
                        )}
                        {team.members.length === 0 && (
                          <Badge variant="secondary" className="text-[10px]">无成员</Badge>
                        )}
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <span className="text-[10px] text-muted-foreground font-mono hidden md:inline">{team.id.slice(0, 8)}</span>
                    <Button variant="outline" size="sm" onClick={() => openEdit(team)}>
                      <Edit className="h-3.5 w-3.5 mr-1" />编辑
                    </Button>
                    <Button
                      variant="ghost" size="icon" className="h-7 w-7"
                      onClick={() => handleDelete(team.id)}
                    >
                      <Trash2 className="h-3 w-3 text-muted-foreground hover:text-destructive" />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* 新建 / 编辑团队 Dialog */}
      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={form.id ? '编辑团队' : '新建团队'}>
        <div className="max-h-[72vh] overflow-y-auto pr-1 space-y-4">
          {/* 基础信息 */}
          <div className="space-y-3">
            <div>
              <label className="text-sm font-medium">团队名称 *</label>
              <Input
                value={form.name}
                onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="例如：平台交付团队"
                onKeyDown={e => e.key === 'Enter' && !form.id && handleSave()}
              />
            </div>
            <div>
              <label className="text-sm font-medium">描述</label>
              <Input
                value={form.description}
                onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
                placeholder="一句话描述团队用途"
              />
            </div>
            <div>
              <label className="text-sm font-medium">协作模式</label>
              <select
                className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                value={form.mode}
                onChange={e => setForm(f => ({ ...f, mode: e.target.value as TeamMode }))}
              >
                <option value="supervisor">主管调度（supervisor）</option>
                <option value="parallel">并行协作（parallel）</option>
                <option value="sequential">顺序流转（sequential）</option>
                <option value="debate">辩论裁决（debate）</option>
              </select>
            </div>
            <div>
              <label className="text-sm font-medium">团队系统提示词</label>
              <Textarea
                value={form.system_prompt}
                onChange={e => setForm(f => ({ ...f, system_prompt: e.target.value }))}
                placeholder="团队整体的目标与约束（可选）"
              />
            </div>

            {/* 团队级编排配置 */}
            <div className="border rounded-lg p-3 space-y-2">
              <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">编排参数</label>
              <div className="grid grid-cols-2 gap-2">
                <div>
                  <label className="text-[11px] text-muted-foreground">超时(秒)</label>
                  <Input
                    type="number" min={10} max={600}
                    value={form.member_timeout_sec}
                    onChange={e => setForm(f => ({ ...f, member_timeout_sec: parseInt(e.target.value) || 90 }))}
                  />
                </div>
                <div>
                  <label className="text-[11px] text-muted-foreground">重试次数</label>
                  <Input
                    type="number" min={0} max={5}
                    value={form.member_retries}
                    onChange={e => setForm(f => ({ ...f, member_retries: parseInt(e.target.value) || 1 }))}
                  />
                </div>
              </div>
              {form.mode === 'debate' && (
                <div>
                  <label className="text-[11px] text-muted-foreground">辩论轮数</label>
                  <Input
                    type="number" min={1} max={10}
                    value={form.debate_rounds}
                    onChange={e => setForm(f => ({ ...f, debate_rounds: parseInt(e.target.value) || 2 }))}
                  />
                </div>
              )}
              <div className="flex items-center gap-3 flex-wrap">
                <label className="flex items-center gap-1 text-xs cursor-pointer">
                  <input type="checkbox" checked={form.enable_tools} onChange={e => setForm(f => ({ ...f, enable_tools: e.target.checked }))} />
                  团队启用工具调用
                </label>
                <label className="flex items-center gap-1 text-xs cursor-pointer">
                  <input type="checkbox" checked={form.supervisor_verify} onChange={e => setForm(f => ({ ...f, supervisor_verify: e.target.checked }))} />
                  主管审核修订
                </label>
              </div>
            </div>
          </div>

          {/* 成员编辑 */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-sm font-medium flex items-center gap-1">
                <Settings2 className="h-3.5 w-3.5" />成员（{form.members.length}）
              </label>
              <Button variant="outline" size="sm" onClick={addMember}><Plus className="h-3.5 w-3.5 mr-1" />添加成员</Button>
            </div>

            {form.members.map((m, i) => (
              <div key={i} className="rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.06)] p-3 space-y-2 relative">
                <div className="flex items-center gap-2">
                  <Input
                    className="flex-1"
                    value={m.agent_name}
                    onChange={e => updateMember(i, { agent_name: e.target.value })}
                    placeholder="成员名称"
                  />
                  <select
                    className="h-10 rounded-md border border-input bg-background px-2 py-2 text-sm"
                    value={m.role}
                    onChange={e => updateMember(i, { role: e.target.value as TeamRole })}
                  >
                    <option value="supervisor">主管</option>
                    <option value="member">成员</option>
                    <option value="finalizer">终结者</option>
                  </select>
                  <label className="flex items-center gap-1 text-xs text-muted-foreground shrink-0" title="是否可终结对话">
                    <input
                      type="checkbox"
                      checked={m.can_finalize}
                      onChange={e => updateMember(i, { can_finalize: e.target.checked })}
                    />
                    可终结
                  </label>
                  {form.members.length > 1 && (
                    <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => removeMember(i)}>
                      <Trash2 className="h-3 w-3 text-muted-foreground hover:text-destructive" />
                    </Button>
                  )}
                </div>
                <Input
                  value={m.agent_id}
                  onChange={e => updateMember(i, { agent_id: e.target.value })}
                  placeholder="agent_id（关联后端代理标识）"
                />
                <div>
                  <label className="text-[11px] text-muted-foreground">人设（persona）</label>
                  <Textarea
                    className="min-h-[52px]"
                    value={m.persona}
                    onChange={e => updateMember(i, { persona: e.target.value })}
                    placeholder="该成员的角色定位与口吻"
                  />
                </div>
                <div>
                  <label className="text-[11px] text-muted-foreground">系统提示词（system_prompt）</label>
                  <Textarea
                    className="min-h-[52px]"
                    value={m.system_prompt}
                    onChange={e => updateMember(i, { system_prompt: e.target.value })}
                    placeholder="该成员专属的指令与约束"
                  />
                </div>
                <div className="flex items-center gap-3 flex-wrap">
                  <label className="flex items-center gap-1 text-xs cursor-pointer">
                    <input type="checkbox" checked={m.enable_tools} onChange={e => updateMember(i, { enable_tools: e.target.checked })} />
                    启用工具调用
                  </label>
                  <div className="flex items-center gap-1">
                    <label className="text-[11px] text-muted-foreground shrink-0">模型</label>
                    <Input
                      className="h-7 w-32 text-xs"
                      value={m.model_name}
                      onChange={e => updateMember(i, { model_name: e.target.value })}
                      placeholder="覆盖默认模型"
                    />
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
          <Button onClick={handleSave} disabled={!form.name.trim()}>{form.id ? '保存修改' : '创建团队'}</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
