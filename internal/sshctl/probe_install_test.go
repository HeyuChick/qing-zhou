package sshctl

import (
	"errors"
	"testing"
)

func TestProbeInstallSummaryDoesNotFailOnMissingVersionFlag(t *testing.T) {
	got := probeInstallSummary(
		"flag provided but not defined: -version\nUsage of /usr/local/bin/qingzhou-probe:\n",
		errors.New("exit: Process exited with status 2"),
	)
	if got != "探针安装完成（二进制已启动，版本将在首次上报后显示）" {
		t.Fatalf("old binary summary = %q", got)
	}
}

func TestProbeInstallSummaryIncludesVersion(t *testing.T) {
	got := probeInstallSummary(" v0.2.41\n", nil)
	if got != "探针安装完成：v0.2.41" {
		t.Fatalf("versioned summary = %q", got)
	}
	if got := probeInstallSummary("  ", nil); got != "探针安装完成" {
		t.Fatalf("empty version summary = %q", got)
	}
}
