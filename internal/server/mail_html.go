package server

import (
	"bytes"
	"html/template"
	"image"
	_ "image/gif" // decoders of the profile pictures
	_ "image/jpeg"
	"image/png"
	"os"
	"strings"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"manage/internal/mail"
	"manage/internal/store"
)

// htmlMailer gives every message an HTML version of its text body, in the
// site's colours, so the bodies are only ever written as text. A message
// that comes with its own HTML keeps it.
type htmlMailer struct {
	next  mail.Sender
	brand mail.Brand
}

func (m htmlMailer) Send(msg mail.Message) error {
	if msg.HTML == "" {
		msg.HTML = mail.HTML(msg.Subject, msg.Body, msg.Lang, m.brand)
	}

	return m.next.Send(msg)
}

// mailBrand is how the site's e-mails look. The logo goes in only as an
// image e-mail clients show: most of them leave out SVG.
func mailBrand(s *Server) mail.Brand {
	b := mail.Brand{Name: s.site.Name, URL: s.site.URL, Color: "#ea580c"}

	if s.logo != nil && s.site.LogoURL != "" && !strings.Contains(s.logo.contentType, "svg") {
		b.LogoURL = s.cfg.BaseURL + s.site.LogoURL
	}

	return b
}

// avatarPx is the side of a profile picture sent in an e-mail, twice the
// size it shows at for sharp screens.
const avatarPx = 96

// mailAvatar is how a person shows in an e-mail's HTML: their profile
// picture, small and round, sent with the message (the pictures are only
// served to signed-in people, which an e-mail client is not), or else
// their initials. The image is nil for initials.
func (s *Server) mailAvatar(p *store.Person) (string, *mail.Inline) {
	const size = "width:28px;height:28px;border-radius:50%;vertical-align:middle;margin:0 6px 0 0;"

	if p.AvatarFile != "" {
		if data, err := s.avatarThumb(p.AvatarFile); err == nil {
			id := "avatar-" + p.UID + "@manage"

			return `<img src="cid:` + template.HTMLEscapeString(id) + `" width="28" height="28" alt="" style="` + size + `border:0;">`,
				&mail.Inline{ID: id, Type: "image/png", Data: data}
		}
	}

	return `<span style="display:inline-block;` + size + `line-height:28px;background:#fff7ed;color:#9a3412;font-size:11px;font-weight:600;text-align:center;">` +
		template.HTMLEscapeString(p.Initials()) + `</span>`, nil
}

// avatarThumb is a profile picture cut square from its middle and shrunk
// to avatarPx, as a PNG. An SVG picture is an error: it does not decode.
func (s *Server) avatarThumb(file string) ([]byte, error) {
	path, err := s.files.Path(file)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}

	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	square := image.Rect(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2, 0, 0)
	square.Max = square.Min.Add(image.Pt(side, side))

	dst := image.NewRGBA(image.Rect(0, 0, avatarPx, avatarPx))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, square, draw.Over, nil)

	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}
