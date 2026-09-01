
import { useState, useEffect } from 'react'
import { X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface ModalFormProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  children: React.ReactNode
  footer?: React.ReactNode
  width?: string
  className?: string
  submitText?: string
  cancelText?: string
  onSubmit?: () => void
  loading?: boolean
}

export function ModalForm({
  open,
  onOpenChange,
  title,
  children,
  footer,
  width = 'max-w-lg',
  className,
  submitText = '确定',
  cancelText = '取消',
  onSubmit,
  loading = false,
}: ModalFormProps) {
  // ESC 关闭
  useEffect(() => {
    const handleEsc = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onOpenChange(false)
    }
    if (open) document.addEventListener('keydown', handleEsc)
    return () => document.removeEventListener('keydown', handleEsc)
  }, [open, onOpenChange])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/50" onClick={() => onOpenChange(false)} />
      <div className={cn("relative z-50 w-full bg-white dark:bg-gray-900 rounded-xl shadow-2xl border", width, className)}>
        {/* 头部 */}
        <div className="flex items-center justify-between px-6 py-4 border-b">
          <h2 className="text-lg font-semibold">{title}</h2>
          <button
            onClick={() => onOpenChange(false)}
            className="p-1 hover:bg-gray-100 dark:hover:bg-gray-800 rounded"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* 内容 */}
        <div className="px-6 py-4 max-h-[60vh] overflow-y-auto">
          {children}
        </div>

        {/* 底部 */}
        {footer !== null && (
          <div className="flex items-center justify-end gap-2 px-6 py-4 border-t">
            {footer || (
              <>
                <Button variant="outline" onClick={() => onOpenChange(false)} disabled={loading}>
                  {cancelText}
                </Button>
                <Button onClick={onSubmit} disabled={loading}>
                  {loading ? '提交中...' : submitText}
                </Button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

// 带表单的 ModalForm
interface ModalFormWithFormProps extends Omit<ModalFormProps, 'onSubmit' | 'children'> {
  initialValues?: Record<string, any>
  onSubmit: (values: Record<string, any>) => Promise<void> | void
  children: (props: {
    values: Record<string, any>
    onChange: (name: string, value: any) => void
  }) => React.ReactNode
}

export function ModalFormWithForm({
  initialValues = {},
  onSubmit,
  children,
  ...props
}: ModalFormWithFormProps) {
  const [values, setValues] = useState(initialValues)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (props.open) setValues(initialValues)
  }, [props.open, initialValues])

  const handleChange = (name: string, value: any) => {
    setValues(prev => ({ ...prev, [name]: value }))
  }

  const handleSubmit = async () => {
    setLoading(true)
    try {
      await onSubmit(values)
      props.onOpenChange(false)
    } finally {
      setLoading(false)
    }
  }

  return (
    <ModalForm {...props} onSubmit={handleSubmit} loading={loading}>
      {children({ values, onChange: handleChange })}
    </ModalForm>
  )
}
