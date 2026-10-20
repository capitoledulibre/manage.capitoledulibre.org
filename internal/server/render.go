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
	"slices"
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
	// TitleText, when set, is the title as is: a pole or edition name
	// rather than an i18n key.
	TitleText string
	// Area selects the navigation: "public", "partner" or "admin".
	Area string
	// Flash is the i18n key of a one-shot message, FlashText its text and
	// FlashError whether it reports a failure.
	Flash      string
	FlashText  string
	FlashError bool
	Path       string
	// RequestURI is the path with its query string, used to come back to
	// the same page after switching language.
	RequestURI string
	// PartnerSpace is where the partner's space of the edition starts, for
	// the partner navigation's links.
	PartnerSpace string
	// Theme is "system", "light" or "dark".
	Theme string
	Dev   bool

	Manager *store.Manager
	// Person is who is signed in with an e-mail link (or, on volunteer
	// pages, the person of a staff member).
	Person        *store.Person
	Org           *store.Organization
	Admin         *store.Admin
	Impersonating bool

	// Site is the event identity (name, website, contact, logo).
	Site Site

	// Vol is the navigation of the volunteer space, on its pages.
	Vol *volunteerNav
	// StaffPerson is set when Person comes from the staff login, not from
	// an e-mail link: logging out then ends the staff session.
	StaffPerson bool
	// SiteNav is the navigation of the site, for a signed-in person.
	SiteNav *siteNav

	// D holds page-specific data.
	D any
}

func (s *Server) page(r *http.Request, title string) *Page {
	c := rc(r)

	area := c.area

	switch {
	case area == "" && strings.HasPrefix(r.URL.Path, "/admin"):
		// The staff's login pages.
		area = "admin"
	case area == "partner" && c.Org == nil, area == "volunteer" && c.Person == nil, area == "":
		area = "public"
	}

	lang := c.Lang
	if area == "admin" {
		// The staff interface is French only.
		lang = "fr"
	}

	partnerSpace := ""
	if slug := r.PathValue("event"); area == "partner" && slug != "" {
		partnerSpace = "/" + slug + "/partner"
	}

	p := &Page{
		Lang:          lang,
		Title:         title,
		Area:          area,
		Path:          r.URL.Path,
		RequestURI:    r.URL.RequestURI(),
		PartnerSpace:  partnerSpace,
		Theme:         detectTheme(r),
		Site:          s.site,
		Dev:           s.cfg.Dev,
		Manager:       c.Manager,
		Person:        c.Person,
		Org:           c.Org,
		Admin:         c.Admin,
		Impersonating: c.Impersonating,
	}

	if area == "volunteer" {
		p.Vol = s.volunteerNav(r)
		p.StaffPerson = c.personToken == "" && c.Admin != nil
	}

	p.SiteNav = s.siteNav(r)

	return p
}

// Home is where the logo leads: the first section of the navigation, else
// the entry of the area.
func (p *Page) Home() string {
	switch {
	case p.SiteNav != nil && len(p.SiteNav.Sections) > 0:
		return p.SiteNav.Sections[0].URL
	case p.Area == "partner":
		return "/partners/"
	case p.Area == "volunteer":
		return "/volunteers/"
	}

	return "/"
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, p *Page) error {
	var args []string

	p.Flash, args = s.popFlash(w, r)
	if p.Flash != "" && i18n.Has(p.Flash) {
		// A text without a verb ("Créneau créé.") takes no argument.
		p.FlashText, p.FlashError = i18n.T(p.Lang, p.Flash), errorFlash(p.Flash)
		if len(args) > 0 && strings.Contains(p.FlashText, "%") {
			p.FlashText = i18n.T(p.Lang, p.Flash, anys(args)...)
		}
	}

	var buf bytes.Buffer
	if err := s.tmpl.render(&buf, name, p); err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)

	return err
}

// errorFlashes are the flash messages that report a failure.
var errorFlashes = map[string]bool{
	"flash.upload_bad_logo": true, "flash.upload_bad_pdf": true, "flash.upload_too_large": true, "flash.upload_missing": true,
	"flash.last_manager": true, "flash.invalid_email": true, "flash.confirm_mismatch": true, "flash.person_staff": true,
	"flash.person_email_taken": true,
}

