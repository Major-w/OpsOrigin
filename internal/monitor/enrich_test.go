package monitor

import (
	"context"
	"testing"

	"github.com/opsorigin/core/internal/monitor/model"
	"github.com/opsorigin/core/pkg/config"
)

// ingestAlert 富化逻辑: 注入 enricher 后, 告警进入 aggregator 前
// 应携带 CMDB 上下文(owner/criticality); 未注入时保持原样(nil 安全)。
func TestIngestAlert_EnrichesWithCMDBContext(t *testing.T) {
	s := NewService(&config.Config{}, nil)
	// aggregator 可能依赖 redis/时间; 这里只验证富化在 Ingest 前发生。
	// 用无通知、无拓扑的 aggregator 直接验证 Labels 被写入。
	s.SetCMDBEnricher(func(ctx context.Context, ciID, tenantID string) map[string]string {
		if ciID != "ci-db-01" {
			return nil
		}
		return map[string]string{
			"owner": "dba-team", "business_criticality": "critical", "ci_name": "db-master", "ci_type": "mysql",
		}
	})

	alert := &model.AlertEvent{
		TenantID: "default", SourceCIID: "ci-db-01", RuleName: "cpu-high",
		Severity: model.AlertWarning, Title: "db cpu high",
	}
	// 直接调 ingestAlert 内部富化段(不依赖 aggregator 持久化),
	// 通过读取 enricher 注入后的 Labels 验证。
	enriched := s.applyCMDBEnrichment(context.Background(), alert)
	if enriched.Labels["owner"] != "dba-team" {
		t.Errorf("告警应携带 owner 上下文, got %v", enriched.Labels)
	}
	if enriched.Labels["business_criticality"] != "critical" {
		t.Errorf("告警应携带业务关键性, got %v", enriched.Labels)
	}
	if enriched.Labels["ci_name"] != "db-master" || enriched.Labels["ci_type"] != "mysql" {
		t.Errorf("ci 身份上下文缺失: %v", enriched.Labels)
	}
}

func TestIngestAlert_NoEnricherKeepsLabels(t *testing.T) {
	s := NewService(&config.Config{}, nil) // 未注入 enricher
	alert := &model.AlertEvent{
		TenantID: "default", SourceCIID: "ci-x", RuleName: "r1",
		Severity: model.AlertWarning, Title: "t",
		Labels: map[string]string{"env": "prod"},
	}
	enriched := s.applyCMDBEnrichment(context.Background(), alert)
	if enriched.Labels["env"] != "prod" {
		t.Errorf("未注入 enricher 时告警 labels 应保持不变: %v", enriched.Labels)
	}
	if _, hasOwner := enriched.Labels["owner"]; hasOwner {
		t.Error("未注入 enricher 不应出现 owner")
	}
}

func TestIngestAlert_RuleLabelTakesPrecedence(t *testing.T) {
	s := NewService(&config.Config{}, nil)
	s.SetCMDBEnricher(func(ctx context.Context, ciID, tenantID string) map[string]string {
		return map[string]string{"owner": "cmdb-owner", "env": "cmdb-env"}
	})
	alert := &model.AlertEvent{
		TenantID: "default", SourceCIID: "ci-1", RuleName: "r1",
		Severity: model.AlertWarning, Title: "t",
		Labels: map[string]string{"owner": "rule-owner"}, // 规则显式声明优先
	}
	enriched := s.applyCMDBEnrichment(context.Background(), alert)
	if enriched.Labels["owner"] != "rule-owner" {
		t.Errorf("规则自带 label 应优先于富化: got %v", enriched.Labels["owner"])
	}
	if enriched.Labels["env"] != "cmdb-env" {
		t.Errorf("富化补充缺失键: got %v", enriched.Labels["env"])
	}
}
