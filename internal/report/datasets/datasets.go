// Package datasets 声明平台内置的报表数据集。
//
// 每个数据集都是对一张(组)物理表的"白名单视图"：只暴露可被前端
// 分组/过滤/聚合的维度与指标，并强制绑定租户列。统一报表模块只认
// 这些注册项，绝不开放裸 SQL —— 任何字段引用都必须命中这里声明的白名单。
//
// 维度名即 SQL 列名(除非在 DimExprs 显式改写)，指标名可自由命名并通过
// MetricExprs 映射到聚合表达式。所有取值最终由 report.SQLResolver 在
// 参数化 SQL 中执行，用户传入的值一律走占位符。
package datasets

import (
	"database/sql"

	"github.com/opsorigin/core/internal/report"
)

// Install 把所有内置数据集注册到给定注册表。db 为 nil 时跳过(无数据源)。
// 重复调用安全：注册表对重名返回错误，调用方应保证装配期只装一次。
func Install(db *sql.DB, reg *report.Registry) error {
	if db == nil {
		return nil
	}
	for _, ds := range builtin(db) {
		if err := reg.Register(ds); err != nil {
			return err
		}
	}
	return nil
}

// builtin 构造全部内置数据集(已绑定 SQL 解析器)。
func builtin(db *sql.DB) []*report.Dataset {
	return []*report.Dataset{
		itsmTickets(db),
		vulnFindings(db),
		agentHosts(db),
		auditEvents(db),
		cmdbCI(db),
	}
}

// —— ITSM 工单 ——

func itsmTickets(db *sql.DB) *report.Dataset {
	ds := &report.Dataset{
		Name:        "itsm.tickets",
		Label:       "ITSM 工单",
		Module:      "itsm",
		Description: "变更/运维工单的形态分布与处理进度",
		TimeField:   "created_at",
		Dimensions: []report.Dimension{
			{Name: "status", Label: "状态", Type: report.FieldString, Filterable: true, Groupable: true,
				Enum: []string{"draft", "submitted", "approved", "rejected", "scheduled", "executing", "completed", "closed", "rolled_back"}},
			{Name: "risk_level", Label: "风险等级", Type: report.FieldString, Filterable: true, Groupable: true,
				Enum: []string{"low", "medium", "high", "critical"}},
			{Name: "created_by_type", Label: "创建者类型", Type: report.FieldString, Filterable: true, Groupable: true,
				Enum: []string{"HUMAN", "AGENT", "SYSTEM"}},
			{Name: "trace_id", Label: "链路 ID", Type: report.FieldString, Filterable: true, Groupable: false},
			{Name: "created_at", Label: "创建时间", Type: report.FieldTime, Filterable: true, Groupable: true},
			{Name: "approved_at", Label: "审批时间", Type: report.FieldTime, Filterable: true, Groupable: true},
			{Name: "executed_at", Label: "执行时间", Type: report.FieldTime, Filterable: true, Groupable: true},
			{Name: "updated_at", Label: "更新时间", Type: report.FieldTime, Filterable: true, Groupable: true},
		},
		Metrics: []report.Metric{
			{Name: "cnt", Label: "工单数", Agg: report.AggCount, Unit: "个"},
			{Name: "approved_cnt", Label: "已审批数", Agg: report.AggCount, Unit: "个"},
			{Name: "executed_cnt", Label: "已执行数", Agg: report.AggCount, Unit: "个"},
		},
	}
	return report.NewSQLDataset(db, ds, report.SQLSpec{
		Table:        "itsm_tickets",
		TenantColumn: "tenant_id",
		MetricExprs: map[string]string{
			"approved_cnt": "approved_at",
			"executed_cnt": "executed_at",
		},
	})
}

// —— 漏洞发现 ——

