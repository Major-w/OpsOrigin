'use client'

// ITSM 自定义报表入口：固定看板已并入「总览」，这里提供自助搭建能力
// （数据探索 / AI 生成 / 自定义看板），默认聚焦 ITSM 数据集，可切回全部。
import { ReportCenter } from '@/components/report/ReportCenter'

export default function ITSMCustomReportsPage() {
  return <div className="p-6">
    <ReportCenter
      title="自定义报表"
      description="按需搭建服务运营报表：选择数据集与指标即可生成图表并沉淀为看板；固定的运营驾驶舱见「总览」"
      modules={['itsm']}
      scopeLabel="ITSM"
    />
  </div>
}
