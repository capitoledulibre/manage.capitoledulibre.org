package files

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func pngData() []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2)))

	return buf.Bytes()
}

func TestSave(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`)

	cases := []struct {
		name     string
		data     []byte
		filename string
		kind     Kind
		max      int64
		wantExt  string
		wantErr  error
	}{
		{"pdf", []byte("%PDF-1.7\n..."), "devis.pdf", KindPDF, 1 << 20, ".pdf", nil},
		{"fake pdf", []byte("<html>%PDF-</html>"), "devis.pdf", KindPDF, 1 << 20, "", ErrBadType},
		{"png as pdf", pngData(), "devis.pdf", KindPDF, 1 << 20, "", ErrBadType},
		{"png logo", pngData(), "logo.png", KindLogo, 1 << 20, ".png", nil},
		{"png named svg", pngData(), "logo.svg", KindLogo, 1 << 20, ".png", nil},
		{"svg logo", svg, "logo.svg", KindLogo, 1 << 20, ".svg", nil},
		{"svg without svg tag", []byte("<?xml version=\"1.0\"?><html></html>"), "logo.svg", KindLogo, 1 << 20, "", ErrBadType},
		{"svg with other extension", svg, "logo.txt", KindLogo, 1 << 20, "", ErrBadType},
		{"pdf as logo", []byte("%PDF-1.7"), "logo.pdf", KindLogo, 1 << 20, "", ErrBadType},
		{"too large", bytes.Repeat([]byte("a"), 2048), "a.pdf", KindPDF, 1024, "", ErrTooLarge},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, err := s.Save(bytes.NewReader(c.data), c.filename, c.kind, c.max)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}

			if c.wantErr == nil && !strings.HasSuffix(name, c.wantExt) {
				t.Errorf("name %q, want extension %s", name, c.wantExt)
			}
		})
	}
}

func TestPathRejectsTraversal(t *testing.T) {
	s, _ := New(t.TempDir())

	for _, name := range []string{"../etc/passwd", "..%2f..%2fx.pdf", "/abs.pdf", "0123456789abcdef0123456789abcdef.exe", "ABCDEF0123456789abcdef0123456789.pdf", ""} {
		if _, err := s.Path(name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q accepted", name)
		}
	}

	if _, err := s.Path("0123456789abcdef0123456789abcdef.pdf"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
}

func TestServeSandboxesSVG(t *testing.T) {
	s, _ := New(t.TempDir())

	name, err := s.Save(strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), "x.svg", KindLogo, 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.Serve(rec, httptest.NewRequest(http.MethodGet, "/", nil), name, "")

	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("serve: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}

	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Error("SVG served without sandbox CSP")
	}

	rec = httptest.NewRecorder()
	s.Serve(rec, httptest.NewRequest(http.MethodGet, "/", nil), "../../etc/passwd", "")

	if rec.Code != 404 {
		t.Errorf("traversal: %d", rec.Code)
	}
}
