
import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input, Button, Tag, Switch, Space } from 'antd'
import { SaveOutlined } from '@ant-design/icons'
import { Key, Shield, Lock, Globe } from 'lucide-react'

export default function SystemAuthPage() {
  const [settings, setSettings] = useState({
    jwtSecret: 'OpsOrigin-Secret-Key-2026',
    tokenExpiry: '24',
    refreshTokenExpiry: '7',
    maxLoginAttempts: '5',
    lockDuration: '30',
    passwordMinLength: '8',
    requireSpecialChars: true,
    allowRegWithoutVerification: false,
    enableSAML: false,
    enableOAuth: false,
  })

  const handleSave = () => {
    // TODO: implement save logic
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">认证设置</h1>
          <p className="text-sm text-muted-foreground">配置系统认证和安全策略</p>
        </div>
        <Button size="middle" onClick={handleSave}><SaveOutlined />保存设置</Button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm flex items-center gap-2"><Key className="h-4 w-4" />JWT 配置</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div>
              <label className="text-sm font-medium">JWT 密钥</label>
              <Input type="password" value={settings.jwtSecret} onChange={e => setSettings({ ...settings, jwtSecret: e.target.value })} />
              <p className="text-xs text-muted-foreground mt-1">用于签名 JWT Token 的密钥，请妥善保管</p>
            </div>
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="text-sm font-medium">Access Token 过期时间 (小时)</label>
                <Input type="number" value={settings.tokenExpiry} onChange={e => setSettings({ ...settings, tokenExpiry: e.target.value })} />
              </div>
              <div>
                <label className="text-sm font-medium">Refresh Token 过期时间 (天)</label>
                <Input type="number" value={settings.refreshTokenExpiry} onChange={e => setSettings({ ...settings, refreshTokenExpiry: e.target.value })} />
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm flex items-center gap-2"><Lock className="h-4 w-4" />密码策略</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div>
              <label className="text-sm font-medium">密码最小长度</label>
              <Input type="number" value={settings.passwordMinLength} onChange={e => setSettings({ ...settings, passwordMinLength: e.target.value })} />
            </div>
            <div className="flex items-center gap-2">
              <Switch
                checked={settings.requireSpecialChars}
                onChange={v => setSettings({ ...settings, requireSpecialChars: v })}
              />
              <label className="text-sm">要求包含特殊字符</label>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm flex items-center gap-2"><Shield className="h-4 w-4" />登录安全</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="text-sm font-medium">最大登录尝试次数</label>
                <Input type="number" value={settings.maxLoginAttempts} onChange={e => setSettings({ ...settings, maxLoginAttempts: e.target.value })} />
              </div>
              <div>
                <label className="text-sm font-medium">锁定时长 (分钟)</label>
                <Input type="number" value={settings.lockDuration} onChange={e => setSettings({ ...settings, lockDuration: e.target.value })} />
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm flex items-center gap-2"><Globe className="h-4 w-4" />第三方登录</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-medium">SAML SSO</p>
                <p className="text-xs text-muted-foreground">支持企业 SAML 单点登录</p>
              </div>
              <Tag color={settings.enableSAML ? 'success' : undefined}>
                {settings.enableSAML ? '已启用' : '未启用'}
              </Tag>
            </div>
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-medium">OAuth 2.0</p>
                <p className="text-xs text-muted-foreground">支持 GitHub, Google 等 OAuth 登录</p>
              </div>
              <Tag color={settings.enableOAuth ? 'success' : undefined}>
                {settings.enableOAuth ? '已启用' : '未启用'}
              </Tag>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
