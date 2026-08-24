package system

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SystemInfo 系统信息结构
type SystemInfo struct {
	Hostname     string         `json:"hostname"`
	OS           string         `json:"os"`
	Arch         string         `json:"arch"`
	GoVersion    string         `json:"go_version"`
	NumCPU       int            `json:"num_cpu"`
	NumGoroutine int            `json:"num_goroutine"`
	Uptime       string         `json:"uptime"`
	CPUs         []CPUInfo      `json:"cpus"`
	Memory       MemoryInfo     `json:"memory"`
	Load         LoadInfo       `json:"load"`
	Disk         []DiskInfo     `json:"disk"`
	DiskIO       DiskIOInfo     `json:"disk_io"`
	Process      ProcessInfo    `json:"process"`
	ProcessTops  ProcessTopList `json:"process_tops"`
	Network      NetworkInfo    `json:"network"`
	Timestamp    int64          `json:"timestamp"`
}

// CPUInfo CPU信息
type CPUInfo struct {
	ModelName string  `json:"model_name"`
	Cores     int     `json:"cores"`
	MHz       float64 `json:"mhz"`
	UsagePct  float64 `json:"usage_pct"`
}

// MemoryInfo 内存信息
type MemoryInfo struct {
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	Available  uint64  `json:"available"`
	Buffers    uint64  `json:"buffers"`
	Cached     uint64  `json:"cached"`
	UsagePct   float64 `json:"usage_pct"`
	TotalHuman string  `json:"total_human"`
	UsedHuman  string  `json:"used_human"`
	FreeHuman  string  `json:"free_human"`
}

// LoadInfo 负载信息
type LoadInfo struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

// DiskInfo 磁盘信息
type DiskInfo struct {
	Filesystem string  `json:"filesystem"`
	Size       uint64  `json:"size"`
	Used       uint64  `json:"used"`
	Available  uint64  `json:"available"`
	UsagePct   float64 `json:"usage_pct"`
	MountedOn  string  `json:"mounted_on"`
	SizeHuman  string  `json:"size_human"`
	UsedHuman  string  `json:"used_human"`
}

// DiskIOInfo 磁盘IO信息
type DiskIOInfo struct {
	ReadOpsSec       float64 `json:"read_ops_sec"`
	WriteOpsSec      float64 `json:"write_ops_sec"`
	ReadMBps         float64 `json:"read_mbps"`
	WriteMBps        float64 `json:"write_mbps"`
	IOWaitPct        float64 `json:"io_wait_pct"`
	ReadOpsSecHuman  string  `json:"read_ops_sec_human"`
	WriteOpsSecHuman string  `json:"write_ops_sec_human"`
	ReadMBpsHuman    string  `json:"read_mbps_human"`
	WriteMBpsHuman   string  `json:"write_mbps_human"`
}

// ProcessInfo 进程信息
type ProcessInfo struct {
	PID        int    `json:"pid"`
	NumFD      int    `json:"num_fd"`
	NumThreads int    `json:"num_threads"`
	RSS        uint64 `json:"rss"`
	VMS        uint64 `json:"vms"`
	RSSHuman   string `json:"rss_human"`
}

// ProcessTopInfo 单个进程Top信息
type ProcessTopInfo struct {
	PID        int     `json:"pid"`
	Name       string  `json:"name"`
	UsagePct   float64 `json:"usage_pct"`
	UsageHuman string  `json:"usage_human"`
}

// ProcessTopList 进程Top列表
type ProcessTopList struct {
	CPU    []ProcessTopInfo `json:"cpu"`
	Memory []ProcessTopInfo `json:"memory"`
	DiskIO []ProcessTopInfo `json:"disk_io"`
}

// NetworkInfo 网络信息
type NetworkInfo struct {
	Connections int   `json:"connections"`
	ListenPorts []int `json:"listen_ports"`
}

// diskIOSnapshot 用于计算磁盘IO速率的采样快照
type diskIOSnapshot struct {
	ReadOps      uint64
	WriteOps     uint64
	ReadSectors  uint64
	WriteSectors uint64
	Timestamp    int64
}

var (
	diskIOCache     diskIOSnapshot
	diskIOCacheMu   sync.RWMutex
	diskIOCacheOnce sync.Once
)

