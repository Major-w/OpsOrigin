import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { EditorView } from '@codemirror/view'
import { cn } from '@/lib/utils'

/**
 * 斜杠命令(/) 菜单的通用实现。
 *
 * 设计要点(修复「输入 `/` 直接弹弹窗导致无法输入斜杠」的问题):
 * - 在空行行首输入 `/` 时 **不阻止默认输入** —— 斜杠作为普通字符落到编辑器里,
 *   同时弹出可用命令清单; 若用户不选择任何命令(继续输入/Esc/点空白), 斜杠原样保留。
 * - 选中命令后, 才删除 `/过滤词` 并执行命令副作用(如打开 AI 编写弹窗)。
 * - 编辑器中立: 通过 SlashEditorAdapter 适配原生 textarea 与 CodeMirror,
 *   两处共用同一套交互与键盘语义。
 */

export interface SlashCommandContext {
  /** 在命令触发处插入文本(已自动跳过被删除的 `/过滤词` 区间) */
  insert: (text: string) => void
}

export interface SlashCommand {
  id: string
  label: string
  /** 右侧说明文字 */
  hint?: string
  /** 过滤关键词(中英文皆可) */
  keywords?: string[]
  icon?: ReactNode
  run: (ctx: SlashCommandContext) => void
}

export interface SlashMenuAnchor {
  /** 视口坐标(px) */
  left: number
  top: number
  /** 光标行高, 用于把菜单放到光标所在行下方 */
  lineHeight: number
}

export interface SlashEditorAdapter {
  /** 承载输入事件的 DOM 节点(用于同步过滤词); null 时仅靠 keydown 同步 */
  dom: HTMLElement | null
  getValue: () => string
  getCaret: () => number
  /** 用 insert 替换 [from, to) 区间, 并把光标置于 from + insert.length */
  replaceRange: (from: number, to: number, insert: string) => void
  /** 计算 index 处插入点的视口坐标 */
  anchorAt: (index: number) => SlashMenuAnchor | null
}

/** 过滤词最大长度: 超过则认为用户不是在用命令, 直接关闭菜单 */
const MAX_QUERY_LEN = 24

export function filterSlashCommands(commands: SlashCommand[], query: string): SlashCommand[] {
  const q = query.trim().toLowerCase()
  if (!q) return commands
  return commands.filter(c => {
    if (c.label.toLowerCase().includes(q)) return true
    if (c.id.toLowerCase().includes(q)) return true
    return (c.keywords || []).some(k => k.toLowerCase().includes(q))
  })
}

interface MenuState {
  open: boolean
  /** `/` 在文档中的起始偏移 */
  from: number
  /** `/` 之后已输入的过滤词(不含 `/`) */
  query: string
  anchor: SlashMenuAnchor | null
}

const CLOSED: MenuState = { open: false, from: -1, query: '', anchor: null }

/**
 * useSlashCommands 管理斜杠命令菜单的开关、过滤与键盘导航。
 * handleKeyDown 返回 true 表示已消费该按键(调用方应 preventDefault)。
 */
