'use client'

import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

interface ConfirmDialogProps {
  open: boolean
  onClose: () => void
  onConfirm: () => void
  title: string
  description?: string
  confirmLabel?: string
  loading?: boolean
}

export function ConfirmDialog({ open, onClose, onConfirm, title, description, confirmLabel = '确定', loading }: ConfirmDialogProps) {
  return (
    <Dialog open={open} onClose={onClose} title={title}>
      {description && <p className="text-sm text-muted-foreground">{description}</p>}
      <DialogFooter>
        <Button variant="outline" size="sm" onClick={onClose} disabled={loading}>取消</Button>
        <Button variant="destructive" size="sm" onClick={onConfirm} disabled={loading}>{confirmLabel}</Button>
      </DialogFooter>
    </Dialog>
  )
}
