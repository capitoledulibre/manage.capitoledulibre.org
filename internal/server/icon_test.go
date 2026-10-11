package server

import (
	"bytes"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"manage/internal/config"
)

func TestSiteIcons(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.SiteName = "Rencontres du Libre" })
	c := e.client()

	_, body := e.get(c, "/")
	mustContain(t, body, `<link rel="icon" href="/icon.svg">`, `<link rel="apple-touch-icon" href="/apple-touch-icon.png">`)

	// The initials on the brand color.
	_, svg := e.get(c, "/icon.svg")
	mustContain(t, svg, "#ea580c", ">RdL<")

	// A PNG of the size a phone asks for, at the root where iOS looks.
	for _, path := range []string{"/apple-touch-icon.png", "/apple-touch-icon-precomposed.png"} {
		resp, err := c.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}

		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		img, err := png.Decode(bytes.NewReader(data))
		if err != nil || resp.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("%s: %v %s", path, err, resp.Header.Get("Content-Type"))
		}

		if b := img.Bounds(); b.Dx() != touchIconSize || b.Dy() != touchIconSize {
			t.Errorf("%s: %v", path, b)
		}

		if !strings.Contains(resp.Header.Get("Cache-Control"), "max-age") || resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d %s", path, resp.StatusCode, resp.Header.Get("Cache-Control"))
		}
	}
}
