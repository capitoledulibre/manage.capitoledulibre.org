package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"unicode"

	"manage/internal/config"
	"manage/internal/files"
)

// Site is the event identity shown on every page: name, website, contact
// address and logo.
type Site struct {
	Name         string
	URL          string
	ContactEmail string
	// VolunteerContactEmail is the address of the volunteer side.
	VolunteerContactEmail string
	// LogoURL is empty when no logo is configured; Initials are shown
	// instead.
	LogoURL  string
	Initials string
}

// brandLogo is the optional event logo, loaded once at startup.
type brandLogo struct {
	data        []byte
	contentType string
	version     string
}

// loadBrandLogo reads and validates the logo file configured with
// MANAGE_SITE_LOGO.
func loadBrandLogo(path string) (*brandLogo, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("MANAGE_SITE_LOGO: %w", err)
	}

	ext, err := files.Detect(data, path, files.KindLogo)
	if err != nil {
		return nil, fmt.Errorf("MANAGE_SITE_LOGO: %s must be an SVG, PNG, JPEG or WebP image", path)
	}

	sum := sha256.Sum256(data)

	return &brandLogo{data: data, contentType: files.ContentType("logo." + ext), version: hex.EncodeToString(sum[:])[:12]}, nil
}

func newSite(cfg *config.Config, logo *brandLogo) Site {
	s := Site{Name: cfg.SiteName, URL: cfg.SiteURL, ContactEmail: cfg.ContactEmail, Initials: siteInitials(cfg.SiteName),
		VolunteerContactEmail: cfg.VolunteerContactEmail}
	if s.VolunteerContactEmail == "" {
		s.VolunteerContactEmail = s.ContactEmail
	}

	if logo != nil {
		s.LogoURL = "/brand/logo?v=" + logo.version
	}

	return s
}

// siteInitials abbreviates the event name: the first letter of each word,
// keeping its case ("Example Conf" gives "EC"), at most three letters.
func siteInitials(name string) string {
	var out []rune

	for _, w := range strings.Fields(name) {
		for _, r := range w {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, r)

				break
			}
		}

		if len(out) == 3 {
			break
		}
	}

	if len(out) == 0 {
		return "?"
	}

	return string(out)
}

func (s *Server) serveBrandLogo(w http.ResponseWriter, r *http.Request) {
	if s.logo == nil {
		http.NotFound(w, r)

		return
	}

	w.Header().Set("Content-Type", s.logo.contentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")

	if r.URL.Query().Get("v") == s.logo.version {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	_, _ = w.Write(s.logo.data)
}
