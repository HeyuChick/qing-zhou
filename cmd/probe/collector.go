//go:build linux

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Metrics holds one snapshot of system metrics.
type Metrics struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemUsed       int64   `json:"mem_used"`
	MemTotal      int64   `json:"mem_total"`
	SwapUsed      int64   `json:"swap_used"`
	SwapTotal     int64   `json:"swap_total"`
	DiskUsed      int64   `json:"disk_used"`
	DiskTotal     int64   `json:"disk_total"`
	NetRx         int64   `json:"net_rx"`
	NetTx         int64   `json:"net_tx"`
	Load1         float64 `json:"load1"`
	Load5         float64 `json:"load5"`
	Load15        float64 `json:"load15"`
	TCPConnections int    `json:"tcp_connections"`
	ProcessCount  int     `json:"process_count"`
	Uptime        int64   `json:"uptime"`
	Hostname      string  `json:"hostname"`
	Platform      string  `json:"platform"`
	Kernel        string  `json:"kernel"`
	Arch          string  `json:"arch"`
}

// cpuTickSample reads /proc/stat and returns the aggregate CPU tick counts.
type cpuTickSample struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (s cpuTickSample) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

func (s cpuTickSample) busy() uint64 {
	return s.total() - s.idle - s.iowait
}

func readCPUTicks() (cpuTickSample, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTickSample{}, err
	}
	// First line: "cpu  user nice system idle iowait irq softirq steal ..."
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		var s cpuTickSample
		s.user, _ = strconv.ParseUint(fields[1], 10, 64)
		s.nice, _ = strconv.ParseUint(fields[2], 10, 64)
		s.system, _ = strconv.ParseUint(fields[3], 10, 64)
		s.idle, _ = strconv.ParseUint(fields[4], 10, 64)
		s.iowait, _ = strconv.ParseUint(fields[5], 10, 64)
		s.irq, _ = strconv.ParseUint(fields[6], 10, 64)
		s.softirq, _ = strconv.ParseUint(fields[7], 10, 64)
		s.steal, _ = strconv.ParseUint(fields[8], 10, 64)
		return s, nil
	}
	return cpuTickSample{}, fmt.Errorf("no cpu line in /proc/stat")
}

// calcCPUPercent computes CPU usage % between two samples.
func calcCPUPercent(prev, cur cpuTickSample) float64 {
	totalDelta := cur.total() - prev.total()
	if totalDelta == 0 {
		return 0
	}
	busyDelta := cur.busy() - prev.busy()
	return float64(busyDelta) / float64(totalDelta) * 100
}

// readMemInfo reads /proc/meminfo for memory and swap.
func readMemInfo() (memUsed, memTotal, swapUsed, swapTotal int64, err error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	vals := make(map[string]int64)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		valStr = strings.TrimSpace(valStr)
		v, e := strconv.ParseInt(valStr, 10, 64)
		if e == nil {
			vals[key] = v * 1024 // kB -> bytes
		}
	}
	memTotal = vals["MemTotal"]
	memAvailable := vals["MemAvailable"]
	memUsed = memTotal - memAvailable
	if memUsed < 0 {
		memUsed = 0
	}
	swapTotal = vals["SwapTotal"]
	swapFree := vals["SwapFree"]
	swapUsed = swapTotal - swapFree
	if swapUsed < 0 {
		swapUsed = 0
	}
	return
}

// readDiskUsage uses statfs on "/" to get disk usage.
func readDiskUsage() (used, total int64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs("/", &stat); err != nil {
		return
	}
	total = int64(stat.Blocks) * int64(stat.Bsize)
	free := int64(stat.Bavail) * int64(stat.Bsize)
	used = total - free
	return
}

// netIfaceSample holds rx/tx byte counters for physical interfaces.
type netIfaceSample struct {
	rx, tx int64
}

