
import { useEffect, useState } from 'react'
import { ConfigProvider, App as AntApp, theme as antdTheme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'
import { useThemeStore } from '@/stores/theme'
import { useLocale } from '@/lib/i18n'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30 * 1000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

export default function Providers({ children }: { children: React.ReactNode }) {
  const { theme } = useThemeStore()
  const locale = useLocale()
  const [mounted, setMounted] = useState(false)

  useEffect(() => { setMounted(true) }, [])

  const isDark = theme === 'dark'

  return (
    <QueryClientProvider client={queryClient}>
      <ConfigProvider
        locale={locale === 'en-US' ? enUS : zhCN}
        theme={{
          algorithm: isDark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
          token: {
            // antd 用 JS 取色计算派生色（hover/active/placeholder/分割线/禁用态等），
            // 传 var(--xxx) 会导致派生色解析失败，必须传真实色值（与 globals.css 双主题变量一致）。
            colorPrimary: isDark ? '#60A5FA' : '#3B82F6',
            // 暗色下对齐手搓主题背景（antd 默认 #141414 与 app 的 #0B0F19 不一致）
            colorBgBase: isDark ? '#0B0F19' : '#FFFFFF',
            borderRadius: 6,
            fontFamily: 'Inter, system-ui, sans-serif',
            colorText: isDark ? '#E2E8F0' : '#1E293B',
            colorTextSecondary: '#64748B',
            colorBorder: isDark ? 'rgba(255, 255, 255, 0.06)' : '#E2E8F0',
            colorSuccess: isDark ? '#4ADE80' : '#22C55E',
            colorWarning: isDark ? '#FBBF24' : '#F59E0B',
            colorError: isDark ? '#F87171' : '#EF4444',
          },
          components: {
            Tag: {
              defaultBg: 'var(--muted)',
              defaultColor: 'var(--muted-foreground)',
            },
            Table: {
              headerBg: 'var(--surface-elevated)',
              rowHoverBg: 'var(--muted)',
              borderColor: 'var(--border)',
            },
            Modal: {
              contentBg: 'var(--surface-elevated)',
              headerBg: 'var(--surface-elevated)',
            },
            Menu: {
              darkItemBg: 'transparent',
              darkSubMenuItemBg: 'transparent',
            },
          },
        }}
      >
        <AntApp>{mounted ? children : <div style={{ visibility: 'hidden' }} />}</AntApp>
      </ConfigProvider>
    </QueryClientProvider>
  )
}
