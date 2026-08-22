package main

import (
	"bufio"
	"bytes"
	"os"
	"runtime"
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
	self  uint64 // 进程自身累计 CPU 滴答(ResourceGuard 用)
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

// --- Agent 自身资源采样 (ResourceGuard 自保护用) ---

var lastSelfCPU = &cpuStat{}
var selfCPUMu sync.Mutex

// readSelfCPUUsage 读取 agent 自身进程的 CPU 占用率(0-100)。
// 基于 /proc/self/stat 的 utime+stime 差值 / 系统总 CPU 时间差值计算。
// 非 Linux 平台退化为 0(不限制),避免误杀。
func readSelfCPUUsage() (float64, bool) {
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(stat))
	// /proc/<pid>/stat 第 14(utime) 15(stime) 字段为时钟滴答,第 2 字段是 comm(可能含空格但被 () 包裹)。
	if len(fields) < 15 {
		return 0, false
	}
	utime, err := strconv.ParseUint(fields[13], 10, 64)
	if err != nil {
		return 0, false
	}
	stime, err := strconv.ParseUint(fields[14], 10, 64)
	if err != nil {
		return 0, false
	}
	selfTicks := utime + stime

	// 读系统总 CPU 时间(来自 /proc/stat 第一行)
	sf, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	sc := bufio.NewScanner(bytes.NewReader(sf))
	if !sc.Scan() {
		return 0, false
	}
	parts := strings.Fields(sc.Text())
	if len(parts) < 5 {
		return 0, false
	}
	var sysTotal uint64
	for i := 1; i < len(parts); i++ {
		v, err := strconv.ParseUint(parts[i], 10, 64)
		if err == nil {
			sysTotal += v
		}
	}

	selfCPUMu.Lock()
	defer selfCPUMu.Unlock()
	if !lastSelfCPU.ok {
		lastSelfCPU.total, lastSelfCPU.idle, lastSelfCPU.ok = sysTotal, 0, true
		lastSelfCPU.self = selfTicks
		return 0, true // 首轮仅建立基线
	}
	dSys := sysTotal - lastSelfCPU.total
	dSelf := selfTicks - lastSelfCPU.self
	lastSelfCPU.total, lastSelfCPU.self = sysTotal, selfTicks
	if dSys == 0 {
		return 0, true
	}
	// 单进程 CPU% = 进程占用滴答 / 系统总滴答 * 100(单核视角,多核可超 100)。
	usage := 100.0 * float64(dSelf) / float64(dSys)
	if usage < 0 {
		usage = 0
	}
	return usage, true
}

// readSelfMemRSSMB 读取 agent 自身进程常驻内存(RSS,单位 MB)。
// 优先读 /proc/self/statm 第 2 字段(RSS 页数 * page_size);不可用时退化为 runtime.MemStats.Alloc。
func readSelfMemRSSMB() (float64, bool) {
	if data, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				var pageSize int64
				if sz := os.Getpagesize(); sz > 0 {
					pageSize = int64(sz)
				} else {
					pageSize = 4096
				}
				return float64(pages*uint64(pageSize)) / (1024 * 1024), true
			}
		}
	}
	// 退化: 用 Go runtime 堆分配近似(非完整 RSS,但跨平台可用)。
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.Alloc) / (1024 * 1024), true
}
