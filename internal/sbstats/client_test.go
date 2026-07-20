package sbstats

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// trackedConn records whether it was actually closed, which is the only thing
// that releases the SSH client underneath a tunnelled connection in production.
type trackedConn struct {
	net.Conn
	mu     sync.Mutex
	closed bool
}

func (c *trackedConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.Conn.Close()
}

func (c *trackedConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// A stats poll dials a fresh connection every time, and when the dialer is
// sshctl's tunnel that connection owns a whole *ssh.Client — closing it is what
// ends the sshd session on the remote node. The transport pools connections and
// never closes them on its own, so a Client that is dropped without Close leaks
// one sshd process per node per minute; a 1-core VPS ran out of memory in hours
// this way. The poll failing changes nothing: the connection was still dialled,
// and a request that died mid-flight leaves it marked busy, where
// CloseIdleConnections alone will not touch it.
func TestClose_ClosesDialedConns_EvenWhenPollFails(t *testing.T) {
	// Accept connections but never speak HTTP/2, so the request hangs until the
	// context expires — the mid-flight failure the force path exists for.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var (
		acceptedMu sync.Mutex
		accepted   []net.Conn
	)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			acceptedMu.Lock()
			accepted = append(accepted, conn) // hold it open, stay silent
			acceptedMu.Unlock()
		}
	}()
	defer func() {
		acceptedMu.Lock()
		for _, c := range accepted {
			c.Close()
		}
		acceptedMu.Unlock()
	}()

	var (
		dialedMu sync.Mutex
		dialed   []*trackedConn
	)
	client := NewWithDialer(ln.Addr().String(), func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tc := &trackedConn{Conn: raw}
		dialedMu.Lock()
		dialed = append(dialed, tc)
		dialedMu.Unlock()
		return tc, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := client.QueryUserTraffic(ctx); err == nil {
		t.Fatal("expected the poll to fail against a silent listener")
	}

	dialedMu.Lock()
	n := len(dialed)
	dialedMu.Unlock()
	if n == 0 {
		t.Fatal("dialer was never called; test proves nothing about Close")
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	dialedMu.Lock()
	defer dialedMu.Unlock()
	for i, c := range dialed {
		if !c.isClosed() {
			t.Errorf("conn %d still open after Close: leaks an sshd session on the node", i)
		}
	}
}