export function useSlashCommands(commands: SlashCommand[]) {
  const [state, setState] = useState<MenuState>(CLOSED)
  const [active, setActive] = useState(0)
  const adapterRef = useRef<SlashEditorAdapter | null>(null)

  const items = useMemo(() => filterSlashCommands(commands, state.query), [commands, state.query])

  const close = useCallback(() => {
    setState(CLOSED)
    setActive(0)
  }, [])

  const openAt = useCallback((adapter: SlashEditorAdapter, from: number) => {
    adapterRef.current = adapter
    setState({ open: true, from, query: '', anchor: adapter.anchorAt(from) })
    setActive(0)
  }, [])

  // 输入同步: 菜单打开时监听输入事件, 用 `/` 之后实际输入的文本更新过滤词;
  // 一旦输入内容不再像命令(空格/换行/超长/删掉斜杠), 自动关闭菜单并保留用户输入。
  useEffect(() => {
    if (!state.open) return
    const dom = adapterRef.current?.dom
    if (!dom) return
    const sync = () => {
      const ad = adapterRef.current
      if (!ad) return
      const value = ad.getValue()
      const caret = ad.getCaret()
      const from = state.from
      const typed = value.slice(from, caret)
      if (!typed.startsWith('/') || typed.length > MAX_QUERY_LEN || /[\s]/.test(typed)) {
        close()
        return
      }
      setState(prev => (prev.open ? { ...prev, query: typed.slice(1) } : prev))
    }
    dom.addEventListener('input', sync)
    return () => dom.removeEventListener('input', sync)
  }, [state.open, state.from, close])

  const select = useCallback((adapter: SlashEditorAdapter, cmd: SlashCommand) => {
    const caret = adapter.getCaret()
    const from = state.from
    const to = Math.max(from, caret)
    // 先收集命令想插入的内容, 再用「一次性区间替换」删除 `/过滤词` 并写入内容 ——
    // 拆成两次替换会基于同一份旧文本互相覆盖(受控 textarea 的状态尚未回写)。
    let pending: string | null = null
    close()
    cmd.run({ insert: (text: string) => { pending = text } })
    if (from >= 0) adapter.replaceRange(from, to, pending ?? '')
  }, [state.from, close])

  const handleKeyDown = useCallback((e: { key: string; shiftKey?: boolean }, adapter: SlashEditorAdapter): boolean => {
    if (state.open) {
      if (e.key === 'ArrowDown') {
        setActive(i => (items.length ? (i + 1) % items.length : 0))
        return true
      }
      if (e.key === 'ArrowUp') {
        setActive(i => (items.length ? (i - 1 + items.length) % items.length : 0))
        return true
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        const cmd = items[active]
        if (cmd) {
          select(adapter, cmd)
          return true
        }
        close()
        return false
      }
      if (e.key === 'Escape') {
        close()
        return true
      }
      // 其余按键交给编辑器: `/` 与后续字符正常落入文本
      return false
    }

    if (e.key === '/') {
      const value = adapter.getValue()
      const caret = adapter.getCaret()
      const atLineStart = caret === 0 || value[caret - 1] === '\n'
      if (atLineStart) {
        // 关键: 返回 false → 不阻止默认输入, 斜杠正常写入;
        // 同时打开命令清单, 用户若不选择则斜杠就是普通字符。
        openAt(adapter, caret)
      }
    }
    return false
  }, [state.open, items, active, select, close, openAt])

  return {
    open: state.open, query: state.query, anchor: state.anchor, items, active, setActive, close, handleKeyDown, openAt,
    /** 供浮层鼠标点击选中(使用最近一次交互绑定的 adapter) */
    selectIndex: (i: number) => {
      const ad = adapterRef.current
      const cmd = items[i]
      if (ad && cmd) select(ad, cmd)
    },
  }
}

