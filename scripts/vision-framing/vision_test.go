package visionframing

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"github.com/sagernet/sing-vmess/vless"
	"github.com/sagernet/sing/common/logger"
	"io"
	"net"
	"testing"
	"time"
)

type fragmentConn struct{ chunks [][]byte }

func (c *fragmentConn) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	b := c.chunks[0]
	n := copy(p, b)
	if n == len(b) {
		c.chunks = c.chunks[1:]
	} else {
		c.chunks[0] = b[n:]
	}
	return n, nil
}
func (c *fragmentConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *fragmentConn) Close() error                     { return nil }
func (c *fragmentConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *fragmentConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *fragmentConn) SetDeadline(time.Time) error      { return nil }
func (c *fragmentConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fragmentConn) SetWriteDeadline(time.Time) error { return nil }

// This is a deterministic protocol-parser regression. No core process, TLS
// handshake, production endpoint, or traffic-statistics integration is tested.
// The tls.Conn is only the type/reflection context required by NewVisionConn.
func TestVisionFragmentedPaddingHeader(t *testing.T) {
	for split := 1; split < 5; split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			var id [16]byte
			id[0] = 1
			first := append(append([]byte{}, id[:]...), 0, 0, 1, 0, 0, 'a')
			second := []byte{1, 0, 3, 0, 0, 'x', 'y', 'z'}
			wire := &fragmentConn{chunks: [][]byte{append(first, second[:split]...), second[split:]}}
			v, e := vless.NewVisionConn(wire, tls.Client(&fragmentConn{}, &tls.Config{}), id, logger.NOP())
			if e != nil {
				t.Fatal(e)
			}
			var out []byte
			for reads := 0; ; reads++ {
				if reads > 20 {
					t.Fatal("decoder made no bounded progress")
				}
				p := make([]byte, 32768)
				n, e := v.Read(p)
				out = append(out, p[:n]...)
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			if !bytes.Equal(out, []byte("axyz")) {
				t.Fatalf("valid fragmented header decoded %x, want %x", out, []byte("axyz"))
			}
		})
	}
}
