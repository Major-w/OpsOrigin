package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// 内置系统监控采集器(阶段 3)。Linux 优先: /proc 直读, 零外部依赖。
// 采集项: CPU 使用率 / 内存 / 根磁盘 / 网络字节计数。
// 非 Linux 平台: 内存/磁盘尽力采集(syscall.Statfs 跨平台), CPU/网络返回空(避免误报)。

// cpuStat 两次 /proc/stat 差值的状态。
type cpuStat struct {
	mu    sync.Mutex
	total uint64
	idle  uint64
	ok    bool
}

var lastCPU = &cpuStat{}

// readCPUUsage 读取 CPU 总使用率(0-100)。内部用两次 /proc/stat 差值计算。
func readCPUUsage() (float64, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, false
	}
	fields := strings.Fields(sc.Text()) // cpu user nice system idle iowait irq softirq steal
	if len(fields) < 5 {
		return 0, false
	}
	var total, idle uint64
	for i := 1; i < len(fields); i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return 0, false
		}
		total += v
	}
	idle = mustParseUint(fields[4])

	lastCPU.mu.Lock()
	defer lastCPU.mu.Unlock()
	if !lastCPU.ok {
		lastCPU.total, lastCPU.idle, lastCPU.ok = total, idle, true
		return 0, true // 首轮只建立基线
	}
	dTotal := total - lastCPU.total
	dIdle := idle - lastCPU.idle
	lastCPU.total, lastCPU.idle = total, idle
	if dTotal == 0 {
		return 0, true
	}
	usage := 100.0 * float64(dTotal-dIdle) / float64(dTotal)
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return usage, true
}

func mustParseUint(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

// readMemory 读取内存使用(单位字节)。/proc/meminfo: MemTotal / MemAvailable。
func readMemory() (total, used, percent float64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()

	var memTotal, memAvail float64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			memTotal = parseMemInfoValue(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			memAvail = parseMemInfoValue(line)
		}
		if memTotal > 0 && memAvail > 0 {
			break
		}
	}
	if memTotal <= 0 {
		return 0, 0, 0, false
	}
	used = memTotal - memAvail
	if used < 0 {
		used = 0
	}
	return memTotal, used, 100.0 * used / memTotal, true
}

// parseMemInfoValue 解析 "MemTotal:       16384000 kB" → 字节。
func parseMemInfoValue(line string) float64 {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	v, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0
	}
	if len(parts) >= 3 && parts[2] == "kB" {
		v *= 1024
	}
	return v
}

// readDiskUsage 读取指定路径所在文件系统的用量(单位字节)。跨平台(syscall.Statfs)。
func readDiskUsage(path string) (total, used, percent float64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, 0, false
	}
	// linux/darwin 字段一致: Bsize/Bavail/Bfree/Blocks
	total = float64(st.Blocks) * float64(st.Bsize)
	free := float64(st.Bavail) * float64(st.Bsize)
	if total <= 0 {
		return 0, 0, 0, false
	}
	used = total - free
	if used < 0 {
		used = 0
	}
	return total, used, 100.0 * used / total, true
}

// readNetBytes 读取网络累计收发字节(/proc/net/dev)。返回 (rx, tx, ok)。
func readNetBytes() (rx, tx float64, ok bool) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 9 {
			continue
		}
		r, err1 := strconv.ParseFloat(fields[0], 64)
		t, err2 := strconv.ParseFloat(fields[8], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rx += r
		tx += t
	}
	return rx, tx, true
}

// CollectSystemMetrics 采集基础系统监控指标(内置 monitor 任务)。
// 返回的指标点: cpu.usage / mem.used / mem.total / mem.percent /
// disk.used / disk.total / disk.percent / net.rx.bytes / net.tx.bytes。
func CollectSystemMetrics() []MetricPoint {
	pts := make([]MetricPoint, 0, 9)
	ts := time.Now()

	if v, ok := readCPUUsage(); ok {
		pts = append(pts, MetricPoint{Name: "cpu.usage", Value: v, Unit: "%", Timestamp: ts})
	}
	if total, used, percent, ok := readMemory(); ok {
		pts = append(pts,
			MetricPoint{Name: "mem.total", Value: total, Unit: "bytes", Timestamp: ts},
			MetricPoint{Name: "mem.used", Value: used, Unit: "bytes", Timestamp: ts},
			MetricPoint{Name: "mem.percent", Value: percent, Unit: "%", Timestamp: ts},
		)
	}
	if total, used, percent, ok := readDiskUsage("/"); ok {
		pts = append(pts,
			MetricPoint{Name: "disk.total", Value: total, Unit: "bytes", Labels: map[string]string{"mount": "/"}, Timestamp: ts},
			MetricPoint{Name: "disk.used", Value: used, Unit: "bytes", Labels: map[string]string{"mount": "/"}, Timestamp: ts},
			MetricPoint{Name: "disk.percent", Value: percent, Unit: "%", Labels: map[string]string{"mount": "/"}, Timestamp: ts},
		)
	}
	if rx, tx, ok := readNetBytes(); ok {
		pts = append(pts,
			MetricPoint{Name: "net.rx.bytes", Value: rx, Unit: "bytes", Timestamp: ts},
			MetricPoint{Name: "net.tx.bytes", Value: tx, Unit: "bytes", Timestamp: ts},
		)
	}
	return pts
}
