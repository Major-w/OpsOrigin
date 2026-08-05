'use client'

import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SwitchField } from '@/components/ui/switch'
import { Select } from '@/components/ui/select'
import { DEFAULT_PLATFORM_POLICY, FREQUENCY_PRESETS, validateFrequency, type PlatformPolicy } from '@/lib/collection'
import { Save, Loader2, Settings2 } from 'lucide-react'
import { useCollectionStore } from '@/stores/collection'
import { toast } from '@/stores/toast'

/**
 * 平台级全局默认采集策略卡片（与后端 /cmdb/collection-policy 对齐）。
 * 资源纳管表单的采集开关预填此处默认值，可逐资源覆盖。
 * 同时被「资源纳管」页与「系统管理 / 采集策略」页复用。
 */
export function CollectionPolicyCard() {
  const { policy: collectPolicy, fetchPolicy, savePolicy, saving: policySaving } = useCollectionStore()
  const [policyDraft, setPolicyDraft] = useState<PlatformPolicy>(DEFAULT_PLATFORM_POLICY)
  const policyDirty = useMemo(() =>
    policyDraft.default_cmdb_enabled !== collectPolicy.default_cmdb_enabled ||
    policyDraft.default_monitor_enabled !== collectPolicy.default_monitor_enabled ||
    policyDraft.cmdb_default_frequency !== collectPolicy.cmdb_default_frequency ||
    policyDraft.monitor_default_frequency !== collectPolicy.monitor_default_frequency,
  [policyDraft, collectPolicy])
  const policyFreqErr = validateFrequency(policyDraft.cmdb_default_frequency) || validateFrequency(policyDraft.monitor_default_frequency)

  useEffect(() => {
    fetchPolicy().then(p => setPolicyDraft(p))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const handleSavePolicy = async () => {
    if (policyFreqErr) { toast.error('采集频率格式无效'); return }
    await savePolicy(policyDraft)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm flex items-center gap-2"><Settings2 className="h-4 w-4" />全局采集策略（资源接入默认值）</CardTitle>
        <CardDescription>
          资源接入时与采集策略定义关联：下方三个纳管表单的「采集设置」开关默认预填这里的值，可针对单个资源覆盖；逐资源配置以纳管时勾选为准。
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-4 md:grid-cols-2">
          <div className="space-y-3">
            <SwitchField
              label="默认开启 CMDB 采集"
              description="CMDB 是平台基石，建议保持开启：新资源纳管后自动创建 cmdb 类采集任务，周期性写入配置项。"
              checked={policyDraft.default_cmdb_enabled}
              onCheckedChange={v => setPolicyDraft(d => ({ ...d, default_cmdb_enabled: v }))}
            />
            <div>
              <label className="text-xs font-medium text-muted-foreground">CMDB 采集默认频率</label>
              <div className="flex gap-2 mt-1">
                <Input
                  value={policyDraft.cmdb_default_frequency}
                  onChange={e => setPolicyDraft(d => ({ ...d, cmdb_default_frequency: e.target.value }))}
                  placeholder="1h"
                  className="font-mono"
                />
                <Select
                  className="w-32"
                  value=""
                  options={[{ value: '', label: '预设' }, ...FREQUENCY_PRESETS.map(f => ({ value: f.value, label: f.label }))]}
                  onChange={e => e.target.value && setPolicyDraft(d => ({ ...d, cmdb_default_frequency: e.target.value }))}
                />
              </div>
            </div>
          </div>

          <div className="space-y-3">
            <SwitchField
              label="默认开启监控采集"
              description="默认关闭：监控采集频率高、资源开销大，建议按需为关键资源单独开启。"
              checked={policyDraft.default_monitor_enabled}
              onCheckedChange={v => setPolicyDraft(d => ({ ...d, default_monitor_enabled: v }))}
            />
            <div>
              <label className="text-xs font-medium text-muted-foreground">监控采集默认频率</label>
              <div className="flex gap-2 mt-1">
                <Input
                  value={policyDraft.monitor_default_frequency}
                  onChange={e => setPolicyDraft(d => ({ ...d, monitor_default_frequency: e.target.value }))}
                  placeholder="5m"
                  className="font-mono"
                />
                <Select
                  className="w-32"
                  value=""
                  options={[{ value: '', label: '预设' }, ...FREQUENCY_PRESETS.map(f => ({ value: f.value, label: f.label }))]}
                  onChange={e => e.target.value && setPolicyDraft(d => ({ ...d, monitor_default_frequency: e.target.value }))}
                />
              </div>
            </div>
          </div>
        </div>

        <div className="flex items-center justify-between pt-1 border-t border-gray-100 dark:border-gray-800">
          <p className="text-xs text-muted-foreground pt-3">
            {policyDirty ? '有未保存的更改，保存后新接入资源将使用新默认值' : '策略已是最新'}
            {collectPolicy.updated_at && !policyDirty && ` · 更新于 ${collectPolicy.updated_at.slice(0, 19).replace('T', ' ')}`}
          </p>
          <div className="flex gap-2 pt-3">
            {policyDirty && <Button variant="outline" onClick={() => setPolicyDraft(collectPolicy)}>撤销</Button>}
            <Button onClick={handleSavePolicy} disabled={!policyDirty || policySaving || !!policyFreqErr}>
              {policySaving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}保存策略
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
