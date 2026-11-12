package mail

import (
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestHTML(t *testing.T) {
	body := "Bonjour <Nina>,\n\n" +
		"Voici votre lien :\n\n" +
		"https://manage.test/auth/abc?next=%2Fx&y=1\n\n" +
		"Un créneau a changé :\n\n" +
		"  Avant :     Accueil, samedi\n" +
		"  Désormais : Accueil, dimanche\n\n" +
		"  • 09:00 – 11:00  Buvette – Bar\n" +
		"    https://manage.test/ec/poles/buvette/slots/1\n" +
		"    Contact : Lou, lou@conf.test\n" +
		"  • 14:00 – 16:00  Accueil\n\n" +
		"Votre planning : https://manage.test/ec/planning?view=mine.\n" +
		"Une question ? benevoles@conf.test"

	got := HTML("Sujet", body, "fr", Brand{Name: "Example Conf", URL: "https://conf.test"})

	for _, want := range []string{
		`<html lang="fr">`, "<title>Sujet</title>", "Example Conf",
		// Text is escaped.
		"Bonjour &lt;Nina&gt;,",
		// A link alone is a button, the link itself below.
		`href="https://manage.test/auth/abc?next=%2Fx&amp;y=1" style="display:inline-block;`, ">Ouvrir le lien</a>",
		"copiez ce lien",
		// An indented paragraph stands out.
		`border-left:3px solid #ea580c;`, "Avant :     Accueil, samedi<br>",
		// A list, its items continued by the indented lines.
		"<li style=", "<strong>09:00 – 11:00  Buvette – Bar</strong><br>",
		`href="https://manage.test/ec/poles/buvette/slots/1" style="color:#ea580c;text-decoration:underline;">Ouvrir le lien →</a><br>`, "Contact : Lou, ",
		// An item of a single line is no title.
		`<li style="margin:0 0 10px;">14:00 – 16:00  Accueil</li>`,
		// Links and addresses are clickable, the closing full stop left out.
		`href="https://manage.test/ec/planning?view=mine"`, `>manage.test/ec/planning?view=mine</a>.`,
		`href="mailto:benevoles@conf.test"`, `href="mailto:lou@conf.test"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	if strings.Count(got, "<li ") != 2 {
		t.Errorf("%d items, want 2", strings.Count(got, "<li "))
	}

	// English, and a logo when there is one.
	got = HTML("s", "https://x.test/a", "en", Brand{Name: "EC", LogoURL: "https://x.test/logo.png"})
	if !strings.Contains(got, ">Open the link</a>") || !strings.Contains(got, `<img src="https://x.test/logo.png" alt="EC"`) {
		t.Errorf("english with a logo:\n%s", got)
	}

	// Nothing but http(s) becomes a link.
	if got := HTML("s", "javascript:alert(1)\n\nftp://x.test", "fr", Brand{}); strings.Contains(got, `href="javascript`) || strings.Contains(got, `href="ftp`) {
		t.Errorf("a link from another scheme:\n%s", got)
	}
}

func TestBuildAlternative(t *testing.T) {
	from := &mail.Address{Name: "EC", Address: "noreply@conf.test"}
	long := strings.Repeat("é", 600)

	raw := build(from, Message{To: []string{"a@b.test"}, Subject: "s", Body: "Bonjour\n" + long, HTML: "<p>" + long + "</p>"})

	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}

	media, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || media != "multipart/alternative" {
		t.Fatalf("content type %q: %v", msg.Header.Get("Content-Type"), err)
	}

	r := multipart.NewReader(msg.Body, params["boundary"])

	var types []string

	for {
		part, err := r.NextPart()
		if err == io.EOF {
			break
		}

		if err != nil {
			t.Fatal(err)
		}

		// The reader undoes the quoted-printable.
		data, _ := io.ReadAll(part)
		types = append(types, part.Header.Get("Content-Type"))

		if !strings.Contains(string(data), long) {
			t.Errorf("%s: the body is not whole", part.Header.Get("Content-Type"))
		}
	}

	if strings.Join(types, ",") != "text/plain; charset=utf-8,text/html; charset=utf-8" {
		t.Errorf("parts: %v", types)
	}

	// No line over the 998 characters SMTP allows.
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 && !strings.Contains(line, "Bonjour") && !strings.HasPrefix(line, "é") {
			t.Errorf("a line of %d characters", len(line))
		}
	}

	// Without an HTML version, plain text as before.
	if plain := string(build(from, Message{To: []string{"a@b.test"}, Subject: "s", Body: "b"})); !strings.Contains(plain, "Content-Type: text/plain; charset=utf-8\r\n") || strings.Contains(plain, "multipart") {
		t.Errorf("plain message:\n%s", plain)
	}
}

func TestDecorAndInline(t *testing.T) {
	// A decoration goes before its text, escaped as it is; not in a link.
	got := HTMLWith("s", "Contact : Ana <B> (référente) ; https://x.test/Ana <B> (référente)", "fr", Brand{},
		map[string]string{"Ana <B> (référente)": `<img src="cid:a@m">`})
	if !strings.Contains(got, `Contact : <img src="cid:a@m">Ana &lt;B&gt; (référente) ;`) || strings.Count(got, "cid:a@m") != 1 {
		t.Errorf("decoration:\n%s", got)
	}

	// The HTML and its images go together, as related parts.
	from := &mail.Address{Address: "noreply@conf.test"}
	raw := build(from, Message{To: []string{"a@b.test"}, Subject: "s", Body: "b", HTML: `<img src="cid:a@m">`,
		Inline: []Inline{{ID: "a@m", Type: "image/png", Data: []byte(strings.Repeat("x", 200))}}})

	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}

	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	outer := multipart.NewReader(msg.Body, params["boundary"])

	if _, err := outer.NextPart(); err != nil { // the text
		t.Fatal(err)
	}

	related, err := outer.NextPart()
	if err != nil {
		t.Fatal(err)
	}

	media, rparams, _ := mime.ParseMediaType(related.Header.Get("Content-Type"))
	if media != "multipart/related" {
		t.Fatalf("second part: %s", media)
	}

	inner := multipart.NewReader(related, rparams["boundary"])

	if html, err := inner.NextPart(); err != nil || html.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("html part: %v", err)
	}

	img, err := inner.NextPart()
	if err != nil {
		t.Fatal(err)
	}

	// The reader undoes quoted-printable only.
	data, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, img))
	if img.Header.Get("Content-ID") != "<a@m>" || img.Header.Get("Content-Type") != "image/png" || string(data) != strings.Repeat("x", 200) {
		t.Errorf("image part: %v %q", img.Header, data)
	}
}
