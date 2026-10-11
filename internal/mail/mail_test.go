package mail

import (
	"net/mail"
	"strings"
	"testing"
)

func TestReplyTo(t *testing.T) {
	from := &mail.Address{Name: "EC", Address: "noreply@conf.test"}

	got := string(build(from, Message{To: []string{"a@b.test"}, Subject: "s", Body: "b", ReplyTo: "benevoles@conf.test"}))
	if !strings.Contains(got, "Reply-To: <benevoles@conf.test>\r\n") {
		t.Errorf("Reply-To missing:\n%s", got)
	}

	// No header without a value, and nothing smuggled through one.
	for _, v := range []string{"", "x@y.test\r\nBcc: z@y.test"} {
		got := string(build(from, Message{To: []string{"a@b.test"}, Subject: "s", Body: "b", ReplyTo: v}))
		if strings.Contains(got, "Reply-To") || strings.Contains(got, "Bcc") {
			t.Errorf("ReplyTo %q gave:\n%s", v, got)
		}
	}
}
