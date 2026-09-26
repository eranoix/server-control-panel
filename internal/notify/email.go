package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// TypeEmail is the channel type id for SMTP e-mail.
const TypeEmail = "email"

// EmailChannel delivers via SMTP. It honors the Router's ctx deadline by dialing
// with DialContext and stamping the connection deadline, since net/smtp itself
// is context-unaware. Supports STARTTLS (ports 587/25) and implicit TLS
// (port 465); optional PLAIN auth when smtp_user is set.
type EmailChannel struct{}

func NewEmailChannel() *EmailChannel { return &EmailChannel{} }

func (e *EmailChannel) Name() string { return TypeEmail }

func (e *EmailChannel) Send(ctx context.Context, ev Event, cfg ChannelConfig) error {
	if cfg.SMTPHost == "" {
		return errMissing("email", "smtp_host")
	}
	if cfg.From == "" {
		return errMissing("email", "from")
	}
	rcpts := splitList(cfg.To)
	if len(rcpts) == 0 {
		return errMissing("email", "to")
	}
	port := cfg.SMTPPort
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, port)

	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	}

	// Implicit TLS (465): wrap before the SMTP handshake.
	if port == 465 {
		conn = tls.Client(conn, &tls.Config{ServerName: cfg.SMTPHost})
	}

	c, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer c.Close()

	// STARTTLS for non-implicit ports when the server advertises it.
	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: cfg.SMTPHost}); err != nil {
				return fmt.Errorf("email: starttls: %w", err)
			}
		}
	}
	if cfg.SMTPUser != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPHost)); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}
	if err := c.Mail(cfg.From); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("email: RCPT %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	if _, err := w.Write(buildMessage(cfg.From, rcpts, ev)); err != nil {
		_ = w.Close()
		return fmt.Errorf("email: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close: %w", err)
	}
	return c.Quit()
}

// buildMessage assembles a minimal RFC 5322 message (plain text, UTF-8).
func buildMessage(from string, to []string, ev Event) []byte {
	subject := ev.Title
	if subject == "" {
		subject = ev.Type
	}
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + sanitizeHeader(subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(FormatText(ev))
	b.WriteString("\r\n")
	return []byte(b.String())
}

// splitList parses a comma/semicolon/whitespace-separated address list.
func splitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// sanitizeHeader strips CR/LF so a crafted title can't inject extra headers.
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
