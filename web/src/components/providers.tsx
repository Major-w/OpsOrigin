
import { useEffect, useState } from 'react'
import { ConfigProvider, App as AntApp, theme as antdTheme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import { useThemeStore } from '@/stores/theme'

export default function Providers({ children }: { children: React.ReactNode }) {
  const { theme } = useThemeStore()
  const [mounted, setMounted] = useState(false)

  useEffect(() => { setMounted(true) }, [])

  return (
    <ConfigProvider
      locale={zhCN}
      theme={{
        algorithm: theme === 'dark' ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
        token: {
          colorPrimary: 'var(--primary)',
          borderRadius: 6,
          fontFamily: 'Inter, system-ui, sans-serif',
          colorBorder: 'var(--border)',
          colorText: 'var(--foreground)',
          colorTextSecondary: 'var(--muted-foreground)',
          colorSuccess: '#22C55E',
          colorWarning: '#F59E0B',
          colorError: '#EF4444',
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
  )
}
