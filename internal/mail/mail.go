// Package mail sends emails over SMTP: plain text, with an HTML version
// made from it when there is one.
package mail

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"manage/internal/config"
)

type Message struct {
	To      []string
	Subject string
	Body    string
	// ReplyTo, when set, is where answers go instead of the sender.
	ReplyTo string
	// Lang is the language of the message, "fr" or "en", for the few words
	// its HTML version adds.
	Lang string
	// HTML, when set, is sent along with Body as the version clients show
	// when they can; Body stays the version for the others.
	HTML string
	// Inline are the images the HTML shows as "cid:" + their ID: sent with
	// the message, they show with no address to fetch.
	Inline []Inline
}

// Inline is an image sent within a message.
type Inline struct {
	ID   string
	Type string
	Data []byte
}

type Sender interface {
	Send(msg Message) error
}

// New returns an SMTP sender, or a logging sender when no SMTP host is
// configured.
func New(cfg *config.Config) Sender {
	if cfg.SMTPHost == "" {
		return logSender{}
	}

	if cfg.SMTPTLSSkipVerify {
		slog.Warn("MANAGE_SMTP_TLS_SKIP_VERIFY is set: the SMTP server certificate is not checked")
	}

	return &smtpSender{cfg: cfg}
}

type logSender struct{}

func (logSender) Send(msg Message) error {
	slog.Info("email (not sent, SMTP not configured)", "to", msg.To, "subject", msg.Subject, "body", "\n"+msg.Body)

	return nil
}

type smtpSender struct {
	cfg *config.Config
}

func (s *smtpSender) Send(msg Message) error {
	from, err := mail.ParseAddress(s.cfg.MailFrom)
	if err != nil {
		return fmt.Errorf("invalid MANAGE_MAIL_FROM: %w", err)
	}

	addr := net.JoinHostPort(s.cfg.SMTPHost, strconv.Itoa(s.cfg.SMTPPort))
	tlsConfig := &tls.Config{
		ServerName: s.cfg.SMTPHost,
		MinVersion: tls.VersionTLS12,
		// Opt-in, for a server whose certificate does not match its name.
		InsecureSkipVerify: s.cfg.SMTPTLSSkipVerify,
	}

	var conn net.Conn
	if s.cfg.SMTPTLS == "tls" {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, tlsConfig)
	} else {
		conn, err = net.DialTimeout("tcp", addr, 15*time.Second)
	}

	if err != nil {
		return err
	}

	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	c, err := smtp.NewClient(conn, s.cfg.SMTPHost)
	if err != nil {
		conn.Close()

		return err
	}
	defer c.Close()

	if s.cfg.SMTPTLS == "starttls" {
		if err := c.StartTLS(tlsConfig); err != nil {
			return err
		}
	}

	if s.cfg.SMTPUser != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPassword, s.cfg.SMTPHost)); err != nil {
			return err
		}
	}

	if err := c.Mail(from.Address); err != nil {
		return err
	}

	for _, to := range msg.To {
		if err := c.Rcpt(to); err != nil {
			return err
		}
	}

	w, err := c.Data()
	if err != nil {
		return err
	}

	if _, err := w.Write(build(from, msg)); err != nil {
		return err
	}

	if err := w.Close(); err != nil {
		return err
	}

	return c.Quit()
}

// replyTo keeps a Reply-To only when it is a plain address, so a setting
// cannot smuggle another header in.
func replyTo(v string) string {
	if v == "" {
		return ""
	}

	a, err := mail.ParseAddress(v)
	if err != nil {
		return ""
	}

	return a.String()
}

// cleanHeader keeps a header value on its line.
func cleanHeader(v string) string {
	return strings.NewReplacer("\r", "", "\n", "", "<", "", ">", "").Replace(v)
}

func build(from *mail.Address, msg Message) []byte {
	var b bytes.Buffer

	id := make([]byte, 12)
	_, _ = rand.Read(id)

	domain := "localhost"
	if at := strings.LastIndex(from.Address, "@"); at >= 0 {
		domain = from.Address[at+1:]
	}

	headers := [][2]string{
		{"From", from.String()},
		{"To", strings.Join(msg.To, ", ")},
		{"Subject", mime.QEncoding.Encode("utf-8", msg.Subject)},
		{"Reply-To", replyTo(msg.ReplyTo)},
		{"Date", time.Now().Format(time.RFC1123Z)},
		{"Message-ID", fmt.Sprintf("<%s@%s>", hex.EncodeToString(id), domain)},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "8bit"},
		{"Auto-Submitted", "auto-generated"},
	}

	_, _ = rand.Read(id)
	boundary := "=_" + hex.EncodeToString(id)

	if msg.HTML != "" {
		headers[7] = [2]string{"Content-Type", `multipart/alternative; boundary="` + boundary + `"`}
		headers[8] = [2]string{"Content-Transfer-Encoding", ""}
	}

	for _, h := range headers {
		if h[1] == "" {
			continue
		}

		fmt.Fprintf(&b, "%s: %s\r\n", h[0], h[1])
	}

	b.WriteString("\r\n")

	text := strings.ReplaceAll(strings.ReplaceAll(msg.Body, "\r\n", "\n"), "\n", "\r\n")
	if msg.HTML == "" {
		b.WriteString(text)

		return b.Bytes()
	}

	// The text first, then the HTML: clients show the last part they can.
	// The HTML goes quoted-printable, its lines too long for SMTP as they
	// are.
	b.WriteString("--" + boundary + "\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(text)
	b.WriteString("\r\n--" + boundary + "\r\n")

	// With images, the HTML and they go together, as related parts.
	related := boundary + "_r"
	if len(msg.Inline) > 0 {
		b.WriteString(`Content-Type: multipart/related; boundary="` + related + `"` + "\r\n\r\n--" + related + "\r\n")
	}

	b.WriteString("Content-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")

	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(msg.HTML))
	_ = qp.Close()

	for _, img := range msg.Inline {
		fmt.Fprintf(&b, "\r\n--%s\r\nContent-Type: %s\r\nContent-Transfer-Encoding: base64\r\nContent-ID: <%s>\r\nContent-Disposition: inline\r\n\r\n",
			related, cleanHeader(img.Type), cleanHeader(img.ID))

		data := base64.StdEncoding.EncodeToString(img.Data)
		for len(data) > 76 {
			b.WriteString(data[:76] + "\r\n")
			data = data[76:]
		}

		b.WriteString(data)
	}

	if len(msg.Inline) > 0 {
		b.WriteString("\r\n--" + related + "--")
	}

	b.WriteString("\r\n--" + boundary + "--\r\n")

	return b.Bytes()
}
