package store

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// A test-local TLS terminator forwards WS to the generated entry core over a
// separately verified TLS connection. No production listener, system proxy or
// network setting is used. Track the raw accepted sockets because net/http no
// longer owns WS connections after ReverseProxy hijacks them.
type relayWSReverseProxy struct {
	server    *httptest.Server
	transport *http.Transport
	port      int
	mu        sync.Mutex
	conns     map[*relayWSTrackedConn]bool
	upgrades  atomic.Int64
	earlyData atomic.Int64
}

type relayWSProxyListener struct {
	net.Listener
	proxy *relayWSReverseProxy
}

func (l relayWSProxyListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tracked := &relayWSTrackedConn{Conn: conn, proxy: l.proxy}
	l.proxy.mu.Lock()
	l.proxy.conns[tracked] = true
	l.proxy.mu.Unlock()
	return tracked, nil
}

type relayWSTrackedConn struct {
	net.Conn
	proxy *relayWSReverseProxy
	once  sync.Once
	err   error
}

func (c *relayWSTrackedConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.proxy.mu.Lock()
		delete(c.proxy.conns, c)
		c.proxy.mu.Unlock()
	})
	return c.err
}

func (p *relayWSReverseProxy) interruptConnections() int {
	p.mu.Lock()
	conns := make([]*relayWSTrackedConn, 0, len(p.conns))
	for conn := range p.conns {
		conns = append(conns, conn)
	}
	p.mu.Unlock()
	for _, conn := range conns {
		conn.Close()
	}
	return len(conns)
}

func newRelayWSReverseProxy(t *testing.T, entryPort int, serverTLS string) *relayWSReverseProxy {
	t.Helper()
	var material struct {
		Certificate []string `json:"certificate"`
		Key         []string `json:"key"`
	}
	if err := json.Unmarshal([]byte(serverTLS), &material); err != nil {
		t.Fatal(err)
	}
	certPEM := []byte(strings.Join(material.Certificate, "\n"))
	certificate, err := tls.X509KeyPair(certPEM, []byte(strings.Join(material.Key, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("WS proxy has no synthetic backend trust anchor")
	}
	backend, err := url.Parse(fmt.Sprintf("https://127.0.0.1:%d", entryPort))
	if err != nil {
		t.Fatal(err)
	}
	p := &relayWSReverseProxy{conns: map[*relayWSTrackedConn]bool{}}
	p.transport = &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}},
		TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
		DisableKeepAlives: true, ResponseHeaderTimeout: 5 * time.Second,
	}
	proxy := httputil.NewSingleHostReverseProxy(backend)
	proxy.Transport = p.transport
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			p.upgrades.Add(1)
			if r.Header.Get("Sec-WebSocket-Protocol") != "" {
				// Never log this header: early data includes authentication bytes.
				p.earlyData.Add(1)
			}
		}
		director(r)
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		t.Logf("synthetic WS reverse proxy request failed: %v", err)
		http.Error(w, "synthetic WS backend unavailable", http.StatusBadGateway)
	}
	p.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.Host != "localhost" || r.URL.Path != "/fixture" {
			http.Error(w, "unexpected synthetic WS proxy request", http.StatusBadRequest)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	p.server.Listener = relayWSProxyListener{Listener: p.server.Listener, proxy: p}
	p.server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	p.server.StartTLS()
	p.port = p.server.Listener.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() {
		p.interruptConnections()
		p.server.Close()
		p.transport.CloseIdleConnections()
	})
	t.Logf("synthetic WS reverse proxy frontend=%s verified_TLS_backend=%s", p.server.URL, backend)
	return p
}

type relayWSStabilityClient struct {
	owner  int64
	client *http.Client
}

type relayWSStream struct {
	id           string
	owner        int64
	download     int
	upload       int
	chunks       int
	delay        time.Duration
	pattern      byte
	seen         atomic.Int64
	started      chan struct{}
	serverDone   chan struct{}
	firstPayload chan struct{}
	startedOnce  sync.Once
	serverOnce   sync.Once
}

