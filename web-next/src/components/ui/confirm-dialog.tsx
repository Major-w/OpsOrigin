'use client'

import { useEffect, useState } from 'react'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

interface ConfirmDialogProps {
  open: boolean
  onClose: () => void
  onConfirm: () => void
  title: string
  description?: string
  confirmLabel?: string
  loading?: boolean
  /** 二次确认：需输入与 requireText 完全一致的文本后才允许确认（如模型名/实例名/数量） */
  requireText?: string
  /** 输入框提示文案（默认「请输入 xxx 以确认」） */
  requireLabel?: string
}

export function ConfirmDialog({ open, onClose, onConfirm, title, description, confirmLabel = '确定', loading, requireText, requireLabel }: ConfirmDialogProps) {
  const [typed, setTyped] = useState('')
  useEffect(() => {
    if (!open) setTyped('')
  }, [open])

  const needText = requireText !== undefined && requireText !== ''
  const matched = !needText || typed === requireText

  return (
    <Dialog open={open} onClose={onClose} title={title}>
      {description && <p className="text-sm text-muted-foreground">{description}</p>}
      {needText && (
        <div className="space-y-1.5">
          <Input
            autoFocus
            value={typed}
            onChange={e => setTyped(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter' && matched && !loading) onConfirm() }}
            placeholder={requireLabel || `请输入 ${requireText} 以确认`}
            className="text-sm"
          />
          <p className="text-[11px] text-muted-foreground">
            请输入{requireLabel ? `「${requireLabel}」` : '完整名称'}以确认删除，此操作不可撤销。
          </p>
        </div>
      )}
      <DialogFooter>
        <Button variant="outline" size="sm" onClick={onClose} disabled={loading}>取消</Button>
        <Button variant="destructive" size="sm" onClick={onConfirm} disabled={loading || !matched}>{confirmLabel}</Button>
      </DialogFooter>
    </Dialog>
  )
}