// getSystemInfo 获取系统信息
func getSystemInfo() (*SystemInfo, error) {
	info := &SystemInfo{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		GoVersion:    runtime.Version(),
		NumCPU:       runtime.NumCPU(),
		NumGoroutine: runtime.NumGoroutine(),
		Timestamp:    time.Now().Unix(),
	}

	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}

	info.Uptime = getUptime()
	info.CPUs = getCPUInfo()
	info.Memory = getMemoryInfo()
	info.Load = getLoadInfo()
	info.Disk = getDiskInfo()
	info.DiskIO = getDiskIOInfo()
	info.Process = getProcessInfo()
	info.ProcessTops = getProcessTopList()
	info.Network = getNetworkInfo()

	return info, nil
}

// getUptime 获取系统运行时间
func getUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "unknown"
	}
	parts := strings.Fields(string(data))
	if len(parts) == 0 {
		return "unknown"
	}
	seconds, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return "unknown"
	}
	d := time.Duration(seconds) * time.Second
	return fmt.Sprintf("%dd %dh %dm", int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60)
}

// getCPUInfo 获取CPU信息
func getCPUInfo() []CPUInfo {
	var cpus []CPUInfo
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return cpus
	}
	defer file.Close()

	var current CPUInfo
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if current.ModelName != "" {
				cpus = append(cpus, current)
			}
			current = CPUInfo{}
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "model name":
			current.ModelName = val
		case "cpu cores":
			if n, err := strconv.Atoi(val); err == nil {
				current.Cores = n
			}
		case "cpu MHz":
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				current.MHz = f
			}
		}
	}
	if current.ModelName != "" {
		cpus = append(cpus, current)
	}

	// 获取CPU使用率（简化版：读取/proc/stat）
	if usage := getCPUUsage(); usage >= 0 && len(cpus) > 0 {
		for i := range cpus {
			cpus[i].UsagePct = usage
		}
	}

	return cpus
}

// cpuUsageSnapshot 保存一次CPU采样数据
type cpuUsageSnapshot struct {
	Total float64
	Idle  float64
}

// readCPUStat 读取 /proc/stat 中 cpu 行的数据
func readCPUStat() *cpuUsageSnapshot {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		var total, idle float64
		for i := 1; i < len(fields); i++ {
			v, _ := strconv.ParseFloat(fields[i], 64)
			total += v
			if i == 4 {
				idle = v
			}
		}
		return &cpuUsageSnapshot{Total: total, Idle: idle}
	}
	return nil
}

// getCPUUsage 获取CPU实时使用率（两次采样计算差值）
func getCPUUsage() float64 {
	// 第一次采样
	snap1 := readCPUStat()
	if snap1 == nil {
		return -1
	}

	// 等待100ms
	time.Sleep(100 * time.Millisecond)

	// 第二次采样
	snap2 := readCPUStat()
	if snap2 == nil {
		return -1
	}

	totalDiff := snap2.Total - snap1.Total
	idleDiff := snap2.Idle - snap1.Idle

	if totalDiff <= 0 {
		return 0
	}

	usage := ((totalDiff - idleDiff) / totalDiff) * 100
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return usage
}

// getMemoryInfo 获取内存信息
func getMemoryInfo() MemoryInfo {
	var mem MemoryInfo
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return mem
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.TrimSuffix(val, " kB")
		v, _ := strconv.ParseUint(val, 10, 64)
		v = v * 1024 // 转换为字节

		switch key {
		case "MemTotal":
			mem.Total = v
		case "MemFree":
			mem.Free = v
		case "MemAvailable":
			mem.Available = v
		case "Buffers":
			mem.Buffers = v
		case "Cached":
			mem.Cached = v
		}
	}

	mem.Used = mem.Total - mem.Available
	if mem.Total > 0 {
		mem.UsagePct = float64(mem.Used) / float64(mem.Total) * 100
	}
	mem.TotalHuman = formatBytes(mem.Total)
	mem.UsedHuman = formatBytes(mem.Used)
	mem.FreeHuman = formatBytes(mem.Available)

	return mem
}

// getLoadInfo 获取系统负载
func getLoadInfo() LoadInfo {
	var load LoadInfo
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return load
	}
	parts := strings.Fields(string(data))
	if len(parts) >= 3 {
		load.Load1, _ = strconv.ParseFloat(parts[0], 64)
		load.Load5, _ = strconv.ParseFloat(parts[1], 64)
		load.Load15, _ = strconv.ParseFloat(parts[2], 64)
	}
	return load
}

