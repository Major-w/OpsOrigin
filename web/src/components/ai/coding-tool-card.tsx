// ============================================================================
// AI 代码助手 —— 工具调用富视图
// 把后端 coding_* 工具返回的「JSON 字符串」args/result 渲染成:
//   - 读/列/搜: 文件内容 / 目录树 / 匹配列表
//   - 编辑: 行级 inline diff(由 old_string/new_string 重建)
//   - 新建: 内容预览 + 复制
//   - 补丁: patch 文本
//   - 命令: 终端样式(命令 + 输出 + 退出码)
// 并对「审批拒绝 / 沙箱越界 / 危险命令拦截」给出前端友好提示。
// 另导出 SessionFileChanges: 把一轮会话内所有 coding_* 调用聚合成文件树变更列表。
// ============================================================================

import { useMemo, useState, type ReactNode } from 'react'
import { cn } from '@/lib/utils'
import type { ToolCallLocal } from './chat-kernel'
import {
  FileText, FileEdit, FilePlus2, FolderTree, Search, Terminal, FileDiff,
  Eye, Copy, AlertOctagon, Ban, CheckCircle2, XCircle, type LucideIcon,
} from 'lucide-react'

// ---------------------------------------------------------------------------
// 基础工具
// ---------------------------------------------------------------------------

function safeParse(s?: string): any {
  if (!s) return null
  try { return JSON.parse(s) } catch { return null }
}

function formatBytes(n: number): string {
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB'
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + ' KB'
  return `${n} B`
}

// LCS 行级 diff(用于编辑操作的 inline 对比)。超长退化为整段替换, 避免 O(n*m) 爆栈。
interface DiffLine { type: 'add' | 'del' | 'eq'; text: string }
function diffLines(oldS: string, newS: string, cap = 600): DiffLine[] {
  const a = (oldS ?? '').split('\n')
  const b = (newS ?? '').split('\n')
  if (a.length > cap || b.length > cap) {
    return [
      ...a.map(t => ({ type: 'del' as const, text: t })),
      ...b.map(t => ({ type: 'add' as const, text: t })),
    ]
  }
  const n = a.length, m = b.length
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
  const out: DiffLine[] = []
  let i = 0, j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) { out.push({ type: 'eq', text: a[i] }); i++; j++ }
    else if (dp[i + 1][j] >= dp[i][j + 1]) { out.push({ type: 'del', text: a[i] }); i++ }
    else { out.push({ type: 'add', text: b[j] }); j++ }
  }
  while (i < n) out.push({ type: 'del', text: a[i++] })
  while (j < m) out.push({ type: 'add', text: b[j++] })
  return out
}

// ---------------------------------------------------------------------------
// 通用小块
// ---------------------------------------------------------------------------

function Header({ icon: Icon, color, title, sub, success }: { icon: LucideIcon; color: string; title: string; sub?: string; success?: boolean }) {
  return (
    <div className="flex items-center gap-2 text-xs mb-1.5">
      <Icon className={cn('h-3.5 w-3.5 shrink-0', color)} />
      <span className="font-medium truncate flex-1 min-w-0">{title}</span>
      {success && <CheckCircle2 className="h-3.5 w-3.5 text-green-500 shrink-0" />}
      {sub && <span className="text-[10px] text-muted-foreground shrink-0">{sub}</span>}
    </div>
  )
}

function PreView({ text, label }: { text: string; label?: string }) {
  if (text == null) return null
  return (
    <pre className="rounded-md border border-gray-200 dark:border-white/10 bg-black/5 dark:bg-black/20 p-2 text-[11px] font-mono whitespace-pre-wrap max-h-60 overflow-y-auto leading-relaxed">
      {label ? <span className="text-muted-foreground select-none">{label}\n</span> : null}{text}
    </pre>
  )
}