// errorFlash tells whether a flash key reports a failure: one of
// errorFlashes, or a volunteer error ("vol.err.…").
func errorFlash(key string) bool { return errorFlashes[key] || strings.HasPrefix(key, "vol.err.") }

func anys(v []string) []any {
	out := make([]any, len(v))
	for i, x := range v {
		out[i] = x
	}

	return out
}

type templates struct {
	pages map[string]*template.Template
	// shared holds the layout and the partials alone, to render a partial
	// as an htmx fragment.
	shared *template.Template
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

	if t.shared, err = template.New("shared").Funcs(funcs).ParseFS(web.Templates, shared...); err != nil {
		return nil, fmt.Errorf("parse partials: %w", err)
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

// fragment renders one partial alone, as the answer to an htmx request.
func (s *Server) fragment(w http.ResponseWriter, name string, data any) error {
	var buf bytes.Buffer
	if err := s.tmpl.shared.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)

	return err
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
	// Volunteer pages: days and hours in the reader's language.
	"dayName":    dayName,
	"dayKeyName": dayKeyName,
	"clock":      clock,
	"dateSpan":   dateSpan,
	"deadline":   deadline,
	"slotWhen":   slotWhen,
	"tn":         plural,
	"named":      named,
	"hasPrefix":  strings.HasPrefix,
	"logLine":    logLine,
	"in":         func(list []string, v string) bool { return slices.Contains(list, v) },
	// minutes renders a duration as "30 min", "2 h" or "1 h 30".
	"minutes": func(n int) string {
		switch {
		case n < 60:
			return fmt.Sprintf("%d\u00a0min", n)
		case n%60 == 0:
			return fmt.Sprintf("%d\u00a0h", n/60)
		}

		return fmt.Sprintf("%d\u00a0h\u00a0%02d", n/60, n%60)
	},
	// inputDT renders a time for a datetime-local field, in the event's
	// time zone.
	"inputDT": func(v any) string {
		t := toTime(v)
		if t == nil {
			return ""
		}

		return t.In(paris).Format("2006-01-02T15:04")
	},
	// urgent marks a slot still short of volunteers that starts within two
	// days. Highlighting every slot not yet full painted whole plannings
	// amber, which drowned the signal.
	"urgent": func(sl *store.Slot) bool { return !sl.IsFull() && isLate(sl) },
	"late":   isLate,
	// queryValue escapes a value going in a query string: a URL with its
	// own query, say, whose "&" html/template leaves as it is there.
	"queryValue": url.QueryEscape,
	"phone":      formatPhone,
	"telHref":    telHref,
	"nl2br": func(v string) template.HTML {
		return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(v), "\n", "<br>"))
	},
	"seq": func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i + 1
		}

		return out
	},
	"tasksDone": func(tasks []*store.SlotTask) int {
		n := 0

		for _, t := range tasks {
			if t.Done() {
				n++
			}
		}

		return n
	},
	"slotOver": func(sl *store.Slot) bool { return time.Now().After(sl.EndsAt) },
	"pct": func(filled, capacity int) int {
		if capacity == 0 {
			return 100
		}

		return min(filled*100/capacity, 100)
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

func init() { funcs["due"] = due }

// dueText is a partner deadline as shown: its date, how soon, and a tone
// that only turns red when it is close or passed.
type dueText struct {
	Text  string
	Class string
}

func due(lang string, v any) dueText {
	tm := toTime(v)
	if tm == nil {
		return dueText{}
	}

	now := time.Now().In(paris)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	day := time.Date(tm.Year(), tm.Month(), tm.Day(), 0, 0, 0, 0, time.UTC)
	days := int(day.Sub(today).Hours() / 24)
	date := funcs["date"].(func(string, any) string)(lang, day)

	switch {
	case days < 0:
		return dueText{i18n.T(lang, "due.passed", date), "text-red-700 font-medium"}
	case days == 0:
		return dueText{i18n.T(lang, "due.today"), "text-red-700 font-medium"}
	case days <= 7:
		return dueText{plural(lang, "due.soon", days, date), "text-amber-800"}
	default:
		return dueText{i18n.T(lang, "due.before", date), "text-gray-600"}
	}
}

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
