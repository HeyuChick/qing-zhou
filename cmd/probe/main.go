//go:build linux

// qingzhou-probe is a lightweight monitoring agent for the 轻舟 panel.
// It collects CPU, memory, disk, network, load, and process metrics from
// /proc and reports them via HTTP POST to the panel's /api/monitor/report.
//
// Usage:
//
//	qingzhou-probe -server https://panel.example.com -token <probe_token> [-interval 60] [-insecure]
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

	"qingzhou/internal/intervalcfg"
	"qingzhou/internal/sysmetrics"
)

var (
	flagServer   = flag.String("server", "", "Panel URL (required)")
	flagToken    = flag.String("token", "", "Probe authentication token (required)")
	flagInterval = flag.Int("interval", 60, "Initial collection interval in seconds (panel may update it live)")
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
		fmt.Fprintf(os.Stderr, "Usage: qingzhou-probe -server <url> -token <token> [-interval 60] [-insecure]\n")
		fmt.Fprintf(os.Stderr, "  Or set QZ_PROBE_SERVER and QZ_PROBE_TOKEN environment variables.\n")
		os.Exit(1)
	}

	interval := *flagInterval
	if interval < 5 {
		interval = 5
	}
	if interval > int(intervalcfg.MaxProbeSeconds) {
		interval = int(intervalcfg.MaxProbeSeconds)
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

	// CPU percentage and network speed are deltas between two reads, so the
	// first sample carries neither. Prime the sampler and throw that one away
	// rather than reporting a snapshot that claims the machine is idle.
	sampler := &sysmetrics.Sampler{}
	sampler.Sample()

	timer := time.NewTimer(time.Duration(interval) * time.Second)
	defer timer.Stop()
	for range timer.C {
		next, err := report(client, reportURL, token, sampler.Sample())
		if err != nil {
			log.Printf("report failed: %v", err)
		} else if next >= int(intervalcfg.MinProbeSeconds) && next <= int(intervalcfg.MaxProbeSeconds) && next != interval {
			log.Printf("collection interval updated by panel: %ds -> %ds", interval, next)
			interval = next
		}
		timer.Reset(time.Duration(interval) * time.Second)
	}
}

// report returns a panel-selected interval in seconds. A zero value means the
// server is an older version (or intentionally omitted the field), so the probe
// keeps its current interval. The unified API envelope is decoded explicitly;
// treating the top-level object as the payload would silently ignore updates.
func report(client *http.Client, url, token string, m sysmetrics.Metrics) (int, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return 0, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("server returned %d", resp.StatusCode)
	}
	if readErr != nil {
		return 0, fmt.Errorf("read response: %w", readErr)
	}
	var envelope struct {
		Data struct {
			ProbeIntervalSeconds int `json:"probe_interval_seconds"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	return envelope.Data.ProbeIntervalSeconds, nil
}