type relayWSStreamResult struct {
	stream  *relayWSStream
	bytes   int
	elapsed time.Duration
	err     error
}

func runRelayWSStabilityTraffic(t *testing.T, clients []relayWSStabilityClient, proxy *relayWSReverseProxy, recordPhase func(string, map[int64]UsageDelta)) {
	t.Helper()
	if len(clients) != 2 || proxy == nil {
		t.Fatal("WS stability requires the shared two-user TLS reverse-proxy fixture")
	}
	var mu sync.Mutex
	streams := map[string]*relayWSStream{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		stream := streams[r.URL.Query().Get("id")]
		mu.Unlock()
		if stream == nil || r.Method != http.MethodPost {
			http.Error(w, "unknown synthetic stream", http.StatusBadRequest)
			return
		}
		if stream.seen.Add(1) != 1 {
			http.Error(w, "non-replayable request was repeated", http.StatusConflict)
			return
		}
		defer stream.serverOnce.Do(func() { close(stream.serverDone) })
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, bytes.Repeat([]byte{stream.pattern}, stream.upload)) {
			http.Error(w, "stream upload mismatch", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(stream.download))
		w.Header().Set("Content-Type", "application/octet-stream")
		controller := http.NewResponseController(w)
		chunk := bytes.Repeat([]byte{stream.pattern}, stream.download/stream.chunks)
		for i := 0; i < stream.chunks; i++ {
			if _, err = w.Write(chunk); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
			stream.startedOnce.Do(func() { close(stream.started) })
			if i+1 < stream.chunks {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(stream.delay):
				}
			}
		}
	}))
	t.Cleanup(target.Close)
	makeStream := func(client int, phase string, chunks int, delay time.Duration) *relayWSStream {
		stream := &relayWSStream{
			id: fmt.Sprintf("%s-owner-%d", phase, clients[client].owner), owner: clients[client].owner,
			download: (client + 1) << 20, upload: (client + 1) * 4096, chunks: chunks, delay: delay,
			pattern: byte('a' + client), started: make(chan struct{}), serverDone: make(chan struct{}), firstPayload: make(chan struct{}),
		}
		mu.Lock()
		streams[stream.id] = stream
		mu.Unlock()
		return stream
	}
	request := func(client relayWSStabilityClient, stream *relayWSStream) relayWSStreamResult {
		started := time.Now()
		result := relayWSStreamResult{stream: stream}
		// Wrapping the body and explicitly leaving GetBody nil makes this POST
		// non-replayable. Recovery always creates a new stream/request ID.
		body := io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{stream.pattern}, stream.upload)))
		req, err := http.NewRequest(http.MethodPost, target.URL+"/?id="+url.QueryEscape(stream.id), body)
		if err != nil {
			result.err = err
			return result
		}
		req.ContentLength, req.GetBody = int64(stream.upload), nil
		bounded := *client.client
		bounded.Timeout = 15 * time.Second // ~3.2-second paced streams, still bounded
		response, err := bounded.Do(req)
		if err != nil {
			result.err, result.elapsed = err, time.Since(started)
			return result
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			result.err = fmt.Errorf("response status %s", response.Status)
			return result
		}
		var once sync.Once
		buffer := make([]byte, 32768)
		for {
			n, err := response.Body.Read(buffer)
			if n > 0 {
				if !bytes.Equal(buffer[:n], bytes.Repeat([]byte{stream.pattern}, n)) {
					result.err = fmt.Errorf("response payload mismatch")
					break
				}
				result.bytes += n
				if result.bytes >= 32768 {
					once.Do(func() { close(stream.firstPayload) })
				}
			}
			if err != nil {
				if err != io.EOF {
					result.err = err
				}
				break
			}
		}
		result.elapsed = time.Since(started)
		if result.err == nil && result.bytes != stream.download {
			result.err = fmt.Errorf("response bytes=%d want=%d", result.bytes, stream.download)
		}
		return result
	}
	waitSignal := func(label string, signal <-chan struct{}) {
		t.Helper()
		select {
		case <-signal:
		case <-time.After(5 * time.Second):
			t.Fatalf("WS stability %s did not occur before deadline", label)
		}
	}
	checkSuccess := func(result relayWSStreamResult, long bool) {
		t.Helper()
		if result.err != nil || result.bytes != result.stream.download || result.stream.seen.Load() != 1 {
			t.Fatalf("WS stability id=%s bytes=%d want=%d arrivals=%d error=%v", result.stream.id, result.bytes, result.stream.download, result.stream.seen.Load(), result.err)
		}
		if long && result.elapsed < 3*time.Second {
			t.Fatalf("WS stream %s did not remain active for the paced long-flow interval: %s", result.stream.id, result.elapsed)
		}
		waitSignal(result.stream.id+" destination completion", result.stream.serverDone)
		t.Logf("WS stability id=%s owner=%d complete_up=%d complete_down=%d elapsed=%s arrivals=1", result.stream.id, result.stream.owner, result.stream.upload, result.bytes, result.elapsed)
	}
	for i, client := range clients {
		stream := makeStream(i, "isolated-long", 64, 50*time.Millisecond)
		checkSuccess(request(client, stream), true)
		recordPhase(stream.id, map[int64]UsageDelta{client.owner: {Up: int64(stream.upload), Down: int64(stream.download)}})
	}
	results := make(chan relayWSStreamResult, 4)
	completed := map[int64]UsageDelta{}
	for round := 0; round < 2; round++ {
		for i, client := range clients {
			stream := makeStream(i, fmt.Sprintf("concurrent-long-%d", round), 64, 50*time.Millisecond)
			go func() { results <- request(client, stream) }()
		}
	}
	for i := 0; i < 4; i++ {
		result := <-results
		checkSuccess(result, true)
		value := completed[result.stream.owner]
		value.Up += int64(result.stream.upload)
		value.Down += int64(result.stream.download)
		completed[result.stream.owner] = value
	}
	recordPhase("concurrent-long", completed)
	faults := make([]*relayWSStream, len(clients))
	for i, client := range clients {
		stream := makeStream(i, "deliberate-interruption", 64, 50*time.Millisecond)
		faults[i] = stream
		go func() { results <- request(client, stream) }()
	}
	for _, stream := range faults {
		waitSignal(stream.id+" destination accepted body and flushed response", stream.started)
		waitSignal(stream.id+" client received payload", stream.firstPayload)
	}
	closed := proxy.interruptConnections()
	if closed < 2 {
		t.Fatalf("expected two active user WS connections to interrupt, got %d", closed)
	}
	failedCompleted := map[int64]UsageDelta{}
	for range faults {
		var result relayWSStreamResult
		select {
		case result = <-results:
		case <-time.After(5 * time.Second):
			t.Fatal("interrupted WS request did not fail before deadline")
		}
		if result.err == nil || result.bytes == 0 || result.bytes >= result.stream.download || result.stream.seen.Load() != 1 {
			t.Fatalf("interrupted non-replayable WS request id=%s bytes=%d total=%d arrivals=%d error=%v", result.stream.id, result.bytes, result.stream.download, result.stream.seen.Load(), result.err)
		}
		waitSignal(result.stream.id+" destination cancellation", result.stream.serverDone)
		failedCompleted[result.stream.owner] = UsageDelta{Up: int64(result.stream.upload), Down: int64(result.bytes)}
		t.Logf("WS stability expected interruption id=%s received=%d total=%d arrivals=1 error=%v", result.stream.id, result.bytes, result.stream.download, result.err)
	}
	// The fully accepted request bodies and already received response bytes
	// are lower bounds only. Billing still uses each machine's exact raw
	// counters, including all failed-flow bytes and additional bytes in flight.
	recordPhase("deliberate-interruption", failedCompleted)
	for i, client := range clients {
		stream := makeStream(i, "new-request-after-interruption", 64, 0)
		checkSuccess(request(client, stream), false)
		recordPhase(stream.id, map[int64]UsageDelta{client.owner: {Up: int64(stream.upload), Down: int64(stream.download)}})
	}
	mu.Lock()
	defer mu.Unlock()
	for _, stream := range streams {
		if stream.seen.Load() != 1 {
			t.Fatalf("WS request %s was lost or replayed: arrivals=%d", stream.id, stream.seen.Load())
		}
	}
	if proxy.upgrades.Load() == 0 || proxy.earlyData.Load() == 0 {
		t.Fatalf("TLS WS proxy saw no upgrade/early-data traffic: upgrades=%d early_data=%d", proxy.upgrades.Load(), proxy.earlyData.Load())
	}
	t.Logf("WS stability verified TLS proxy upgrades=%d early_data_headers=%d isolated_long_streams=2 concurrent_long_streams=4 interrupted_once=2 independent_recoveries=2; no request retries", proxy.upgrades.Load(), proxy.earlyData.Load())
}

