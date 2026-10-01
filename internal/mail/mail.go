// Package mail sends plain-text e-mails over SMTP, or logs them in dev mode.
package mail

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log"
	"mime"
	"net"
	"net/mail"
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
	msg := m.message(to, subject, body, time.Now())

	addr := net.JoinHostPort(m.Host, fmt.Sprint(m.Port))
	var auth smtp.Auth
	if m.User != "" {
		auth = smtp.PlainAuth("", m.User, m.Pass, m.Host)
	}
	if m.Port == 465 {
		return m.sendTLS(addr, auth, to, msg)
	}
	return smtp.SendMail(addr, auth, m.From, []string{to}, []byte(msg))
}

// message renders the headers and body of one e-mail.
func (m *Mailer) message(to, subject, body string, now time.Time) string {
	var msg strings.Builder
	fmt.Fprintf(&msg, "From: %s\r\n", m.From)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&msg, "Date: %s\r\n", now.Format(time.RFC1123Z))
	// Spam filters count a missing Message-ID against a message, and a login
	// link lost in the spam folder is a volunteer who cannot log in.
	fmt.Fprintf(&msg, "Message-ID: <%s@%s>\r\n", messageToken(), senderDomain(m.From))
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	msg.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return msg.String()
}

// messageToken returns the unique left part of a Message-ID.
func messageToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// senderDomain is the domain of the From address, which is where a
// Message-ID is expected to come from.
func senderDomain(from string) string {
	addr := from
	if a, err := mail.ParseAddress(from); err == nil {
		addr = a.Address
	}
	if i := strings.LastIndexByte(addr, '@'); i >= 0 && i < len(addr)-1 {
		return addr[i+1:]
	}
	return "localhost"
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
