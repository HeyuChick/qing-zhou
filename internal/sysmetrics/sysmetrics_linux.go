//go:build linux

package sysmetrics

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Supported reports whether this build can read host metrics at all.
func Supported() bool { return true }

// Sampler turns the counters in /proc into rates. CPU percentage and network
// speed are deltas between two reads, so the first Sample of a process reports
// zero for both and only later ones carry real numbers — keep one Sampler for
// the lifetime of the collector rather than making a fresh one per tick.
//
// Not safe for concurrent use; one goroutine per Sampler.
type Sampler struct {
	prevCPU *cpuTickSample
	prevNet *netIfaceSample
	prevAt  time.Time
}

// Sample reads one snapshot. Individual readings that fail are left at zero
// rather than failing the whole snapshot: a machine with no swap, no
// /etc/os-release, or an unreadable mount is still worth reporting.
func (s *Sampler) Sample() Metrics {
	var m Metrics
	now := time.Now()

	curCPU, err := readCPUTicks()
	if err == nil {
		if s.prevCPU != nil {
			m.CPUPercent = calcCPUPercent(*s.prevCPU, curCPU)
		}
		s.prevCPU = &curCPU
	}

	m.MemUsed, m.MemTotal, m.SwapUsed, m.SwapTotal, _ = readMemInfo()
	m.DiskUsed, m.DiskTotal, _ = readDiskUsage()

	curNet, err := readNetDev()
	if err == nil {
		m.NetRxTotal = curNet.rx
		m.NetTxTotal = curNet.tx
		m.NetTotalsValid = true
		if s.prevNet != nil && !s.prevAt.IsZero() {
			m.NetRx, m.NetTx = calcNetSpeed(*s.prevNet, curNet, now.Sub(s.prevAt).Seconds())
		}
		s.prevNet = &curNet
	}
	s.prevAt = now

	m.Load1, m.Load5, m.Load15, _ = readLoadAvg()
	m.TCPConnections = readTCPConnections()
	m.ProcessCount = readProcessCount()
	m.Uptime, _ = readUptime()
	m.Hostname, m.Platform, m.Kernel, m.Arch = readSysInfo()
	return m
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

func bsize(stat syscall.Statfs_t) int64 {
	if stat.Frsize > 0 {
		return stat.Frsize
	}
	return stat.Bsize
}

// readDiskUsage sums disk usage across all real mounted filesystems.
// Pseudo filesystems (proc, sysfs, tmpfs, devtmpfs, overlay, squashfs, etc.)
// are excluded so the reported numbers match `df` total for real disks.
func readDiskUsage() (used, total int64, err error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		// Fallback: root partition only
		var stat syscall.Statfs_t
		if e := syscall.Statfs("/", &stat); e != nil {
			return 0, 0, e
		}
		bs := bsize(stat)
		total = int64(stat.Blocks) * bs
		used = int64(stat.Blocks-stat.Bfree) * bs
		return used, total, nil
	}

	seen := make(map[string]bool) // dedup by device to avoid double-counting bind mounts
	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		dev := fields[0]
		mount := fields[1]
		fstype := fields[2]

		// Skip pseudo / virtual filesystems and loop/squashfs images.
		if isPseudoFS(fstype) || strings.HasPrefix(dev, "/dev/loop") {
			continue
		}
		// Only consider block devices (skip tmpfs, cgroup, etc. even if not caught above).
		if !strings.HasPrefix(dev, "/dev/") {
			continue
		}
		// Dedup by device: a bind mount shares the same underlying device,
		// so counting it again would double-count the disk.
		if seen[dev] {
			continue
		}
		seen[dev] = true

		var stat syscall.Statfs_t
		if e := syscall.Statfs(mount, &stat); e != nil {
			continue
		}
		bs := bsize(stat)
		t := int64(stat.Blocks) * bs
		u := int64(stat.Blocks-stat.Bfree) * bs
		total += t
		used += u
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	return used, total, nil
}

// readNetDev reads /proc/net/dev and sums rx/tx for physical interfaces.
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
