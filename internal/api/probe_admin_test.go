package api

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestNormalizeProbeArch(t *testing.T) {
	tests := map[string]string{
		"x86_64":  "linux-amd64",
		"amd64":   "linux-amd64",
		"aarch64": "linux-arm64",
		"arm64":   "linux-arm64",
	}
	for input, want := range tests {
		got, err := normalizeProbeArch("  " + input + "\n")
		if err != nil || got != want {
			t.Fatalf("normalizeProbeArch(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := normalizeProbeArch("riscv64"); !errors.Is(err, errUnsupportedProbeArch) {
		t.Fatalf("unsupported architecture error = %v", err)
	}
}

func TestProbeBinaryPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QZ_PROBE_DIR", dir)
	got, err := probeBinaryPath("linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "probe-linux-amd64"); got != want {
		t.Fatalf("probeBinaryPath = %q; want %q", got, want)
	}
	if _, err := probeBinaryPath("windows-amd64"); !errors.Is(err, errUnsupportedProbeArch) {
		t.Fatalf("unsupported path error = %v", err)
	}
}
