
import { useState } from 'react'
import { apiFetch } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { t } from '@/lib/i18n'
import { Card, Button, Select, Input, Space, Alert, Tabs, Tag } from 'antd'
import { Webhook, Send } from 'lucide-react'

const SAMPLE_ALERTMANAGER = `{
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "HighCpuUsage",
        "severity": "warning",
        "instance": "10.0.1.10",
        "host": "db-prod-01"
      },
      "annotations": {
        "summary": "CPU 使用率过高",
        "description": "node_cpu 持续超过 85%"
      }
    }
  ]
}`

const SAMPLE_ZABBIX = `{
  "host": "db-prod-01",
  "trigger": "CPU load is high",
  "severity": "High",
  "status": "problem",
  "message": "CPU load 持续高于阈值"
}`

export default function MonitorIngestPage() {
  const [format, setFormat] = useState<'alertmanager' | 'zabbix'>('alertmanager')
  const [body, setBody] = useState(SAMPLE_ALERTMANAGER)
  const [sending, setSending] = useState(false)
  const [result, setResult] = useState<any>(null)

  const onFormatChange = (f: 'alertmanager' | 'zabbix') => {
    setFormat(f)
    setBody(f === 'alertmanager' ? SAMPLE_ALERTMANAGER : SAMPLE_ZABBIX)
    setResult(null)
  }

  const send = async () => {
    setSending(true)
    setResult(null)
    try {
      const res = await apiFetch<any>('/monitor/alerts/ingest', { method: 'POST', body })
      setResult({ ok: true, data: res })
      getMessage().success(`接入成功：新增 ${res?.ingested ?? 0} / 解除 ${res?.resolved ?? 0}`)
    } catch (e: any) {
      setResult({ ok: false, error: e.message })
      getMessage().error(`接入失败: ${e.message}`)
    } finally {
      setSending(false)
    }
  }

  const endpoint = typeof window !== 'undefined' ? `${window.location.origin}/api/v1/monitor/alerts/ingest` : '/api/v1/monitor/alerts/ingest'

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2">
          <Webhook className="h-6 w-6 text-blue-500" />{t('monitor.ingest') || '告警接入'}
        </h1>
        <p className="text-sm text-muted-foreground">
          统一接入第三方告警（Zabbix / Prometheus Alertmanager / 通用 webhook），归一化后汇入告警中心（去重、聚类、抑制、通知 + CMDB 富化）。
        </p>
      </div>

      <Card>
        <div className="mb-3">
          <span className="text-sm font-medium">接入端点（POST）</span>
          <div className="mt-1 flex items-center gap-2">
            <code className="text-xs bg-muted px-2 py-1 rounded break-all">{endpoint}</code>
            <Tag color="blue">需携带有效鉴权头（同其他 API）</Tag>
          </div>
        </div>

        <Tabs
          items={[
            { key: 'alertmanager', label: 'Prometheus Alertmanager', children: null },
            { key: 'zabbix', label: 'Zabbix', children: null },
          ]}
          activeKey={format}
          onChange={(k) => onFormatChange(k as any)}
        />

        <Space direction="vertical" className="w-full" size={8}>
          <Select
            className="w-full"
            value={format}
            onChange={onFormatChange}
            options={[
              { label: 'Prometheus Alertmanager 格式', value: 'alertmanager' },
              { label: 'Zabbix webhook 格式', value: 'zabbix' },
            ]}
          />
          <Input.TextArea
            rows={14}
            value={body}
            onChange={e => setBody(e.target.value)}
            className="font-mono text-xs"
          />
          <Button type="primary" onClick={send} loading={sending}>
            <Send className="h-3 w-3 mr-1" />发送测试
          </Button>
        </Space>

        {result && (
          result.ok ? (
            <Alert type="success" showIcon className="mt-3" message="接入成功"
              description={`新增 ${result.data?.ingested ?? 0} 条，解除 ${result.data?.resolved ?? 0} 条。`} />
          ) : (
            <Alert type="error" showIcon className="mt-3" message="接入失败" description={result.error} />
          )
        )}
      </Card>
    </div>
  )
}
