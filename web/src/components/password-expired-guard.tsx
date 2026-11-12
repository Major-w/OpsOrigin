
/**
 * PasswordExpiredGuard 密码过期全局守卫。
 * 登录响应 password_expired=true 时由 auth store 写入 localStorage 标志；
 * 此处在 dashboard 布局挂载，检测到标志即弹强制改密 Modal（不可关闭，改密成功后解除）。
 * LDAP 域账号同样适用（改的是本地密码备份；目录密码由管理员在 AD 侧维护）。
 */
import { useEffect, useState } from 'react'
import { Modal, Input, Alert, Button, message as antMessage } from 'antd'
import { apiFetch, apiFetchRaw } from '@/lib/utils'

interface PasswordPolicy {
  min_len: number
  min_classes: number
  max_age_days: number
}

const FLAG_KEY = 'opsorigin_password_expired'

export function usePasswordExpiredFlag() {
  const [expired, setExpired] = useState(false)
  useEffect(() => {
    const sync = () => setExpired(localStorage.getItem(FLAG_KEY) === '1')
    sync()
    window.addEventListener('storage', sync)
    return () => window.removeEventListener('storage', sync)
  }, [])
  return [expired, () => localStorage.removeItem(FLAG_KEY)] as const
}

export default function PasswordExpiredGuard() {
  const [expired, clearFlag] = usePasswordExpiredFlag()
  const [policy, setPolicy] = useState<PasswordPolicy | null>(null)
  const [oldPwd, setOldPwd] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [confirmPwd, setConfirmPwd] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (!expired) return
    apiFetch<PasswordPolicy>('/users/password-policy')
      .then(setPolicy)
      .catch(() => setPolicy(null))
  }, [expired])

  if (!expired) return null

  // 当前登录用户 ID（localStorage opsorigin_user 由 auth store 写入）
  const userId = (() => {
    try {
      return JSON.parse(localStorage.getItem('opsorigin_user') || '{}')?.user_id as string | undefined
    } catch {
      return undefined
    }
  })()

  const hint = policy
    ? `至少 ${policy.min_len} 位，包含 ${policy.min_classes} 类字符（小写/大写/数字/符号）${policy.max_age_days > 0 ? `；有效期 ${policy.max_age_days} 天` : ''}`
    : '至少 8 位，包含 2 类字符'

  const handleSubmit = async () => {
    if (submitting) return
    if (!oldPwd || !newPwd) {
      antMessage.warning('请填写旧密码与新密码')
      return
    }
    if (newPwd !== confirmPwd) {
      antMessage.warning('两次输入的新密码不一致')
      return
    }
    setSubmitting(true)
    try {
      const raw = await apiFetchRaw(`/users/${userId}/password`, {
        method: 'PUT',
        body: JSON.stringify({ old_password: oldPwd, new_password: newPwd }),
      })
      if (!raw.ok) {
        const body = await raw.json().catch(() => ({}))
        throw new Error(body.error || `HTTP ${raw.status}`)
      }
      antMessage.success('密码修改成功，请妥善保管')
      clearFlag()
      setOldPwd('')
      setNewPwd('')
      setConfirmPwd('')
    } catch (e: any) {
      antMessage.error(`修改失败: ${e.message}`)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal
      open
      title="密码已过期，请修改密码"
      footer={null}
      closable={false}
      maskClosable={false}
      keyboard={false}
      width={440}
    >
      <Alert type="warning" showIcon message="当前密码已超过有效期，修改前无法继续使用系统" className="mb-4" />
      <div className="space-y-3">
        <div>
          <label className="text-sm font-medium">旧密码</label>
          <Input.Password value={oldPwd} onChange={e => setOldPwd(e.target.value)} />
        </div>
        <div>
          <label className="text-sm font-medium">新密码</label>
          <Input.Password value={newPwd} onChange={e => setNewPwd(e.target.value)} />
          <p className="text-xs text-muted-foreground mt-1">策略要求：{hint}</p>
        </div>
        <div>
          <label className="text-sm font-medium">确认新密码</label>
          <Input.Password value={confirmPwd} onChange={e => setConfirmPwd(e.target.value)} />
        </div>
        <Button type="primary" block loading={submitting} onClick={handleSubmit}>修改密码并继续</Button>
      </div>
    </Modal>
  )
}
