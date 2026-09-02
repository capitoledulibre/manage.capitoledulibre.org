// Package web is the HTTP layer: routing, sessions, rendering.
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"benevoles/internal/config"
	"benevoles/internal/db"
	"benevoles/internal/ratelimit"
	"benevoles/internal/reminders"
	"benevoles/internal/upload"
)

// Mailer delivers e-mails.
type Mailer interface {
	Send(to, subject, body string) error
}

//go:embed templates static
var assets embed.FS

const (
	sessionCookie = "session"
	flashCookie   = "flash"
	sessionTTL    = 90 * 24 * time.Hour
	magicLinkTTL  = 30 * time.Minute
)

// Server holds the dependencies of the HTTP handlers.
type Server struct {
	DB      *db.DB
	Cfg     config.Config
	Mailer  Mailer
	Uploads *upload.Store

	loginByEmail *ratelimit.Limiter
	loginByIP    *ratelimit.Limiter

	base  *template.Template
	pages map[string]*template.Template
}

// M is a convenience alias for template data.
type M map[string]any

// New builds the server and parses templates.
func New(d *db.DB, cfg config.Config, mailer Mailer) (*Server, error) {
	store, err := upload.New(cfg.UploadDir)
	if err != nil {
		return nil, fmt.Errorf("upload directory: %w", err)
	}
	s := &Server{
		DB: d, Cfg: cfg, Mailer: mailer, Uploads: store,
		loginByEmail: ratelimit.New(3, 15*time.Minute),
		loginByIP:    ratelimit.New(30, 15*time.Minute),
	}
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) parseTemplates() error {
	loc := s.Cfg.Timezone
	funcs := template.FuncMap{
		"time":     func(t time.Time) string { return t.In(loc).Format("15:04") },
		"day":      func(t time.Time) string { return reminders.FormatDay(t, loc) },
		"dayKey":   func(t time.Time) string { return t.In(loc).Format("2006-01-02") },
		"datetime": func(t time.Time) string { return t.In(loc).Format("02/01/2006 15:04") },
		"date":     func(t time.Time) string { return t.In(loc).Format("02/01/2006") },
		"inputDT": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return ""
			}
			return t.In(loc).Format("2006-01-02T15:04")
		},
		"inputDTv": func(t time.Time) string { return t.In(loc).Format("2006-01-02T15:04") },
		"parseDay": func(key string) string {
			t, err := time.ParseInLocation("2006-01-02", key, loc)
			if err != nil {
				return key
			}
			return reminders.FormatDay(t, loc)
		},
		"pct": func(filled, capacity int) int {
			if capacity == 0 {
				return 100
			}
			return filled * 100 / capacity
		},
		"status":   statusLabel,
		"mode":     modeLabel,
		"modeHelp": modeHelp,
		"firstName": func(u *db.User) string {
			if u == nil {
				return ""
			}
			name := strings.TrimSpace(u.Name)
			if name == "" {
				return u.Email
			}
			return strings.Fields(name)[0]
		},
		"nl2br": func(s string) template.HTML {
			return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>"))
		},
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i + 1
			}
			return out
		},
		"add": func(a, b int) int { return a + b },
		"dict": func(kv ...any) M {
			m := M{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m
		},
	}
	base, err := template.New("").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/partials/*.html")
	if err != nil {
		return err
	}
	s.base = base
	s.pages = map[string]*template.Template{}
	entries, err := fs.ReadDir(assets, "templates/pages")
	if err != nil {
		return err
	}
	for _, e := range entries {
		clone, err := base.Clone()
		if err != nil {
			return err
		}
		t, err := clone.ParseFS(assets, "templates/pages/"+e.Name())
		if err != nil {
			return fmt.Errorf("template %s: %w", e.Name(), err)
		}
		s.pages[strings.TrimSuffix(e.Name(), ".html")] = t
	}
	return nil
}

// clientIP returns the caller address, honouring X-Forwarded-For from a proxy.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func statusLabel(st string) string {
	switch st {
	case db.StatusDraft:
		return "Brouillon"
	case db.StatusOpen:
		return "Inscriptions ouvertes"
	case db.StatusFrozen:
		return "Gelée"
	case db.StatusArchived:
		return "Archivée"
	}
	return st
}

func modeLabel(m string) string {
	switch m {
	case db.ModePublic:
		return "Public"
	case db.ModePrivate:
		return "Sur lien d'invitation"
	case db.ModeInvitation:
		return "Sur invitation"
	case db.ModeRestricted:
		return "Restreint"
	}
	return m
}

func modeHelp(m string) string {
	switch m {
	case db.ModePublic:
		return "N'importe quel bénévole inscrit peut rejoindre le pôle."
	case db.ModePrivate:
		return "Les bénévoles rejoignent via un lien d'invitation que vous leur transmettez."
	case db.ModeInvitation:
		return "Les responsables et administrateurs ajoutent eux-mêmes les bénévoles."
	case db.ModeRestricted:
		return "Seuls les administrateurs ajoutent des bénévoles. Le pôle est masqué des autres."
	}
	return ""
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", s.Uploads.Handler()))

	// Auth
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("GET /auth/{token}", s.authToken)
	mux.HandleFunc("POST /logout", s.logout)

	// Volunteer
	mux.HandleFunc("GET /{$}", s.requireLogin(s.home))
	mux.HandleFunc("GET /me", s.requireLogin(s.myPlanning))
	mux.HandleFunc("GET /me.ics", s.requireLogin(s.myPlanningICS))
	mux.HandleFunc("GET /profile", s.requireLogin(s.profileForm))
	mux.HandleFunc("POST /profile", s.requireLogin(s.profileSubmit))
	mux.HandleFunc("POST /profile/delete", s.requireLogin(s.profileDelete))
	mux.HandleFunc("GET /poles/{id}", s.requireLogin(s.polePage))
	mux.HandleFunc("GET /poles/{id}/join", s.requireLogin(s.poleInvite))
	mux.HandleFunc("POST /poles/{id}/join", s.requireLogin(s.poleJoin))
	mux.HandleFunc("POST /poles/{id}/leave", s.requireLogin(s.poleLeave))
	mux.HandleFunc("GET /slots/{id}", s.requireLogin(s.slotPage))
	mux.HandleFunc("POST /slots/{id}/signup", s.requireLogin(s.slotSignup))
	mux.HandleFunc("POST /slots/{id}/unsignup", s.requireLogin(s.slotUnsignup))
	mux.HandleFunc("POST /tasks/{id}/toggle", s.requireLogin(s.taskToggle))

	// Pole management (managers + admins)
	mux.HandleFunc("GET /manage/poles/{id}", s.requireLogin(s.managePole))
	mux.HandleFunc("POST /manage/poles/{id}", s.requireLogin(s.managePoleUpdate))
	mux.HandleFunc("POST /manage/poles/{id}/key", s.requireLogin(s.managePoleKey))
	mux.HandleFunc("POST /manage/poles/{id}/image", s.requireLogin(s.managePoleImage))
	mux.HandleFunc("POST /manage/poles/{id}/image/delete", s.requireLogin(s.managePoleImageDelete))
	mux.HandleFunc("GET /manage/poles/{id}/users", s.requireLogin(s.manageUserSearch))
	mux.HandleFunc("POST /manage/poles/{id}/members", s.requireLogin(s.manageMemberAdd))
	mux.HandleFunc("POST /manage/poles/{id}/members/{uid}/delete", s.requireLogin(s.manageMemberRemove))
	mux.HandleFunc("POST /manage/poles/{id}/locations", s.requireLogin(s.manageLocationCreate))
	mux.HandleFunc("POST /manage/locations/{id}/delete", s.requireLogin(s.manageLocationDelete))
	mux.HandleFunc("POST /manage/poles/{id}/messages", s.requireLogin(s.managePoleMessage))
	mux.HandleFunc("POST /manage/messages/{id}/delete", s.requireLogin(s.manageMessageDelete))
	mux.HandleFunc("GET /manage/poles/{id}/slots/new", s.requireLogin(s.manageSlotNew))
	mux.HandleFunc("POST /manage/poles/{id}/slots", s.requireLogin(s.manageSlotCreate))
	mux.HandleFunc("GET /manage/slots/{id}", s.requireLogin(s.manageSlotEdit))
	mux.HandleFunc("POST /manage/slots/{id}", s.requireLogin(s.manageSlotUpdate))
	mux.HandleFunc("POST /manage/slots/{id}/delete", s.requireLogin(s.manageSlotDelete))
	mux.HandleFunc("POST /manage/slots/{id}/tasks", s.requireLogin(s.manageTaskCreate))
	mux.HandleFunc("POST /manage/tasks/{id}/delete", s.requireLogin(s.manageTaskDelete))
	mux.HandleFunc("POST /manage/slots/{id}/messages", s.requireLogin(s.manageSlotMessage))
	mux.HandleFunc("POST /manage/slots/{id}/volunteers", s.requireLogin(s.manageSlotAddVolunteer))
	mux.HandleFunc("POST /manage/slots/{id}/volunteers/{uid}/delete", s.requireLogin(s.manageSlotRemoveVolunteer))

	// Administration
	mux.HandleFunc("GET /admin", s.requireAdmin(s.adminHome))
	mux.HandleFunc("POST /admin/settings", s.requireAdmin(s.adminSettings))
	mux.HandleFunc("POST /admin/logo", s.requireAdmin(s.adminLogo))
	mux.HandleFunc("POST /admin/logo/delete", s.requireAdmin(s.adminLogoDelete))
	mux.HandleFunc("GET /admin/editions/{id}/planning", s.requireAdmin(s.adminPlanning))
	mux.HandleFunc("POST /admin/editions/{id}/duplicate", s.requireAdmin(s.adminEditionDuplicate))
	mux.HandleFunc("POST /admin/poles/{id}/copy", s.requireAdmin(s.adminPoleCopy))
	mux.HandleFunc("GET /admin/editions/new", s.requireAdmin(s.adminEditionNew))
	mux.HandleFunc("POST /admin/editions", s.requireAdmin(s.adminEditionCreate))
	mux.HandleFunc("GET /admin/editions/{id}", s.requireAdmin(s.adminEdition))
	mux.HandleFunc("POST /admin/editions/{id}", s.requireAdmin(s.adminEditionUpdate))
	mux.HandleFunc("POST /admin/editions/{id}/delete", s.requireAdmin(s.adminEditionDelete))
	mux.HandleFunc("POST /admin/editions/{id}/poles", s.requireAdmin(s.adminPoleCreate))
	mux.HandleFunc("POST /admin/editions/{id}/messages", s.requireAdmin(s.adminEditionMessage))
	mux.HandleFunc("GET /admin/editions/{id}/volunteers", s.requireAdmin(s.adminVolunteers))
	mux.HandleFunc("GET /admin/editions/{id}/volunteers.csv", s.requireAdmin(s.adminVolunteersCSV))
	mux.HandleFunc("POST /admin/poles/{id}/delete", s.requireAdmin(s.adminPoleDelete))
	mux.HandleFunc("POST /admin/poles/{id}/managers", s.requireAdmin(s.adminManagerAdd))
	mux.HandleFunc("POST /admin/poles/{id}/managers/{uid}/delete", s.requireAdmin(s.adminManagerRemove))
	mux.HandleFunc("GET /admin/users", s.requireAdmin(s.adminUsers))
	mux.HandleFunc("POST /admin/users/{id}/admin", s.requireAdmin(s.adminUserToggleAdmin))
	mux.HandleFunc("POST /admin/users/{id}/delete", s.requireAdmin(s.adminUserDelete))

	// Cross-origin protection covers every state-changing request (CSRF).
	cop := http.NewCrossOriginProtection()
	return s.withUser(cop.Handler(mux))
}

// --- context / auth helpers ---------------------------------------------

type ctxKey int

const userKey ctxKey = 1

func (s *Server) withUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if u, err := s.DB.SessionUser(c.Value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), userKey, &u))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// currentUser returns the logged-in user or nil.
func currentUser(r *http.Request) *db.User {
	u, _ := r.Context().Value(userKey).(*db.User)
	return u
}

func (s *Server) requireLogin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r) == nil {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next := r.URL.RequestURI()
			http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
			return
		}
		h(w, r)
	}
}

func (s *Server) requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return s.requireLogin(func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r).IsAdmin {
			s.forbidden(w, r)
			return
		}
		h(w, r)
	})
}

// canManage tells whether the user may administer the pole.
func (s *Server) canManage(u *db.User, poleID int64) bool {
	return u != nil && (u.IsAdmin || s.DB.IsPoleManager(poleID, u.ID))
}

// --- rendering ----------------------------------------------------------

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data M) {
	t, ok := s.pages[page]
	if !ok {
		s.serverError(w, r, fmt.Errorf("unknown page %q", page))
		return
	}
	if data == nil {
		data = M{}
	}
	data["User"] = currentUser(r)
	data["Org"] = s.DB.GetSetting("org_name", "Bénévoles")
	data["Flash"] = s.popFlash(w, r)
	data["Path"] = r.URL.Path
	data["DevMode"] = s.Cfg.DevMode
	data["BaseURL"] = s.Cfg.BaseURL
	data["Logo"] = s.DB.GetSetting("org_logo", "")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("render %s: %v", page, err)
	}
}

// fragment renders a partial for htmx responses.
func (s *Server) fragment(w http.ResponseWriter, name string, data M) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.base.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("fragment %s: %v", name, err)
	}
}

func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	s.render(w, r, "error", M{"Title": "Page introuvable", "Message": "Cette page n'existe pas ou a été supprimée."})
}

func (s *Server) forbidden(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusForbidden)
	s.render(w, r, "error", M{"Title": "Accès refusé", "Message": "Vous n'avez pas les droits pour accéder à cette page."})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	w.WriteHeader(http.StatusInternalServerError)
	s.render(w, r, "error", M{"Title": "Erreur", "Message": "Une erreur inattendue s'est produite. Réessayez dans un instant."})
}

// fail maps a database error to the right error page.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, db.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	s.serverError(w, r, err)
}

// --- flash --------------------------------------------------------------

func (s *Server) flash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: url.QueryEscape(msg), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.Cfg.Secure})
}

func (s *Server) popFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.Cfg.Secure})
	v, _ := url.QueryUnescape(c.Value)
	return v
}

// --- form helpers -------------------------------------------------------

func pathID(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n
}

func formInt(r *http.Request, name string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))
	if err != nil {
		return def
	}
	return n
}

func formInt64(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue(name)), 10, 64)
	return n
}

// formTime parses a datetime-local input in the display timezone.
func (s *Server) formTime(r *http.Request, name string) (time.Time, bool) {
	v := strings.TrimSpace(r.FormValue(name))
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", v, s.Cfg.Timezone)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (s *Server) formTimePtr(r *http.Request, name string) *time.Time {
	t, ok := s.formTime(r, name)
	if !ok {
		return nil
	}
	return &t
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	if isHX(r) {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