// getDiskInfo 获取磁盘信息
func getDiskInfo() []DiskInfo {
	var disks []DiskInfo
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return disks
	}

	seen := make(map[string]bool)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		fs := fields[0]
		mount := fields[1]
		// 跳过虚拟文件系统
		if strings.HasPrefix(fs, "/dev/") {
			if seen[mount] {
				continue
			}
			seen[mount] = true
			if d := getDiskUsage(mount); d != nil {
				d.Filesystem = fs
				d.MountedOn = mount
				d.SizeHuman = formatBytes(d.Size)
				d.UsedHuman = formatBytes(d.Used)
				disks = append(disks, *d)
			}
		}
	}
	return disks
}

// readDiskStatsRaw 读取 /proc/diskstats 原始数据，返回物理磁盘累计值
func readDiskStatsRaw() (readOps, writeOps, readSectors, writeSectors uint64) {
	data, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}
		name := fields[2]
		if len(name) == 0 || (name[0] >= '0' && name[0] <= '9') {
			continue
		}
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}
		ro, _ := strconv.ParseUint(fields[3], 10, 64)
		rs, _ := strconv.ParseUint(fields[5], 10, 64)
		wo, _ := strconv.ParseUint(fields[7], 10, 64)
		ws, _ := strconv.ParseUint(fields[9], 10, 64)
		readOps += ro
		writeOps += wo
		readSectors += rs
		writeSectors += ws
	}
	return
}

// getDiskIOInfo 获取磁盘IO统计信息（计算实时速率）
func getDiskIOInfo() DiskIOInfo {
	var io DiskIOInfo

	now := time.Now().UnixMilli()
	readOps, writeOps, readSectors, writeSectors := readDiskStatsRaw()

	diskIOCacheMu.Lock()
	prev := diskIOCache
	// 更新缓存
	diskIOCache = diskIOSnapshot{
		ReadOps:      readOps,
		WriteOps:     writeOps,
		ReadSectors:  readSectors,
		WriteSectors: writeSectors,
		Timestamp:    now,
	}
	diskIOCacheMu.Unlock()

	// 如果是第一次采样，没有历史数据，返回空值
	if prev.Timestamp == 0 {
		return io
	}

	elapsedSec := float64(now-prev.Timestamp) / 1000.0
	if elapsedSec <= 0 {
		return io
	}

	// 计算每秒速率
	io.ReadOpsSec = float64(readOps-prev.ReadOps) / elapsedSec
	io.WriteOpsSec = float64(writeOps-prev.WriteOps) / elapsedSec

	// 扇区通常 512 字节
	readBytesDiff := float64(readSectors-prev.ReadSectors) * 512
	writeBytesDiff := float64(writeSectors-prev.WriteSectors) * 512
	io.ReadMBps = readBytesDiff / elapsedSec / (1024 * 1024)
	io.WriteMBps = writeBytesDiff / elapsedSec / (1024 * 1024)

	// 格式化显示
	io.ReadOpsSecHuman = formatRate(io.ReadOpsSec, "ops/s")
	io.WriteOpsSecHuman = formatRate(io.WriteOpsSec, "ops/s")
	io.ReadMBpsHuman = fmt.Sprintf("%.1f MB/s", io.ReadMBps)
	io.WriteMBpsHuman = fmt.Sprintf("%.1f MB/s", io.WriteMBps)

	// 计算 IO wait 百分比（通过 iostat 风格：如果磁盘忙碌时间占比高）
	// 简化：当读写速率很高时，认为 IO 繁忙
	if io.ReadMBps+io.WriteMBps > 50 {
		io.IOWaitPct = 70 + (io.ReadMBps+io.WriteMBps-50)/5
		if io.IOWaitPct > 99 {
			io.IOWaitPct = 99
		}
	} else if io.ReadMBps+io.WriteMBps > 10 {
		io.IOWaitPct = 30 + (io.ReadMBps+io.WriteMBps-10)*1
	} else {
		io.IOWaitPct = (io.ReadMBps + io.WriteMBps) * 3
	}

	return io
}