func vulnFindings(db *sql.DB) *report.Dataset {
	ds := &report.Dataset{
		Name:        "vuln.findings",
		Label:       "漏洞发现",
		Module:      "vuln",
		Description: "扫描发现的漏洞按严重度与组件分布",
		TimeField:   "created_at",
		Dimensions: []report.Dimension{
			{Name: "severity", Label: "严重度", Type: report.FieldString, Filterable: true, Groupable: true,
				Enum: []string{"critical", "high", "medium", "low", "info"}},
			{Name: "affected_package", Label: "受影响组件", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "scan_id", Label: "扫描任务", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "cve", Label: "CVE 编号", Type: report.FieldString, Filterable: true, Groupable: false},
			{Name: "created_at", Label: "发现时间", Type: report.FieldTime, Filterable: true, Groupable: true},
		},
		Metrics: []report.Metric{
			{Name: "cnt", Label: "漏洞数", Agg: report.AggCount, Unit: "个"},
			{Name: "avg_rank", Label: "平均风险分", Agg: report.AggAvg, Decimals: 1},
			{Name: "max_rank", Label: "最高风险分", Agg: report.AggMax},
		},
	}
	return report.NewSQLDataset(db, ds, report.SQLSpec{
		Table:        "vuln_findings",
		TenantColumn: "tenant_id",
		MetricExprs: map[string]string{
			"avg_rank": "rank",
			"max_rank": "rank",
		},
	})
}

// —— Agent 纳管主机 ——

func agentHosts(db *sql.DB) *report.Dataset {
	ds := &report.Dataset{
		Name:        "agent.hosts",
		Label:       "纳管主机",
		Module:      "agent",
		Description: "被纳管主机的在线状态与操作系统分布",
		TimeField:   "created_at",
		Dimensions: []report.Dimension{
			{Name: "status", Label: "状态", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "os_type", Label: "操作系统类型", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "type", Label: "资源类型", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "auth_type", Label: "认证方式", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "install_method", Label: "安装方式", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "environment", Label: "环境", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "created_at", Label: "纳管时间", Type: report.FieldTime, Filterable: true, Groupable: true},
			{Name: "last_seen", Label: "最近在线", Type: report.FieldTime, Filterable: true, Groupable: true},
		},
		Metrics: []report.Metric{
			{Name: "cnt", Label: "主机数", Agg: report.AggCount, Unit: "台"},
		},
	}
	return report.NewSQLDataset(db, ds, report.SQLSpec{
		Table:        "agent_hosts",
		TenantColumn: "tenant_id",
	})
}

// —— 审计事件 ——

func auditEvents(db *sql.DB) *report.Dataset {
	ds := &report.Dataset{
		Name:        "audit.events",
		Label:       "审计事件",
		Module:      "audit",
		Description: "全栈操作审计流水按类型与成败分布",
		TimeField:   "timestamp",
		Dimensions: []report.Dimension{
			{Name: "event_type", Label: "事件类型", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "action", Label: "动作", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "actor_type", Label: "操作方类型", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "target_type", Label: "目标类型", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "success", Label: "是否成功", Type: report.FieldBool, Filterable: true, Groupable: true},
			{Name: "timestamp", Label: "发生时间", Type: report.FieldTime, Filterable: true, Groupable: true},
		},
		Metrics: []report.Metric{
			{Name: "cnt", Label: "事件数", Agg: report.AggCount, Unit: "条"},
			{Name: "fail_cnt", Label: "失败事件数", Agg: report.AggCount, Unit: "条"},
		},
	}
	return report.NewSQLDataset(db, ds, report.SQLSpec{
		Table:        "audit_events",
		TenantColumn: "tenant_id",
		MetricExprs: map[string]string{
			"fail_cnt": "CASE WHEN NOT success THEN 1 END",
		},
	})
}

// —— CMDB 配置项 ——

func cmdbCI(db *sql.DB) *report.Dataset {
	ds := &report.Dataset{
		Name:        "cmdb.ci",
		Label:       "CMDB 配置项",
		Module:      "cmdb",
		Description: "配置项按类型与状态分布",
		TimeField:   "created_at",
		Dimensions: []report.Dimension{
			{Name: "type_id", Label: "模型类型", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "status", Label: "状态", Type: report.FieldString, Filterable: true, Groupable: true},
			{Name: "channel", Label: "来源通道", Type: report.FieldNumber, Filterable: true, Groupable: true},
			{Name: "name", Label: "名称", Type: report.FieldString, Filterable: true, Groupable: false},
			{Name: "created_at", Label: "创建时间", Type: report.FieldTime, Filterable: true, Groupable: true},
			{Name: "updated_at", Label: "更新时间", Type: report.FieldTime, Filterable: true, Groupable: true},
		},
		Metrics: []report.Metric{
			{Name: "cnt", Label: "配置项数", Agg: report.AggCount, Unit: "个"},
		},
	}
	return report.NewSQLDataset(db, ds, report.SQLSpec{
		Table:        "cmdb_ci",
		TenantColumn: "tenant_id",
	})
}