// These are additional end-to-end stability paths. They do not replace or
// reduce the 49-path native relay matrix or its TCP/UDP attribution assertions.
func TestMeteringRelayRealSingboxWSStability(t *testing.T) {
	bin, coreInfo := relayFixtureCore(t)
	for _, protocol := range []string{"vless", "vmess", "trojan"} {
		p := relayProtocolFixture{protocol: protocol, tlsMode: "ws-tls"}
		for _, phase := range []string{"config-check", "traffic"} {
			t.Run(protocol+"/"+phase, func(t *testing.T) {
				runRelaySharedUserPath(t, bin, coreInfo, relayTrafficScenario{
					hops: []relayProtocolFixture{p, p}, checkOnly: phase == "config-check", wsStability: true,
				})
			})
		}
	}
}

// Test only the local reverse-proxy helper with standard WS peers: verified TLS,
// a real upgraded tunnel, forced closure including hijacked sockets, and a new
// independent connection. The sing-box test above supplies protocol/ledger proof.
func TestRelayWSReverseProxyTunnelLifecycle(t *testing.T) {
	serverJSON, _, _ := relayFixtureTLS(t)
	var material struct {
		Certificate []string `json:"certificate"`
		Key         []string `json:"key"`
	}
	if err := json.Unmarshal([]byte(serverJSON), &material); err != nil {
		t.Fatal(err)
	}
	certPEM := []byte(strings.Join(material.Certificate, "\n"))
	cert, err := tls.X509KeyPair(certPEM, []byte(strings.Join(material.Key, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("missing helper-test certificate")
	}
	var arrivals atomic.Int64
	backend := httptest.NewUnstartedServer(websocket.Handler(func(conn *websocket.Conn) {
		arrivals.Add(1)
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}))
	backend.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12}
	backend.StartTLS()
	t.Cleanup(backend.Close)
	proxy := newRelayWSReverseProxy(t, backend.Listener.Addr().(*net.TCPAddr).Port, serverJSON)
	connect := func(payload string) *websocket.Conn {
		t.Helper()
		raw, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", fmt.Sprintf("127.0.0.1:%d", proxy.port), &tls.Config{
			RootCAs: roots, ServerName: "localhost", NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12,
		})
		if err != nil {
			t.Fatal(err)
		}
		config, err := websocket.NewConfig("wss://localhost/fixture", "https://localhost")
		if err != nil {
			raw.Close()
			t.Fatal(err)
		}
		conn, err := websocket.NewClient(config, raw)
		if err != nil {
			raw.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		if err = conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		if _, err = io.ReadFull(conn, got); err != nil || string(got) != payload {
			t.Fatalf("WS tunnel echo got=%q err=%v", got, err)
		}
		return conn
	}
	first := connect("first independent WS stream")
	if n := proxy.interruptConnections(); n != 1 {
		t.Fatalf("expected one tracked hijacked connection, got %d", n)
	}
	_, err = first.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("interrupted WS tunnel still readable")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("WS interruption timed out instead of closing the hijacked socket")
	}
	second := connect("new independent WS stream after interruption")
	second.Close()
	if arrivals.Load() != 2 || proxy.upgrades.Load() != 2 {
		t.Fatalf("helper lost or repeated a connection: backend arrivals=%d upgrades=%d", arrivals.Load(), proxy.upgrades.Load())
	}
}
