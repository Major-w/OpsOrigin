'use client'

import { useEffect, useState } from 'react'
import { useSysConfigStore, type ConfigItem, type ConfigGroup } from '@/stores/sysconfig'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Save, RotateCcw, Eye, EyeOff, AlertTriangle, CheckCircle, Search, Server, Wifi, Lock, Database, Key, Bot, Send, Shield, Cloud } from 'lucide-react'

// 分组图标映射
const categoryIcons: Record<string, any> = {
  core: Server,
  agent_ws: Wifi,
  tls: Lock,
  tunnel: Server,
  database: Database,
  auth: Key,
  llm: Bot,
  outbox: Send,
  ldap: Shield,
  sso: Cloud,
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
    <div className="space-y-4">
      {/* 顶部操作栏 */}
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">全局参数</h2>
          <p className="text-sm text-muted-foreground">管理系统服务配置,包括端口、认证、数据库连接等参数</p>
        </div>
        <div className="flex items-center gap-2">
          {changedCount > 0 && (
            <Badge variant="warning">{changedCount} 项待保存</Badge>
          )}
          <Button variant="outline" size="sm" onClick={handleResetAll} disabled={changedCount === 0 || saving}>
            <RotateCcw className="h-4 w-4 mr-1" />重置修改
          </Button>
          <Button size="sm" onClick={() => setSaveDialog(true)} disabled={changedCount === 0 || saving}>
            <Save className="h-4 w-4 mr-1" />{saving ? '保存中...' : '保存修改'}
          </Button>
        </div>
      </div>

      {/* 重启提醒 */}
      {restartNotice && (
        <Card className="border-amber-300 bg-amber-50 dark:bg-amber-950/20">
          <CardContent className="py-3 flex items-center gap-2">
            <AlertTriangle className="h-5 w-5 text-amber-500" />
            <span className="text-sm text-amber-700 dark:text-amber-300">
              部分配置项已更新,需要重启服务后才能生效。请手动重启 Core 服务使新配置生效。
            </span>
            <Button variant="outline" size="sm" className="ml-auto" onClick={() => setRestartNotice(false)}>知道了</Button>
          </CardContent>
        </Card>
      )}

      <div className="flex gap-4">
        {/* 左侧分组导航 */}
        <div className="w-48 shrink-0 space-y-1">
          {groups.map(g => {
            const Icon = categoryIcons[g.category] || Server
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
                  <Badge variant="warning" className="text-[10px] px-1 py-0">{changedInGroup}</Badge>
                )}
              </button>
            )
          })}
        </div>

        {/* 右侧配置项 */}
        <div className="flex-1 space-y-3">
          {/* 搜索框 */}
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
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
      <Dialog open={saveDialog} onClose={() => setSaveDialog(false)} title="确认保存配置">
        <div className="space-y-3 py-2">
          <p className="text-sm">确认要保存以下 <span className="font-semibold">{changedCount}</span> 项配置修改吗?</p>
          <div className="max-h-60 overflow-y-auto space-y-1.5 rounded-md border p-3 bg-muted/50">
            {Object.entries(getChangedItems()).map(([key, value]) => {
              const item = findItem(key)
              return (
                <div key={key} className="flex items-start gap-2 text-xs">
                  {item?.requires_restart ? (
                    <AlertTriangle className="h-3.5 w-3.5 text-amber-500 mt-0.5 shrink-0" />
                  ) : (
                    <CheckCircle className="h-3.5 w-3.5 text-green-500 mt-0.5 shrink-0" />
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
            <AlertTriangle className="h-4 w-4" />
            <span>标记为"需重启"的配置项保存后需手动重启服务生效</span>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setSaveDialog(false)}>取消</Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? '保存中...' : '确认保存'}
          </Button>
        </DialogFooter>
      </Dialog>
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
                <Badge variant="outline" className="text-[10px] text-amber-600 border-amber-400">
                  <AlertTriangle className="h-2.5 w-2.5 mr-0.5" />需重启
                </Badge>
              )}
              {item.sensitive && (
                <Badge variant="outline" className="text-[10px]">敏感</Badge>
              )}
              {item.type === 'bool' && (
                <Badge variant="outline" className="text-[10px]">布尔</Badge>
              )}
            </div>
            <p className="text-xs text-muted-foreground mt-1">{item.description}</p>
            <p className="text-[10px] text-muted-foreground/60 mt-1 font-mono">{item.key}</p>
          </div>
          <div className="flex items-center gap-1 shrink-0">
            {isChanged && (
              <Button variant="ghost" size="sm" onClick={onReset} className="h-7 px-2 text-xs">
                <RotateCcw className="h-3 w-3" />
              </Button>
            )}
          </div>
        </div>
        <div className="mt-3">
          {item.type === 'bool' ? (
            <div className="flex items-center gap-2">
              <button
                onClick={() => onEdit(displayValue === 'true' ? 'false' : 'true')}
                className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                  displayValue === 'true' ? 'bg-primary' : 'bg-muted'
                }`}
              >
                <span className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                  displayValue === 'true' ? 'translate-x-6' : 'translate-x-1'
                }`} />
              </button>
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
                <Button variant="ghost" size="sm" onClick={onToggleSecret} className="shrink-0">
                  {showSecret ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
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
