package process

import (
	"strconv"
	"strings"
	"time"
)

// parseStepDuration 解析定时器步骤的 duration 字段("1h"/"30m"/"3d"/"90s", 支持组合如 "1h30m")。
// 返回 (时长, 是否可解析且 > 0)。
func parseStepDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var total time.Duration
	num := ""
	for _, r := range s {
		if r >= '0' && r <= '9' || r == '.' {
			num += string(r)
			continue
		}
		if num == "" {
			return 0, false
		}
		var unit time.Duration
		switch r {
		case 's':
			unit = time.Second
		case 'm':
			unit = time.Minute
		case 'h':
			unit = time.Hour
		case 'd':
			unit = 24 * time.Hour
		default:
			return 0, false
		}
		v, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, false
		}
		total += time.Duration(v * float64(unit))
		num = ""
	}
	if num != "" || total <= 0 {
		return 0, false
	}
	return total, true
}
