import { useCallback, useRef, useState, type RefObject } from 'react'
import { Modal, Button, Input, Select } from 'antd'
import { Loader2, Sparkles, RefreshCw, CornerDownLeft } from 'lucide-react'
import { apiFetch } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { MarkdownViewer } from '@/components/markdown-viewer'

const { TextArea } = Input

export interface AIWriteModalProps {
  open: boolean
  onClose: () => void
  /** 生成完成并确认后, 把 Markdown 回填到编辑器光标处 */
  onInsert: (markdown: string) => void
  /** 预填分类(跟随当前文章分类) */
  defaultCategory?: string
  /** 可选分类列表 */
  categories?: string[]
}

const TONE_OPTIONS = [
  { value: 'professional', label: '专业务实（默认）' },
  { value: 'formal', label: '正式严谨（SOP/规范）' },
  { value: 'friendly', label: '亲切易懂（上手/科普）' },
  { value: 'concise', label: '精简要点（速查/FAQ）' },
]

const LENGTH_OPTIONS = [
  { value: 'short', label: '短（约 300 字）' },
  { value: 'medium', label: '中（约 600-800 字）' },
  { value: 'long', label: '长（1200 字以上）' },
]

/** AI 辅助编写知识弹窗: 填主题/大纲/语气/篇幅 → 生成 Markdown 预览 → 插入编辑器。 */
export function AIWriteModal({ open, onClose, onInsert, defaultCategory, categories = [] }: AIWriteModalProps) {
  const [topic, setTopic] = useState('')
  const [outline, setOutline] = useState('')
  const [tone, setTone] = useState('professional')
  const [length, setLength] = useState('medium')
  const [category, setCategory] = useState(defaultCategory || '')
  const [loading, setLoading] = useState(false)
  const [result, setResult] = useState('')
  const [showPreview, setShowPreview] = useState(true)

  const reset = () => {
    setResult('')
    setLoading(false)
  }

  const handleClose = () => {
    if (loading) return
    onClose()
    // 关闭后清空上次结果, 下次打开是干净状态(保留分类预填)
    setTimeout(reset, 200)
  }

  const generate = async () => {
    const t = topic.trim()
    if (!t) {
      getMessage().warning('请先填写文章主题')
      return
    }
    setLoading(true)
    try {
      const res = await apiFetch<{ content: string }>('/knowledge/ai-write', {
        method: 'POST',
        body: JSON.stringify({ topic: t, outline, tone, length, category }),
      })
      setResult(res?.content || '')
      if (!res?.content) getMessage().warning('AI 未返回内容，请调整主题后重试')
    } catch (e: any) {
      getMessage().error(`AI 编写失败：${e?.message || '未知错误'}`)
    } finally {
      setLoading(false)
    }
  }

  const insert = () => {
    if (!result.trim()) return
    onInsert(result)
    handleClose()
    getMessage().success('已插入到编辑器')
  }

  return (
    <Modal
      title={<span className="flex items-center gap-2"><Sparkles className="h-4 w-4 text-blue-500" />AI 辅助编写</span>}
      open={open}
      onCancel={handleClose}
      width={860}
      maskClosable={!loading}
      destroyOnHidden
      footer={
        <div className="flex items-center justify-between">
          <span className="text-[11px] text-muted-foreground">
            提示：在编辑器空行输入 <kbd className="rounded border border-border px-1">/</kbd> 也可唤起本弹窗
          </span>
          <div className="flex gap-2">
            <Button onClick={handleClose} disabled={loading}>取消</Button>
            {result.trim() ? (
              <>
                <Button onClick={generate} disabled={loading} icon={loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}>
                  重新生成
                </Button>
                <Button type="primary" onClick={insert} disabled={loading} icon={<CornerDownLeft className="h-4 w-4" />}>
                  插入到编辑器
                </Button>
              </>
            ) : (
              <Button type="primary" onClick={generate} disabled={loading} icon={loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}>
                {loading ? '生成中…' : '生成文章'}
              </Button>
            )}
          </div>
        </div>
      }
    >
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 py-1">
        <div className="space-y-3">
          <div>
            <label className="block text-xs font-medium mb-1">文章主题 <span className="text-red-500">*</span></label>
            <Input
              value={topic}
              onChange={e => setTopic(e.target.value)}
              placeholder="如：Kubernetes 节点 NotReady 排查手册"
              maxLength={200}
              disabled={loading}
            />
          </div>
          <div>
            <label className="block text-xs font-medium mb-1">大纲要点（可选，每行一条）</label>
            <TextArea
              value={outline}
              onChange={e => setOutline(e.target.value)}
              placeholder={'常见原因分类\n排查步骤\n修复与验证\n预防建议'}
              rows={5}
              disabled={loading}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium mb-1">语气</label>
              <Select value={tone} onChange={setTone} options={TONE_OPTIONS} className="w-full" disabled={loading} />
            </div>
            <div>
              <label className="block text-xs font-medium mb-1">篇幅</label>
              <Select value={length} onChange={setLength} options={LENGTH_OPTIONS} className="w-full" disabled={loading} />
            </div>
          </div>
          {categories.length > 0 && (
            <div>
              <label className="block text-xs font-medium mb-1">目标分类</label>
              <Select
                value={category || undefined}
                onChange={v => setCategory(v || '')}
                placeholder="跟随当前分类"
                allowClear
                className="w-full"
                disabled={loading}
                options={categories.map(c => ({ value: c, label: c }))}
              />
            </div>
          )}
        </div>

        <div className="space-y-1">
          <div className="flex items-center justify-between">
            <label className="block text-xs font-medium">生成结果</label>
            {result.trim() && (
              <button
                type="button"
                className="text-[11px] text-muted-foreground hover:text-foreground"
                onClick={() => setShowPreview(v => !v)}
              >
                {showPreview ? '查看源码' : '查看预览'}
              </button>
            )}
          </div>
          <div className="h-[340px] rounded-md border border-input bg-muted/20 overflow-auto">
            {loading ? (
              <div className="h-full flex flex-col items-center justify-center text-muted-foreground text-xs gap-2">
                <Loader2 className="h-5 w-5 animate-spin text-blue-500" />
                AI 正在撰写，请稍候…
              </div>
            ) : result.trim() ? (
              showPreview
                ? <div className="p-3"><MarkdownViewer content={result} /></div>
                : <pre className="p-3 text-[11px] whitespace-pre-wrap break-words font-mono">{result}</pre>
            ) : (
              <div className="h-full flex items-center justify-center text-muted-foreground text-xs">
                填写左侧信息后点击「生成文章」
              </div>
            )}
          </div>
        </div>
      </div>
    </Modal>
  )
}

