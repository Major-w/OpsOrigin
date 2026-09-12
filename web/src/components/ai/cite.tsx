
// AI 对话引用（溯源）共享模块：解析带类型的引用标记、渲染内联 chip 与底部列表条目。
// 引用串格式（后端 ExtractCitations 产出）：
//   - 知识库文章: 无前缀原始 ID，如 "a1b2c3"（视为 kb 类型，兼容旧数据）
//   - CMDB 配置项: "ci:<CI的ID>"
//   - 监控指标/告警: "mon:<ID>"
// 正文内标记（模型书写）: [^kb:<ID>] / [^ci:<ID>] / [^mon:<ID>] / 简写 [^<ID>]

export type CiteType = 'kb' | 'ci' | 'mon'

export interface CiteMeta {
  /** 中文名 */
  label: string
  /** 短标（chip 内字母） */
  short: string
  /** 文字色（tailwind，明暗双态） */
  color: string
  /** 背景/描边色（tailwind） */
  bg: string
  /** 跳转路由构造 */
  route: (raw: string) => string
}

export const citeMeta: Record<CiteType, CiteMeta> = {
  kb: {
    label: '知识库',
    short: 'K',
    color: 'text-sky-600 dark:text-sky-300',
    bg: 'bg-sky-500/15 border-sky-500/30',
    route: (raw) => `/knowledge/${encodeURIComponent(raw)}`,
  },
  ci: {
    label: 'CMDB',
    short: 'C',
    color: 'text-emerald-600 dark:text-emerald-300',
    bg: 'bg-emerald-500/15 border-emerald-500/30',
    route: (raw) => `/cmdb/instances/${encodeURIComponent(raw)}`,
  },
  mon: {
    label: '监控',
    short: 'M',
    color: 'text-amber-600 dark:text-amber-300',
    bg: 'bg-amber-500/15 border-amber-500/30',
    route: () => `/monitor`,
  },
}

/** 解析引用串为类型 + 原始 ID。 */
export function parseCite(id: string): { type: CiteType; raw: string } {
  const i = id.indexOf(':')
  if (i > 0) {
    const t = id.slice(0, i)
    if (t === 'kb' || t === 'ci' || t === 'mon') return { type: t as CiteType, raw: id.slice(i + 1) }
  }
  return { type: 'kb', raw: id }
}

/** 引用跳转路由（监控类无具体 ID，落到监控中心首页）。 */
export function citeRoute(id: string): string {
  const { type, raw } = parseCite(id)
  return citeMeta[type].route(raw)
}

import type { ReactNode } from 'react'

/** 底部引用列表中的类型徽标 + 原文（可点击跳转）。 */
export function CiteChip({ id, onClick }: { id: string; onClick?: () => void }) {
  const { type, raw } = parseCite(id)
  const m = citeMeta[type]
  return (
    <span
      className={`inline-flex items-center gap-1 align-middle text-[10px] px-1.5 py-0.5 rounded-full border ${m.bg} ${m.color} cursor-pointer hover:brightness-110 transition`}
      title={`${m.label} · ${raw}`}
      onClick={onClick}
    >
      <span className="font-semibold">{m.short}</span>
      <span className="opacity-80 max-w-[8rem] truncate">{raw}</span>
    </span>
  )
}

/**
 * 将容器内正文里的 [^type:id] / [^id] 标记替换为内联 chip（点击滚动并高亮到底部对应引用条目）。
 * 跳过 code/pre/a/button 内部，避免破坏代码与已有链接。幂等由调用方 shouldLinkify 控制。
 */
export function linkifyInlineCites(root: HTMLElement, ids: string[], anchor: string) {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const p = node.parentElement
      if (!p) return NodeFilter.FILTER_REJECT
      if (p.closest('pre, code, a, button, sup')) return NodeFilter.FILTER_REJECT
      return NodeFilter.FILTER_ACCEPT
    },
  })
  const targets: Text[] = []
  let n = walker.nextNode()
  while (n) {
    targets.push(n as Text)
    n = walker.nextNode()
  }
  const re = /\[\^([^\]]+)\]/g
  for (const node of targets) {
    const text = node.nodeValue || ''
    if (!re.test(text)) continue
    re.lastIndex = 0
    let m: RegExpExecArray | null
    let last = 0
    let changed = false
    const frag = document.createDocumentFragment()
    while ((m = re.exec(text))) {
      const mark = m[1]
      const idx = ids.indexOf(mark)
      if (idx < 0) continue // 标记不在引用列表内（如模型误写），原样保留
      changed = true
      if (m.index > last) frag.appendChild(document.createTextNode(text.slice(last, m.index)))
      const { type, raw } = parseCite(mark)
      const meta = citeMeta[type]
      const span = document.createElement('span')
      span.className = `ai-cite-chip inline-flex items-center gap-0.5 align-middle text-[10px] px-1 py-[1px] rounded-full border ${meta.bg} ${meta.color} cursor-pointer hover:brightness-110 transition mx-0.5`
      span.title = `${meta.label} · ${raw}`
      const s = document.createElement('span')
      s.className = 'font-semibold'
      s.textContent = meta.short
      span.appendChild(s)
      const lbl = document.createElement('span')
      lbl.className = 'opacity-80 max-w-[7rem] truncate'
      lbl.textContent = raw.length > 12 ? raw.slice(0, 12) + '…' : raw
      span.appendChild(lbl)
      span.onclick = () => {
        const el = document.getElementById(`cite-${anchor}-${idx + 1}`)
        if (el) {
          el.scrollIntoView({ behavior: 'smooth', block: 'center' })
          el.classList.add('ai-cite-flash')
          setTimeout(() => el.classList.remove('ai-cite-flash'), 1200)
        }
      }
      frag.appendChild(span)
      last = m.index + m[0].length
    }
    if (changed) {
      if (last < text.length) frag.appendChild(document.createTextNode(text.slice(last)))
      node.parentNode?.replaceChild(frag, node)
    }
  }
}

/** 底部引用列表条目：类型图标 + 标题/原文 + 跳转。 */
export function CitationRow({
  id,
  title,
  dead,
  anchor,
  onClickTitle,
}: {
  id: string
  title?: string
  dead?: boolean
  anchor?: string
  onClickTitle?: () => void
}) {
  const { type, raw } = parseCite(id)
  const m = citeMeta[type]
  const route = citeRoute(id)
  if (dead) {
    return (
      <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground/70 pl-2 border-l-2 border-gray-200 dark:border-white/10 truncate cursor-not-allowed" title="该条目不存在或已删除">
        <span className={`shrink-0 h-3.5 w-3.5 rounded-full border flex items-center justify-center text-[9px] font-semibold ${m.bg} ${m.color}`}>{m.short}</span>
        [{raw}] 不存在或已删除
      </div>
    )
  }
  return (
    <a
      href={route}
      target={type === 'mon' ? undefined : '_blank'}
      rel="noopener"
      className={`flex items-center gap-1.5 text-[10px] pl-2 border-l-2 truncate transition-colors hover:underline ${m.color} border-current/30`}
      title={title || raw}
      onClick={onClickTitle}
    >
      <span className={`shrink-0 h-3.5 w-3.5 rounded-full border flex items-center justify-center text-[9px] font-semibold ${m.bg} ${m.color}`}>{m.short}</span>
      {title ? <><span className="truncate">{title}</span> <span className="text-muted-foreground">[{raw}]</span></> : <>[<span className="truncate">{raw}</span>]</>}
    </a>
  )
}

export type { ReactNode }