// formatRate 格式化速率
func formatRate(rate float64, unit string) string {
	if rate < 1000 {
		return fmt.Sprintf("%.0f %s", rate, unit)
	}
	if rate < 1000000 {
		return fmt.Sprintf("%.1fK %s", rate/1000, unit)
	}
	return fmt.Sprintf("%.1fM %s", rate/1000000, unit)
}

// getDiskUsage 获取单个挂载点的磁盘使用
func getDiskUsage(path string) *DiskInfo {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil
	}
	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	free := stat.Bavail * bsize
	used := total - free
	var pct float64
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}
	return &DiskInfo{
		Size:      total,
		Used:      used,
		Available: free,
		UsagePct:  pct,
	}
}

// getProcessTopList 获取CPU/内存/磁盘IO占用最高的前6个进程
func getProcessTopList() ProcessTopList {
	var result ProcessTopList

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return result
	}

	type procSnapshot struct {
		pid        int
		name       string
		cpuTime    uint64 // utime + stime (clock ticks)
		memRSS     uint64 // pages
		memVMS     uint64 // pages
		readBytes  uint64
		writeBytes uint64
	}

	var procs []procSnapshot

	// 第一次采样
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		// 读取进程名
		name := "-"
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
			name = strings.TrimSpace(string(data))
		}

		// 读取 /proc/[pid]/stat 获取 CPU 时间
		var cpuTime uint64
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
			fields := strings.Fields(string(data))
			// stat 格式: pid (comm) state ... utime(14) stime(15) ...
			// 需要找到 comm 后的字段，comm 可能包含空格和括号
			if len(fields) >= 15 {
				// 找到最后一个 ')' 的位置
				content := string(data)
				idx := strings.LastIndex(content, ")")
				if idx > 0 {
					afterComm := strings.Fields(content[idx+1:])
					if len(afterComm) >= 13 {
						// afterComm[11] = utime, afterComm[12] = stime (0-indexed, 从 state 开始)
						// state 是 afterComm[0], 所以 utime=afterComm[11], stime=afterComm[12]
						utime, _ := strconv.ParseUint(afterComm[11], 10, 64)
						stime, _ := strconv.ParseUint(afterComm[12], 10, 64)
						cpuTime = utime + stime
					}
				}
			}
		}

		// 读取 /proc/[pid]/statm 获取内存
		var memRSS, memVMS uint64
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid)); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) >= 2 {
				memVMS, _ = strconv.ParseUint(fields[0], 10, 64) // size
				memRSS, _ = strconv.ParseUint(fields[1], 10, 64) // resident
			}
		}

		// 读取 /proc/[pid]/io 获取磁盘IO
		var readBytes, writeBytes uint64
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid)); err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "read_bytes:") {
					readBytes, _ = strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "read_bytes:")), 10, 64)
				} else if strings.HasPrefix(line, "write_bytes:") {
					writeBytes, _ = strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "write_bytes:")), 10, 64)
				}
			}
		}

		procs = append(procs, procSnapshot{
			pid:        pid,
			name:       name,
			cpuTime:    cpuTime,
			memRSS:     memRSS,
			memVMS:     memVMS,
			readBytes:  readBytes,
			writeBytes: writeBytes,
		})
	}

	if len(procs) == 0 {
		return result
	}

	// 等待100ms进行第二次采样
	time.Sleep(100 * time.Millisecond)

	// 获取系统总CPU时间用于计算百分比
	var totalCPUStart, totalCPUEnd uint64
	if data, err := os.ReadFile("/proc/stat"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "cpu ") {
				fields := strings.Fields(line)
				for i := 1; i < len(fields); i++ {
					v, _ := strconv.ParseUint(fields[i], 10, 64)
					totalCPUStart += v
				}
				break
			}
		}
	}

	// 第二次采样
	type procResult struct {
		pid      int
		name     string
		cpuPct   float64
		memPct   float64
		memRSS   uint64
		dioBytes uint64
	}

	var results []procResult

	for _, p := range procs {
		var cpuTime2 uint64
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", p.pid)); err == nil {
			content := string(data)
			idx := strings.LastIndex(content, ")")
			if idx > 0 {
				afterComm := strings.Fields(content[idx+1:])
				if len(afterComm) >= 13 {
					utime, _ := strconv.ParseUint(afterComm[11], 10, 64)
					stime, _ := strconv.ParseUint(afterComm[12], 10, 64)
					cpuTime2 = utime + stime
				}
			}
		}

		var memRSS2 uint64
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", p.pid)); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) >= 2 {
				memRSS2, _ = strconv.ParseUint(fields[1], 10, 64)
			}
		}

		var readBytes2, writeBytes2 uint64
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", p.pid)); err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "read_bytes:") {
					readBytes2, _ = strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "read_bytes:")), 10, 64)
				} else if strings.HasPrefix(line, "write_bytes:") {
					writeBytes2, _ = strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "write_bytes:")), 10, 64)
				}
			}
		}

		cpuDiff := cpuTime2 - p.cpuTime
		memDiff := memRSS2 - p.memRSS
		if memDiff < 0 {
			memDiff = 0
		}
		readDiff := int64(readBytes2) - int64(p.readBytes)
		writeDiff := int64(writeBytes2) - int64(p.writeBytes)
		if readDiff < 0 {
			readDiff = 0
		}
		if writeDiff < 0 {
			writeDiff = 0
		}

		// CPU百分比: (cpuDiff / 100ms) * 100 / numCPU = cpuDiff * 1000 / numCPU (因为100ms = 100个tick, 假设100Hz)
		// 实际上 Linux 的 USER_HZ 通常是 100，所以 100ms 内 tick 差值直接对应 CPU 百分比
		cpuPct := float64(cpuDiff) * 10.0 / float64(runtime.NumCPU())
		if cpuPct < 0 {
			cpuPct = 0
		}
		if cpuPct > 100 {
			cpuPct = 100
		}

		// 内存百分比
		memTotal := getMemoryInfo().Total
		memPct := 0.0
		if memTotal > 0 && memRSS2 > 0 {
			memPct = float64(memRSS2*getpagesize()) / float64(memTotal) * 100
		}

		// 磁盘IO (读+写字节数)
		dioBytes := uint64(readDiff + writeDiff)

		results = append(results, procResult{
			pid:      p.pid,
			name:     p.name,
			cpuPct:   cpuPct,
			memPct:   memPct,
			memRSS:   memRSS2 * getpagesize(),
			dioBytes: dioBytes,
		})
	}

	// 再次读取系统总CPU时间
	if data, err := os.ReadFile("/proc/stat"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "cpu ") {
				fields := strings.Fields(line)
				for i := 1; i < len(fields); i++ {
					v, _ := strconv.ParseUint(fields[i], 10, 64)
					totalCPUEnd += v
				}
				break
			}
		}
	}

	// 使用系统总CPU时间来重新校准CPU百分比
	totalCPUDiff := totalCPUEnd - totalCPUStart
	if totalCPUDiff > 0 {
		for i := range results {
			// 重新读取每个进程的CPU时间差
			for _, p := range procs {
				if p.pid == results[i].pid {
					var cpuTime2 uint64
					if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", p.pid)); err == nil {
						content := string(data)
						idx := strings.LastIndex(content, ")")
						if idx > 0 {
							afterComm := strings.Fields(content[idx+1:])
							if len(afterComm) >= 13 {
								utime, _ := strconv.ParseUint(afterComm[11], 10, 64)
								stime, _ := strconv.ParseUint(afterComm[12], 10, 64)
								cpuTime2 = utime + stime
							}
						}
					}
					cpuDiff := cpuTime2 - p.cpuTime
					results[i].cpuPct = float64(cpuDiff) / float64(totalCPUDiff) * 100 * float64(runtime.NumCPU())
					if results[i].cpuPct < 0 {
						results[i].cpuPct = 0
					}
					if results[i].cpuPct > 100 {
						results[i].cpuPct = 100
					}
					break
				}
			}
		}
	}

	// 按CPU排序取前6
	cpuSorted := make([]procResult, len(results))
	copy(cpuSorted, results)
	for i := 0; i < len(cpuSorted); i++ {
		for j := i + 1; j < len(cpuSorted); j++ {
			if cpuSorted[j].cpuPct > cpuSorted[i].cpuPct {
				cpuSorted[i], cpuSorted[j] = cpuSorted[j], cpuSorted[i]
			}
		}
	}
	for i := 0; i < 6 && i < len(cpuSorted); i++ {
		result.CPU = append(result.CPU, ProcessTopInfo{
			PID:        cpuSorted[i].pid,
			Name:       cpuSorted[i].name,
			UsagePct:   cpuSorted[i].cpuPct,
			UsageHuman: fmt.Sprintf("%.1f%%", cpuSorted[i].cpuPct),
		})
	}

	// 按内存排序取前6
	memSorted := make([]procResult, len(results))
	copy(memSorted, results)
	for i := 0; i < len(memSorted); i++ {
		for j := i + 1; j < len(memSorted); j++ {
			if memSorted[j].memRSS > memSorted[i].memRSS {
				memSorted[i], memSorted[j] = memSorted[j], memSorted[i]
			}
		}
	}
	for i := 0; i < 6 && i < len(memSorted); i++ {
		result.Memory = append(result.Memory, ProcessTopInfo{
			PID:        memSorted[i].pid,
			Name:       memSorted[i].name,
			UsagePct:   memSorted[i].memPct,
			UsageHuman: formatBytes(memSorted[i].memRSS),
		})
	}

	// 按磁盘IO排序取前6
	dioSorted := make([]procResult, len(results))
	copy(dioSorted, results)
	for i := 0; i < len(dioSorted); i++ {
		for j := i + 1; j < len(dioSorted); j++ {
			if dioSorted[j].dioBytes > dioSorted[i].dioBytes {
				dioSorted[i], dioSorted[j] = dioSorted[j], dioSorted[i]
			}
		}
	}
	for i := 0; i < 6 && i < len(dioSorted); i++ {
		// dioBytes 是 100ms 采样间隔内的总字节差值，需要乘以 10 得到每秒速率
		// 使用 float64 避免 uint64 乘法溢出
		dioBytesPerSec := float64(dioSorted[i].dioBytes) * 10.0
		result.DiskIO = append(result.DiskIO, ProcessTopInfo{
			PID:        dioSorted[i].pid,
			Name:       dioSorted[i].name,
			UsagePct:   dioBytesPerSec, // 传递原始字节速率供前端进度条归一化
			UsageHuman: formatBytes(uint64(dioBytesPerSec)) + "/s",
		})
	}

	return result
}

