package servicehours

import (
	"math"
	"testing"
	"time"
)

// bizWeek 构造标准 5x8 窗口（周一~周五 09:00-18:00，Asia/Shanghai）。
func bizWeek() *Window {
	w := &Window{Name: "5x8", Timezone: "Asia/Shanghai"}
	w.Weekly = defaultWeekly()
	return w
}

// 必须用真实时区名验证 Loc()；2026-01-05 是周一，01-10 周六，01-11 周日。
func at(y int, mo time.Month, d int, h, m int) time.Time {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return time.Date(y, mo, d, h, m, 0, 0, loc)
}

func TestIsOpen(t *testing.T) {
	w := bizWeek()
	cases := []struct {
		at   time.Time
		want bool
		note string
	}{
		{at(2026, 1, 5, 10, 0), true, "周一 10:00 窗口内"},
		{at(2026, 1, 5, 9, 0), true, "开始时刻含边界"},
		{at(2026, 1, 5, 18, 0), false, "结束时刻不含"},
		{at(2026, 1, 5, 8, 0), false, "上班前"},
		{at(2026, 1, 10, 10, 0), false, "周六"},
		{at(2026, 1, 11, 10, 0), false, "周日"},
	}
	for _, c := range cases {
		if got := w.IsOpen(c.at); got != c.want {
			t.Errorf("IsOpen(%s) [%s] = %v, want %v", c.at, c.note, got, c.want)
		}
	}
}

func TestIsOpenHoliday(t *testing.T) {
	w := bizWeek()
	w.Holidays = []string{"2026-01-05"} // 周一标记为节假日
	if w.IsOpen(at(2026, 1, 5, 10, 0)) {
		t.Error("节假日应视为关闭")
	}
	if !w.IsOpen(at(2026, 1, 6, 10, 0)) {
		t.Error("次日（周二）应正常开放")
	}
}

func TestAddBusinessTimeWithinWindow(t *testing.T) {
	w := bizWeek()
	// 周一 10:00 + 2h = 周一 12:00
	got := w.AddBusinessTime(at(2026, 1, 5, 10, 0), 2*time.Hour)
	if !got.Equal(at(2026, 1, 5, 12, 0)) {
		t.Errorf("窗口内推进: got %v, want 周一 12:00", got)
	}
}

func TestAddBusinessTimeAcrossWindowBoundary(t *testing.T) {
	w := bizWeek()
	// 周一 17:00 + 2h：当天剩 1h(17~18)，周二从窗口起点 09:00 起 + 1h → 周二 10:00
	got := w.AddBusinessTime(at(2026, 1, 5, 17, 0), 2*time.Hour)
	if !got.Equal(at(2026, 1, 6, 10, 0)) {
		t.Errorf("跨窗口推进: got %v, want 周二 10:00", got)
	}
}

func TestAddBusinessTimeSkipWeekend(t *testing.T) {
	w := bizWeek()
	// 周五 17:30 + 2h：周五 0.5h + 跳过周末 + 周一 1.5h → 周一 10:30
	got := w.AddBusinessTime(at(2026, 1, 9, 17, 30), 2*time.Hour)
	if !got.Equal(at(2026, 1, 12, 10, 30)) {
		t.Errorf("跨周末推进: got %v, want 周一 10:30", got)
	}
}

func TestAddBusinessTimeSkipHoliday(t *testing.T) {
	w := bizWeek()
	w.Holidays = []string{"2026-01-12"} // 下周一放假
	// 周五 17:30 + 2h → 跳过周末与节假日 → 周二 10:30
	got := w.AddBusinessTime(at(2026, 1, 9, 17, 30), 2*time.Hour)
	if !got.Equal(at(2026, 1, 13, 10, 30)) {
		t.Errorf("跳过节假日: got %v, want 周二 10:30", got)
	}
}

func TestAddBusinessTimeStartsOutsideWindow(t *testing.T) {
	w := bizWeek()
	// 周六 15:00 + 1h → 下周一 10:00（起点在窗口外快进到下一窗口起点）
	got := w.AddBusinessTime(at(2026, 1, 10, 15, 0), time.Hour)
	if !got.Equal(at(2026, 1, 12, 10, 0)) {
		t.Errorf("窗口外起点: got %v, want 周一 10:00", got)
	}
}

func TestAddBusinessTimeFallbackAllDisabled(t *testing.T) {
	w := &Window{Name: "empty", Timezone: "Asia/Shanghai"} // 全部禁用
	from := at(2026, 1, 5, 10, 0)
	got := w.AddBusinessTime(from, 3*time.Hour)
	if !got.Equal(from.Add(3 * time.Hour)) {
		t.Errorf("全禁用兜底应按墙钟: got %v", got)
	}
}

func TestBusinessElapsed(t *testing.T) {
	w := bizWeek()
	// 周一 10:00 → 周一 20:00：业务时长 = 8h（10~18）
	got := w.BusinessElapsed(at(2026, 1, 5, 10, 0), at(2026, 1, 5, 20, 0))
	if math.Abs(got.Hours()-8) > 1e-9 {
		t.Errorf("BusinessElapsed 同日: got %v, want 8h", got)
	}
	// 周五 17:00 → 周一 11:00：1h + 2h = 3h
	got = w.BusinessElapsed(at(2026, 1, 9, 17, 0), at(2026, 1, 12, 11, 0))
	if math.Abs(got.Hours()-3) > 1e-9 {
		t.Errorf("BusinessElapsed 跨周末: got %v, want 3h", got)
	}
	// 倒序返回 0
	if got := w.BusinessElapsed(at(2026, 1, 5, 12, 0), at(2026, 1, 5, 10, 0)); got != 0 {
		t.Errorf("BusinessElapsed 倒序应返回 0, got %v", got)
	}
}

func TestNormalizeDefaults(t *testing.T) {
	w := &Window{Name: "x"} // 无时区、weekly 零值
	w.normalize()
	if w.Timezone != defaultTimezone {
		t.Errorf("时区默认应为 %s, got %s", defaultTimezone, w.Timezone)
	}
	if !w.Weekly[1].Enabled || w.Weekly[0].Enabled {
		t.Error("默认 weekly 应为周一~周五启用")
	}
	if w.Holidays == nil {
		t.Error("Holidays 应归一为非 nil 切片")
	}
}
