// 知识库报表入口：复用平台统一 ReportCenter，默认聚焦知识库数据集
// （knowledge.articles 文章分布/浏览量 + knowledge.search_logs 检索质量）。
// 检索逐条明细与知识盲区清单见「检索可观测」页，这里面向聚合统计。
import { ReportCenter } from '@/components/report/ReportCenter'

export default function KnowledgeReportsPage() {
  return <div className="p-6">
    <ReportCenter
      title="知识库报表"
      description="按需搭建知识运营报表：文章分布、浏览量、检索质量（零结果率 = 知识盲区）等；逐条检索明细见「检索可观测」"
      modules={['knowledge']}
      scopeLabel="知识库"
    />
  </div>
}