// getpagesize 获取内存页大小
func getpagesize() uint64 {
	return uint64(syscall.Getpagesize())
}

// getProcessInfo 获取当前进程信息
func getProcessInfo() ProcessInfo {
	var p ProcessInfo
	p.PID = os.Getpid()

	// 读取 /proc/self/status
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return p
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch key {
		case "Threads":
			p.NumThreads, _ = strconv.Atoi(val)
		case "VmRSS":
			val = strings.TrimSuffix(val, " kB")
			if v, err := strconv.ParseUint(val, 10, 64); err == nil {
				p.RSS = v * 1024
			}
		case "VmSize":
			val = strings.TrimSuffix(val, " kB")
			if v, err := strconv.ParseUint(val, 10, 64); err == nil {
				p.VMS = v * 1024
			}
		}
	}

	// 统计打开的文件描述符数量
	if fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", p.PID)); err == nil {
		p.NumFD = len(fds)
	}

	p.RSSHuman = formatBytes(p.RSS)
	return p
}

// getNetworkInfo 获取网络信息
func getNetworkInfo() NetworkInfo {
	var net NetworkInfo
	// 统计 TCP 连接数
	data, err := os.ReadFile("/proc/net/tcp")
	if err == nil {
		lines := strings.Split(string(data), "\n")
		net.Connections = len(lines) - 1 // 减去标题行
		if net.Connections < 0 {
			net.Connections = 0
		}
	}
	// 读取监听的端口
	data, err = os.ReadFile("/proc/net/tcp")
	if err == nil {
		seen := make(map[int]bool)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if i == 0 {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}
			// 状态 0A = LISTEN
			if fields[3] == "0A" {
				local := fields[1]
				parts := strings.Split(local, ":")
				if len(parts) == 2 {
					if port, err := strconv.ParseInt(parts[1], 16, 64); err == nil && port > 0 {
						if !seen[int(port)] {
							seen[int(port)] = true
							net.ListenPorts = append(net.ListenPorts, int(port))
						}
					}
				}
			}
		}
	}
	return net
}

// formatBytes 格式化字节大小
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
