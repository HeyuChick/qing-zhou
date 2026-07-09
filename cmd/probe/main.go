//go:build linux

// qingzhou-probe is a lightweight monitoring agent for the 轻舟 panel.
// It collects CPU, memory, disk, network, load, and process metrics from
// /proc and reports them via HTTP POST to the panel's /api/monitor/report.
//
// Usage:
//
//	qingzhou-probe -server https://panel.example.com -token <probe_token> [-interval 30] [-insecure]
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

var (
	flagServer   = flag.String("server", "", "Panel URL (required)")
	flagToken    = flag.String("token", "", "Probe authentication token (required)")
	flagInterval = flag.Int("interval", 30, "Collection interval in seconds")
	flagInsecure = flag.Bool("insecure", false, "Skip TLS certificate verification")
)

func main() {
	flag.Parse()

	// Also accept env vars as fallback (useful for systemd EnvironmentFile).
	server := *flagServer
	if server == "" {
		server = os.Getenv("QZ_PROBE_SERVER")
	}
	token := *flagToken
	if token == "" {
		token = os.Getenv("QZ_PROBE_TOKEN")
	}
	if server == "" || token == "" {
		fmt.Fprintf(os.Stderr, "Usage: qingzhou-probe -server <url> -token <token> [-interval 30] [-insecure]\n")
		fmt.Fprintf(os.Stderr, "  Or set QZ_PROBE_SERVER and QZ_PROBE_TOKEN environment variables.\n")
		os.Exit(1)
	}

	interval := *flagInterval
	if interval < 5 {
		interval = 5
	}

	// A token passed on the command line is visible to any local user via
	// ps / /proc/<pid>/cmdline. The installer uses QZ_PROBE_TOKEN (systemd
	// EnvironmentFile, mode 600) instead — prefer that.
	if *flagToken != "" {
		log.Printf("WARNING: -token on the command line is visible via ps/proc; prefer the QZ_PROBE_TOKEN env var")
	}

	// Build HTTP client.
	client := &http.Client{
		Timeout: 15 * time.Second,
	}
	if *flagInsecure {
		log.Printf("WARNING: -insecure disables TLS certificate verification; the probe token and metrics are exposed to a man-in-the-middle. Use only for local testing.")
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	reportURL := server + "/api/monitor/report"
	log.Printf("qingzhou-probe starting: server=%s interval=%ds", server, interval)

	// We need two samples 1 second apart for CPU and network speed deltas.
	// On the first iteration, we collect a "previous" sample and skip reporting.
	var prevCPU *cpuTickSample
	var prevNet *netIfaceSample
	var prevTime time.Time

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	// Collect initial sample immediately.
	cpu0, _ := readCPUTicks()
	net0, _ := readNetDev()
	prevCPU = &cpu0
	prevNet = &net0
	prevTime = time.Now()

	// Wait 1 second for the first delta, then start the loop.
	time.Sleep(time.Second)

	for range ticker.C {
		now := time.Now()
		elapsed := now.Sub(prevTime).Seconds()

		metrics, cpu, net := Collect(prevCPU, prevNet, elapsed)
		prevCPU = &cpu
		prevNet = &net
		prevTime = now

		if err := report(client, reportURL, token, metrics); err != nil {
			log.Printf("report failed: %v", err)
		}
	}
}

func report(client *http.Client, url, token string, m Metrics) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) // drain

	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}
