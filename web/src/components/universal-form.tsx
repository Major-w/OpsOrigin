
import { useState, useCallback, useMemo, type ReactNode } from 'react'
import { Modal, Button, Input, Select, Switch } from 'antd'
import { getMessage } from '@/lib/message'

export type FormFieldType =
  | 'text'
  | 'password'
  | 'email'
  | 'textarea'
  | 'number'
  | 'select'
  | 'switch'
  | 'custom'

export interface ValidationRule {
  required?: boolean
  message?: string
  pattern?: RegExp
  min?: number
  max?: number
  validator?: (value: any, form: Record<string, any>) => string | undefined
}

export interface FormField<T extends Record<string, any> = Record<string, any>> {
  name: string
  label: ReactNode
  type: FormFieldType
  placeholder?: string
  description?: ReactNode
  rules?: ValidationRule[]
  options?: { value: string; label: ReactNode; disabled?: boolean }[]
  disabled?: boolean
  colspan?: number
  grid?: boolean
  onChange?: (value: any) => void
  render?: (value: any, form: T) => ReactNode
}

export interface UniversalFormProps<T extends Record<string, any>> {
  visible: boolean
  title: ReactNode
  form: T
  fields: FormField[]
  onCancel: () => void
  onSubmit: (form: T) => Promise<void> | void
  submitLabel?: ReactNode
  cancelLabel?: ReactNode
  loading?: boolean
  width?: number | string
  footer?: ReactNode | null
  grid?: boolean
  description?: ReactNode
}

export function UniversalForm<T extends Record<string, any>>({
  visible,
  title,
  form,
  fields,
  onCancel,
  onSubmit,
  submitLabel = '保存',
  cancelLabel = '取消',
  loading = false,
  width = 640,
  footer,
  description,
}: UniversalFormProps<T>) {
  const [errors, setErrors] = useState<Record<string, string>>({})

  const validateField = useCallback(
    (field: FormField<T>, value: any): string | undefined => {
      if (!field.rules) return undefined
      for (const rule of field.rules) {
        if (rule.required && (value == null || String(value).trim() === '')) {
          return rule.message || `${field.label}不能为空`
        }
        if (rule.pattern && value && !rule.pattern.test(String(value))) {
          return rule.message || `${field.label}格式不正确`
        }
        if (rule.min != null && value && String(value).length < rule.min) {
          return rule.message || `${field.label}至少${rule.min}个字符`
        }
        if (rule.max != null && value && String(value).length > rule.max) {
          return rule.message || `${field.label}最多${rule.max}个字符`
        }
        if (rule.validator) {
          return rule.validator(value, form)
        }
      }
      return undefined
    },
    [form]
  )

  const validateForm = useCallback((): boolean => {
    const newErrors: Record<string, string> = {}
    let valid = true
    for (const field of fields) {
      const err = validateField(field, form[field.name])
      if (err) {
        newErrors[field.name] = err
        valid = false
      }
    }
    setErrors(newErrors)
    return valid
  }, [fields, form, validateField])

  const handleChange = useCallback(
    (name: string, value: any) => {
      if (errors[name]) {
        const newErrors = { ...errors }
        delete newErrors[name]
        setErrors(newErrors)
      }
    },
    [errors]
  )

  const handleSave = useCallback(async () => {
    if (loading) return
    if (!validateForm()) return
    try {
      await onSubmit(form)
    } catch (e: any) {
      getMessage().error(e.message || '保存失败')
    }
  }, [loading, validateForm, onSubmit, form])

  const canSave = useMemo(() => {
    for (const field of fields) {
      if (validateField(field, form[field.name])) return !loading
    }
    return !loading
  }, [fields, form, validateField, loading])

  const getFieldComponent = (field: FormField) => {
    const value = form[field.name]
    const error = errors[field.name]
    const commonProps = {
      placeholder: field.placeholder,
      disabled: field.disabled,
      style: error ? { borderColor: 'var(--destructive)' } : undefined,
      onChange: (e: any) => {
        const newValue = e?.target?.value ?? e
        const newForm = { ...form, [field.name]: newValue } as T
        field.onChange?.(newValue)
        handleChange(field.name, newValue)
      },
    }

    switch (field.type) {
      case 'password':
        return <Input.Password key={field.name} {...commonProps} value={value} />
      case 'textarea':
        return <Input.TextArea key={field.name} {...commonProps} value={value} rows={4} />
      case 'number':
        return (
          <Input
            key={field.name}
            type="number"
            value={value}
            onChange={(e) => {
              const newForm = { ...form, [field.name]: e.target.value } as T
              field.onChange?.(e.target.value)
              handleChange(field.name, e.target.value)
            }}
            placeholder={field.placeholder}
            disabled={field.disabled}
          />
        )
      case 'select':
        return (
          <Select
            key={field.name}
            value={value}
            placeholder={field.placeholder}
            disabled={field.disabled}
            style={{ width: '100%', ...(error ? { borderColor: 'var(--destructive)' } : undefined) }}
            onChange={(v) => {
              const newForm = { ...form, [field.name]: v } as T
              field.onChange?.(v)
              handleChange(field.name, v)
            }}
            options={field.options}
          />
        )
      case 'switch':
        return (
          <Switch
            key={field.name}
            checked={!!value}
            disabled={field.disabled}
            onChange={(checked) => {
              const newForm = { ...form, [field.name]: checked } as T
              field.onChange?.(checked)
              handleChange(field.name, checked)
            }}
          />
        )
      case 'custom':
        return field.render ? field.render(value, form) : null
      default:
        return <Input key={field.name} type={field.type} {...commonProps} value={value} />
    }
  }

  const renderFields = () => {
    return fields.map((field, idx) => {
      const error = errors[field.name]
      const hasAsterisk = field.rules?.some((r) => r.required)
      const label = (
        <label className="text-sm font-medium">
          {field.label}
          {hasAsterisk && <span className="text-destructive"> *</span>}
        </label>
      )

      const fieldContent = (
        <div key={field.name} className="space-y-1">
          {label}
          {getFieldComponent(field)}
          {error && <p className="text-xs text-destructive">{error}</p>}
          {field.description && <p className="text-xs text-muted-foreground">{field.description}</p>}
        </div>
      )

      return field.grid ? (
        <div key={field.name} className={`col-span-${field.colspan ?? 1}`}>
          {fieldContent}
        </div>
      ) : (
        <div key={field.name} className="space-y-3">
          {fieldContent}
        </div>
      )
    })
  }

  const defaultFooter = (
    <div className="flex justify-end gap-2">
      <Button onClick={onCancel} disabled={loading}>
        {cancelLabel}
      </Button>
      <Button type="primary" onClick={handleSave} loading={loading} disabled={!canSave}>
        {submitLabel}
      </Button>
    </div>
  )

  return (
    <Modal
      open={visible}
      title={title}
      onCancel={onCancel}
      footer={footer ?? defaultFooter as any}
      width={width}
      centered
    >
      <div className="space-y-4 max-h-[60vh] overflow-y-auto">
        {fields.length > 0 && (
          <div className={fields.some((f) => f.grid) ? 'grid grid-cols-2 gap-3' : 'space-y-3'}>
            {renderFields()}
          </div>
        )}
        {description && <div className="text-xs text-muted-foreground bg-muted/30 rounded p-2">{description}</div>}
        {footer === null && defaultFooter}
      </div>
    </Modal>
  )
}
