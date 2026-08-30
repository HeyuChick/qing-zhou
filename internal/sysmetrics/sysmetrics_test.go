package sysmetrics

import "testing"

// The tick counters are cumulative and unsigned. A reversed pair (a counter
// reset, or samples handed over in the wrong order) underflows into a huge
// uint64 and would report a plausible-looking percentage from nonsense.
func TestCalcCPUPercent(t *testing.T) {
	idle := cpuTickSample{idle: 1000}
	// 100 busy ticks out of 400 total.
	busy := cpuTickSample{user: 100, idle: 1300}
	if got := calcCPUPercent(idle, busy); got != 25 {
		t.Fatalf("cpu = %v, want 25", got)
	}
	// No time passed between samples: no basis for a percentage.
	if got := calcCPUPercent(idle, idle); got != 0 {
		t.Fatalf("cpu with no delta = %v, want 0", got)
	}
}

// Interface counters reset when the machine reboots or the NIC is reset. The
// negative delta that follows must read as zero, not as a wildly negative or
// wrapped-around speed on the dashboard.
func TestCalcNetSpeed(t *testing.T) {
	rx, tx := calcNetSpeed(netIfaceSample{rx: 1000, tx: 500}, netIfaceSample{rx: 3000, tx: 1500}, 2)
	if rx != 1000 || tx != 500 {
		t.Fatalf("speed = %d/%d, want 1000/500", rx, tx)
	}
	if rx, tx := calcNetSpeed(netIfaceSample{rx: 5000, tx: 5000}, netIfaceSample{rx: 10, tx: 10}, 2); rx != 0 || tx != 0 {
		t.Fatalf("counter reset = %d/%d, want 0/0", rx, tx)
	}
	// A zero interval would divide by zero.
	if rx, tx := calcNetSpeed(netIfaceSample{}, netIfaceSample{rx: 100}, 0); rx != 0 || tx != 0 {
		t.Fatalf("zero interval = %d/%d, want 0/0", rx, tx)
	}
}

// Disk totals are supposed to match what `df` says about real disks. Counting
// tmpfs or an overlay would inflate both used and total on every container-ish
// host; missing a real filesystem would understate them.
func TestFilesystemAndInterfaceClassification(t *testing.T) {
	for _, fs := range []string{"tmpfs", "overlay", "proc", "cgroup2", "squashfs"} {
		if !isPseudoFS(fs) {
			t.Errorf("%s should be excluded from disk totals", fs)
		}
	}
	for _, fs := range []string{"ext4", "xfs", "btrfs", "zfs"} {
		if isPseudoFS(fs) {
			t.Errorf("%s is a real filesystem and must be counted", fs)
		}
	}
	for _, n := range []string{"eth0", "ens3", "enp0s3", "enx001122aabbcc", "eno1", "wlan0", "em1", "venet0"} {
		if !isPhysicalIface(n) {
			t.Errorf("%s should count as physical", n)
		}
	}
	// Loopback and tunnels carry the proxy's own traffic; counting them would
	// double or triple what the machine actually moved over the wire.
	for _, n := range []string{"lo", "tun0", "docker0", "veth1234", "sing-box"} {
		if isPhysicalIface(n) {
			t.Errorf("%s must not count as physical", n)
		}
	}
}
