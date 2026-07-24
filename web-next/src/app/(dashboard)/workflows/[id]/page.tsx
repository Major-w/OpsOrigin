'use client'

import { useEffect, useState, useMemo, useCallback } from 'react'
import { useParams, useRouter } from 'next/navigation'
import {
  ReactFlow, Controls, Background, BackgroundVariant, MiniMap,
  type Node, type Edge, type Connection, type NodeProps,
  Handle, Position, ReactFlowProvider,
  MarkerType,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import {
  useWorkflowStore,
  type Workflow,
  type WorkflowStep,
  type StepType,
  type StepApproval,
  type WorkflowExecution,
} from '@/stores/workflow'
import { useAgentStore } from '@/stores/agent'
import { useScriptStore } from '@/stores/script'
import { useNotificationStore } from '@/stores/notification'
import { useFileStore } from '@/stores/file'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatTime } from '@/lib/utils'
import {
  Plus, Trash2, Play, GitBranch, Workflow as WorkflowIcon,
  Code, CheckSquare, Split, Clock, Save, X, Flag, Check, Ban,
  ChevronLeft, Loader2, Pencil, Bell, FolderTree, Infinity,
  Globe, MessageSquare, FileDown, PlayCircle, StopCircle,
} from 'lucide-react'

// ---------- helpers ----------
const NODE_META: Record<StepType, { label: string; icon: any; color: string; bg: string }> = {
  start:     { label: '开始',      icon: PlayCircle,  color: '#10b981', bg: '#ecfdf5' },
  end:       { label: '结束',      icon: StopCircle,  color: '#ef4444', bg: '#fef2f2' },
  script:    { label: '脚本节点',  icon: Code,        color: '#3b82f6', bg: '#eff6ff' },
  approval:  { label: '人工审批',  icon: CheckSquare, color: '#f59e0b', bg: '#fffbeb' },
  condition: { label: '分支网关',  icon: GitBranch,   color: '#8b5cf6', bg: '#faf5ff' },
  parallel:  { label: '并行网关',  icon: Split,       color: '#06b6d4', bg: '#ecfeff' },
  wait:      { label: '等待节点',  icon: Clock,       color: '#64748b', bg: '#f8fafc' },
  notify:    { label: '通知节点',  icon: Bell,        color: '#f97316', bg: '#fff7ed' },
  subflow:   { label: '子流程',    icon: FolderTree,  color: '#8b5cf6', bg: '#faf5ff' },
  loop:      { label: '循环节点',  icon: Infinity,    color: '#06b6d4', bg: '#ecfeff' },
  webhook:   { label: 'Webhook',   icon: Globe,       color: '#6366f1', bg: '#eef2ff' },
  comment:   { label: '注释',      icon: MessageSquare, color: '#94a3b8', bg: '#f1f5f9' },
  filedist:  { label: '文件分发',  icon: FileDown,    color: '#14b8a6', bg: '#f0fdfa' },
}

