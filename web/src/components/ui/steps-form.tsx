
import { useState } from 'react'
import { Check } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface Step {
  title: string
  description?: string
  content: React.ReactNode
}

interface StepsFormProps {
  steps: Step[]
  onFinish: (values: Record<string, any>) => Promise<void> | void
  initialValues?: Record<string, any>
  className?: string
}

export function StepsForm({ steps, onFinish, initialValues = {}, className }: StepsFormProps) {
  const [current, setCurrent] = useState(0)
  const [values, setValues] = useState<Record<string, any>>(initialValues)
  const [loading, setLoading] = useState(false)

  const handleChange = (name: string, value: any) => {
    setValues(prev => ({ ...prev, [name]: value }))
  }

  const isLast = current === steps.length - 1

  const handleNext = async () => {
    if (isLast) {
      setLoading(true)
      try {
        await onFinish(values)
      } finally {
        setLoading(false)
      }
    } else {
      setCurrent(prev => prev + 1)
    }
  }

  const handlePrev = () => {
    setCurrent(prev => prev - 1)
  }

  return (
    <div className={cn("space-y-6", className)}>
      {/* 步骤条 */}
      <div className="flex items-center">
        {steps.map((step, index) => (
          <div key={index} className="flex items-center flex-1">
            <div className="flex items-center gap-2">
              <div
                className={cn(
                  "w-8 h-8 rounded-full flex items-center justify-center text-sm font-medium transition-colors",
                  index < current && "bg-green-500 text-white",
                  index === current && "bg-blue-500 text-white",
                  index > current && "bg-gray-200 dark:bg-gray-700 text-gray-500"
                )}
              >
                {index < current ? (
                  <Check className="h-4 w-4" />
                ) : (
                  index + 1
                )}
              </div>
              <div className="hidden sm:block">
                <p className={cn(
                  "text-sm font-medium",
                  index <= current ? "text-foreground" : "text-muted-foreground"
                )}>
                  {step.title}
                </p>
                {step.description && (
                  <p className="text-xs text-muted-foreground">{step.description}</p>
                )}
              </div>
            </div>
            {index < steps.length - 1 && (
              <div className={cn(
                "flex-1 h-px mx-4",
                index < current ? "bg-green-500" : "bg-gray-200 dark:bg-gray-700"
              )} />
            )}
          </div>
        ))}
      </div>

      {/* 内容区 */}
      <div className="min-h-[200px]">
        {steps[current].content && (
          <div>
            {typeof steps[current].content === 'function'
              ? (steps[current].content as Function)({ values, onChange: handleChange })
              : steps[current].content}
          </div>
        )}
      </div>

      {/* 操作按钮 */}
      <div className="flex justify-end gap-2 pt-4 border-t">
        {current > 0 && (
          <Button variant="outline" onClick={handlePrev}>
            上一步
          </Button>
        )}
        <Button onClick={handleNext} disabled={loading}>
          {loading ? '提交中...' : isLast ? '完成' : '下一步'}
        </Button>
      </div>
    </div>
  )
}