function CopyBtn({ text }: { text: string }) {
  const [ok, setOk] = useState(false)
  return (
    <button
      onClick={() => {
        navigator.clipboard?.writeText(text).then(() => {
          setOk(true)
          setTimeout(() => setOk(false), 1200)
        }).catch(() => { /* 剪贴板不可用时静默 */ })
      }}
      className="inline-flex items-center gap-1 text-[10px] text-muted-foreground hover:text-blue-500 transition-colors"
      title="复制内容"
    >
      {ok ? <CheckCircle2 className="h-3 w-3 text-green-500" /> : <Copy className="h-3 w-3" />}
      {ok ? '已复制' : '复制'}
    </button>
  )
}

function DiffView({ oldText, newText }: { oldText: string; newText: string }) {
  const lines = useMemo(() => diffLines(oldText, newText), [oldText, newText])
  return (
    <div className="rounded-md border border-gray-200 dark:border-white/10 overflow-hidden text-[11px] font-mono">
      <div className="flex flex-col max-h-72 overflow-y-auto">
        {lines.map((l, idx) => (
          <div key={idx} className={cn(
            'flex px-2 py-0.5 leading-relaxed whitespace-pre-wrap',
            l.type === 'add' && 'bg-green-500/10 text-green-700 dark:text-green-300',
            l.type === 'del' && 'bg-red-500/10 text-red-700 dark:text-red-300',
            l.type === 'eq' && 'text-gray-600 dark:text-gray-400',
          )}>
            <span className="w-4 shrink-0 select-none opacity-60">{l.type === 'add' ? '+' : l.type === 'del' ? '-' : ' '}</span>
            <span className="flex-1 min-w-0 break-all">{l.text || ' '}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function TerminalView({ command, output, error, exitCode }: { command: string; output?: string; error?: string; exitCode?: number }) {
  return (
    <div className="rounded-md overflow-hidden border border-gray-200 dark:border-white/10 bg-gray-900 text-gray-100 text-[11px] font-mono">
      <div className="px-3 py-1.5 bg-gray-800/80 flex items-center gap-2 text-gray-300">
        <Terminal className="h-3 w-3" /><span>shell</span>
        {typeof exitCode === 'number' && (
          <span className={cn('ml-auto', exitCode === 0 ? 'text-green-400' : 'text-red-400')}>exit {exitCode}</span>
        )}
      </div>
      <div className="px-3 py-2 space-y-2">
        <div><span className="text-blue-400">$ </span><span className="whitespace-pre-wrap break-all">{command}</span></div>
        {output && <pre className="whitespace-pre-wrap max-h-60 overflow-y-auto">{output}</pre>}
        {error && <pre className="text-red-400 whitespace-pre-wrap max-h-60 overflow-y-auto">{error}</pre>}
      </div>
    </div>
  )
}

// 友好拦截态: 审批拒绝 / 沙箱越界 / 危险命令
type BlockKind = 'sandbox' | 'danger' | 'rejected'
function friendlyBlocked(result: any): { kind: BlockKind | null; msg: string } {
  if (!result) return { kind: null, msg: '' }
  const msg = String(result.message || result.error || result.reason || '')
  if (result.status === 'rejected') return { kind: 'rejected', msg }
  if (/沙箱|sandbox|越界|前缀|prefix|不允许|not allowed|outside|超出工作区/i.test(msg)) return { kind: 'sandbox', msg }
  if (/危险|dangerous|黑名单|blacklist|禁止执行|blocked|拦截|高危/i.test(msg)) return { kind: 'danger', msg }
  return { kind: null, msg }
}

function BlockedState({ kind, msg }: { kind: BlockKind; msg: string }) {
  const map: Record<BlockKind, { icon: LucideIcon; color: string; bg: string; title: string; desc: string }> = {
    sandbox: {
      icon: Ban, color: 'text-red-500', bg: 'bg-red-500/10 border-red-500/20',
      title: '🚫 沙箱越界，已阻止',
      desc: '该路径不在允许的工作区内。代码助手只能在沙箱根目录内读写，越界操作会被拒绝以保护你的仓库。',
    },
    danger: {
      icon: AlertOctagon, color: 'text-amber-500', bg: 'bg-amber-500/10 border-amber-500/20',
      title: '⚠️ 危险命令已拦截',
      desc: '该命令命中危险操作黑名单（如 rm -rf / 格式化 / 关机等），出于安全不会执行。',
    },
    rejected: {
      icon: XCircle, color: 'text-violet-500', bg: 'bg-violet-500/10 border-violet-500/20',
      title: '🔒 操作被拒绝',
      desc: '该写操作需要人工批准，等待超时或你选择「拒绝」后未执行。',
    },
  }
  const c = map[kind]
  const Icon = c.icon
  return (
    <div className={cn('rounded-md border px-3 py-2 text-xs', c.bg)}>
      <div className={cn('flex items-center gap-2 font-medium', c.color)}><Icon className="h-4 w-4" />{c.title}</div>
      <p className="mt-1 text-muted-foreground leading-relaxed">{c.desc}</p>
      {msg && <pre className="mt-1.5 text-[10px] whitespace-pre-wrap opacity-80 max-h-24 overflow-y-auto">{msg}</pre>}
    </div>
  )
}

// ---------------------------------------------------------------------------
// 主卡片 —— 按工具名分发
// ---------------------------------------------------------------------------

export function CodingToolCard({ toolCall }: { toolCall: ToolCallLocal }) {
  const name = toolCall.name || ''
  const args = safeParse(toolCall.args)
  const result = safeParse(toolCall.result)
  const blocked = friendlyBlocked(result)
  if (blocked.kind) return <BlockedState kind={blocked.kind} msg={blocked.msg} />

  if (name === 'coding_read_file') {
    const path = args?.path || ''
    return (
      <div data-coding-path={path}>
        <Header icon={Eye} color="text-sky-500" title={`读取 ${path}`}
          sub={result ? `${result.shown_lines ?? '?'} / ${result.total_lines ?? '?'} 行` : ''} />
        {result?.content != null && <PreView text={String(result.content)} />}
      </div>
    )
  }

  if (name === 'coding_list_files') {
    const paths: string[] = result?.paths || []
    return (
      <div>
        <Header icon={FolderTree} color="text-indigo-500" title="列出目录"
          sub={`${result?.count ?? paths.length} 项`} />
        {paths.length > 0 && (
          <ul className="text-[11px] font-mono text-gray-600 dark:text-gray-300 max-h-48 overflow-y-auto space-y-0.5">
            {paths.map((p, i) => <li key={i} className="truncate">{p}</li>)}
          </ul>
        )}
        {result?.note && <p className="text-[10px] text-muted-foreground mt-1">{result.note}</p>}
      </div>
    )
  }

  if (name === 'coding_grep_files') {
    const matches: any[] = result?.matches || []
    return (
      <div>
        <Header icon={Search} color="text-orange-500" title="搜索内容"
          sub={`${result?.count ?? matches.length} 匹配`} />
        {matches.length > 0 && (
          <ul className="text-[11px] font-mono max-h-56 overflow-y-auto space-y-1">
            {matches.slice(0, 80).map((mt: any, i: number) => (
              <li key={i} className="truncate">
                <span className="text-blue-500">{mt.path}</span>
                {mt.line != null && <span className="text-amber-500">:{mt.line}</span>}
                <span className="text-gray-600 dark:text-gray-300"> {mt.text}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    )
  }

  if (name === 'coding_edit_file') {
    const path = args?.path || ''
    const ok = result?.status === 'ok'
    return (
      <div data-coding-path={path}>
        <Header icon={FileEdit} color="text-amber-500" title={`编辑 ${path}`}
          sub={result?.replacements != null ? `替换 ${result.replacements} 处` : ''} success={ok} />
        {args?.old_string != null && args?.new_string != null
          ? <DiffView oldText={String(args.old_string)} newText={String(args.new_string)} />
          : args?.new_string != null && <PreView text={String(args.new_string)} />}
        {args?.new_string != null && (
          <div className="mt-1.5"><CopyBtn text={String(args.new_string)} /></div>
        )}
      </div>
    )
  }

  if (name === 'coding_write_file') {
    const path = args?.path || ''
    return (
      <div data-coding-path={path}>
        <Header icon={FilePlus2} color="text-green-500" title={`新建 ${path}`}
          sub={result?.bytes != null ? formatBytes(result.bytes) : ''} success />
        {args?.content != null && <PreView text={String(args.content)} />}
        {args?.content != null && (
          <div className="mt-1.5"><CopyBtn text={String(args.content)} /></div>
        )}
      </div>
    )
  }

  if (name === 'coding_apply_patch') {
    return (
      <div>
        <Header icon={FileDiff} color="text-purple-500" title="应用补丁"
          sub={result?.applied != null ? `已应用 ${result.applied} 处` : ''} success={result?.status === 'ok'} />
        {args?.patch && <PreView text={String(args.patch)} />}
      </div>
    )
  }

  if (name === 'coding_run_shell') {
    return (
      <div>
        <TerminalView command={args?.command || ''} output={result?.output} error={result?.error} exitCode={result?.exit_code} />
      </div>
    )
  }

  // 兜底: 未识别的 coding 工具 —— 原样展示
  return (
    <pre className="text-[10px] whitespace-pre-wrap bg-black/5 dark:bg-black/20 p-2 rounded">
      {toolCall.result || toolCall.args}
    </pre>
  )
}

// ---------------------------------------------------------------------------
// 会话级文件变更树(SessionFileChanges)
// 把一轮对话内所有 coding_* 工具调用聚合成「路径 → 状态」映射, 再渲染成目录树。
// ---------------------------------------------------------------------------

export type CodingFileStatus = 'read' | 'modified' | 'created' | 'blocked'

export interface CodingFileChange {
  path: string
  status: CodingFileStatus
  ops: number
}

export function aggregateCodingChanges(toolCalls: ToolCallLocal[]): CodingFileChange[] {
  const map = new Map<string, CodingFileChange>()
  const bump = (path: string, status: CodingFileStatus) => {
    if (!path) return
    const ex = map.get(path)
    if (ex) {
      ex.ops += 1
      if (status !== 'read') ex.status = status
    } else {
      map.set(path, { path, status, ops: 1 })
    }
  }
  for (const tc of toolCalls) {
    const name = tc.name || ''
    if (!name.startsWith('coding_')) continue
    const a = safeParse(tc.args)
    const r = safeParse(tc.result)
    if (!a) continue
    if (name === 'coding_read_file') bump(a.path, 'read')
    else if (name === 'coding_edit_file') bump(a.path, r?.status === 'rejected' ? 'blocked' : 'modified')
    else if (name === 'coding_write_file') bump(a.path, r?.status === 'rejected' ? 'blocked' : 'created')
    else if (name === 'coding_apply_patch') {
      const patch = a.patch || ''
      const re = /^\+\+\+ b\/(.+)$/gm
      let m: RegExpExecArray | null
      const found: string[] = []
      while ((m = re.exec(patch))) found.push(m[1])
      if (found.length) found.forEach(p => bump(p, 'modified'))
    }
  }
  return [...map.values()].sort((x, y) => x.path.localeCompare(y.path))
}

interface TreeNode { name: string; path: string; children: Map<string, TreeNode>; change?: CodingFileChange }

function buildTree(changes: CodingFileChange[]): TreeNode {
  const root: TreeNode = { name: '', path: '', children: new Map() }
  for (const c of changes) {
    const parts = c.path.split('/').filter(Boolean)
    let cur = root
    let acc = ''
    parts.forEach((part, i) => {
      acc = acc ? `${acc}/${part}` : part
      const isFile = i === parts.length - 1
      let child = cur.children.get(part)
      if (!child) {
        child = { name: part, path: acc, children: new Map(), change: isFile ? c : undefined }
        cur.children.set(part, child)
      } else if (isFile) {
        child.change = c
      }
      cur = child
    })
  }
  return root
}

function statusIcon(status: CodingFileStatus): ReactNode {
  switch (status) {
    case 'modified': return <FileEdit className="h-3 w-3 text-amber-500" />
    case 'created': return <FilePlus2 className="h-3 w-3 text-green-500" />
    case 'read': return <Eye className="h-3 w-3 text-sky-500" />
    case 'blocked': return <XCircle className="h-3 w-3 text-red-500" />
  }
}

function Badge({ color, label }: { color: string; label: string }) {
  return <span className={cn('inline-flex items-center px-1.5 py-0.5 rounded bg-black/5 dark:bg-white/5', color)}>{label}</span>
}

function TreeView({ nodes, onJump, depth = 0 }: { nodes: TreeNode; onJump: (p: string) => void; depth?: number }) {
  const entries = [...nodes.children.values()].sort((a, b) => {
    const af = a.change ? 1 : 0
    const bf = b.change ? 1 : 0
    if (af !== bf) return af - bf // 文件夹在前
    return a.name.localeCompare(b.name)
  })
  return (
    <div className="text-[11px]">
      {entries.map(node => {
        const isFolder = node.children.size > 0
        return (
          <div key={node.path}>
            <div
              className={cn(
                'flex items-center gap-1.5 px-2 py-1',
                node.change ? 'hover:bg-gray-50 dark:hover:bg-white/5 cursor-pointer' : 'text-muted-foreground',
                depth > 0 && 'border-l border-gray-100 dark:border-white/5 ml-2',
              )}
              style={{ paddingLeft: 8 + depth * 12 }}
              onClick={() => node.change && onJump(node.path)}
              title={node.change ? '点击定位到对话中的该操作' : undefined}
            >
              {isFolder ? <FolderTree className="h-3 w-3 text-gray-400 shrink-0" /> : statusIcon(node.change!.status)}
              <span className={cn('truncate', node.change ? '' : 'text-muted-foreground')}>{node.name}</span>
              {node.change && node.change.ops > 1 && (
                <span className="text-[9px] text-muted-foreground ml-auto">×{node.change.ops}</span>
              )}
            </div>
            {isFolder && <TreeView nodes={node} onJump={onJump} depth={depth + 1} />}
          </div>
        )
      })}
    </div>
  )
}

export function SessionFileChanges({ toolCalls, className }: { toolCalls: ToolCallLocal[]; className?: string }) {
  const changes = useMemo(() => aggregateCodingChanges(toolCalls), [toolCalls])
  if (changes.length === 0) {
    return <div className={cn('text-xs text-muted-foreground', className)}>本次会话暂无文件变更。</div>
  }
  const counts = changes.reduce((acc, c) => { acc[c.status] = (acc[c.status] || 0) + 1; return acc }, {} as Record<string, number>)
  const tree = useMemo(() => buildTree(changes), [changes])
  const jump = (path: string) => {
    try {
      const el = document.querySelector(`[data-coding-path="${CSS.escape(path)}"]`)
      el?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    } catch { /* 选择器非法时忽略 */ }
  }
  return (
    <div className={cn('space-y-2', className)}>
      <div className="flex flex-wrap gap-2 text-[10px]">
        <Badge color="text-amber-500" label={`修改 ${counts.modified || 0}`} />
        <Badge color="text-green-500" label={`新建 ${counts.created || 0}`} />
        <Badge color="text-sky-500" label={`读取 ${counts.read || 0}`} />
        {counts.blocked ? <Badge color="text-red-500" label={`拒绝 ${counts.blocked}`} /> : null}
      </div>
      <div className="rounded-md border border-gray-200 dark:border-white/10 overflow-hidden">
        <TreeView nodes={tree} onJump={jump} />
      </div>
    </div>
  )
}
