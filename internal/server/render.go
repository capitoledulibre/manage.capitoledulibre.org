package server

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"manage/internal/i18n"
	"manage/internal/store"
	"manage/web"
)

// Page is the data passed to every template.
type Page struct {
	Lang  string
	Title string
	// Area selects the navigation: "public", "partner" or "admin".
	Area  string
	Flash string
	Path  string
	// RequestURI is the path with its query string, used to come back to
	// the same page after switching language.
	RequestURI string
	// EventQuery keeps the selected edition ("?event=slug") in partner
	// navigation links.
	EventQuery string
	// Theme is "system", "light" or "dark".
	Theme string
	// Nav is the active admin section: "pipeline", "partners", "editions"
	// or "profile".
	Nav string
	Dev bool

	Manager       *store.Manager
	Org           *store.Organization
	Admin         *store.Admin
	Impersonating bool

	// Site is the event identity (name, website, contact, logo).
	Site Site

	// D holds page-specific data.
	D any
}

func (s *Server) page(r *http.Request, title string) *Page {
	c := rc(r)

	area := "public"

	switch {
	case strings.HasPrefix(r.URL.Path, "/admin"):
		area = "admin"
	case strings.HasPrefix(r.URL.Path, "/partners/") && c.Org != nil:
		area = "partner"
	}

	lang := c.Lang
	if area == "admin" {
		// The staff interface is French only.
		lang = "fr"
	}

	eventQuery := ""
	if slug := r.URL.Query().Get("event"); area == "partner" && slug != "" {
		eventQuery = "?event=" + url.QueryEscape(slug)
	}

	return &Page{
		Lang:          lang,
		Title:         title,
		Area:          area,
		Path:          r.URL.Path,
		RequestURI:    r.URL.RequestURI(),
		EventQuery:    eventQuery,
		Theme:         detectTheme(r),
		Site:          s.site,
		Nav:           adminNav(r.URL.Path),
		Dev:           s.cfg.Dev,
		Manager:       c.Manager,
		Org:           c.Org,
		Admin:         c.Admin,
		Impersonating: c.Impersonating,
	}
}

var pipelinePath = regexp.MustCompile(`^/admin/events/[^/]+$`)

// adminNav maps an admin path to its menu section.
func adminNav(path string) string {
	switch {
	case path == "/admin/" || pipelinePath.MatchString(path) && path != "/admin/events/new":
		return "pipeline"
	case strings.HasPrefix(path, "/admin/organizations"):
		return "partners"
	case strings.HasPrefix(path, "/admin/events"):
		return "editions"
	case strings.HasPrefix(path, "/admin/profile"):
		return "profile"
	}

	return ""
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, p *Page) error {
	p.Flash = s.popFlash(w, r)

	var buf bytes.Buffer
	if err := s.tmpl.render(&buf, name, p); err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)

	return err
}

type templates struct {
	pages map[string]*template.Template
}

func loadTemplates() (*templates, error) {
	t := &templates{pages: map[string]*template.Template{}}

	var shared []string

	err := fs.WalkDir(web.Templates, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}

		if path.Base(p) == "base.html" || strings.HasPrefix(p, "templates/partials/") {
			shared = append(shared, p)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	err = fs.WalkDir(web.Templates, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}

		if path.Base(p) == "base.html" || strings.HasPrefix(p, "templates/partials/") {
			return nil
		}

		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")

		tpl, err := template.New(name).Funcs(funcs).ParseFS(web.Templates, append(append([]string{}, shared...), p)...)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}

		t.pages[name] = tpl

		return nil
	})

	return t, err
}

func (t *templates) render(w io.Writer, name string, p *Page) error {
	tpl, ok := t.pages[name]
	if !ok {
		return fmt.Errorf("unknown template %q", name)
	}

	return tpl.ExecuteTemplate(w, "base", p)
}

var funcs = template.FuncMap{
	"t": i18n.T,
	"date": func(lang string, v any) string {
		tm := toTime(v)
		if tm == nil {
			return ""
		}

		if lang == "en" {
			return tm.Format("Jan 2, 2006")
		}

		return tm.Format("02/01/2006")
	},
	"datetime": func(lang string, v any) string {
		tm := toTime(v)
		if tm == nil {
			return ""
		}

		local := tm.In(paris)
		if lang == "en" {
			return local.Format("Jan 2, 2006 15:04")
		}

		return local.Format("02/01/2006 15:04")
	},
	"inputDate": func(v any) string {
		tm := toTime(v)
		if tm == nil {
			return ""
		}

		return tm.Format("2006-01-02")
	},
	"money": formatMoney,
	"bytes": formatBytes,
	// uploadLimit returns the maximum size of an upload kind, shared with
	// the server-side checks.
	"uploadLimit": func(kind string) int {
		switch kind {
		case "logo":
			return maxLogoSize
		case "avatar":
			return maxAvatarSize
		}

		return maxPDFSize
	},
	"asset": assetURL,
	"trimScheme": func(u string) string {
		return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"), "/")
	},
	"levelMedal":   func(l *store.Level, size int) Medal { return newMedal(l, size) },
	"medalPresets": func() any { return medalPresets },
	"logoURL": func(file string) string {
		if file == "" {
			return ""
		}

		return "/logos/" + file
	},
	"isID": func(p *int64, id int64) bool { return p != nil && *p == id },
	"add":  func(a, b int) int { return a + b },
	"hasPerk": func(m map[int64]time.Time, id int64) bool {
		_, ok := m[id]

		return ok
	},
	"matrix":   func(m map[[2]int64]bool, levelID, perkID int64) bool { return m[[2]int64{levelID, perkID}] },
	"statuses": func() []string { return store.Statuses },
	"list":     func(v ...string) []string { return v },
	"networks": func() []string { return store.Networks },
	"taxModes": func() []string { return store.TaxModes },
	"network":  networkName,
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}

		return m
	},
	// actor hides staff emails from partners.
	"actor": func(a string) string {
		if strings.HasPrefix(a, "staff:") {
			return i18n.Site()
		}

		return a
	},
}

var paris = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		return time.UTC
	}

	return loc
}()

func toTime(v any) *time.Time {
	switch t := v.(type) {
	case *time.Time:
		if t == nil || t.IsZero() {
			return nil
		}

		return t
	case time.Time:
		if t.IsZero() {
			return nil
		}

		return &t
	}

	return nil
}

// formatBytes shows a file size limit, e.g. "10 Mo" or "10 MB".
func formatBytes(lang string, n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%d\u00a0%s", n>>20, i18n.T(lang, "upload.mb"))
	}

	return fmt.Sprintf("%d\u00a0%s", n>>10, i18n.T(lang, "upload.kb"))
}

func formatMoney(n int) string {
	s := fmt.Sprintf("%d", n)

	var b strings.Builder

	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}

		b.WriteRune(r)
	}

	return b.String() + " €"
}
