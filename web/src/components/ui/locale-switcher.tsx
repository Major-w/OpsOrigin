import { Dropdown, Button } from 'antd'
import { GlobalOutlined } from '@ant-design/icons'
import { useLocale, setLocale, type Locale } from '@/lib/i18n'

const locales: Array<{ key: Locale; label: string }> = [
  { key: 'zh-CN', label: '中文' },
  { key: 'en-US', label: 'English' },
]

export default function LocaleSwitcher() {
  const locale = useLocale()

  return (
    <Dropdown
      menu={{
        items: locales.map((l) => ({
          key: l.key,
          label: l.label,
          onClick: () => setLocale(l.key),
        })),
        selectedKeys: [locale],
      }}
      trigger={['click']}
    >
      <button className="p-1.5 rounded-md transition-colors text-gray-400 hover:text-white hover:bg-white/10">
        <GlobalOutlined className="h-4 w-4" />
      </button>
    </Dropdown>
  )
}
