
import { useEffect, useState } from 'react'
import { useSysConfigStore, type ConfigItem, type ConfigGroup } from '@/stores/sysconfig'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Modal, Input, Button, Tag, Space, Switch, message } from 'antd'
import { SaveOutlined, ReloadOutlined, EyeInvisibleOutlined, EyeOutlined, AlertOutlined, CheckCircleOutlined, SearchOutlined, DesktopOutlined, WifiOutlined, LockOutlined, DatabaseOutlined, KeyOutlined, RobotOutlined, SendOutlined, SafetyOutlined, CloudOutlined } from '@ant-design/icons'

// 分组图标映射
const categoryIcons: Record<string, any> = {
  core: DesktopOutlined,
  agent_ws: WifiOutlined,
  tls: LockOutlined,
  tunnel: DesktopOutlined,
  database: DatabaseOutlined,
  auth: KeyOutlined,
  llm: RobotOutlined,
  outbox: SendOutlined,
  ldap: SafetyOutlined,
  sso: CloudOutlined,
}

export default function SystemSettingsPage() {
  const { groups, loading, saving, fetch, update } = useSysConfigStore()
  const [activeCategory, setActiveCategory] = useState<string>('core')
  const [editValues, setEditValues] = useState<Record<string, string>>({})
  const [showSecrets, setShowSecrets] = useState<Record<string, boolean>>({})
  const [searchQuery, setSearchQuery] = useState('')
  const [saveDialog, setSaveDialog] = useState(false)
  const [restartNotice, setRestartNotice] = useState(false)

  useEffect(() => {
    fetch()
  }, [])

  // 当前编辑的分组
  const currentGroup = groups.find(g => g.category === activeCategory)

  // 过滤搜索
  const filteredItems = currentGroup?.items.filter(item => {
    if (!searchQuery) return true
    const q = searchQuery.toLowerCase()
    return item.label.toLowerCase().includes(q) ||
      item.key.toLowerCase().includes(q) ||
      item.description.toLowerCase().includes(q)
  }) || []

  // 获取已修改的项
  const getChangedItems = (): Record<string, string> => {
    const changes: Record<string, string> = {}
    for (const [key, value] of Object.entries(editValues)) {
      const item = findItem(key)
      if (item && value !== item.current_value && value !== '') {
        changes[key] = value
      }
    }
    return changes
  }

  const findItem = (key: string): ConfigItem | undefined => {
    for (const g of groups) {
      const item = g.items.find(i => i.key === key)
      if (item) return item
    }
    return undefined
  }

  const handleEdit = (key: string, value: string) => {
    setEditValues(prev => ({ ...prev, [key]: value }))
  }

  const handleReset = (key: string) => {
    const item = findItem(key)
    if (item) {
      setEditValues(prev => {
        const next = { ...prev }
        delete next[key]
        return next
      })
    }
  }

  const handleResetAll = () => {
    setEditValues({})
  }

  const handleSave = async () => {
    const changes = getChangedItems()
    if (Object.keys(changes).length === 0) {
      setSaveDialog(false)
      return
    }
    const result = await update(changes)
    setSaveDialog(false)
    if (result && result.requires_restart > 0) {
      setRestartNotice(true)
    }
    // 清空编辑状态
    setEditValues({})
  }

  const toggleSecret = (key: string) => {
    setShowSecrets(prev => ({ ...prev, [key]: !prev[key] }))
  }

  const changedCount = Object.keys(getChangedItems()).length

  return (
    <div className="p-6 space-y-4">
      {/* 顶部操作栏 */}
      <div className="flex items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold">全局参数</h1>
          <p className="text-sm text-muted-foreground">管理系统服务配置,包括端口、认证、数据库连接等参数</p>
        </div>
        <div className="flex items-center gap-2">
          {changedCount > 0 && (
            <Tag color="warning">{changedCount} 项待保存</Tag>
          )}
          <Button size="middle" onClick={handleResetAll} disabled={changedCount === 0 || saving} icon={<ReloadOutlined />}>
            重置修改
          </Button>
          <Button type="primary" size="middle" onClick={() => setSaveDialog(true)} disabled={changedCount === 0 || saving} icon={<SaveOutlined />}>
            {saving ? '保存中...' : '保存修改'}
          </Button>
        </div>
      </div>

      {/* 重启提醒 */}
      {restartNotice && (
        <Card className="border-amber-300 bg-amber-50 dark:bg-amber-950/20">
          <CardContent className="py-3 flex items-center gap-2">
            <AlertOutlined className="text-amber-500" />
            <span className="text-sm text-amber-700 dark:text-amber-300">
              部分配置项已更新,需要重启服务后才能生效。请手动重启 Core 服务使新配置生效。
            </span>
            <Button size="middle" className="ml-auto" onClick={() => setRestartNotice(false)}>知道了</Button>
          </CardContent>
        </Card>
      )}

      <div className="flex gap-4">
        {/* 左侧分组导航 */}
        <div className="w-48 shrink-0 space-y-1">
          {groups.map(g => {
            const Icon = categoryIcons[g.category] || DesktopOutlined
            const changedInGroup = g.items.filter(i => editValues[i.key] !== undefined && editValues[i.key] !== i.current_value).length
            return (
              <button
                key={g.category}
                onClick={() => setActiveCategory(g.category)}
                className={`w-full flex items-center gap-2 px-3 py-2 rounded-md text-sm transition-colors ${
                  activeCategory === g.category
                    ? 'bg-primary text-primary-foreground'
                    : 'hover:bg-muted'
                }`}
              >
                <Icon className="h-4 w-4 shrink-0" />
                <span className="flex-1 text-left">{g.label}</span>
                {changedInGroup > 0 && (
                  <Tag color="warning" className="text-[10px] px-1 py-0">{changedInGroup}</Tag>
                )}
              </button>
            )
          })}
        </div>

        {/* 右侧配置项 */}
        <div className="flex-1 space-y-3">
          {/* 搜索框 */}
          <div className="relative">
            <SearchOutlined className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="搜索配置项..."
              value={searchQuery}
              onChange={e => setSearchQuery(e.target.value)}
              className="pl-9"
            />
          </div>

          {loading ? (
            <Card>
              <CardContent className="py-8 text-center text-muted-foreground">加载中...</CardContent>
            </Card>
          ) : filteredItems.length === 0 ? (
            <Card>
              <CardContent className="py-8 text-center text-muted-foreground">未找到匹配的配置项</CardContent>
            </Card>
          ) : (
            filteredItems.map(item => (
              <ConfigItemCard
                key={item.key}
                item={item}
                editValue={editValues[item.key]}
                showSecret={showSecrets[item.key]}
                onEdit={(v) => handleEdit(item.key, v)}
                onReset={() => handleReset(item.key)}
                onToggleSecret={() => toggleSecret(item.key)}
              />
            ))
          )}
        </div>
      </div>

      {/* 保存确认对话框 */}
      <Modal
        open={saveDialog}
        onCancel={() => setSaveDialog(false)}
        title="确认保存配置"
        onOk={handleSave}
        okText="确认保存"
        cancelText="取消"
        confirmLoading={saving}
      >
        <div className="space-y-3 py-2">
          <p className="text-sm">确认要保存以下 <span className="font-semibold">{changedCount}</span> 项配置修改吗?</p>
          <div className="max-h-60 overflow-y-auto space-y-1.5 rounded-md border p-3 bg-muted/50">
            {Object.entries(getChangedItems()).map(([key, value]) => {
              const item = findItem(key)
              return (
                <div key={key} className="flex items-start gap-2 text-xs">
                  {item?.requires_restart ? (
                    <AlertOutlined className="text-amber-500 mt-0.5 shrink-0" />
                  ) : (
                    <CheckCircleOutlined className="text-blue-500 mt-0.5 shrink-0" />
                  )}
                  <div className="flex-1">
                    <span className="font-medium">{item?.label || key}</span>
                    <span className="text-muted-foreground ml-2">({key})</span>
                    <div className="text-muted-foreground mt-0.5">
                      {item?.sensitive ? '**** → **** (敏感字段已隐藏)' : `${item?.current_value} → ${value}`}
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
          <div className="flex items-center gap-2 text-xs text-amber-600 dark:text-amber-400">
            <AlertOutlined className="text-sm" />
            <span>标记为"需重启"的配置项保存后需手动重启服务生效</span>
          </div>
        </div>
      </Modal>
    </div>
  )
}

// ConfigItemCard 单个配置项卡片
function ConfigItemCard({
  item,
  editValue,
  showSecret,
  onEdit,
  onReset,
  onToggleSecret,
}: {
  item: ConfigItem
  editValue: string | undefined
  showSecret: boolean | undefined
  onEdit: (v: string) => void
  onReset: () => void
  onToggleSecret: () => void
}) {
  const isEditing = editValue !== undefined
  const isChanged = isEditing && editValue !== item.current_value
  const displayValue = isEditing ? editValue : item.current_value

  return (
    <Card className={isChanged ? 'border-primary' : ''}>
      <CardContent className="py-3">
        <div className="flex items-start justify-between gap-3">
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-sm font-medium">{item.label}</span>
              {item.requires_restart && (
                <Tag color="warning" className="text-[10px]">
                  <AlertOutlined className="mr-0.5" />需重启
                </Tag>
              )}
              {item.sensitive && (
                <Tag className="text-[10px]">敏感</Tag>
              )}
              {item.type === 'bool' && (
                <Tag className="text-[10px]">布尔</Tag>
              )}
            </div>
            <p className="text-xs text-muted-foreground mt-1">{item.description}</p>
            <p className="text-[10px] text-muted-foreground/60 mt-1 font-mono">{item.key}</p>
          </div>
          <div className="flex items-center gap-1 shrink-0">
            {isChanged && (
              <Button type="text" size="middle" onClick={onReset} className="h-7 px-2">
                <ReloadOutlined className="text-xs" />
              </Button>
            )}
          </div>
        </div>
        <div className="mt-3">
          {item.type === 'bool' ? (
            <div className="flex items-center gap-2">
              <Switch
                checked={displayValue === 'true'}
                onChange={(v) => onEdit(v ? 'true' : 'false')}
              />
              <span className="text-sm text-muted-foreground">
                {displayValue === 'true' ? '已启用' : '已禁用'}
              </span>
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <Input
                type={item.sensitive && !showSecret ? 'password' : 'text'}
                value={displayValue || ''}
                onChange={e => onEdit(e.target.value)}
                placeholder={item.default_value || '请输入值'}
                className={`font-mono text-sm ${isChanged ? 'border-primary' : ''}`}
              />
              {item.sensitive && (
                <Button type="text" size="middle" onClick={onToggleSecret} className="shrink-0 text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white">
                  {showSecret ? <EyeInvisibleOutlined /> : <EyeOutlined />}
                </Button>
              )}
            </div>
          )}
          {!isEditing && item.current_value !== item.default_value && (
            <p className="text-[10px] text-muted-foreground/60 mt-1">默认值: {item.sensitive ? '****' : item.default_value}</p>
          )}
        </div>
      </CardContent>
    </Card>
  )
}