// ---------------------------------------------------------------------------
// useAIWrite: 把弹窗开合 + 编辑器「/」触发 + 光标处回填收敛成一个 hook, 两个编辑器页复用。
// ---------------------------------------------------------------------------
export interface UseAIWriteOptions {
  /** 读取当前编辑器内容 */
  getContent: () => string
  /** 写回编辑器内容 */
  setContent: (v: string) => void
  defaultCategory?: string
  categories?: string[]
  /** 复用调用方已有的 textarea ref(如新建页的 mdTextareaRef); 不传则 hook 内部创建 */
  editorRef?: RefObject<HTMLTextAreaElement>
}

export function useAIWrite({ getContent, setContent, defaultCategory, categories, editorRef: externalRef }: UseAIWriteOptions) {
  const [open, setOpen] = useState(false)
  const internalRef = useRef<HTMLTextAreaElement>(null)
  const editorRef: RefObject<HTMLTextAreaElement> = externalRef ?? internalRef

  /** 任意入口打开弹窗 */
  const openModal = useCallback(() => setOpen(true), [])
  const closeModal = useCallback(() => setOpen(false), [])

  /** 在光标处插入 Markdown, 并恢复光标到插入内容之后 */
  const insertAtCursor = useCallback((md: string) => {
    const ta = editorRef.current
    const cur = getContent() || ''
    if (!ta) {
      setContent(cur ? `${cur}\n\n${md}` : md)
      return
    }
    const start = ta.selectionStart ?? cur.length
    const end = ta.selectionEnd ?? start
    const before = cur.slice(0, start)
    const after = cur.slice(end)
    const preSep = before && !before.endsWith('\n') ? '\n\n' : ''
    const postSep = after && !after.startsWith('\n') ? '\n' : ''
    const next = before + preSep + md + postSep + after
    setContent(next)
    const caret = (before + preSep + md).length
    requestAnimationFrame(() => {
      try {
        ta.focus()
        ta.setSelectionRange(caret, caret)
      } catch { /* ignore */ }
    })
  }, [getContent, setContent])

  /** 编辑器 keydown: 在空行行首输入 `/` 唤起弹窗(不插入字符) */
  const onEditorKeyDown = useCallback((e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== '/' || e.nativeEvent.isComposing) return
    const ta = e.currentTarget
    const before = ta.value.slice(0, ta.selectionStart ?? 0)
    if (before === '' || before.endsWith('\n')) {
      e.preventDefault()
      setOpen(true)
    }
  }, [])

  return {
    open,
    openModal,
    closeModal,
    editorRef,
    onEditorKeyDown,
    insertAtCursor,
    modalProps: {
      open,
      onClose: closeModal,
      onInsert: insertAtCursor,
      defaultCategory,
      categories,
    } as AIWriteModalProps,
  }
}
