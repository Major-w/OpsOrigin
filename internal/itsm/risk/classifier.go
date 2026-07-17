package risk

import (
	"strings"

	"github.com/opsorigin/core/internal/itsm/model"
)

var criticalPatterns = []string{
	"database", "ddl", "dml", "drop table", "alter table", "truncate",
	"firewall", "security", "iam", "certificate", "tls", "ssl",
	"delete", "destroy", "terminate", "集群", "cluster",
	"production", "prod", "主库", "master",
}

var highPatterns = []string{
	"restart", "reboot", "upgrade", "rollback", "migration",
	"config", "configuration", "deploy", "release", "滚动",
	"scale", "扩容", "缩容", "ingress", "loadbalancer",
}

var mediumPatterns = []string{
	"restart service", "restart app", "restart application",
	"single node", "单机", "无状态", "stateless",
	"deployment", "环境变量", "env",
}

type Classifier struct{}

func New() *Classifier {
	return &Classifier{}
}

func (c *Classifier) Classify(title, description, changePlan string) model.RiskLevel {
	text := strings.ToLower(title + " " + description + " " + changePlan)

	for _, p := range criticalPatterns {
		if strings.Contains(text, p) {
			return model.RiskCritical
		}
	}

	for _, p := range highPatterns {
		if strings.Contains(text, p) {
			return model.RiskHigh
		}
	}

	for _, p := range mediumPatterns {
		if strings.Contains(text, p) {
			return model.RiskMedium
		}
	}

	return model.RiskLow
}