// readNetDev reads /proc/net/dev and sums rx/tx for physical interfaces.
// Physical interfaces are those matching eth*, ens*, enp*, wlan*, em*.
func readNetDev() (netIfaceSample, error) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return netIfaceSample{}, err
	}
	var sample netIfaceSample
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 { // skip header lines
			continue
		}
		line := scanner.Text()
		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:colonIdx])
		if !isPhysicalIface(iface) {
			continue
		}
		fields := strings.Fields(line[colonIdx+1:])
		if len(fields) < 10 {
			continue
		}
		rx, _ := strconv.ParseInt(fields[0], 10, 64)
		tx, _ := strconv.ParseInt(fields[8], 10, 64)
		sample.rx += rx
		sample.tx += tx
	}
	return sample, nil
}

func isPhysicalIface(name string) bool {
	prefixes := []string{"eth", "ens", "enp", "wlan", "em", "eno"}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// calcNetSpeed computes bytes/s between two samples given the interval.
func calcNetSpeed(prev, cur netIfaceSample, intervalSec float64) (rxSpeed, txSpeed int64) {
	if intervalSec <= 0 {
		return 0, 0
	}
	rxDelta := cur.rx - prev.rx
	txDelta := cur.tx - prev.tx
	if rxDelta < 0 {
		rxDelta = 0
	}
	if txDelta < 0 {
		txDelta = 0
	}
	return int64(float64(rxDelta) / intervalSec), int64(float64(txDelta) / intervalSec)
}

// readLoadAvg reads /proc/loadavg for 1/5/15 minute load averages.
func readLoadAvg() (l1, l5, l15 float64, err error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected /proc/loadavg format")
	}
	l1, _ = strconv.ParseFloat(fields[0], 64)
	l5, _ = strconv.ParseFloat(fields[1], 64)
	l15, _ = strconv.ParseFloat(fields[2], 64)
	return
}

// readTCPConnections counts established TCP connections from /proc/net/tcp.
func readTCPConnections() int {
	count := 0
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if lineNum <= 1 { // skip header
				continue
			}
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 4 && fields[3] == "01" { // 01 = ESTABLISHED
				count++
			}
		}
	}
	return count
}

// readProcessCount counts numeric directories in /proc (one per process).
func readProcessCount() int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			// Check if name is numeric (PID).
			if _, err := strconv.Atoi(e.Name()); err == nil {
				count++
			}
		}
	}
	return count
}

// readUptime reads /proc/uptime for system uptime in seconds.
func readUptime() (int64, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0, fmt.Errorf("empty /proc/uptime")
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	return int64(f), nil
}

// readSysInfo reads system information (hostname, platform, kernel, arch).
func readSysInfo() (hostname, platform, kernel, arch string) {
	hostname, _ = os.Hostname()
	arch = runtime.GOARCH

	// Kernel version from /proc/version or uname
	if data, err := os.ReadFile("/proc/version"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			kernel = fields[2]
		}
	}

	// Platform from /etc/os-release
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				v := strings.TrimPrefix(line, "PRETTY_NAME=")
				v = strings.Trim(v, "\"")
				platform = v
				break
			}
		}
	}
	return
}

// Collect gathers all system metrics. prevCPU and prevNet are the previous
// samples (nil on first call); intervalSec is the time between samples.
// Returns the metrics and the raw samples for the next call.
func Collect(prevCPU *cpuTickSample, prevNet *netIfaceSample, intervalSec float64) (Metrics, cpuTickSample, netIfaceSample) {
	var m Metrics

	// CPU
	curCPU, err := readCPUTicks()
	if err == nil && prevCPU != nil {
		m.CPUPercent = calcCPUPercent(*prevCPU, curCPU)
	}

	// Memory
	m.MemUsed, m.MemTotal, m.SwapUsed, m.SwapTotal, _ = readMemInfo()

	// Disk
	m.DiskUsed, m.DiskTotal, _ = readDiskUsage()

	// Network
	curNet, err := readNetDev()
	if err == nil && prevNet != nil {
		m.NetRx, m.NetTx = calcNetSpeed(*prevNet, curNet, intervalSec)
	}

	// Load
	m.Load1, m.Load5, m.Load15, _ = readLoadAvg()

	// TCP connections
	m.TCPConnections = readTCPConnections()

	// Process count
	m.ProcessCount = readProcessCount()

	// Uptime
	m.Uptime, _ = readUptime()

	// System info
	m.Hostname, m.Platform, m.Kernel, m.Arch = readSysInfo()

	return m, curCPU, curNet
}