function newStep(type: StepType, idx: number): WorkflowStep {
  return {
    id: `${type}_${Date.now().toString(36)}_${idx}`,
    name: NODE_META[type].label,
    type,
    position: { x: 80 + (idx % 3) * 260, y: 60 + Math.floor(idx / 3) * 140 },
    ...(type === 'script' ? { timeout: 300 } : {}),
    ...(type === 'wait' ? { wait_seconds: 10 } : {}),
    ...(type === 'approval' ? { approvers: [] } : {}),
    ...(type === 'end' ? { end_status: 'success' as const } : {}),
    ...(type === 'notify' ? { notify_channel: 'dingtalk' } : {}),
    ...(type === 'loop' ? { loop_over: '$items', loop_max: 100 } : {}),
    ...(type === 'webhook' ? { webhook_method: 'POST' } : {}),
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
  return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB'
}

// ====== ReactFlow 自定义节点类型 ======

const BASE_NODE = 'shadow-md rounded-xl border-2 bg-white dark:bg-gray-800 min-w-[180px]'
const HEADER = 'flex items-center gap-2 px-3 py-2 rounded-t-xl text-white font-medium text-xs'

function BaseNode({ color, icon: Icon, label, children, ports }: {
  color: string; icon: any; label: string; children: React.ReactNode
  ports?: { type: 'source' | 'target'; position: Position; id?: string; className?: string }[]
}) {
  return (
    <div className={BASE_NODE} style={{ borderColor: color }}>
      <Handle type="target" position={Position.Top} style={{ background: color, width: 10, height: 10 }} />
      <div className={HEADER} style={{ backgroundColor: color }}><Icon className="h-3.5 w-3.5" />{label}</div>
      <div className="px-3 py-2 text-[11px] text-muted-foreground">{children}</div>
      {ports?.map((p, i) => (
        <Handle key={i} type={p.type} position={p.position} id={p.id} className={p.className}
          style={!p.className ? { background: color, width: 10, height: 10 } : undefined} />
      ))}
      {!ports && <Handle type="source" position={Position.Bottom} style={{ background: color, width: 10, height: 10 }} />}
    </div>
  )
}

function StartNode({ data }: NodeProps) {
  return (
    <div className="shadow-md rounded-full border-2 bg-white dark:bg-gray-800 min-w-[120px] w-[120px] h-[60px] flex items-center justify-center" style={{ borderColor: NODE_META.start.color }}>
      <PlayCircle className="h-5 w-5 mr-1.5" style={{ color: NODE_META.start.color }} />
      <span className="text-xs font-semibold">{data.label}</span>
      <Handle type="source" position={Position.Bottom} style={{ background: NODE_META.start.color, width: 10, height: 10 }} />
    </div>
  )
}

function EndNode({ data }: NodeProps) {
  const statusColor = data.endStatus === 'failure' ? '#ef4444' : data.endStatus === 'timeout' ? '#f59e0b' : '#10b981'
  return (
    <div className="shadow-md rounded-full border-2 bg-white dark:bg-gray-800 min-w-[120px] w-[120px] h-[60px] flex items-center justify-center" style={{ borderColor: statusColor }}>
      <StopCircle className="h-5 w-5 mr-1.5" style={{ color: statusColor }} />
      <span className="text-xs font-semibold">{data.label}</span>
      <Handle type="target" position={Position.Top} style={{ background: statusColor, width: 10, height: 10 }} />
    </div>
  )
}

function ScriptNode({ data }: NodeProps) {
  const Icon = NODE_META.script.icon
  const color = NODE_META.script.color
  return (
    <div className={BASE_NODE} style={{ borderColor: color }}>
      <Handle type="target" position={Position.Top} style={{ background: color, width: 10, height: 10 }} />
      <div className={HEADER} style={{ backgroundColor: color }}><Icon className="h-3.5 w-3.5" />{data.label}</div>
      <div className="px-3 py-2 text-[11px] text-muted-foreground space-y-0.5">
        {data.scriptName && <p className="truncate">📜 {data.scriptName}</p>}
        <p>⏱ {data.timeout || 300}s</p>
      </div>
      {data.outputParams?.length > 0 && (
        <div className="px-2 pb-2">
          {data.outputParams.map((p: any) => (
            <div key={p.name} className="flex items-center gap-1.5 text-[10px]">
              <Handle type="source" position={Position.Bottom} id={p.name}
                className="!relative !left-auto !right-auto !transform-none !w-2 !h-2 !bg-emerald-500 !border-0" />
              <span className="text-muted-foreground">{p.name}</span>
            </div>
          ))}
        </div>
      )}
      {(!data.outputParams || data.outputParams.length === 0) && (
        <Handle type="source" position={Position.Bottom} style={{ background: color, width: 10, height: 10 }} />
      )}
    </div>
  )
}

function ApprovalNode({ data }: NodeProps) {
  return <BaseNode color={NODE_META.approval.color} icon={NODE_META.approval.icon} label={data.label}>
    <p>👥 {data.approvers?.length || 0} 人审批</p>
    {data.approvalMsg && <p className="truncate">💬 {data.approvalMsg}</p>}
  </BaseNode>
}

function ConditionNode({ data }: NodeProps) {
  const Icon = NODE_META.condition.icon; const color = NODE_META.condition.color
  return (
    <div className={`${BASE_NODE} rotate-0`} style={{ borderColor: color }}>
      <Handle type="target" position={Position.Top} style={{ background: color, width: 10, height: 10 }} />
      <div className={HEADER} style={{ backgroundColor: color }}><Icon className="h-3.5 w-3.5" />{data.label}</div>
      <div className="px-3 py-2 text-[11px] font-mono text-muted-foreground truncate">{data.condition || '未设置'}</div>
      <div className="flex border-t" style={{ borderColor: color }}>
        <div className="flex-1 text-center py-1 text-[10px] text-emerald-600 relative">
          ✅ 满足<Handle type="source" position={Position.Bottom} id="on_success" className="!w-2.5 !h-2.5 !bg-emerald-500 !bottom-0.5" />
        </div>
        <div className="flex-1 text-center py-1 text-[10px] text-red-500 relative">
          ❌ 不满足<Handle type="source" position={Position.Bottom} id="on_failure" className="!w-2.5 !h-2.5 !bg-red-500 !bottom-0.5" />
        </div>
      </div>
    </div>
  )
}

function ParallelNode({ data }: NodeProps) {
  return <BaseNode color={NODE_META.parallel.color} icon={NODE_META.parallel.icon} label={data.label}>
    <p>⚡ 并行 {data.parallelCount || 0} 步</p>
  </BaseNode>
}

function WaitNode({ data }: NodeProps) {
  return <BaseNode color={NODE_META.wait.color} icon={NODE_META.wait.icon} label={data.label}>
    <p>⏳ 等待 {data.waitSeconds || 0}s</p>
  </BaseNode>
}

function NotifyNode({ data }: NodeProps) {
  return <BaseNode color={NODE_META.notify.color} icon={NODE_META.notify.icon} label={data.label}>
    <p>📡 {data.notifyChannelName || data.notifyChannel || '未选择渠道'}</p>
    {data.notifyTitle && <p className="truncate">📝 {data.notifyTitle}</p>}
  </BaseNode>
}

function SubflowNode({ data }: NodeProps) {
  return <BaseNode color={NODE_META.subflow.color} icon={NODE_META.subflow.icon} label={data.label}>
    <p>🔗 {data.subflowName || data.subflowId?.slice(0, 12) || '未选择'}</p>
  </BaseNode>
}

function LoopNode({ data }: NodeProps) {
  return <BaseNode color={NODE_META.loop.color} icon={NODE_META.loop.icon} label={data.label}>
    <p>🔁 遍历: {data.loopOver || '$items'}</p>
    <p>🔢 最大 {data.loopMax || 100} 次</p>
  </BaseNode>
}

function WebhookNode({ data }: NodeProps) {
  return <BaseNode color={NODE_META.webhook.color} icon={NODE_META.webhook.icon} label={data.label}>
    <p className="truncate">{data.webhookMethod || 'POST'} {data.webhookUrl ? '✔' : '未配置'}</p>
  </BaseNode>
}

function CommentNode({ data }: NodeProps) {
  return (
    <div className="border-2 border-dashed rounded-lg bg-yellow-50/50 dark:bg-yellow-950/20 min-w-[180px]"
      style={{ borderColor: NODE_META.comment.color }}>
      <div className="px-3 py-2 text-[11px] text-muted-foreground italic whitespace-pre-wrap">
        💬 {data.commentText || '(双击编辑)'}
      </div>
    </div>
  )
}

function FiledistNode({ data }: NodeProps) {
  return <BaseNode color={NODE_META.filedist.color} icon={NODE_META.filedist.icon} label={data.label}>
    <p>📦 {data.filedistFileName || '未选择文件'}</p>
    <p>🎯 {data.filedistTargets?.length || 0} 台主机</p>
    {data.filedistDownloadDir && <p>📁 {data.filedistDownloadDir}</p>}
  </BaseNode>
}

const nodeTypes = {
  start: StartNode, end: EndNode,
  script: ScriptNode, approval: ApprovalNode,
  condition: ConditionNode, parallel: ParallelNode, wait: WaitNode,
  notify: NotifyNode, subflow: SubflowNode, loop: LoopNode,
  webhook: WebhookNode, comment: CommentNode, filedist: FiledistNode,
}

// ====== 步骤属性面板 ======
function StepPanel({ step, steps, scripts, channels, workflows, files, agents, onChange }: {
  step: WorkflowStep
  steps: WorkflowStep[]
  scripts: any[]
  channels: any[]
  workflows: any[]
  files: any[]
  agents: any[]
  onChange: (p: Partial<WorkflowStep>) => void
}) {
  const meta = NODE_META[step.type]
  const IconEl = meta.icon
  const otherSteps = steps.filter(s => s.id !== step.id)
  const ctxVars = steps.filter(s => s.id !== step.id).map(s => ({ id: s.id, name: s.name }))

  return (
    <Card>
      <CardHeader className="py-3">
        <div className="flex items-center gap-2">
          <IconEl className="h-4 w-4" />
          <CardTitle className="text-sm">{meta.label} 属性</CardTitle>
        </div>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <div>
          <label className="text-xs font-medium">节点名称</label>
          <Input value={step.name} onChange={e => onChange({ name: e.target.value })} className="h-8 mt-1" />
        </div>
        <div>
          <label className="text-xs font-medium">超时(秒)</label>
          <Input type="number" value={step.timeout || 0}
            onChange={e => onChange({ timeout: parseInt(e.target.value) || 0 })} className="h-8 mt-1" />
        </div>

        {step.type === 'script' && (
          <div>
            <label className="text-xs font-medium">关联脚本</label>
            <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
              value={step.script_id || ''} onChange={e => onChange({ script_id: e.target.value })}>
              <option value="">选择脚本</option>
              {scripts.map((s: any) => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </div>
        )}

        {step.type === 'condition' && (
          <div className="space-y-2">
            <label className="text-xs font-medium">条件表达式</label>
            <p className="text-[10px] text-muted-foreground">支持 == != &gt;= &lt;= &gt; &lt; =~ &amp;&amp; ||</p>
            <details className="text-[10px]">
              <summary className="cursor-pointer text-muted-foreground">插入上游节点变量</summary>
              <div className="mt-1 space-y-0.5">
                {ctxVars.map(v => (
                  <button key={v.id} onClick={() => onChange({ condition: (step.condition || '') + ' ' + v.name })}
                    className="block text-blue-500 hover:underline text-left">{v.name}</button>
                ))}
              </div>
            </details>
            <textarea
              className="flex h-16 w-full rounded-md border border-input bg-background px-2 py-1 text-xs font-mono"
              value={step.condition || ''}
              onChange={e => onChange({ condition: e.target.value })}
              placeholder="exit_code == 0"
            />
          </div>
        )}

        {step.type === 'wait' && (
          <div>
            <label className="text-xs font-medium">等待秒数</label>
            <Input type="number" value={step.wait_seconds || 0}
              onChange={e => onChange({ wait_seconds: parseInt(e.target.value) || 0 })} className="h-8 mt-1" />
          </div>
        )}

        {step.type === 'approval' && (
          <div>
            <label className="text-xs font-medium">审批消息</label>
            <Input value={step.approval_msg || ''} onChange={e => onChange({ approval_msg: e.target.value })}
              placeholder="请审批本次操作" className="h-8 mt-1" />
          </div>
        )}

        {step.type === 'parallel' && (
          <div className="text-xs text-muted-foreground bg-muted/30 rounded p-2">
            并行节点下所有后继节点将同时执行
          </div>
        )}

        {step.type === 'start' && (
          <div className="text-xs text-muted-foreground bg-muted/30 rounded p-2">流程入口，无需配置</div>
        )}

        {step.type === 'end' && (
          <div>
            <label className="text-xs font-medium">结束状态</label>
            <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
              value={step.end_status || 'success'} onChange={e => onChange({ end_status: e.target.value as any })}>
              <option value="success">✅ 成功</option>
              <option value="failure">❌ 失败</option>
              <option value="timeout">⏱ 超时</option>
            </select>
          </div>
        )}

        {step.type === 'notify' && (
          <>
            <div>
              <label className="text-xs font-medium">通知渠道</label>
              <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
                value={step.notify_channel || ''} onChange={e => onChange({ notify_channel: e.target.value })}>
                <option value="">选择已有渠道</option>
                {channels.filter((c: any) => c.enabled).map((c: any) => (
                  <option key={c.id} value={c.id}>{c.name} ({c.type})</option>
                ))}
              </select>
            </div>
            <div>
              <label className="text-xs font-medium">通知标题</label>
              <Input value={step.notify_title || ''} onChange={e => onChange({ notify_title: e.target.value })}
                placeholder="如: [告警] 磁盘使用率超阈值" className="h-8 mt-1" />
            </div>
            <div>
              <label className="text-xs font-medium">通知内容（支持变量 {'${step.xxx}'}）</label>
              <textarea className="flex h-16 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1"
                value={step.notify_body || ''} onChange={e => onChange({ notify_body: e.target.value })}
                placeholder="磁盘已使用 ${step1.usage}%..." />
            </div>
          </>
        )}

        {step.type === 'subflow' && (
          <div>
            <label className="text-xs font-medium">选择子流程</label>
            <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
              value={step.subflow_id || ''}
              onChange={e => {
                const wf = workflows.find((w: any) => w.id === e.target.value)
                onChange({ subflow_id: e.target.value, subflow_name: wf?.name || '' })
              }}>
              <option value="">选择已有流程</option>
              {workflows.map((w: any) => (
                <option key={w.id} value={w.id}>{w.name} ({w.steps?.length || 0} 步骤)</option>
              ))}
            </select>
          </div>
        )}

        {step.type === 'loop' && (
          <>
            <div>
              <label className="text-xs font-medium">循环变量名</label>
              <Input value={step.loop_over || '$items'} onChange={e => onChange({ loop_over: e.target.value })}
                placeholder="$items" className="h-8 mt-1" />
            </div>
            <div>
              <label className="text-xs font-medium">最大循环次数</label>
              <Input type="number" value={step.loop_max || 100}
                onChange={e => onChange({ loop_max: parseInt(e.target.value) || 100 })} className="h-8 mt-1" />
            </div>
          </>
        )}

        {step.type === 'webhook' && (
          <>
            <div>
              <label className="text-xs font-medium">请求方法</label>
              <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
                value={step.webhook_method || 'POST'} onChange={e => onChange({ webhook_method: e.target.value })}>
                <option value="GET">GET</option>
                <option value="POST">POST</option>
                <option value="PUT">PUT</option>
              </select>
            </div>
            <div>
              <label className="text-xs font-medium">URL</label>
              <Input value={step.webhook_url || ''} onChange={e => onChange({ webhook_url: e.target.value })}
                placeholder="https://hooks.example.com/callback" className="h-8 mt-1" />
            </div>
            <div>
              <label className="text-xs font-medium">Headers (JSON, 可选)</label>
              <textarea className="flex h-12 w-full rounded-md border border-input bg-background px-2 py-1 text-xs font-mono mt-1"
                value={step.webhook_headers ? JSON.stringify(step.webhook_headers) : ''}
                onChange={e => { try { onChange({ webhook_headers: JSON.parse(e.target.value) }) } catch { } }}
                placeholder='{"Authorization":"Bearer xxx"}' />
            </div>
            <div>
              <label className="text-xs font-medium">Body (JSON, 可选)</label>
              <textarea className="flex h-16 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1"
                value={step.webhook_body || ''} onChange={e => onChange({ webhook_body: e.target.value })}
                placeholder='{"status":"ok","step":"${step1.name}"}' />
            </div>
          </>
        )}

        {step.type === 'comment' && (
          <div>
            <label className="text-xs font-medium">注释内容</label>
            <textarea className="flex h-24 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1"
              value={step.comment_text || ''} onChange={e => onChange({ comment_text: e.target.value })}
              placeholder="说明此段流程的用途、注意事项等..." />
          </div>
        )}

        {step.type === 'filedist' && (
          <>
            <div>
              <label className="text-xs font-medium">选择文件（可多选）</label>
              <div className="max-h-32 overflow-y-auto border rounded-md mt-1 p-1 space-y-0.5">
                {files.length === 0 ? (
                  <p className="text-[10px] text-muted-foreground p-2">请先在「文件分发」页面上传文件</p>
                ) : (
                  files.map((f: any) => {
                    const ids: string[] = step.filedist_file_id ? step.filedist_file_id.split(',') : []
                    const names: string[] = step.filedist_file_name ? step.filedist_file_name.split('|') : []
                    const checked = ids.includes(f.id)
                    const toggle = () => {
                      if (checked) {
                        const ni = ids.filter(i => i !== f.id)
                        const nn = names.filter(i => i !== f.original_name)
                        onChange({ filedist_file_id: ni.join(','), filedist_file_name: nn.join('|') })
                      } else {
                        onChange({
                          filedist_file_id: [...ids, f.id].join(','),
                          filedist_file_name: [...names, f.original_name || f.id.slice(0, 8)].join('|'),
                        })
                      }
                    }
                    return (
                      <label key={f.id} className="flex items-center gap-1.5 text-[11px] cursor-pointer hover:bg-accent rounded px-1 py-0.5">
                        <input type="checkbox" checked={checked} onChange={toggle} />
                        <span className="truncate">{f.original_name || f.id.slice(0, 8)}</span>
                        {f.size_bytes > 0 && <span className="text-[10px] text-muted-foreground shrink-0">{formatSize(f.size_bytes)}</span>}
                      </label>
                    )
                  })
                )}
              </div>
            </div>
            <div>
              <label className="text-xs font-medium">下载目录</label>
              <Input value={step.filedist_download_dir || '/tmp'} onChange={e => onChange({ filedist_download_dir: e.target.value })}
                placeholder="/tmp" className="h-8 mt-1" />
            </div>
            <div>
              <label className="text-xs font-medium">目标主机</label>
              <div className="max-h-32 overflow-y-auto border rounded-md mt-1 p-1 space-y-0.5">
                {agents.filter((a: any) => a.type !== 'proxy' && a.status === 'online').length === 0 ? (
                  <p className="text-[10px] text-muted-foreground p-2">无在线 Agent</p>
                ) : (
                  agents.filter((a: any) => a.type !== 'proxy' && a.status === 'online').map((a: any) => {
                    const targets = step.filedist_targets || []
                    const checked = targets.includes(a.id)
                    return (
                      <label key={a.id} className="flex items-center gap-1.5 text-[11px] cursor-pointer hover:bg-accent rounded px-1 py-0.5">
                        <input type="checkbox" checked={checked}
                          onChange={() => onChange({ filedist_targets: checked ? targets.filter((t: string) => t !== a.id) : [...targets, a.id] })} />
                        <span className="truncate">{a.name || a.id.slice(0, 8)}</span>
                        <span className="text-[10px] text-muted-foreground shrink-0">{a.address}</span>
                      </label>
                    )
                  })
                )}
              </div>
            </div>
          </>
        )}

        <div className="pt-2 border-t space-y-2">
          <p className="text-xs font-medium">跳转配置（也可在画布上拖拽连线）</p>
          <div>
            <label className="text-[10px] text-muted-foreground">成功 →</label>
            <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
              value={step.on_success || ''} onChange={e => onChange({ on_success: e.target.value })}>
              <option value="">无</option>
              {otherSteps.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </div>
          <div>
            <label className="text-[10px] text-muted-foreground">失败 →</label>
            <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
              value={step.on_failure || ''} onChange={e => onChange({ on_failure: e.target.value })}>
              <option value="">无</option>
              {otherSteps.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

// ====== 画布编辑器（React Flow） ======
function CanvasEditor({ workflow, scripts, channels, workflows, files, agents, onSave }: {
  workflow: Workflow
  scripts: any[]
  channels: any[]
  workflows: any[]
  files: any[]
  agents: any[]
  onSave: (steps: WorkflowStep[]) => Promise<void>
}) {
  const [steps, setSteps] = useState<WorkflowStep[]>(workflow.steps || [])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)

  // 当前选中步骤
  const selectedStep = steps.find(s => s.id === selectedId) || null

  // 根据 steps 构造 React Flow 的 nodes 和 edges
  const { nodes, edges } = useMemo(() => {
    const ns: Node[] = steps.map((s, i) => {
      const script = s.script_id ? scripts.find(sc => sc.id === s.script_id) : null
      return {
        id: s.id,
        type: s.type as string,
        position: s.position || { x: 80 + (i % 3) * 260, y: 60 + Math.floor(i / 3) * 140 },
        data: {
          label: s.name,
          ...(s.type === 'script' ? {
            scriptId: s.script_id,
            scriptName: script?.name || s.script_id?.slice(0, 8),
            timeout: s.timeout || 300,
            outputParams: script?.output_params || [],
          } : {}),
          ...(s.type === 'approval' ? {
            approvers: s.approvers || [],
            approvalMsg: s.approval_msg,
          } : {}),
          ...(s.type === 'condition' ? {
            condition: s.condition,
          } : {}),
          ...(s.type === 'parallel' ? {
            parallelCount: s.parallel_steps?.length || 0,
          } : {}),
          ...(s.type === 'wait' ? {
            waitSeconds: s.wait_seconds || 0,
          } : {}),
          ...(s.type === 'end' ? {
            endStatus: s.end_status || 'success',
          } : {}),
          ...(s.type === 'notify' ? {
            notifyChannel: s.notify_channel || '',
            notifyChannelName: channels.find((c: any) => c.id === s.notify_channel)?.name || s.notify_channel,
            notifyTitle: s.notify_title || '',
          } : {}),
          ...(s.type === 'subflow' ? {
            subflowId: s.subflow_id,
            subflowName: s.subflow_name || s.subflow_id?.slice(0, 12),
          } : {}),
          ...(s.type === 'loop' ? {
            loopOver: s.loop_over || '$items',
            loopMax: s.loop_max || 100,
          } : {}),
          ...(s.type === 'webhook' ? {
            webhookMethod: s.webhook_method || 'POST',
            webhookUrl: s.webhook_url || '',
          } : {}),
          ...(s.type === 'comment' ? {
            commentText: s.comment_text || '',
          } : {}),
          ...(s.type === 'filedist' ? {
            filedistFileName: s.filedist_file_name || '',
            filedistTargets: s.filedist_targets || [],
            filedistDownloadDir: s.filedist_download_dir || '/tmp',
          } : {}),
          // start 无需额外数据
        },
      }
    })

    const es: Edge[] = []
    for (const s of steps) {
      if (s.on_success && steps.some(t => t.id === s.on_success)) {
        es.push({
          id: `${s.id}->${s.on_success}`, source: s.id, target: s.on_success,
          type: 'smoothstep',
          animated: true,
          style: { stroke: '#10b981', strokeWidth: 2 },
          markerEnd: { type: MarkerType.ArrowClosed, color: '#10b981' },
          label: '成功',
          labelStyle: { fill: '#10b981', fontSize: 10 },
          labelBgPadding: [4, 2] as [number, number],
          labelBgStyle: { fill: '#f0fdf4', fillOpacity: 0.9 },
        })
      }
      if (s.on_failure && steps.some(t => t.id === s.on_failure)) {
        es.push({
          id: `${s.id}-!>${s.on_failure}`, source: s.id, target: s.on_failure,
          type: 'smoothstep',
          animated: false,
          style: { stroke: '#ef4444', strokeWidth: 2, strokeDasharray: '5,3' },
          markerEnd: { type: MarkerType.ArrowClosed, color: '#ef4444' },
          label: '失败',
          labelStyle: { fill: '#ef4444', fontSize: 10 },
          labelBgPadding: [4, 2] as [number, number],
          labelBgStyle: { fill: '#fef2f2', fillOpacity: 0.9 },
        })
      }
    }

    return { nodes: ns, edges: es }
  }, [steps, scripts])

  // 当 workflow.id 或 workflow.steps 变化时重置
  useEffect(() => {
    setSteps(workflow.steps || [])
    setDirty(false)
    setSelectedId(null)
  }, [workflow.id])

  // 添加节点
  const addStep = (type: StepType) => {
    const s = newStep(type, steps.length)
    setSteps([...steps, s])
    setSelectedId(s.id)
    setDirty(true)
  }

  // 连线 → 自动填充 on_success
  const onConnect = useCallback((conn: Connection) => {
    if (!conn.source || !conn.target) return
    // 条件节点的两个端口分对应 on_success / on_failure
    const sourceStep = steps.find(s => s.id === conn.source)
    if (sourceStep?.type === 'condition') {
      if (conn.sourceHandle === 'on_failure') {
        updateStep(conn.source, { on_failure: conn.target })
      } else {
        updateStep(conn.source, { on_success: conn.target })
      }
    } else {
      updateStep(conn.source, { on_success: conn.target })
    }
  }, [steps])

  // 更新步骤（同时同步到 React Flow nodes）
  const updateStep = (id: string, patch: Partial<WorkflowStep>) => {
    setSteps(prev => prev.map(s => s.id === id ? { ...s, ...patch } : s))
    setDirty(true)
  }

  // 删除节点和关联边
  const deleteStep = (id: string) => {
    setSteps(prev => prev.filter(s => s.id !== id).map(s => ({
      ...s,
      on_success: s.on_success === id ? '' : s.on_success,
      on_failure: s.on_failure === id ? '' : s.on_failure,
    })))
    if (selectedId === id) setSelectedId(null)
    setDirty(true)
  }

  // 监听画布节点拖拽结束，将 React Flow 位置同步回 steps.position
  const onNodeDragStop = useCallback((_event: any, node: Node) => {
    updateStep(node.id, { position: node.position })
  }, [steps])

  const handleSave = async () => {
    setSaving(true)
    await onSave(steps)
    setSaving(false)
    setDirty(false)
  }

  return (
    <div className="flex gap-3" style={{ height: 560 }}>
      {/* 左侧节点面板 */}
      <div className="w-32 shrink-0 space-y-1">
        <p className="text-xs text-muted-foreground mb-2">点击添加</p>
        {(Object.keys(NODE_META) as StepType[]).map(type => {
          const meta = NODE_META[type]; const IconEl = meta.icon
          return (
            <button key={type} onClick={() => addStep(type)}
              className="w-full flex items-center gap-2 px-2 py-2 text-xs rounded-md border border-dashed hover:border-solid hover:bg-accent">
              <IconEl className="h-3 w-3 shrink-0" />{meta.label}
            </button>
          )
        })}
        <div className="pt-2 mt-2 border-t text-[10px] text-muted-foreground space-y-1">
          <p>· 拖拽节点端口连线</p>
          <p>· 滚轮缩放画布</p>
          <p>· 选中后右侧编辑</p>
        </div>
      </div>

      {/* 中间 React Flow 画布 */}
      <div className="flex-1 border rounded-md overflow-hidden bg-muted/20">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onConnect={onConnect}
          onNodeClick={(_e, node) => setSelectedId(node.id)}
          onNodeDragStop={onNodeDragStop}
          onPaneClick={() => setSelectedId(null)}
          onNodesDelete={nodes => nodes.forEach(n => deleteStep(n.id))}
          fitView
          attributionPosition="bottom-left"
          snapToGrid
          snapGrid={[10, 10]}
          deleteKeyCode={['Backspace', 'Delete']}
          defaultEdgeOptions={{
            type: 'smoothstep',
            style: { strokeWidth: 2 },
          }}
        >
          <Controls position="bottom-right" />
          <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
          <MiniMap
            position="bottom-left"
            nodeStrokeWidth={2}
            pannable
            zoomable
            style={{ height: 100 }}
          />
        </ReactFlow>
      </div>

      {/* 右侧属性面板 */}
      <div className="w-64 shrink-0">
        {selectedStep ? (
          <div className="flex flex-col gap-2 h-full">
            <StepPanel
              step={selectedStep}
              steps={steps}
              scripts={scripts}
              channels={channels}
              workflows={workflows}
              files={files}
              agents={agents}
              onChange={patch => updateStep(selectedStep.id, patch)}
            />
            <Button
              variant="destructive"
              size="sm"
              className="w-full"
              onClick={() => deleteStep(selectedStep.id)}
            >
              <Trash2 className="h-3.5 w-3.5 mr-1" />删除节点
            </Button>
          </div>
        ) : (
          <Card className="h-full min-h-[200px] flex items-center justify-center">
            <CardContent className="text-center text-muted-foreground text-xs">
              <p>选中节点查看属性</p>
              <p className="mt-1 text-[10px]">或从左侧面板添加节点</p>
            </CardContent>
          </Card>
        )}
      </div>

      {/* 浮动保存按钮 */}
      {dirty && (
        <div className="fixed bottom-6 right-6 z-50 flex gap-2 bg-background border rounded-full shadow-lg px-4 py-2">
          <span className="text-sm text-amber-600 self-center">有未保存的修改</span>
          <Button size="sm" onClick={handleSave} disabled={saving}>
            {saving ? <Loader2 className="h-3 w-3 mr-1 animate-spin" /> : <Save className="h-3 w-3 mr-1" />}
            保存
          </Button>
          <Button size="sm" variant="outline" onClick={() => {
            setSteps(workflow.steps || [])
            setDirty(false)
          }}>
            <X className="h-3 w-3 mr-1" />放弃
          </Button>
        </div>
      )}
    </div>
  )
}

// ====== Main Page ======
export default function WorkflowDetailPage() {
  const params = useParams()
  const router = useRouter()
  const workflowId = String(params.id || '')
  const isNew = workflowId === 'new' || !workflowId

  const {
    workflows, executions, approvals,
    fetch, create, update, execute,
    fetchExecutions, fetchApprovals, approve, reject,
  } = useWorkflowStore()
  const { agents } = useAgentStore()
  const { scripts, fetch: fetchScripts } = useScriptStore()
  const { channels, fetchChannels } = useNotificationStore()
  const { files, fetch: fetchFiles } = useFileStore()

  const [loading, setLoading] = useState(!isNew)
  const [viewWF, setViewWF] = useState<Workflow | null>(null)
  const [mainTab, setMainTab] = useState('canvas')
  const [execDialog, setExecDialog] = useState(false)
  const [execAgents, setExecAgents] = useState<string[]>([])
  const [execInputs, setExecInputs] = useState<Record<string, string>>({})
  const [createForm, setCreateForm] = useState({ name: '', description: '' })
  const [creating, setCreating] = useState(false)

  useEffect(() => {
    if (isNew) {
      setLoading(false)
      return
    }
    setLoading(true)
    Promise.all([fetch(), fetchScripts(), fetchApprovals(), fetchChannels(), fetchFiles()]).then(() => setLoading(false))
  }, [workflowId, isNew])

  useEffect(() => {
    if (isNew || loading) return
    const wf = workflows.find(w => w.id === workflowId)
    if (wf) {
      setViewWF(wf)
      fetchExecutions(wf.id)
    }
  }, [workflows, workflowId, isNew, loading])

  const handleCreate = async () => {
    if (!createForm.name.trim()) return
    setCreating(true)
    const id = await create({
      name: createForm.name,
      description: createForm.description,
      steps: [],
      input_params: [],
    })
    setCreating(false)
    if (id) {
      router.push(`/workflows/${id}`)
    }
  }

  const handleExecute = async () => {
    if (!viewWF) return
    await execute(viewWF.id, execAgents, execInputs)
    setExecDialog(false)
    await fetchExecutions(viewWF.id)
    await fetchApprovals()
  }

  const handleCanvasSave = async (steps: WorkflowStep[]) => {
    if (!viewWF) return
    const ok = await update(viewWF.id, { ...viewWF, steps })
    if (ok) {
      setViewWF({ ...viewWF, steps })
    }
  }

  // 待审批列表
  const pendingApprovals = approvals.filter(a => a.status === 'pending')

  if (loading) {
    return (
      <div className="p-6 flex items-center justify-center min-h-[400px]">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (isNew) {
    return (
      <div className="p-6 space-y-4">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="icon" onClick={() => router.push('/workflows')}>
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <WorkflowIcon className="h-5 w-5 text-emerald-400" />
          <div>
            <h1 className="text-xl font-bold">新建作业流程</h1>
            <p className="text-xs text-muted-foreground">创建后将进入编排画布</p>
          </div>
        </div>
        <Card>
          <CardContent className="p-6">
            <div className="space-y-4 max-w-xl">
              <div>
                <label className="text-sm font-medium">流程名称 *</label>
                <Input value={createForm.name} onChange={e => setCreateForm(f => ({ ...f, name: e.target.value }))}
                  placeholder="例: 发布上线流程" className="mt-1" />
              </div>
              <div>
                <label className="text-sm font-medium">流程描述</label>
                <textarea
                  className="flex h-24 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1"
                  value={createForm.description}
                  onChange={e => setCreateForm(f => ({ ...f, description: e.target.value }))}
                  placeholder="流程说明..."
                />
              </div>
              <Button onClick={handleCreate} disabled={creating || !createForm.name.trim()}>
                {creating ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Plus className="h-4 w-4 mr-1" />}
                创建并进入画布
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
    )
  }

  if (!viewWF) {
    return (
      <div className="p-6 flex items-center justify-center min-h-[400px]">
        <div className="text-center">
          <WorkflowIcon className="h-12 w-12 mx-auto mb-3 text-muted-foreground/30" />
          <p className="text-muted-foreground">流程不存在或已删除</p>
          <Button variant="outline" className="mt-3" onClick={() => router.push('/workflows')}>返回列表</Button>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3 min-w-0">
          <Button variant="ghost" size="icon" onClick={() => router.push('/workflows')}>
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <WorkflowIcon className="h-5 w-5 text-emerald-400 shrink-0" />
          <div className="min-w-0">
            <h1 className="text-xl font-bold truncate">{viewWF.name}</h1>
            <p className="text-xs text-muted-foreground">{viewWF.description || '无描述'} · {viewWF.steps?.length || 0} 步骤</p>
          </div>
        </div>
        <div className="flex gap-2 shrink-0">
          <Button variant="outline" size="sm" onClick={() => setMainTab('approvals')}
            className={mainTab === 'approvals' ? 'border-amber-500 text-amber-600' : ''}>
            <CheckSquare className="h-3.5 w-3.5 mr-1" />待审批 ({pendingApprovals.length})
          </Button>
          <Button size="sm" onClick={() => { setExecDialog(true); setExecAgents([]); setExecInputs({}) }}>
            <Play className="h-3.5 w-3.5 mr-1" />执行
          </Button>
        </div>
      </div>

      <Card>
        <CardContent className="p-4">
          <Tabs value={mainTab} onValueChange={setMainTab}>
            <TabsList>
              <TabsTrigger value="canvas"><WorkflowIcon className="h-3 w-3 mr-1" />编排画布</TabsTrigger>
              <TabsTrigger value="execs">执行历史 ({executions.length})</TabsTrigger>
              <TabsTrigger value="approvals">审批 ({pendingApprovals.length})</TabsTrigger>
            </TabsList>

            <TabsContent value="canvas" className="mt-3">
              <ReactFlowProvider>
                <CanvasEditor workflow={viewWF} scripts={scripts} channels={channels} workflows={workflows} files={files} agents={agents} onSave={handleCanvasSave} />
              </ReactFlowProvider>
            </TabsContent>

            <TabsContent value="execs" className="mt-3">
              <ExecutionsPanel executions={executions} />
            </TabsContent>

            <TabsContent value="approvals" className="mt-3">
              <ApprovalsPanel
                approvals={pendingApprovals}
                onApprove={(id, c) => approve(id, c)}
                onReject={(id, c) => reject(id, c)}
              />
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>

      {execDialog && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setExecDialog(false)} />
          <div className="relative z-50 w-full max-w-lg bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg max-h-[85vh] overflow-y-auto">
            <h3 className="text-lg font-semibold mb-4 flex items-center gap-2">
              <Play className="h-5 w-5 text-emerald-500" />执行流程
            </h3>
            <ExecDialog
              workflow={viewWF}
              agents={agents}
              execAgents={execAgents}
              setExecAgents={setExecAgents}
              execInputs={execInputs}
              setExecInputs={setExecInputs}
              onConfirm={handleExecute}
              onCancel={() => setExecDialog(false)}
            />
          </div>
        </div>
      )}
    </div>
  )
}

// ====== 执行/历史/审批面板 ======

function ExecDialog({ workflow, agents, execAgents, setExecAgents, execInputs, setExecInputs, onConfirm, onCancel }: any) {
  const onlineAgents = agents.filter((a: any) => a.type !== 'proxy' && a.status === 'online')
  return (
    <div className="space-y-4">
      <div>
        <label className="text-sm font-medium">选择执行代理 ({execAgents.length} 已选)</label>
        <div className="mt-2 space-y-1 max-h-40 overflow-y-auto">
          {onlineAgents.length === 0 ? (
            <p className="text-xs text-muted-foreground">无可用在线代理</p>
          ) : (
            onlineAgents.map((a: any) => (
              <label key={a.id} className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={execAgents.includes(a.id)}
                  onChange={e => setExecAgents(e.target.checked ? [...execAgents, a.id] : execAgents.filter((id: string) => id !== a.id))} />
                <span className="truncate">{a.name || a.id} ({a.address})</span>
              </label>
            ))
          )}
        </div>
      </div>
      {workflow.input_params?.length > 0 && (
        <div>
          <label className="text-sm font-medium">输入参数</label>
          <div className="space-y-2 mt-1">
            {workflow.input_params.map((p: any) => (
              <div key={p.name}>
                <label className="text-xs text-muted-foreground">{p.name}</label>
                <Input className="h-8 mt-0.5" value={execInputs[p.name] || ''}
                  onChange={e => setExecInputs({ ...execInputs, [p.name]: e.target.value })} />
              </div>
            ))}
          </div>
        </div>
      )}
      <div className="flex justify-end gap-2 pt-2 border-t">
        <Button variant="outline" onClick={onCancel}>取消</Button>
        <Button onClick={onConfirm} disabled={execAgents.length === 0}>立即执行</Button>
      </div>
    </div>
  )
}

function ExecutionsPanel({ executions }: { executions: WorkflowExecution[] }) {
  const [expanded, setExpanded] = useState<string | null>(null)
  if (executions.length === 0) {
    return (
      <div className="py-8 text-center text-muted-foreground">
        <Play className="h-8 w-8 mx-auto mb-2 opacity-30" />
        <p className="text-sm">无执行记录</p>
        <p className="text-xs mt-1">点击右上角「执行」按钮开始流程</p>
      </div>
    )
  }
  return (
    <div className="space-y-2">
      {executions.map(e => {
        const isOpen = expanded === e.id
        const results = Object.values(e.step_results || {}) as any[]
        const succeeded = results.filter(r => r.status === 'succeeded').length
        const failed = results.filter(r => r.status === 'failed').length
        return (
          <div key={e.id} className="border rounded-md">
            <button className="w-full px-3 py-2 flex items-center justify-between hover:bg-accent text-left"
              onClick={() => setExpanded(isOpen ? null : e.id)}>
              <div className="flex items-center gap-3">
                <Badge variant={e.status === 'running' ? 'default' : e.status === 'succeeded' ? 'success' : e.status === 'failed' ? 'destructive' : 'outline'} className="text-xs">
                  {e.status === 'running' ? '运行中' : e.status === 'succeeded' ? '成功' : e.status === 'failed' ? '失败' : e.status}
                </Badge>
                <span className="text-sm font-medium">#{e.id.slice(0, 8)}</span>
                <span className="text-xs text-muted-foreground">
                  {succeeded}/{results.length} 成功{failed > 0 && ` · ${failed} 失败`}
                </span>
              </div>
              <span className="text-xs text-muted-foreground">{formatTime(e.started_at)}</span>
            </button>
            {isOpen && (
              <div className="border-t px-3 py-2 space-y-1">
                {results.length === 0 ? (
                  <p className="text-xs text-muted-foreground">无步骤详情</p>
                ) : (
                  results.map((r: any, i: number) => (
                    <div key={i} className="flex items-center gap-2 text-xs">
                      <Badge variant={r.status === 'succeeded' ? 'success' : r.status === 'failed' ? 'destructive' : 'outline'} className="text-[10px]">
                        {r.status === 'succeeded' ? '✓' : r.status === 'failed' ? '✗' : '…'}
                      </Badge>
                      <span className="text-muted-foreground">{r.step_name || `步骤${i + 1}`}</span>
                      {r.duration_ms > 0 && <span className="text-[10px] text-muted-foreground">{r.duration_ms}ms</span>}
                      {r.error && <span className="text-red-500 truncate max-w-[200px]">{r.error}</span>}
                    </div>
                  ))
                )}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}

function ApprovalsPanel({ approvals, onApprove, onReject }: {
  approvals: StepApproval[]
  onApprove: (id: string, comment?: string) => Promise<any>
  onReject: (id: string, comment?: string) => Promise<any>
}) {
  const [comment, setComment] = useState<Record<string, string>>({})
  if (approvals.length === 0) {
    return (
      <div className="py-8 text-center text-muted-foreground">
        <CheckSquare className="h-8 w-8 mx-auto mb-2 opacity-30" />
        <p className="text-sm">无待审批项</p>
      </div>
    )
  }
  return (
    <div className="space-y-3">
      {approvals.map(a => (
        <Card key={a.id} className="border-amber-200 bg-amber-50/50">
          <CardContent className="p-4 text-sm space-y-3">
            <div>
              <p className="font-medium">{a.step_name}</p>
              <p className="text-xs text-muted-foreground">{a.message || '请求审批'}</p>
              <p className="text-xs text-muted-foreground mt-1">审批人: {a.approvers?.join(', ') || '管理员'}</p>
            </div>
            <Input placeholder="审批意见（可选）" className="h-8"
              value={comment[a.id] || ''} onChange={e => setComment({ ...comment, [a.id]: e.target.value })} />
            <div className="flex gap-2">
              <Button size="sm" variant="outline" className="text-emerald-600" onClick={() => onApprove(a.id, comment[a.id])}>
                <Check className="h-3.5 w-3.5 mr-1" />通过
              </Button>
              <Button size="sm" variant="outline" className="text-red-500" onClick={() => onReject(a.id, comment[a.id])}>
                <Ban className="h-3.5 w-3.5 mr-1" />驳回
              </Button>
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
