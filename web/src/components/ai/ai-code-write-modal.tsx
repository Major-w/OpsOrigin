import { useState } from 'react'
import { Modal, Button, Input, Select } from 'antd'
import { Loader2, Sparkles, RefreshCw, CornerDownLeft, Copy } from 'lucide-react'
import { apiFetch } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { CodeEditor } from '@/components/ui/code-editor'

const { TextArea } = Input

const LANG_OPTIONS = [
  { value: 'shell', label: 'Shell (bash)' },
  { value: 'python', label: 'Python' },
  { value: 'bat', label: 'Batch (Windows)' },
]

const RIGOR_OPTIONS = [
  { value: 'simple', label: '简洁（最少代码，够用即止）' },
  { value: 'standard', label: '标准（含参数校验与输出）' },
  { value: 'robust', label: '健壮（含错误处理/幂等/回滚提示）' },
]

export interface AICodeWriteModalProps {
  open: boolean
  onClose: () => void
  /** 生成完成并确认后, 把代码回填到编辑器光标处 */
  onInsert: (code: string) => void
  /** 当前脚本语言, 作为默认值 */
  language?: string
  /** 当前脚本名, 作为需求描述预填 */
  scriptName?: string
  /** 当前脚本已有内容, 作为上下文一并交给模型改写/续写 */
  currentContent?: string
}

/** AI 辅助编写脚本弹窗: 描述需求 → 生成代码 → 预览 → 插入编辑器光标处。 */
export function AICodeWriteModal({
  open, onClose, onInsert, language = 'shell', scriptName = '', currentContent = '',
}: AICodeWriteModalProps) {
  const [requirement, setRequirement] = useState('')
  const [notes, setNotes] = useState('')
  const [lang, setLang] = useState(language)
  const [rigor, setRigor] = useState('standard')
  const [refine, setRefine] = useState(false) // 基于现有内容改写而非从零生成
  const [loading, setLoading] = useState(false)
  const [result, setResult] = useState('')

  const handleClose = () => {
    if (loading) return
    onClose()
    setTimeout(() => { setResult(''); setLoading(false) }, 200)
  }

  const generate = async () => {
    const req = requirement.trim() || scriptName.trim()
    if (!req) {
      getMessage().warning('请先描述脚本要做什么')
      return
    }
    setLoading(true)
    try {
      const res = await apiFetch<{ content: string }>('/scripts/ai-write', {
        method: 'POST',
        body: JSON.stringify({
          requirement: req,
          language: lang,
          notes,
          rigor,
          existing: refine ? currentContent : '',
        }),
      })
      setResult(res?.content || '')
      if (!res?.content) getMessage().warning('AI 未返回代码，请补充需求后重试')
    } catch (e: any) {
      getMessage().error(`AI 编写失败：${e?.message || '未知错误'}`)
    } finally {
      setLoading(false)
    }
  }

  const insert = () => {
    if (!result.trim()) return
    onInsert(result.trimEnd())
    onClose()
    setTimeout(() => { setResult(''); setLoading(false) }, 200)
    getMessage().success('已插入到编辑器')
  }

  return (
    <Modal
      title={<span className="flex items-center gap-2"><Sparkles className="h-4 w-4 text-blue-500" />AI 辅助编写脚本</span>}
      open={open}
      onCancel={handleClose}
      width={920}
      maskClosable={!loading}
      destroyOnHidden
      footer={
        <div className="flex items-center justify-between">
          <span className="text-[11px] text-muted-foreground">
            提示：在编辑器空行开头输入 <kbd className="rounded border border-border px-1">/</kbd> 也可唤起本弹窗
          </span>
          <div className="flex gap-2">
            <Button onClick={handleClose} disabled={loading}>取消</Button>
            {result.trim() ? (
              <>
                <Button
                  onClick={() => { navigator.clipboard?.writeText(result); getMessage().success('已复制代码') }}
                  disabled={loading}
                  icon={<Copy className="h-4 w-4" />}
                >复制</Button>
                <Button onClick={generate} disabled={loading} icon={loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}>
                  重新生成
                </Button>
                <Button type="primary" onClick={insert} disabled={loading} icon={<CornerDownLeft className="h-4 w-4" />}>
                  插入到编辑器
                </Button>
              </>
            ) : (
              <Button type="primary" onClick={generate} disabled={loading} icon={loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}>
                {loading ? '生成中…' : '生成脚本'}
              </Button>
            )}
          </div>
        </div>
      }
    >
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 py-1">
        <div className="space-y-3">
          <div>
            <label className="block text-xs font-medium mb-1">脚本要做什么 <span className="text-red-500">*</span></label>
            <TextArea
              value={requirement}
              onChange={e => setRequirement(e.target.value)}
              placeholder={'例：清理 /var/log 下 7 天前的 .log 文件，保留最近的，并输出释放的空间大小'}
              rows={4}
              maxLength={800}
              disabled={loading}
            />
          </div>
          <div>
            <label className="block text-xs font-medium mb-1">关键要点（可选，每行一条）</label>
            <TextArea
              value={notes}
              onChange={e => setNotes(e.target.value)}
              placeholder={'必须幂等，可重复执行\n用 rsync 而不是 cp\n失败时不要删除源文件'}
              rows={3}
              disabled={loading}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium mb-1">脚本语言</label>
              <Select value={lang} onChange={setLang} options={LANG_OPTIONS} className="w-full" disabled={loading} />
            </div>
            <div>
              <label className="block text-xs font-medium mb-1">严谨度</label>
              <Select value={rigor} onChange={setRigor} options={RIGOR_OPTIONS} className="w-full" disabled={loading} />
            </div>
          </div>
          {currentContent.trim() && (
            <label className="flex items-center gap-2 text-xs text-muted-foreground cursor-pointer select-none">
              <input type="checkbox" checked={refine} disabled={loading} onChange={e => setRefine(e.target.checked)} />
              基于编辑器现有内容改写（而不是从零生成）
            </label>
          )}
        </div>

        <div className="space-y-1">
          <div className="flex items-center justify-between">
            <label className="block text-xs font-medium">生成结果</label>
            {!!result.trim() && <span className="text-[11px] text-muted-foreground">{result.split('\n').length} 行</span>}
          </div>
          {loading ? (
            <div className="h-[340px] rounded-md border border-input bg-muted/20 flex flex-col items-center justify-center text-muted-foreground text-xs gap-2">
              <Loader2 className="h-5 w-5 animate-spin text-blue-500" />
              AI 正在编写脚本，请稍候…
            </div>
          ) : result.trim() ? (
            <CodeEditor value={result} language={lang} readOnly height="340px" />
          ) : (
            <div className="h-[340px] rounded-md border border-input bg-muted/20 flex items-center justify-center text-muted-foreground text-xs">
              填写左侧需求后点击「生成脚本」
            </div>
          )}
        </div>
      </div>
    </Modal>
  )
}
