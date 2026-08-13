//go:build !linux

package sysmetrics

// The readings all come from /proc, which only Linux has. The panel itself is
// cross-platform (it is developed on Windows and macOS), so rather than fail to
// build there, this reports that there is nothing to collect and the caller
// skips starting a collector at all.

// Supported reports whether this build can read host metrics at all.
func Supported() bool { return false }

// Sampler is the no-op counterpart of the Linux sampler.
type Sampler struct{}

// Sample returns zero metrics; callers must check Supported first.
func (s *Sampler) Sample() Metrics { return Metrics{} }
