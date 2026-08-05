package risk

import (
	"testing"

	"github.com/opsorigin/core/internal/itsm/model"
)

func TestClassify(t *testing.T) {
	c := New()
	cases := []struct {
		name              string
		title, desc, plan string
		want              model.RiskLevel
	}{
		{"critical drop table", "紧急", "drop table users", "", model.RiskCritical},
		{"critical production master db", "发布", "对 production 主库执行 ddl", "", model.RiskCritical},
		{"critical cluster", "变更", "重建 cluster 集群", "", model.RiskCritical},
		{"high restart", "重启", "restart app", "", model.RiskHigh},
		{"high deploy", "上线", "deploy release", "", model.RiskHigh},
		{"medium stateless", "调整", "单机无状态服务", "", model.RiskMedium},
		{"medium env", "配置", "修改环境变量", "", model.RiskMedium},
		{"low routine", "常规", "更新文档", "同步知识库", model.RiskLow},
		{"case insensitive", "X", "DROP TABLE", "", model.RiskCritical},
		{"critical beats high", "x", "restart drop table", "", model.RiskCritical},
		{"high beats medium", "x", "restart 修改环境变量", "", model.RiskHigh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Classify(tc.title, tc.desc, tc.plan)
			if got != tc.want {
				t.Errorf("Classify(%q,%q,%q)=%v want %v", tc.title, tc.desc, tc.plan, got, tc.want)
			}
		})
	}
}
