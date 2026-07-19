package mailer

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP answers EHLO with the given extension lines, then accepts nothing.
func fakeSMTP(t *testing.T, ehlo []string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				br := bufio.NewReader(c)
				c.Write([]byte("220 fake ESMTP\r\n"))
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						for i, e := range ehlo {
							sep := "-"
							if i == len(ehlo)-1 {
								sep = " "
							}
							c.Write([]byte("250" + sep + e + "\r\n"))
						}
					case strings.HasPrefix(line, "QUIT"):
						c.Write([]byte("221 bye\r\n"))
						return
					default:
						c.Write([]byte("250 ok\r\n"))
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

// A server that doesn't offer STARTTLS must abort the send, not silently
// continue in plaintext. Stripping "250-STARTTLS" from the EHLO reply is all an
// on-path attacker has to do; with no SMTP auth configured the message itself
// — carrying a password-reset link — would otherwise go out in the clear.
func TestSendStartTLS_RefusesWhenExtensionMissing(t *testing.T) {
	addr := fakeSMTP(t, []string{"fake greets you", "SIZE 10240000"}) // no STARTTLS
	host, _, _ := net.SplitHostPort(addr)
	m := &Mailer{Host: host, From: "a@example.test", Security: "starttls"}

	err := m.sendStartTLS(addr, nil, []string{"b@example.test"}, []byte("Subject: x\r\n\r\nbody"))
	if err == nil {
		t.Fatal("send proceeded over an unencrypted connection")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("error should name the missing extension, got: %v", err)
	}
}

// When the server does advertise it, the code must attempt the upgrade — here
// the fake has no TLS so StartTLS itself fails, which is still the correct
// branch (it proves we didn't skip straight to delivery).
func TestSendStartTLS_AttemptsUpgradeWhenOffered(t *testing.T) {
	addr := fakeSMTP(t, []string{"fake greets you", "STARTTLS"})
	host, _, _ := net.SplitHostPort(addr)
	m := &Mailer{Host: host, From: "a@example.test", Security: "starttls"}

	err := m.sendStartTLS(addr, nil, []string{"b@example.test"}, []byte("Subject: x\r\n\r\nbody"))
	if err == nil {
		t.Fatal("expected the TLS handshake against a plaintext fake to fail")
	}
	if strings.Contains(err.Error(), "未提供 STARTTLS") {
		t.Errorf("STARTTLS was offered but the code reported it missing: %v", err)
	}
}
