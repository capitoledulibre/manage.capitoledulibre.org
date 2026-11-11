package mail

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"manage/internal/config"
)

// fakeSMTPS serves implicit-TLS SMTP with a self-signed certificate for
// another name, like a server whose certificate does not match its host.
func fakeSMTPS(t *testing.T) (host string, port int) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "other.test"},
		DNSNames:     []string{"other.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go serveSMTP(conn)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)

	return "127.0.0.1", addr.Port
}

func serveSMTP(conn net.Conn) {
	defer conn.Close()

	r := bufio.NewReader(conn)
	reply := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

	reply("220 fake")

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}

		switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			reply("250 fake")
		case cmd == "DATA":
			reply("354 go on")

			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}

				if l == ".\r\n" {
					break
				}
			}

			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")

			return
		default:
			reply("250 ok")
		}
	}
}

func TestSMTPTLSSkipVerify(t *testing.T) {
	host, port := fakeSMTPS(t)
	msg := Message{To: []string{"a@b.test"}, Subject: "s", Body: "b"}

	cfg := &config.Config{SMTPHost: host, SMTPPort: port, SMTPTLS: "tls", MailFrom: "noreply@conf.test"}

	err := New(cfg).Send(msg)
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("certificate for another name accepted by default: %v", err)
	}

	cfg.SMTPTLSSkipVerify = true

	if err := New(cfg).Send(msg); err != nil {
		t.Fatalf("send with MANAGE_SMTP_TLS_SKIP_VERIFY: %v", err)
	}

}
