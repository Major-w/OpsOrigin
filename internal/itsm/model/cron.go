package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseCron 解析标准 5 段 cron 表达式「分 时 日 月 周」，返回各字段的取值集合。
// 支持: * 数字 逗号列表 区间 a-b 步进 */n 与 a-b/n；星期 0/7 均为周日。
// 语义简化说明: 标准 cron 中「日」与「周」同时受限时为 OR 命中，本实现按标准语义处理。
func ParseCron(expr string) (*CronExpr, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 需要 5 段「分 时 日 月 周」: %q", expr)
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}} // 周 0/7 均为周日(后者归一)
	sets := [5]map[int]bool{}
	for i, f := range fields {
		s, err := parseCronField(f, bounds[i][0], bounds[i][1])
		if err != nil {
			return nil, fmt.Errorf("cron 第 %d 段 %q: %w", i+1, f, err)
		}
		sets[i] = s
	}
	// 0/7 归一为周日
	for d := range sets[4] {
		if d == 7 {
			sets[4][0] = true
			delete(sets[4], 7)
		}
	}
	return &CronExpr{minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4]}, nil
}

// CronExpr 已解析的 cron 表达式，Matches 判定某时刻是否命中（分钟粒度）。
type CronExpr struct {
	minute, hour, dom, month, dow map[int]bool
}

// Matches 判定时刻 t（本地时区）是否命中本表达式。
func (c *CronExpr) Matches(t time.Time) bool {
	if !c.minute[t.Minute()] || !c.hour[t.Hour()] || !c.month[int(t.Month())] {
		return false
	}
	domRestricted := !fullRange(c.dom, 1, 31)
	dowRestricted := !fullRange(c.dow, 0, 6)
	domOK := c.dom[t.Day()]
	dowOK := c.dow[int(t.Weekday())]
	switch {
	case domRestricted && dowRestricted:
		return domOK || dowOK // 标准 cron 语义: 两者同时受限取 OR
	case domRestricted:
		return domOK
	case dowRestricted:
		return dowOK
	default:
		return true
	}
}

func fullRange(m map[int]bool, lo, hi int) bool {
	if len(m) != hi-lo+1 {
		return false
	}
	for v := lo; v <= hi; v++ {
		if !m[v] {
			return false
		}
	}
	return true
}

func parseCronField(f string, lo, hi int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(f, ",") {
		step := 1
		if _, rest, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(rest)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("步进非法")
			}
			step = n
			part = strings.TrimSuffix(part, "/"+rest)
		}
		start, end := lo, hi
		switch {
		case part == "*" || part == "":
			// 全范围（"" 出现在 "*/n" 场景）
		case strings.Contains(part, "-"):
			a, b, ok := strings.Cut(part, "-")
			if !ok {
				return nil, fmt.Errorf("区间非法")
			}
			var err error
			if start, err = strconv.Atoi(a); err != nil {
				return nil, fmt.Errorf("区间起点非法")
			}
			if end, err = strconv.Atoi(b); err != nil {
				return nil, fmt.Errorf("区间终点非法")
			}
		default:
			v, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("数字非法")
			}
			start, end = v, v
		}
		if start < lo || end > hi || start > end {
			return nil, fmt.Errorf("取值越界 [%d,%d]", lo, hi)
		}
		for v := start; v <= end; v += step {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("空取值")
	}
	return out, nil
}
