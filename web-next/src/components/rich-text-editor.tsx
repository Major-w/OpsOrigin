'use client'

import { useEffect, useRef } from 'react'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import UnderlineExt from '@tiptap/extension-underline'
import TaskList from '@tiptap/extension-task-list'
import TaskItem from '@tiptap/extension-task-item'
import Link from '@tiptap/extension-link'
import { Markdown } from 'tiptap-markdown'
import { cn } from '@/lib/utils'
import {
  Bold, Italic, Strikethrough, Underline, Code, Heading1, Heading2, Heading3,
  Quote, List, ListOrdered, ListChecks, Link as LinkIcon, Minus, Undo2, Redo2,
} from 'lucide-react'

interface RichTextEditorProps {
  value: string
  onChange: (markdown: string) => void
  className?: string
}

/**
 * 富文本编辑器(WYSIWYG), 基于 TipTap + tiptap-markdown。
 * 对外契约: value/onChange 均为 **Markdown 字符串** —— 存储格式与后端/渲染层完全兼容。
 * 功能: 标题/粗斜删除线/下划线/行内代码/代码块/引用/有序无序任务列表/链接/分割线/撤销重做。
 */
export function RichTextEditor({ value, onChange, className }: RichTextEditorProps) {
  const lastValueRef = useRef(value)

  const editor = useEditor({
    extensions: [
      StarterKit.configure({ heading: { levels: [1, 2, 3] } }),
      UnderlineExt,
      TaskList,
      TaskItem.configure({ nested: true }),
      Link.configure({ openOnClick: false, autolink: true, linkOnPaste: true }),
      Markdown.configure({
        html: false,
        tightLists: true,
        bulletListMarker: '-',
        linkify: true,
        transformPastedText: true,
        transformCopiedText: true,
      }),
    ],
    content: value,
    immediatelyRender: false,
    onUpdate: ({ editor }) => {
      const md = (editor as any).getMarkdown() as string
      lastValueRef.current = md
      onChange(md)
    },
  })

  // 外部 value 变化(AI 追加/版本回滚/录入方式切换)时同步编辑器; 正在输入时不覆盖
  useEffect(() => {
    if (!editor) return
    if (value !== lastValueRef.current) {
      lastValueRef.current = value
      editor.commands.setContent(value || '')
    }
  }, [value, editor])

  if (!editor) {
    return <div className="min-h-[400px] rounded-md border border-input bg-background" />
  }

  const run = (fn: () => void) => () => { fn(); editor.view.focus() }
  const active = (name: string, attrs?: Record<string, unknown>) => editor.isActive(name, attrs)
  const btnCls = (on: boolean) => cn(
    'flex h-8 w-8 shrink-0 items-center justify-center rounded-md transition-colors cursor-pointer',
    on ? 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground hover:bg-muted',
  )

  const setLink = () => {
    const prev = (editor.getAttributes('link').href as string) || ''
    const url = window.prompt('链接地址(留空移除链接)', prev || 'https://')
    if (url === null) return
    if (url === '') { editor.chain().focus().extendMarkRange('link').unsetLink().run(); return }
    editor.chain().focus().extendMarkRange('link').setLink({ href: url }).run()
  }

  return (
    <div className={cn('rounded-md border border-input bg-background', className)}>
      <div className="flex flex-wrap items-center gap-0.5 border-b border-border px-2 py-1.5">
        <button type="button" title="标题 1" className={btnCls(active('heading', { level: 1 }))} onClick={run(() => editor.chain().toggleHeading({ level: 1 }).run())}><Heading1 className="h-4 w-4" /></button>
        <button type="button" title="标题 2" className={btnCls(active('heading', { level: 2 }))} onClick={run(() => editor.chain().toggleHeading({ level: 2 }).run())}><Heading2 className="h-4 w-4" /></button>
        <button type="button" title="标题 3" className={btnCls(active('heading', { level: 3 }))} onClick={run(() => editor.chain().toggleHeading({ level: 3 }).run())}><Heading3 className="h-4 w-4" /></button>
        <span className="mx-1 h-5 w-px bg-border" />
        <button type="button" title="粗体 (⌘B)" className={btnCls(active('bold'))} onClick={run(() => editor.chain().toggleBold().run())}><Bold className="h-4 w-4" /></button>
        <button type="button" title="斜体 (⌘I)" className={btnCls(active('italic'))} onClick={run(() => editor.chain().toggleItalic().run())}><Italic className="h-4 w-4" /></button>
        <button type="button" title="删除线" className={btnCls(active('strike'))} onClick={run(() => editor.chain().toggleStrike().run())}><Strikethrough className="h-4 w-4" /></button>
        <button type="button" title="下划线" className={btnCls(active('underline'))} onClick={run(() => editor.chain().toggleUnderline().run())}><Underline className="h-4 w-4" /></button>
        <button type="button" title="行内代码" className={btnCls(active('code'))} onClick={run(() => editor.chain().toggleCode().run())}><Code className="h-4 w-4" /></button>
        <span className="mx-1 h-5 w-px bg-border" />
        <button type="button" title="引用" className={btnCls(active('blockquote'))} onClick={run(() => editor.chain().toggleBlockquote().run())}><Quote className="h-4 w-4" /></button>
        <button type="button" title="无序列表" className={btnCls(active('bulletList'))} onClick={run(() => editor.chain().toggleBulletList().run())}><List className="h-4 w-4" /></button>
        <button type="button" title="有序列表" className={btnCls(active('orderedList'))} onClick={run(() => editor.chain().toggleOrderedList().run())}><ListOrdered className="h-4 w-4" /></button>
        <button type="button" title="任务列表" className={btnCls(active('taskList'))} onClick={run(() => editor.chain().toggleTaskList().run())}><ListChecks className="h-4 w-4" /></button>
        <button type="button" title="代码块" className={btnCls(active('codeBlock'))} onClick={run(() => editor.chain().toggleCodeBlock().run())}><Code className="h-4 w-4" /></button>
        <button type="button" title="链接" className={btnCls(active('link'))} onClick={setLink}><LinkIcon className="h-4 w-4" /></button>
        <button type="button" title="分割线" className={btnCls(false)} onClick={run(() => editor.chain().setHorizontalRule().run())}><Minus className="h-4 w-4" /></button>
        <span className="mx-1 h-5 w-px bg-border" />
        <button type="button" title="撤销" className={btnCls(false)} onClick={run(() => editor.chain().undo().run())}><Undo2 className="h-4 w-4" /></button>
        <button type="button" title="重做" className={btnCls(false)} onClick={run(() => editor.chain().redo().run())}><Redo2 className="h-4 w-4" /></button>
      </div>
      <EditorContent editor={editor} className="markdown-body rich-editor" />
    </div>
  )
}