/** SlashCommandMenu 命令清单浮层(视口定位, 行下方展开, 空间不足时向上翻转)。 */
export function SlashCommandMenu({
  anchor, items, active, onHover, onSelect,
}: {
  anchor: SlashMenuAnchor | null
  items: SlashCommand[]
  active: number
  onHover: (index: number) => void
  onSelect: (cmd: SlashCommand) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [flipUp, setFlipUp] = useState(false)

  useEffect(() => {
    const el = ref.current
    if (!el || !anchor) return
    const h = el.offsetHeight || 240
    setFlipUp(anchor.top + anchor.lineHeight + h + 8 > window.innerHeight)
  }, [anchor, items.length])

  if (!anchor) return null

  const top = flipUp
    ? Math.max(8, anchor.top - (ref.current?.offsetHeight || 240) - 4)
    : anchor.top + anchor.lineHeight + 4

  return (
    <div
      ref={ref}
      data-slash-menu
      role="listbox"
      className="fixed z-[1080] w-64 max-h-[280px] overflow-auto rounded-md border border-border bg-popover text-popover-foreground shadow-lg py-1"
      style={{ left: Math.min(anchor.left, window.innerWidth - 272), top }}
    >
      {items.length === 0 ? (
        <div className="px-3 py-2 text-xs text-muted-foreground">无匹配命令</div>
      ) : (
        items.map((c, i) => (
          <button
            key={c.id}
            type="button"
            role="option"
            aria-selected={i === active}
            onMouseEnter={() => onHover(i)}
            onMouseDown={e => { e.preventDefault(); onSelect(c) }}
            className={cn(
              'w-full flex items-center gap-2 px-2.5 py-1.5 text-left text-sm',
              i === active ? 'bg-accent text-accent-foreground' : 'hover:bg-muted/60',
            )}
          >
            <span className="shrink-0 text-muted-foreground">{c.icon}</span>
            <span className="flex-1 truncate">{c.label}</span>
            {c.hint && <span className="shrink-0 text-[10px] text-muted-foreground">{c.hint}</span>}
          </button>
        ))
      )}
    </div>
  )
}

/**
 * codeMirrorSlashAdapter 为 CodeMirror(脚本编辑器)构造适配器。
 * 坐标取自 coordsAtPos(视口坐标), 文本改动通过 dispatch 生效。
 */
export function codeMirrorSlashAdapter(view: EditorView): SlashEditorAdapter {
  return {
    dom: view.dom,
    getValue: () => view.state.doc.toString(),
    getCaret: () => view.state.selection.main.head,
    replaceRange: (from, to, insert) => {
      view.dispatch({
        changes: { from, to, insert },
        selection: { anchor: from + insert.length },
      })
      view.focus()
    },
    anchorAt: (index) => {
      const coords = view.coordsAtPos(index)
      if (!coords) return null
      return {
        left: coords.left,
        top: coords.top,
        lineHeight: Math.max(16, coords.bottom - coords.top),
      }
    },
  }
}

/**
 * textareaSlashAdapter 为原生 textarea 构造适配器。
 * 通过镜像 div 计算光标像素位置, 使菜单贴着光标显示。
 */
export function textareaSlashAdapter(
  ta: HTMLTextAreaElement,
  setValue: (v: string) => void,
): SlashEditorAdapter {
  return {
    dom: ta,
    getValue: () => ta.value,
    getCaret: () => ta.selectionStart ?? ta.value.length,
    replaceRange: (from, to, insert) => {
      const v = ta.value
      const next = v.slice(0, from) + insert + v.slice(to)
      setValue(next)
      const caret = from + insert.length
      requestAnimationFrame(() => {
        try {
          ta.focus()
          ta.setSelectionRange(caret, caret)
        } catch { /* ignore */ }
      })
    },
    anchorAt: (index) => caretAnchorInTextarea(ta, index),
  }
}

// 需要从 textarea 复制到镜像节点上的样式属性(保证换行/字宽完全一致)。
const MIRROR_PROPS = [
  'boxSizing', 'width', 'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth',
  'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
  'fontStyle', 'fontVariant', 'fontWeight', 'fontStretch', 'fontSize', 'lineHeight', 'fontFamily',
  'textAlign', 'textTransform', 'textIndent', 'letterSpacing', 'wordSpacing', 'tabSize',
]

/** caretAnchorInTextarea 用镜像 div 计算 (index) 处光标在视口中的坐标。 */
export function caretAnchorInTextarea(ta: HTMLTextAreaElement, index: number): SlashMenuAnchor | null {
  const style = window.getComputedStyle(ta)
  const mirror = document.createElement('div')
  mirror.style.position = 'absolute'
  mirror.style.visibility = 'hidden'
  mirror.style.whiteSpace = 'pre-wrap'
  mirror.style.wordWrap = 'break-word'
  mirror.style.top = '0'
  mirror.style.left = '0'
  for (const p of MIRROR_PROPS) {
    // 逐项拷贝计算样式; 个别属性在部分浏览器缺失时赋空值也不会抛错
    ;(mirror.style as unknown as Record<string, string>)[p] = (style as unknown as Record<string, string>)[p] || ''
  }
  mirror.textContent = ta.value.slice(0, index)
  const marker = document.createElement('span')
  marker.textContent = ta.value.slice(index) || '.'
  mirror.appendChild(marker)
  document.body.appendChild(mirror)
  const rect = ta.getBoundingClientRect()
  const lineHeight = parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.5 || 20
  const left = rect.left + marker.offsetLeft
  const top = rect.top + marker.offsetTop - ta.scrollTop
  document.body.removeChild(mirror)
  return { left: Math.max(8, left), top, lineHeight }
}
