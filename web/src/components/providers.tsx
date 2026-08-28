
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
          colorPrimary: '#3B82F6',
          borderRadius: 6,
          fontFamily: 'Inter, system-ui, sans-serif',
          colorBgContainer: theme === 'dark' ? '#161B22' : '#FFFFFF',
          colorBgElevated: theme === 'dark' ? '#1C2333' : '#FFFFFF',
          colorBgLayout: theme === 'dark' ? '#0B0F19' : '#F5F7FA',
          colorBorder: theme === 'dark' ? '#30363D' : '#E2E8F0',
          colorText: theme === 'dark' ? '#E2E8F0' : '#1E293B',
          colorTextSecondary: theme === 'dark' ? '#8B949E' : '#64748B',
          colorSuccess: '#3B82F6',
          colorWarning: '#F59E0B',
          colorError: '#EF4444',
        },
        components: {
          Table: {
            headerBg: theme === 'dark' ? '#161B22' : '#F8FAFC',
            rowHoverBg: theme === 'dark' ? 'rgba(255,255,255,0.03)' : '#F1F5F9',
            borderColor: theme === 'dark' ? '#30363D' : '#E2E8F0',
          },
          Modal: {
            contentBg: theme === 'dark' ? '#161B22' : '#FFFFFF',
            headerBg: theme === 'dark' ? '#161B22' : '#FFFFFF',
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
