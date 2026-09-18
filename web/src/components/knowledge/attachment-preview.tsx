
// 知识库附件在线预览弹层。
// 背景: 后端下载端点此前硬编码 Content-Disposition: attachment(只能下载), 现已改为默认 inline。
// 本组件按文件类型分流渲染: 图片/PDF 走 blob 内联(带鉴权), Office/文本走后端抽取的纯文本。
// 二进制无法预览时给出明确的下载出口, 不留白屏。

import { useCallback, useEffect, useState } from 'react'
import { Modal, Button, Spin } from 'antd'
import { Download, FileQuestion, Loader2, AlertCircle } from 'lucide-react'
import { apiFetchRaw, apiFetch } from '@/lib/utils'

export interface PreviewAttachment {
  id: number
  file_name: string
  file_size: number
  file_type: string
}

const IMG_EXT = ['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp']
const TEXT_EXT = ['txt', 'md', 'markdown', 'csv', 'json', 'yaml', 'yml', 'log', 'sql', 'xml', 'ini', 'toml', 'conf', 'go', 'py', 'js', 'ts', 'java', 'sh']

type Kind = 'image' | 'pdf' | 'text' | 'office' | 'binary'

function kindOf(name: string): Kind {
  const ext = (name.split('.').pop() || '').toLowerCase()
  if (IMG_EXT.includes(ext)) return 'image'
  if (ext === 'pdf') return 'pdf'
  if (TEXT_EXT.includes(ext)) return 'text'
  if (['docx', 'xlsx', 'pptx', 'doc', 'xls', 'ppt', 'html', 'htm'].includes(ext)) return 'office'
  return 'binary'
}

export function AttachmentPreview({
  open,
  articleId,
  attachment,
  onClose,
}: {
  open: boolean
  articleId: string
  attachment: PreviewAttachment | null
  onClose: () => void
}) {
  const [loading, setLoading] = useState(false)
  const [blobUrl, setBlobUrl] = useState<string>('')
  const [text, setText] = useState<string>('')
  const [error, setError] = useState<string>('')

  const reset = useCallback(() => {
    setLoading(false)
    setError('')
    setText('')
    if (blobUrl) URL.revokeObjectURL(blobUrl)
    setBlobUrl('')
  }, [blobUrl])

  useEffect(() => {
    if (!open || !attachment) return
    let cancelled = false
    const kind = kindOf(attachment.file_name)
    const run = async () => {
      setLoading(true)
      setError('')
      setText('')
      try {
        if (kind === 'image' || kind === 'pdf') {
          // 内联渲染需要同源 blob: 直接用 <img>/<iframe src=API> 发不出鉴权头, 故先取回字节。
          const res = await apiFetchRaw(`/knowledge/${articleId}/attachments/${attachment.id}`)
          if (!res.ok) throw new Error(`读取失败(${res.status})`)
          const blob = await res.blob()
          if (cancelled) return
          setBlobUrl(URL.createObjectURL(blob))
        } else if (kind === 'text') {
          const res = await apiFetchRaw(`/knowledge/${articleId}/attachments/${attachment.id}`)
          if (!res.ok) throw new Error(`读取失败(${res.status})`)
          const t = await res.text()
          if (cancelled) return
          setText(t)
        } else {
          // Office/HTML: 走后端抽取端点拿纯文本(PDF/DOCX/XLSX/PPTX 均已支持)
          const r = await apiFetch<{ text: string }>(`/knowledge/${articleId}/attachments/${attachment.id}/text`)
          if (cancelled) return
          setText(r?.text || '')
        }
      } catch (e: any) {
        if (!cancelled) setError(e?.message || '预览失败')
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    run()
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, articleId, attachment?.id])

  const handleClose = () => {
    reset()
    onClose()
  }

  const handleDownload = async () => {
    if (!attachment) return
    try {
      const res = await apiFetchRaw(`/knowledge/${articleId}/attachments/${attachment.id}?download=1`)
      if (!res.ok) throw new Error(`下载失败(${res.status})`)
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = attachment.file_name
      link.click()
      URL.revokeObjectURL(url)
    } catch (e: any) {
      setError(e?.message || '下载失败')
    }
  }

  const kind = attachment ? kindOf(attachment.file_name) : 'binary'

  return (
    <Modal
      open={open}
      onCancel={handleClose}
      footer={[
        <Button key="dl" size="small" icon={<Download className="h-3.5 w-3.5" />} onClick={handleDownload}>下载</Button>,
        <Button key="close" size="small" type="primary" onClick={handleClose}>关闭</Button>,
      ]}
      title={<span className="text-sm truncate inline-block max-w-[24rem] align-middle">{attachment?.file_name}</span>}
      width={880}
      destroyOnHidden
    >
      <div className="min-h-[240px] max-h-[65vh] overflow-auto">
        {loading && (
          <div className="flex items-center justify-center gap-2 py-16 text-xs text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" /> 正在加载预览…
          </div>
        )}
        {!loading && error && (
          <div className="flex flex-col items-center justify-center gap-2 py-16 text-xs">
            <AlertCircle className="h-6 w-6 text-amber-500" />
            <span className="text-muted-foreground">{error}</span>
            <span className="text-[10px] text-muted-foreground">该格式无法在线预览，可下载后查看</span>
          </div>
        )}
        {!loading && !error && kind === 'image' && blobUrl && (
          <div className="flex justify-center bg-muted/30 rounded p-2">
            <img src={blobUrl} alt={attachment?.file_name} className="max-h-[60vh] object-contain" />
          </div>
        )}
        {!loading && !error && kind === 'pdf' && blobUrl && (
          <iframe src={blobUrl} title={attachment?.file_name} className="w-full h-[60vh] border-0 rounded" />
        )}
        {!loading && !error && (kind === 'text' || kind === 'office') && (
          text
            ? <pre className="whitespace-pre-wrap break-words text-xs leading-relaxed font-mono bg-muted/30 rounded p-3">{text}</pre>
            : <div className="flex flex-col items-center justify-center gap-2 py-16 text-xs text-muted-foreground">
                <FileQuestion className="h-6 w-6" /> 未抽取到文本内容（可能是扫描件或空文件）
              </div>
        )}
        {!loading && !error && kind === 'binary' && (
          <div className="flex flex-col items-center justify-center gap-2 py-16 text-xs text-muted-foreground">
            <FileQuestion className="h-6 w-6" /> 该格式暂不支持在线预览，请下载后查看
          </div>
        )}
      </div>
      {loading && <div className="hidden"><Spin /></div>}
    </Modal>
  )
}
