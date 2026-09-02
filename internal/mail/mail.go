// Package mail sends plain-text e-mails over SMTP, or logs them in dev mode.
package mail

import (
	"crypto/tls"
	"fmt"
	"log"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Mailer sends e-mails.
type Mailer struct {
	Host string
	Port int
	User string
	Pass string
	From string
	// Dev prints messages to the log instead of sending them.
	Dev bool
}

// Send delivers one plain-text message.
func (m *Mailer) Send(to, subject, body string) error {
	if m.Dev || m.Host == "" {
		log.Printf("[mail] to=%s subject=%q\n%s\n", to, subject, body)
		return nil
	}
	msg := strings.Builder{}
	fmt.Fprintf(&msg, "From: %s\r\n", m.From)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	msg.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))

	addr := net.JoinHostPort(m.Host, fmt.Sprint(m.Port))
	var auth smtp.Auth
	if m.User != "" {
		auth = smtp.PlainAuth("", m.User, m.Pass, m.Host)
	}
	if m.Port == 465 {
		return m.sendTLS(addr, auth, to, msg.String())
	}
	return smtp.SendMail(addr, auth, m.From, []string{to}, []byte(msg.String()))
}

// sendTLS handles implicit-TLS servers (port 465), which net/smtp.SendMail does not.
func (m *Mailer) sendTLS(addr string, auth smtp.Auth, to, msg string) error {
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
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
