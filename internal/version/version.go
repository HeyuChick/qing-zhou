// Package version holds the build-time version of the qingzhou binary and a
// small semver comparison used by the in-panel updater to decide whether a
// newer GitHub release is available.
//
// Version is injected at build time via ldflags, e.g.:
//
//	go build -ldflags "-X qingzhou/internal/version.Version=v0.2.1" .
//
// When built without that flag (plain `go run .` / `go build .`), it stays
// "dev", which the updater treats as "unknown / always offer the latest".
package version

import (
	"strconv"
	"strings"
)

// Version is the current build's version. Overridden at build time via ldflags.
var Version = "dev"

// Current returns the running binary's version string.
func Current() string { return Version }

// IsDev reports whether this is an untagged development build (no ldflags
// injection). Such a build has no reliable version to compare against.
func IsDev() bool { return Version == "" || Version == "dev" }

// Compare compares two version strings a and b of the form "v1.2.3" (the
// leading "v" and any pre-release suffix after "-" or "+" are ignored). It
// returns -1 if a < b, 0 if equal, and +1 if a > b. Missing numeric segments
// are treated as 0, so "v1.2" == "v1.2.0".
func Compare(a, b string) int {
	pa, pb := parse(a), parse(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// parse turns "v1.2.3-rc1" into [1 2 3], ignoring the "v" prefix and any
// pre-release/build metadata after the first '-' or '+'.
func parse(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}
