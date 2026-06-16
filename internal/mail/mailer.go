// Package mail sends transactional emails (e.g. password-reset codes).
//
// When SMTP is not configured (empty host) NewMailer returns a logging mailer
// that records messages to the application log instead of sending them — handy
// for development before real SMTP credentials are available.
package mail

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Mailer sends a plain-text email.
type Mailer interface {
	Send(to, subject, body string) error
	// Enabled reports whether real delivery is configured (false = log-only).
	Enabled() bool
}

// Config holds SMTP settings. Host == "" selects the log-only mailer.
type Config struct {
	Host string
	Port int
	User string
	Pass string
	From string // defaults to User when empty
}

// NewMailer returns an SMTP-backed mailer, or a log-only mailer when Host is empty.
func NewMailer(cfg Config, log *zap.Logger) Mailer {
	if strings.TrimSpace(cfg.Host) == "" {
		log.Warn("SMTP not configured; password-reset codes will be written to the log, not emailed")
		return &logMailer{log: log}
	}
	from := cfg.From
	if strings.TrimSpace(from) == "" {
		from = cfg.User
	}
	if cfg.Port == 0 {
		cfg.Port = 465
	}
	log.Info("SMTP mailer configured", zap.String("host", cfg.Host), zap.Int("port", cfg.Port), zap.String("from", from))
	return &smtpMailer{cfg: cfg, from: from, log: log}
}

// ─── log-only mailer ───────────────────────────────────────────────────────────

type logMailer struct{ log *zap.Logger }

func (m *logMailer) Enabled() bool { return false }

func (m *logMailer) Send(to, subject, body string) error {
	m.log.Warn("email NOT sent (SMTP disabled) — printing for development",
		zap.String("to", to),
		zap.String("subject", subject),
		zap.String("body", body),
	)
	return nil
}

// ─── SMTP mailer ─────────────────────────────────────────────────────────────

type smtpMailer struct {
	cfg  Config
	from string
	log  *zap.Logger
}

func (m *smtpMailer) Enabled() bool { return true }

func (m *smtpMailer) Send(to, subject, body string) error {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	auth := smtp.PlainAuth("", m.cfg.User, m.cfg.Pass, m.cfg.Host)
	msg := buildMessage(m.from, to, subject, body)

	// Port 465 uses implicit TLS (SSL on connect); 25/587 use plain/STARTTLS,
	// which net/smtp.SendMail negotiates automatically.
	if m.cfg.Port == 465 {
		return m.sendImplicitTLS(addr, auth, to, msg)
	}
	return smtp.SendMail(addr, auth, m.from, []string{to}, msg)
}

func (m *smtpMailer) sendImplicitTLS(addr string, auth smtp.Auth, to string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: m.cfg.Host})
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer c.Close()

	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := c.Mail(m.from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return c.Quit()
}

// buildMessage assembles an RFC 822 plain-text UTF-8 message. The subject is
// MIME-encoded so non-ASCII (Chinese) headers survive transport.
func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return []byte(b.String())
}
