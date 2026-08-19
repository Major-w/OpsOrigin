package agentic

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
)

// RCARecord 根因分析基准记录: 记录 AI 给出的 RCA 结论, 由人工复盘时标注准确性,
// 沉淀内部 RCA 准确率证据(对标文章"诊断占事故时间 40-70%, RCA 质量是第一指标")。
type RCARecord struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	IncidentID string     `json:"incident_id"`
	Title      string     `json:"title"`
	Hypothesis string     `json:"hypothesis"`         // AI 给出的根因假设
	Confidence float64    `json:"confidence"`         // AI 自评置信度 0-1
	Resolved   bool       `json:"resolved"`           // 事件是否已解决(对照)
	Accurate   *bool      `json:"accurate,omitempty"` // 人工标注: 根因是否准确
	Note       string     `json:"note,omitempty"`     // 人工备注
	CreatedAt  time.Time  `json:"created_at"`
	LabeledAt  *time.Time `json:"labeled_at,omitempty"`
}

// RCAAccuracyStats RCA 基准统计。
type RCAAccuracyStats struct {
	Total         int     `json:"total"`          // 已记录总数
	Labeled       int     `json:"labeled"`        // 已标注数
	Accurate      int     `json:"accurate"`       // 标注准确数
	AccuracyRate  float64 `json:"accuracy_rate"`  // 准确率(labeled>0)
	AvgConfidence float64 `json:"avg_confidence"` // 平均自评置信度(labeled)
}

// RecordRCA 记录一条 AI 根因分析(供事后人工标注形成基准)。
func (e *Engine) RecordRCA(tenantID string, rec *RCARecord) *RCARecord {
	if rec.ID == "" {
		rec.ID = uuid.New().String()
	}
	rec.TenantID = tenantID
	rec.CreatedAt = time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rcaRecords == nil {
		e.rcaRecords = map[string]*RCARecord{}
	}
	e.rcaRecords[rec.ID] = rec
	slog.Info("rca record created", "incident", rec.IncidentID, "title", rec.Title)
	return rec
}

// LabelRCA 人工标注一条 RCA 是否准确(形成基准)。
func (e *Engine) LabelRCA(tenantID, id string, accurate bool, note string) (*RCARecord, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rec, ok := e.rcaRecords[id]
	if !ok || rec.TenantID != tenantID {
		return nil, fmt.Errorf("rca record not found: %s", id)
	}
	now := time.Now()
	rec.Accurate = &accurate
	rec.Note = note
	rec.LabeledAt = &now
	return rec, nil
}

// ListRCARecords 列出租户 RCA 记录(按创建时间倒序)。
func (e *Engine) ListRCARecords(tenantID string) []*RCARecord {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []*RCARecord
	for _, rec := range e.rcaRecords {
		if rec.TenantID == tenantID {
			cp := *rec
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// RCAAccuracy 统计租户 RCA 准确率。
func (e *Engine) RCAAccuracy(tenantID string) RCAAccuracyStats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	stats := RCAAccuracyStats{}
	var confSum float64
	for _, rec := range e.rcaRecords {
		if rec.TenantID != tenantID {
			continue
		}
		stats.Total++
		if rec.Accurate != nil {
			stats.Labeled++
			if *rec.Accurate {
				stats.Accurate++
			}
			confSum += rec.Confidence
		}
	}
	if stats.Labeled > 0 {
		stats.AccuracyRate = float64(stats.Accurate) / float64(stats.Labeled)
		stats.AvgConfidence = confSum / float64(stats.Labeled)
	}
	return stats
}

// ensureRCARecordsInit 兜底初始化(单测/内存态)。
func (e *Engine) ensureRCARecordsInit() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rcaRecords == nil {
		e.rcaRecords = map[string]*RCARecord{}
	}
}
