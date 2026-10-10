package mail

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMessageHeaders(t *testing.T) {
	m := &Mailer{From: "Bénévoles <volunteers@example.org>"}
	msg := m.message("vol@example.test", "[EC] Votre lien de connexion", "Bonjour,\nle lien.", time.Now())
	if !regexp.MustCompile(`\r\nMessage-ID: <[0-9a-f]{32}@example\.org>\r\n`).MatchString(msg) {
		t.Fatalf("missing or malformed Message-ID:\n%s", msg)
	}
	if !strings.Contains(msg, "\r\n\r\nBonjour,\r\nle lien.") {
		t.Fatalf("body not CRLF-terminated:\n%q", msg)
	}
	if a, b := m.message("x@y", "s", "b", time.Now()), m.message("x@y", "s", "b", time.Now()); a == b {
		t.Fatal("two messages share a Message-ID")
	}
}

func TestSenderDomain(t *testing.T) {
	for from, want := range map[string]string{
		"benevoles@example.org":             "example.org",
		"Bénévoles <benevoles@example.org>": "example.org",
		"nobody":                            "localhost",
	} {
		if got := senderDomain(from); got != want {
			t.Errorf("senderDomain(%q) = %q, want %q", from, got, want)
		}
	}
}
