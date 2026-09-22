
import { useEffect, useMemo, useState } from 'react'
import { useMonitorStore, type MetricSource, type TSQueryResult } from '@/stores/monitor'
import { t } from '@/lib/i18n'
import { EChartsWrapper, type EChartsOption } from '@/components/charts/echarts'
import { Card, Button, Tag, Modal, Select, Input, InputNumber, Popconfirm, Table, Space, Empty, Alert } from 'antd'
import { Plus, Trash2, Database, Server, LineChart, RefreshCw, Loader2, Bell } from 'lucide-react'

const TYPE_COLOR: Record<string, string> = { zabbix: 'blue', prometheus: 'orange' }
const TYPE_LABEL: Record<string, string> = { zabbix: 'Zabbix Agent', prometheus: 'Prometheus Exporter' }
const DEFAULT_PORT: Record<string, number> = { zabbix: 10050, prometheus: 9100 }

const RANGE_OPTIONS = [
  { label: '最近 1 小时', value: 3600 },
  { label: '最近 6 小时', value: 21600 },
  { label: '最近 24 小时', value: 86400 },
]

export default function MonitorSourcesPage() {
  const store = useMonitorStore()
  const { sources } = store

  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({
    type: 'zabbix' as 'zabbix' | 'prometheus',
    name: '',
    address: '',
    port: 10050,
    path: '',
    keys: '',
    ci_id: '',
    interval_sec: 30,
  })

  // 趋势图状态
  const [selectedId, setSelectedId] = useState<string>('')
  const [query, setQuery] = useState('')
  const [range, setRange] = useState<number>(3600)
  const [chart, setChart] = useState<TSQueryResult | null>(null)
  const [chartLoading, setChartLoading] = useState(false)

  // 一键建告警
  const [alertOpen, setAlertOpen] = useState(false)
  const [alertMetric, setAlertMetric] = useState('')
  const [alertOp, setAlertOp] = useState('>')
  const [alertTh, setAlertTh] = useState(80)
  const [alertSev, setAlertSev] = useState('warning')
  const [alertSubmitting, setAlertSubmitting] = useState(false)

  useEffect(() => {
    store.fetchSources()
  }, [])

  const onTypeChange = (type: 'zabbix' | 'prometheus') => {
    setForm(f => ({
      ...f,
      type,
      port: DEFAULT_PORT[type] ?? f.port,
      path: type === 'prometheus' ? (f.path || '/metrics') : '',
    }))
  }

  const resetForm = () => setForm({ type: 'zabbix', name: '', address: '', port: 10050, path: '', keys: '', ci_id: '', interval_sec: 30 })

  const handleCreate = async () => {
    const keys = form.keys.split(/[,\s]+/).map(s => s.trim()).filter(Boolean)
    const ok = await store.createSource({
      type: form.type,
      name: form.name,
      address: form.address,
      port: form.port,
      path: form.type === 'prometheus' ? form.path : '',
      keys: form.type === 'zabbix' ? keys : [],
      ci_id: form.ci_id || '',
      interval_sec: form.interval_sec,
    })
    if (ok) {
      setDialogOpen(false)
      resetForm()
    }
  }

  // 选中数据源 → 预填 PromQL + 拉取指标目录
  const handleSelectSource = (id: string) => {
    setSelectedId(id)
    const src = sources.find(s => s.id === id)
    if (src) {
      setQuery(`{source="${src.type}", instance="${src.address}"}`)
      setChart(null)
      store.fetchSourceMetrics(id)
    }
  }

  const handleQuery = async () => {
    if (!query.trim()) return
    const now = Math.floor(Date.now() / 1000)
    setChartLoading(true)
    const res = await store.querySeries(query.trim(), now - range, now, 60)
    setChart(res)
    setChartLoading(false)
  }

  const openAlert = (metric: string) => {
    setAlertMetric(metric)
    setAlertOpen(true)
  }

  const handleCreateAlert = async () => {
    const src = sources.find(s => s.id === selectedId)
    if (!src) return
    setAlertSubmitting(true)
    const ok = await store.createRule({
      name: `阈值告警-${alertMetric}`,
      metric_name: alertMetric,
      operator: alertOp,
      threshold: alertTh,
      severity: alertSev,
      type: 'threshold',
      labels: { source: src.type, instance: src.address },
    })
    setAlertSubmitting(false)
    if (ok) setAlertOpen(false)
  }

  const chartOption: EChartsOption | null = useMemo(() => {
    if (!chart || chart.status !== 'success' || !chart.data || chart.data.result.length === 0) return null
    const series = chart.data.result.map(r => {
      const nameKeys = Object.entries(r.metric).filter(([k]) => k !== '__name__')
      const name = (r.metric['__name__'] || 'metric') + (nameKeys.length ? ' ' + nameKeys.map(([k, v]) => `${k}=${v}`).join(' ') : '')
      return {
        name,
        type: 'line' as const,
        showSymbol: false,
        smooth: true,
        data: r.values.map(([ts, v]) => [ts * 1000, parseFloat(v)]),
      }
    })
    return {
      tooltip: { trigger: 'axis' },
      legend: { bottom: 0, type: 'scroll', data: series.map(s => s.name) },
      grid: { top: 24, left: 56, right: 16, bottom: 56 },
      xAxis: { type: 'time' },
      yAxis: { type: 'value', scale: true },
      dataZoom: [{ type: 'inside' }, { type: 'slider', bottom: 24, height: 16 }],
      series,
    }
  }, [chart])

  const columns = [
    { title: '名称', dataIndex: 'name', key: 'name', render: (v: string) => <span className="font-medium">{v}</span> },
    {
      title: '类型', dataIndex: 'type', key: 'type',
      render: (v: string) => <Tag color={TYPE_COLOR[v]}>{TYPE_LABEL[v] || v}</Tag>,
    },
    { title: '地址', dataIndex: 'address', key: 'address', render: (v: string, r: MetricSource) => <span className="text-muted-foreground">{v}{r.port ? `:${r.port}` : ''}</span> },
    { title: '路径', dataIndex: 'path', key: 'path', render: (v: string) => v || <span className="text-muted-foreground">—</span> },
    {
      title: 'Keys', dataIndex: 'keys', key: 'keys',
      render: (v: string[]) =>
        v && v.length ? (
          <span className="text-xs text-muted-foreground">{v.length} 项</span>
        ) : (
          <span className="text-xs text-muted-foreground">默认集</span>
        ),
    },
    { title: '间隔', dataIndex: 'interval_sec', key: 'interval_sec', render: (v: number) => `${v}s` },
    { title: 'CI', dataIndex: 'ci_id', key: 'ci_id', render: (v: string) => v || <span className="text-muted-foreground">—</span> },
    {
      title: '状态', dataIndex: 'healthy', key: 'healthy', width: 150,
      render: (_: any, r: MetricSource) => (
        <Space direction="vertical" size={0}>
          <Tag color={r.healthy ? 'green' : r.last_error ? 'red' : 'default'}>
            {r.healthy ? '采集正常' : r.last_error ? '采集异常' : '待采集'}
          </Tag>
          {r.last_success ? <span className="text-xs text-muted-foreground">{r.last_success}</span> : null}
          {r.last_error ? (
            <span className="text-xs text-red-500 max-w-[140px] truncate block" title={r.last_error}>{r.last_error}</span>
          ) : null}
        </Space>
      ),
    },
    {
      title: '', key: 'op', width: 60,
      render: (_: any, r: MetricSource) => (
        <Popconfirm title="确认删除该数据源？" onConfirm={() => store.deleteSource(r.id)}>
          <Button type="text" danger size="small" className="text-red-400 hover:text-red-600">
            <Trash2 className="h-3 w-3" />
          </Button>
        </Popconfirm>
      ),
    },
  ]

  const selectedSource = sources.find(s => s.id === selectedId)

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2">
          <Database className="h-6 w-6 text-blue-500" />{t('monitor.sources')}
        </h1>
        <p className="text-sm text-muted-foreground">
          纳管外部指标采集源：兼容 Zabbix Agent（被动检查协议，复用机房已有 Agent 的全部 item key）与 Prometheus Exporter（HTTP /metrics 抓取，复用整个 Prometheus 采集生态）。
        </p>
      </div>

      <Card>
        <div className="flex justify-end mb-2">
          <Button type="primary" onClick={() => setDialogOpen(true)}>
            <Plus className="h-3 w-3 mr-1" />新增数据源
          </Button>
        </div>
        <Table
          rowKey="id"
          size="small"
          columns={columns as any}
          dataSource={sources}
          pagination={false}
          locale={{ emptyText: '暂无数据源。新增 Zabbix Agent 或 Prometheus Exporter 即可开始采集。' }}
          onRow={(r) => ({ onClick: () => handleSelectSource(r.id), style: { cursor: 'pointer', background: r.id === selectedId ? 'rgba(59,130,246,0.06)' : undefined } })}
        />
      </Card>

      <Card>
        <div className="flex items-center gap-2 mb-3">
          <LineChart className="h-5 w-5 text-blue-500" />
          <span className="font-semibold">指标趋势</span>
          <span className="text-xs text-muted-foreground">点击上表任意数据源可自动预填查询</span>
        </div>

        <Space wrap className="mb-3" size={8}>
          <Select
            placeholder="选择数据源"
            style={{ width: 240 }}
            value={selectedId || undefined}
            onChange={handleSelectSource}
            options={sources.map(s => ({ label: `${s.name} (${TYPE_LABEL[s.type]})`, value: s.id }))}
          />
          <Input
            style={{ width: 420 }}
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder='PromQL，例如 {source="zabbix", instance="10.0.1.10"}'
          />
          <Select value={range} onChange={setRange} options={RANGE_OPTIONS} style={{ width: 130 }} />
          <Button type="primary" onClick={handleQuery} disabled={!query.trim()} loading={chartLoading}>
            <RefreshCw className="h-3 w-3 mr-1" />查询
          </Button>
        </Space>

        {chartLoading ? (
          <div className="flex items-center justify-center gap-2 py-16 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />查询中...
          </div>
        ) : chartOption ? (
          <EChartsWrapper option={chartOption} height={360} />
        ) : chart && chart.status !== 'success' ? (
          <Alert type="error" showIcon message="时序查询失败" description={chart.error || '请确认后端已配置 OPSORIGIN_CORE_VM_ADDR 并已写入指标。'} />
        ) : chart && chart.data && chart.data.result.length === 0 ? (
          <Empty description="该查询在所选时间范围内无数据。请确认数据源已采集并落库（需配置 VMAddr）。" />
        ) : (
          <div className="py-16 text-center text-sm text-muted-foreground">
            选择数据源并点击「查询」查看指标趋势。数据源采集落库依赖 VictoriaMetrics（OPSORIGIN_CORE_VM_ADDR），未配置时图表无数据。
          </div>
        )}
      </Card>

      {selectedSource && (
        <Card>
          <div className="flex items-center gap-2 mb-3">
            <Bell className="h-5 w-5 text-blue-500" />
            <span className="font-semibold">指标目录</span>
            <span className="text-xs text-muted-foreground">来自 {selectedSource.name} 当前上报的指标，可一键建阈值告警</span>
          </div>
          {store.sourceMetrics.length === 0 ? (
            <Empty description="该数据源暂无可列举的指标。请确认已采集并落库（需配置 VMAddr），或稍候采集周期后重试。" />
          ) : (
            <div className="flex flex-wrap gap-2">
              {store.sourceMetrics.map(m => (
                <div key={m} className="flex items-center gap-2 border rounded px-2 py-1 bg-muted/30">
                  <code className="text-xs">{m}</code>
                  <Button type="link" size="small" className="text-blue-500 px-1" onClick={() => openAlert(m)}>
                    <Bell className="h-3 w-3 mr-1" />建告警
                  </Button>
                </div>
              ))}
            </div>
          )}
        </Card>
      )}

      <Modal
        title="新增数据源"
        open={dialogOpen}
        onCancel={() => setDialogOpen(false)}
        footer={null}
      >
        <div className="space-y-3">
          <div>
            <label className="text-sm font-medium">类型 *</label>
            <Select
              className="w-full"
              value={form.type}
              onChange={onTypeChange}
              options={[
                { label: 'Zabbix Agent（被动检查 :10050）', value: 'zabbix' },
                { label: 'Prometheus Exporter（HTTP /metrics）', value: 'prometheus' },
              ]}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">名称 *</label>
              <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="生产数据库" />
            </div>
            <div>
              <label className="text-sm font-medium">地址 *</label>
              <Input value={form.address} onChange={e => setForm(f => ({ ...f, address: e.target.value }))} placeholder="10.0.1.10" />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">端口</label>
              <InputNumber className="w-full" value={form.port} min={1} max={65535} onChange={v => setForm(f => ({ ...f, port: v || 0 }))} />
            </div>
            <div>
              <label className="text-sm font-medium">采集间隔(秒)</label>
              <InputNumber className="w-full" value={form.interval_sec} min={5} max={3600} onChange={v => setForm(f => ({ ...f, interval_sec: v || 30 }))} />
            </div>
          </div>
          {form.type === 'prometheus' && (
            <div>
              <label className="text-sm font-medium">路径</label>
              <Input value={form.path} onChange={e => setForm(f => ({ ...f, path: e.target.value }))} placeholder="/metrics" />
            </div>
          )}
          {form.type === 'zabbix' && (
            <div>
              <label className="text-sm font-medium">Item Keys（逗号/空格分隔，留空用默认集）</label>
              <Input.TextArea
                rows={3}
                value={form.keys}
                onChange={e => setForm(f => ({ ...f, keys: e.target.value }))}
                placeholder="system.cpu.load[all,avg1], vm.memory.size[used], net.if.in[eth0]"
              />
            </div>
          )}
          <div>
            <label className="text-sm font-medium">CI ID（可选，用于与 CMDB 联动）</label>
            <Input value={form.ci_id} onChange={e => setForm(f => ({ ...f, ci_id: e.target.value }))} placeholder="关联 CMDB 配置项" />
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setDialogOpen(false)}>取消</Button>
          <Button type="primary" onClick={handleCreate} disabled={!form.name || !form.address}>创建</Button>
        </div>
      </Modal>

      <Modal
        title={`为指标建阈值告警：${alertMetric}`}
        open={alertOpen}
        onCancel={() => setAlertOpen(false)}
        footer={null}
      >
        <div className="space-y-3">
          <div className="text-xs text-muted-foreground">
            将创建一条阈值规则：当 <code>{alertMetric}</code>{' '}
            {alertOp} {alertTh} 时触发告警（标签自动绑定 source/instance，与所选数据源一致）。
          </div>
          <div className="grid grid-cols-3 gap-3">
            <div>
              <label className="text-sm font-medium">运算符</label>
              <Select className="w-full" value={alertOp} onChange={setAlertOp}
                options={['>', '>=', '<', '<=', '==', '!='].map(o => ({ label: o, value: o }))} />
            </div>
            <div>
              <label className="text-sm font-medium">阈值</label>
              <InputNumber className="w-full" value={alertTh} onChange={v => setAlertTh(v ?? 0)} />
            </div>
            <div>
              <label className="text-sm font-medium">严重度</label>
              <Select className="w-full" value={alertSev} onChange={setAlertSev}
                options={[
                  { label: '提示 info', value: 'info' },
                  { label: '警告 warning', value: 'warning' },
                  { label: '严重 critical', value: 'critical' },
                ]} />
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setAlertOpen(false)}>取消</Button>
          <Button type="primary" onClick={handleCreateAlert} loading={alertSubmitting} disabled={!alertMetric}>创建规则</Button>
        </div>
      </Modal>
    </div>
  )
}
