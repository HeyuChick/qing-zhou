// Package sysmetrics reads one host's CPU / memory / disk / network / load
// figures out of /proc.
//
// It exists as a package rather than living in the probe because the panel is
// itself one of the machines worth watching: it has no servers row and nothing
// to SSH into, so the probe's route (install a binary, hand it a token, POST
// over HTTP) is a lot of ceremony to describe the machine the code is already
// running on. The panel calls this directly; the probe calls it and reports.
// One implementation, so the two can't drift into disagreeing about what
// "disk used" means.
//
// The pure arithmetic and the classification rules live here, testable on any
// OS. Everything that actually opens /proc is in sysmetrics_linux.go.
package sysmetrics

// Metrics holds one snapshot of system metrics. The JSON tags are the probe's
// wire format and match store.ServerMetrics.
type Metrics struct {
	ProbeVersion string  `json:"probe_version"`
	CPUPercent   float64 `json:"cpu_percent"`
	MemUsed      int64   `json:"mem_used"`
	MemTotal     int64   `json:"mem_total"`
	SwapUsed     int64   `json:"swap_used"`
	SwapTotal    int64   `json:"swap_total"`
	DiskUsed     int64   `json:"disk_used"`
	DiskTotal    int64   `json:"disk_total"`
	NetRx        int64   `json:"net_rx"`
	NetTx        int64   `json:"net_tx"`
	// Net*Total are the kernel's cumulative byte counters. NetRx/NetTx above are
	// rates and cannot be added reliably when reports are delayed or skipped;
	// the totals let the panel calculate honest per-machine usage for any range.
	NetRxTotal     int64   `json:"net_rx_total"`
	NetTxTotal     int64   `json:"net_tx_total"`
	NetTotalsValid bool    `json:"net_totals_valid"`
	Load1          float64 `json:"load1"`
	Load5          float64 `json:"load5"`
	Load15         float64 `json:"load15"`
	TCPConnections int     `json:"tcp_connections"`
	ProcessCount   int     `json:"process_count"`
	Uptime         int64   `json:"uptime"`
	Hostname       string  `json:"hostname"`
	Platform       string  `json:"platform"`
	Kernel         string  `json:"kernel"`
	Arch           string  `json:"arch"`
}

// cpuTickSample is the aggregate CPU tick counts from the "cpu" line of
// /proc/stat.
type cpuTickSample struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
}

func (s cpuTickSample) total() uint64 {
	return s.user + s.nice + s.system + s.idle + s.iowait + s.irq + s.softirq + s.steal
}

func (s cpuTickSample) busy() uint64 {
	return s.total() - s.idle - s.iowait
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

// netIfaceSample holds rx/tx byte counters summed over physical interfaces.
type netIfaceSample struct {
	rx, tx int64
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

// isPseudoFS reports whether the filesystem type is virtual/pseudo and should
// be excluded from disk-usage aggregation.
func isPseudoFS(fstype string) bool {
	switch fstype {
	case "proc", "sysfs", "tmpfs", "devtmpfs", "devpts", "cgroup", "cgroup2",
		"pstore", "bpf", "mqueue", "hugetlbfs", "fuse", "fusectl", "fuse.gvfsd-fuse",
		"autofs", "rpc_pipefs", "securityfs", "debugfs", "tracefs", "configfs",
		"selinuxfs", "binfmt_misc", "efivarfs", "none", "overlay", "squashfs",
		"iso9660", "nsfs", "anon_inode":
		return true
	}
	return false
}

// isPhysicalIface reports whether an interface name looks like real hardware,
// so loopback and tunnel devices don't inflate the traffic figures.
func isPhysicalIface(name string) bool {
	// enx is Linux's MAC-based Ethernet name; venet is the provider-facing
	// interface on older OpenVZ VPSes. They carry billable traffic just like
	// eth/ens and must not silently report zero merely because of their name.
	prefixes := []string{"eth", "ens", "enp", "enx", "wlan", "em", "eno", "venet"}
	for _, p := range prefixes {
		if len(name) >= len(p) && name[:len(p)] == p {
			return true
		}
	}
	return false
}
