
import { useRef, useEffect, useState, type MutableRefObject } from 'react'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter } from '@codemirror/view'
import { EditorState } from '@codemirror/state'
import { defaultKeymap, indentWithTab, history, historyKeymap } from '@codemirror/commands'
import { syntaxHighlighting, defaultHighlightStyle, bracketMatching, foldGutter, indentOnInput } from '@codemirror/language'
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search'
import { autocompletion, completionKeymap, closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete'
import { lintKeymap } from '@codemirror/lint'
import { oneDark } from '@codemirror/theme-one-dark'
import { python } from '@codemirror/lang-python'
import { javascript } from '@codemirror/lang-javascript'
import { sql } from '@codemirror/lang-sql'
import { cn } from '@/lib/utils'

const LANG_EXTENSIONS: Record<string, any> = {
  shell: javascript(),
  python: python(),
  javascript: javascript(),
  sql: sql(),
  bat: javascript(),
}

interface CodeEditorProps {
  value: string
  onChange?: (value: string) => void
  language?: string
  placeholder?: string
  readOnly?: boolean
  className?: string
  height?: string
  /** 暴露内部 EditorView, 供调用方做「光标处插入」等编辑操作 */
  viewRef?: MutableRefObject<EditorView | null>
  /** 键盘钩子: 返回 true 表示已消费该按键(阻止默认输入), 如空行行首输入 `/` 唤起 AI 编写 */
  onKeyDown?: (e: KeyboardEvent, view: EditorView) => boolean | void
}

export function CodeEditor({
  value, onChange, language = 'shell', placeholder, readOnly = false, className, height = '300px',
  viewRef: externalViewRef, onKeyDown,
}: CodeEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const [isDark, setIsDark] = useState(false)
  // 回调走 ref: 编辑器实例只在 language/isDark 变化时重建, 直接闭包会捕获旧回调
  const cbRef = useRef({ onChange, onKeyDown })
  cbRef.current = { onChange, onKeyDown }

  useEffect(() => {
    setIsDark(document.documentElement.classList.contains('dark'))
    const observer = new MutationObserver(() => setIsDark(document.documentElement.classList.contains('dark')))
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    if (!containerRef.current) return

    const langExt = LANG_EXTENSIONS[language] || javascript()

    const updateListener = EditorView.updateListener.of((update) => {
      if (update.docChanged) {
        cbRef.current.onChange?.(update.state.doc.toString())
      }
    })

    const eventHandlers = EditorView.domEventHandlers({
      keydown: (e, view) => {
        const handled = cbRef.current.onKeyDown?.(e, view)
        if (handled) {
          e.preventDefault()
          return true
        }
        return false
      },
    })

    const themeExt = isDark ? oneDark : []
    const state = EditorState.create({
      doc: value,
      extensions: [
        lineNumbers(),
        highlightActiveLineGutter(),
        history(),
        foldGutter(),
        indentOnInput(),
        syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
        bracketMatching(),
        closeBrackets(),
        autocompletion(),
        highlightActiveLine(),
        highlightSelectionMatches(),
        keymap.of([
          ...closeBracketsKeymap,
          ...defaultKeymap,
          ...searchKeymap,
          ...historyKeymap,
          ...completionKeymap,
          ...lintKeymap,
          indentWithTab,
        ]),
        langExt,
        themeExt,
        updateListener,
        eventHandlers,
        EditorView.lineWrapping,
        readOnly ? EditorState.readOnly.of(true) : [],
        placeholder ? EditorView.contentAttributes.of({ 'aria-placeholder': placeholder }) : [],
      ].filter(Boolean),
    })

    const view = new EditorView({
      state,
      parent: containerRef.current,
    })

    viewRef.current = view
    if (externalViewRef) externalViewRef.current = view

    return () => {
      view.destroy()
      viewRef.current = null
      if (externalViewRef) externalViewRef.current = null
    }
  }, [language, isDark])

  useEffect(() => {
    if (viewRef.current && value !== viewRef.current.state.doc.toString()) {
      viewRef.current.dispatch({
        changes: { from: 0, to: viewRef.current.state.doc.length, insert: value },
      })
    }
  }, [value])

  return (
    <div
      ref={containerRef}
      className={cn("rounded-md border border-input overflow-auto", className)}
      style={{ height }}
    />
  )
}
