
// 可操作产物（Actionable Artifact）：AI 在对话中生成的 SQL / Shell / Runbook / PowerShell 等
// 可直接执行的代码块，渲染为带语法高亮的卡片，支持「一键采纳为工单」。
// 这是运维场景区别于通用 ChatBot 的关键差异化能力：答案不止于文本，而是能落地的工单草稿。

import { useMemo, useState } from 'react'
import { Sparkles, Copy, Check, Ticket, Terminal, Database, BookOpen, FileCode, type LucideIcon } from 'lucide-react'
import { apiFetchRaw } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { highlightCode } from '@/lib/code-highlight'

export type ArtifactKind = 'sql' | 'shell' | 'runbook' | 'powershell'

export interface ActionableArtifact {
  id: string
  kind: ArtifactKind
  lang: string
  title: string
  code: string
}

const ACTIONABLE_LANGS: Record<string, ArtifactKind> = {
  sql: 'sql',
  shell: 'shell',
  bash: 'shell',
  sh: 'shell',
  runbook: 'runbook',
  powershell: 'powershell',
}

const KIND_META: Record<ArtifactKind, { label: string; badge: string; icon: LucideIcon; color: string }> = {
  sql: { label: 'SQL 语句', badge: 'SQL', icon: Database, color: '#38bdf8' },
  shell: { label: 'Shell 脚本', badge: 'Shell', icon: Terminal, color: '#34d399' },
  runbook: { label: 'Runbook 处置手册', badge: 'Runbook', icon: BookOpen, color: '#a78bfa' },
  powershell: { label: 'PowerShell 脚本', badge: 'PowerShell', icon: FileCode, color: '#fbbf24' },
}

/** 取代码块前最近的一个紧邻 markdown 标题作为标题；无则按类型给默认名。 */
function nearestHeading(before: string): string | null {
  const lines = before.split('\n')
  for (let i = lines.length - 1; i >= 0; i--) {
    const t = lines[i].trim()
    const h = t.match(/^#{1,6}\s+(.+?)\s*#*\s*$/)
    if (h) return h[1].trim()
    if (t === '') continue
    break // 遇到非标题非空行即停止（标题必须紧邻代码块）
  }
  return null
}

/** 从模型回答中解析可操作产物：仅识别 sql/shell/runbook/powershell 等围栏代码块。 */
export function detectActionableArtifacts(content: string): ActionableArtifact[] {
  const out: ActionableArtifact[] = []
  if (!content) return out
  const fence = /```(\w+)?\n([\s\S]*?)```/g
  let m: RegExpExecArray | null
  let idx = 0
  while ((m = fence.exec(content))) {
    const lang = (m[1] || '').toLowerCase()
    const kind = ACTIONABLE_LANGS[lang]
    if (!kind) continue
    const code = m[2].replace(/\n$/, '')
    if (!code.trim()) continue
    const title = nearestHeading(content.slice(0, m.index)) || KIND_META[kind].label
    out.push({ id: `art-${idx++}`, kind, lang, title: title.slice(0, 60), code })
  }
  return out
}

function ActionableArtifactCard({ art }: { art: ActionableArtifact }) {
  const [adopted, setAdopted] = useState(false)
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)
  const meta = KIND_META[art.kind]
  const Icon = meta.icon
  const html = useMemo(() => highlightCode(art.code, art.lang), [art.code, art.lang])

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(art.code)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch { /* ignore */ }
  }

  const adopt = async () => {
    if (busy || adopted) return
    setBusy(true)
    try {
      const r = await apiFetchRaw('/itsm/tickets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: art.title,
          description: `【AI 生成 ${meta.badge}】\n\n${art.code}`,
          labels: ['AI产物', meta.badge],
        }),
      }, { redirectOn401: false })
      if (r.ok) {
        getMessage().success('已采纳为工单')
        setAdopted(true)
      } else {
        getMessage().error('采纳失败，请稍后重试')
      }
    } catch {
      getMessage().error('采纳失败，请检查网络')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-lg border border-slate-200 dark:border-white/10 bg-slate-50 dark:bg-white/[0.03] overflow-hidden">
      <div className="flex items-center gap-2 px-3 h-9 border-b border-slate-200 dark:border-white/10 bg-slate-100/60 dark:bg-white/[0.04]">
        <Icon className="h-3.5 w-3.5 shrink-0" style={{ color: meta.color }} />
        <span className="text-[11px] font-medium text-slate-700 dark:text-slate-200 truncate">{art.title}</span>
        <span className="ml-auto shrink-0 text-[9px] uppercase font-semibold px-1.5 py-0.5 rounded-full border" style={{ color: meta.color, borderColor: `${meta.color}55`, background: `${meta.color}15` }}>
          {meta.badge}
        </span>
      </div>
      <pre className="text-[11px] leading-relaxed max-h-52 overflow-auto px-3 py-2 m-0 bg-transparent"><code dangerouslySetInnerHTML={{ __html: html }} /></pre>
      <div className="flex items-center gap-2 px-3 py-2 border-t border-slate-200 dark:border-white/10">
        <button
          onClick={copy}
          className="inline-flex items-center gap-1 text-[11px] px-2 py-1 rounded border border-slate-300 dark:border-white/15 text-slate-600 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-white/10 transition-colors"
        >
          {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
          {copied ? '已复制' : '复制'}
        </button>
        <button
          onClick={adopt}
          disabled={busy || adopted}
          className="inline-flex items-center gap-1 text-[11px] px-2.5 py-1 rounded font-medium text-white transition-colors disabled:opacity-60"
          style={{ background: adopted ? '#34d399' : '#2563eb' }}
          title="将脚本作为工单草稿提交，进入 ITSM 审批流程"
        >
          {adopted ? <Check className="h-3 w-3" /> : <Ticket className="h-3 w-3" />}
          {adopted ? '已采纳' : busy ? '采纳中…' : '采纳为工单'}
        </button>
      </div>
    </div>
  )
}

/** 可操作产物列表：渲染在 AI 回复正文下方，提示用户这些片段可一键落地为工单。 */
export function ActionableArtifactList({ artifacts }: { artifacts: ActionableArtifact[] }) {
  if (!artifacts || !artifacts.length) return null
  return (
    <div className="mt-3 space-y-2">
      <div className="flex items-center gap-1.5 text-[11px] font-medium text-slate-500 dark:text-slate-400">
        <Sparkles className="h-3.5 w-3.5 text-violet-500" />
        可操作产物（一键采纳为工单）
      </div>
      {artifacts.map(a => <ActionableArtifactCard key={a.id} art={a} />)}
    </div>
  )
}
