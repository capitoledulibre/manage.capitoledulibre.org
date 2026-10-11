package server

import (
	"bytes"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The icons of the site, for a browser tab and a phone's home screen: the
// configured logo when there is one, else the initials of the event on the
// brand color, as in the header. A phone wants a PNG it can round itself,
// so the initials are drawn once, at startup.

// brandColor is the brand-600 of the stylesheet.
var brandColor = color.RGBA{0xea, 0x58, 0x0c, 0xff}

// touchIconSize is the size iOS asks for.
const touchIconSize = 180

// siteIcons are the icons served, built from the site identity.
type siteIcons struct {
	svg   []byte
	touch []byte
}

func newSiteIcons(site Site, logo *brandLogo) (*siteIcons, error) {
	icons := &siteIcons{svg: initialsSVG(site.Initials)}

	// A raster logo makes a fine home screen icon; a vector one cannot be
	// drawn here, so the initials stand in for it.
	if logo != nil && logo.contentType != "image/svg+xml" {
		icons.touch = logo.data

		return icons, nil
	}

	touch, err := initialsPNG(site.Initials, touchIconSize)
	if err != nil {
		return nil, fmt.Errorf("home screen icon: %w", err)
	}

	icons.touch = touch

	return icons, nil
}

func initialsSVG(initials string) []byte {
	size := 28
	if len([]rune(initials)) > 2 {
		size = 22
	}

	return fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">`+
		`<rect width="64" height="64" rx="14" fill="#%02x%02x%02x"/>`+
		`<text x="32" y="32" dy=".35em" text-anchor="middle" font-family="system-ui,sans-serif" font-weight="700" font-size="%d" fill="#fff">%s</text></svg>`,
		brandColor.R, brandColor.G, brandColor.B, size, html.EscapeString(initials))
}

// initialsPNG draws the initials, white and centered, on a square of the
// brand color.
func initialsPNG(initials string, size int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{brandColor}, image.Point{}, draw.Src)

	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}

	points := float64(size) * 0.4
	if len([]rune(initials)) > 2 {
		points = float64(size) * 0.3
	}

	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: points, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer face.Close()

	d := &font.Drawer{Dst: img, Src: image.White, Face: face}
	width := d.MeasureString(initials)
	m := face.Metrics()
	// Centered on the height of capitals, not on the line with its descent.
	d.Dot = fixed.Point26_6{
		X: (fixed.I(size) - width) / 2,
		Y: (fixed.I(size) + m.CapHeight) / 2,
	}
	d.DrawString(initials)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (s *Server) serveIconSVG(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(s.icons.svg)
}

func (s *Server) serveTouchIcon(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", http.DetectContentType(s.icons.touch))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(s.icons.touch)
}
