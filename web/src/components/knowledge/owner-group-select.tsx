'use client'

import { useEffect } from 'react'
import { Select } from 'antd'
import { useIAMStore } from '@/stores/iam'

/**
 * 归属团队选择器（可多选）。
 *
 * 语义：该文章对**所选团队中任一团队**的成员可见（数组重叠匹配，取并集）。
 * 一篇跨职能 SOP 同时给「运维组」和「网络组」时选两个即可。
 *
 * 为什么必须显式选：可见范围为「仅团队」时若归属为空，检索谓词里
 * `owner_group_ids && $n::text[]` 对任何提问者都不成立 —— 文章会对所有人静默不可见。
 * 因此这里把「未选择」标成错误态，后端同样会 400 兜底。
 */
export interface OwnerGroupSelectProps {
  value?: string[]
  onChange?: (value: string[]) => void
  /** 归因为空时的错误态（可见范围=团队但未选） */
  invalid?: boolean
  disabled?: boolean
  className?: string
}

export function OwnerGroupSelect({ value = [], onChange, invalid, disabled, className }: OwnerGroupSelectProps) {
  const { groups, fetchGroups } = useIAMStore()

  // 仅在尚未加载时拉取：IAM 页与本页共用同一份 store 缓存。
  useEffect(() => {
    if (groups.length === 0) fetchGroups()
  }, [groups.length, fetchGroups])

  return (
    <Select
      mode="multiple"
      className={className}
      style={{ minWidth: 220, maxWidth: 340 }}
      placeholder="归属团队 *（可多选）"
      value={value}
      onChange={v => onChange?.(v ?? [])}
      options={groups.map(g => ({ label: g.name, value: g.id }))}
      optionFilterProp="label"
      maxTagCount="responsive"
      status={invalid ? 'error' : undefined}
      disabled={disabled}
      title="所选团队的成员可在 AI 检索中命中该文章"
      notFoundContent="暂无团队，请先在「权限管理」中创建用户组"
    />
  )
}
