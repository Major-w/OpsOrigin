package model

import (
	"testing"
	"time"
)

// TestCollectionTaskIsDue 覆盖三种情形：
//  1. 从未运行过（LastRunAt==nil）→ 立即到期；
//  2. 距上次运行已超过间隔 → 到期；
//  3. 距上次运行未达间隔 → 未到期。
func TestCollectionTaskIsDue(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	interval := time.Hour

	neverRan := &CollectionTask{}
	if !neverRan.IsDue(now, interval) {
		t.Errorf("IsDue: never-ran task should be due, got false")
	}

	due := &CollectionTask{LastRunAt: ptrTime(now.Add(-2 * time.Hour))}
	if !due.IsDue(now, interval) {
		t.Errorf("IsDue: task past interval should be due, got false")
	}

	notDue := &CollectionTask{LastRunAt: ptrTime(now.Add(-30 * time.Minute))}
	if notDue.IsDue(now, interval) {
		t.Errorf("IsDue: task within interval should not be due, got true")
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
