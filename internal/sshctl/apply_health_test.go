package sshctl

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestApplyConfigRechecksServiceAfterAnEarlierNoOp guards the health check in
// ApplyConfig. A matching config hash is not durable evidence that the process
// is still running: the node may reboot or sing-box may crash while the panel
// and its in-memory state stay alive.
func TestApplyConfigRechecksServiceAfterAnEarlierNoOp(t *testing.T) {
	configJSON := []byte(`{"inbounds":[]}`)
	remoteHash := configHash(configJSON)

	var mu sync.Mutex
	active := true
	restarts := 0

	host, port := startApplyTestSSHServer(t, func(command string) (string, uint32) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case strings.Contains(command, "sha256sum") && strings.Contains(command, "systemctl is-active"):
			if active {
				return remoteHash + "\nactive\n", 0
			}
			return remoteHash + "\ninactive\n", 3
		case strings.HasPrefix(command, "for c in "):
			return "/usr/local/bin/sing-box\n", 0
		case strings.Contains(command, "systemctl restart"):
			restarts++
			active = true
			return "", 0
		default:
			// Upload, validation and atomic install all succeed in this focused
			// test; only whether ApplyConfig reaches them matters here.
			return "", 0
		}
	})

	m := New(WithTimeout(2 * time.Second))
	cfg := &ServerConfig{
		ID: 1, Host: host, Port: port, SSHUser: "deploy", SSHPassword: "fixture",
		ConfigPath: "/etc/sing-box/config.json", SystemdUnit: "sing-box",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The first pass sees matching bytes and an active unit, so it is a no-op.
	if err := m.ApplyConfig(ctx, cfg, configJSON); err != nil {
		t.Fatalf("initial no-op apply: %v", err)
	}
	mu.Lock()
	if restarts != 0 {
		t.Fatalf("healthy node was restarted %d time(s)", restarts)
	}
	active = false
	mu.Unlock()

	// The same manager and same config must still re-check the node. The removed
	// in-memory fast path returned before dialling here and left the node down.
	if err := m.ApplyConfig(ctx, cfg, configJSON); err != nil {
		t.Fatalf("apply after service stopped: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if restarts != 1 {
		t.Fatalf("stopped node was restarted %d time(s), want 1", restarts)
	}
}

// startApplyTestSSHServer starts the smallest SSH exec server ApplyConfig needs.
// Each command is delegated to handler, which supplies stdout and an exit code.
func startApplyTestSSHServer(t *testing.T, handler func(string) (string, uint32)) (string, int) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	serverConfig.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go serveApplyTestSSHConn(raw, serverConfig, handler)
		}
	}()

	host, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func serveApplyTestSSHConn(raw net.Conn, config *ssh.ServerConfig, handler func(string) (string, uint32)) {
	defer raw.Close()
	_, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	for incoming := range channels {
		if incoming.ChannelType() != "session" {
			_ = incoming.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		channel, channelRequests, err := incoming.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for request := range channelRequests {
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
					_ = request.Reply(false, nil)
					return
				}
				_ = request.Reply(true, nil)
				out, status := handler(payload.Command)
				if out != "" {
					_, _ = channel.Write([]byte(out))
				}
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				return
			}
		}()
	}
}
