
import { useState } from 'react'
import { Settings, X, Moon, Sun, Check } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useThemeStore } from '@/stores/theme'
import { cn } from '@/lib/utils'

interface SettingsDrawerProps {
  children?: React.ReactNode
}

export function SettingsDrawer({ children }: SettingsDrawerProps) {
  const [open, setOpen] = useState(false)
  const { theme, toggle } = useThemeStore()

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className="fixed right-4 bottom-4 z-50 p-3 rounded-full bg-white dark:bg-gray-800 shadow-lg border border-gray-200 dark:border-gray-700 hover:shadow-xl transition-shadow"
        title="全局配置"
      >
        <Settings className="h-5 w-5 text-gray-600 dark:text-gray-300 animate-spin-slow" />
      </button>

      {open && (
        <>
          <div className="fixed inset-0 bg-black/30 z-50" onClick={() => setOpen(false)} />
          <div className="fixed right-0 top-0 bottom-0 w-80 bg-white dark:bg-gray-900 z-50 shadow-2xl overflow-y-auto">
            <div className="flex items-center justify-between p-4 border-b">
              <h2 className="text-lg font-semibold">全局配置</h2>
              <button onClick={() => setOpen(false)} className="p-1 hover:bg-gray-100 dark:hover:bg-gray-800 rounded">
                <X className="h-5 w-5" />
              </button>
            </div>

            <div className="p-4 space-y-6">
              {/* 主题切换 */}
              <div>
                <h3 className="text-sm font-medium mb-3">主题</h3>
                <div className="flex gap-2">
                  <button
                    onClick={() => theme === 'dark' && toggle()}
                    className={cn(
                      "flex-1 p-3 rounded-lg border-2 transition-all flex flex-col items-center gap-2",
                      theme === 'light'
                        ? "border-blue-500 bg-blue-50 dark:bg-blue-500/10"
                        : "border-gray-200 dark:border-gray-700 hover:border-gray-300"
                    )}
                  >
                    <Sun className="h-5 w-5" />
                    <span className="text-xs">浅色</span>
                    {theme === 'light' && <Check className="h-4 w-4 text-blue-500" />}
                  </button>
                  <button
                    onClick={() => theme === 'light' && toggle()}
                    className={cn(
                      "flex-1 p-3 rounded-lg border-2 transition-all flex flex-col items-center gap-2",
                      theme === 'dark'
                        ? "border-blue-500 bg-blue-50 dark:bg-blue-500/10"
                        : "border-gray-200 dark:border-gray-700 hover:border-gray-300"
                    )}
                  >
                    <Moon className="h-5 w-5" />
                    <span className="text-xs">深色</span>
                    {theme === 'dark' && <Check className="h-4 w-4 text-blue-500" />}
                  </button>
                </div>
              </div>

              {/* 内容区域 */}
              {children}

              {/* 重置按钮 */}
              <Button
                variant="outline"
                className="w-full"
                onClick={() => {
                  // 重置为默认配置
                  if (theme !== 'light') toggle()
                }}
              >
                重置为默认
              </Button>
            </div>
          </div>
        </>
      )}
    </>
  )
}
