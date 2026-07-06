// Package mailer sends transactional email over SMTP. It supports implicit TLS
// (port 465) and STARTTLS (587/25), with or without auth.
package mailer

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Mailer struct {
	Host     string
	Port     string
	User     string
	Pass     string
	From     string
	FromName string
	Security string // "ssl" | "starttls" | "none"
}

func (m *Mailer) security() string {
	if m.Security != "" {
		return m.Security
	}
	if m.Port == "465" {
		return "ssl"
	}
	return "starttls"
}

func (m *Mailer) Send(to []string, subject, htmlBody string) error {
	if m.Host == "" {
		return fmt.Errorf("mailer: SMTP host not configured")
	}
	addr := net.JoinHostPort(m.Host, m.Port)
	msg := buildMessage(m.From, m.FromName, to, subject, htmlBody)

	var auth smtp.Auth
	if m.User != "" {
		auth = smtp.PlainAuth("", m.User, m.Pass, m.Host)
	}

	if m.security() == "ssl" {
		return m.sendImplicitTLS(addr, auth, to, msg)
	}
	// STARTTLS (smtp.SendMail upgrades automatically if offered) or plain.
	return smtp.SendMail(addr, auth, m.From, to, msg)
}

func (m *Mailer) sendImplicitTLS(addr string, auth smtp.Auth, to []string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.Host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(m.From); err != nil {
		return err
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func buildMessage(fromAddr, fromName string, to []string, subject, htmlBody string) []byte {
	from := fromAddr
	if fromName != "" {
		from = mime.QEncoding.Encode("utf-8", fromName) + " <" + fromAddr + ">"
	}
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	return []byte(b.String())
}
