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
	"sync"
	"time"
	"unicode"

	"benevoles/internal/avatar"
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
	Avatars *avatar.Fetcher

	loginByEmail *ratelimit.Limiter
	loginByIP    *ratelimit.Limiter

	base  *template.Template
	pages map[string]*template.Template

	// mailWG tracks notifications sent off the request goroutine.
	mailWG sync.WaitGroup
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
		Avatars:      avatar.New(d, store, cfg.Gravatar),
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
		"status":    statusLabel,
		"longDate":  func(iso string) string { return longDate(iso, loc) },
		"dateRange": func(from, to string) string { return dateRange(from, to, loc) },
		"deadline":  func(t *time.Time) string { return deadline(t, loc) },
		"mode":      modeLabel,
		"modeHelp":  modeHelp,
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
		"initials": userInitials,
		// named keeps the accounts that have a name. Volunteer pages list pole
		// managers by name; one who never filled theirs in would otherwise be
		// shown by e-mail address, to every volunteer and, on the login page
		// reached through an invitation link, to anyone holding the link.
		"named": func(users []db.User) []db.User {
			var out []db.User
			for _, u := range users {
				if strings.TrimSpace(u.Name) != "" {
					out = append(out, u)
				}
			}
			return out
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
		// urgent marks a slot that still needs volunteers and starts soon.
		// Highlighting every not-yet-full slot painted whole plannings amber
		// on day one, which drowned the signal.
		"urgent": func(sl db.Slot) bool {
			if sl.IsFull() {
				return false
			}
			d := time.Until(sl.StartsAt)
			return d > 0 && d < 48*time.Hour
		},
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
		return "Gelé"
	case db.StatusArchived:
		return "Archivé"
	}
	return st
}

// longDate renders a YYYY-MM-DD date as "samedi 14 novembre 2026".
func longDate(iso string, loc *time.Location) string {
	t, err := time.ParseInLocation("2006-01-02", iso, loc)
	if err != nil {
		return iso
	}
	return fmt.Sprintf("%s %d", reminders.FormatDay(t, loc), t.Year())
}

// dateRange renders two YYYY-MM-DD dates as "du 13 au 15 novembre 2026".
func dateRange(from, to string, loc *time.Location) string {
	a, err1 := time.ParseInLocation("2006-01-02", from, loc)
	b, err2 := time.ParseInLocation("2006-01-02", to, loc)
	if err1 != nil || err2 != nil {
		return from + " → " + to
	}
	monthYear := func(t time.Time) string {
		parts := strings.Fields(reminders.FormatDay(t, loc)) // weekday day month
		return parts[2] + " " + fmt.Sprint(t.Year())
	}
	switch {
	case a.Equal(b):
		return "le " + longDate(from, loc)
	case a.Month() == b.Month() && a.Year() == b.Year():
		return fmt.Sprintf("du %d au %d %s", a.Day(), b.Day(), monthYear(b))
	default:
		return fmt.Sprintf("du %d %s au %d %s", a.Day(), monthYear(a), b.Day(), monthYear(b))
	}
}

// deadline renders the moment signups close as the end of a sentence
// starting with "jusqu'au": "samedi 15 novembre à 18:00". A freeze at
// midnight reads as the whole previous day, "vendredi 14 novembre inclus",
// which is what the organiser meant and what a volunteer understands.
func deadline(t *time.Time, loc *time.Location) string {
	if t == nil {
		return ""
	}
	d := t.In(loc)
	if d.Hour() == 0 && d.Minute() == 0 {
		return reminders.FormatDay(d.AddDate(0, 0, -1), loc) + " inclus"
	}
	return reminders.FormatDay(d, loc) + " à " + d.Format("15:04")
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
		return "Seuls les administrateurs et le staff de l'événement ajoutent des bénévoles. Le pôle est masqué des autres."
	}
	return ""
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/{file}", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.Handle("GET /uploads/{file}", http.StripPrefix("/uploads/", s.Uploads.Handler()))

	// Auth
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("GET /auth/{token}", s.authConfirm)
	mux.HandleFunc("POST /auth/{token}", s.authToken)
	mux.HandleFunc("POST /logout", s.logout)

	// Volunteer
	mux.HandleFunc("GET /{$}", s.requireLogin(s.home))
	mux.HandleFunc("GET /me", s.requireLogin(s.myPlanning))
	mux.HandleFunc("GET /me.ics", s.requireLogin(s.myPlanningICS))
	mux.HandleFunc("POST /me/calendar/reset", s.requireLogin(s.calendarReset))
	// Subscription feed: authenticated by its own secret, because a calendar
	// application polls it without a session.
	// The wildcard has to span the whole segment (ServeMux rejects a literal
	// suffix after it), so the handler strips the .ics that calendar clients
	// like to see in the URL.
	mux.HandleFunc("GET /calendar/{token}", s.calendarFeed)
	mux.HandleFunc("GET /welcome", s.requireLogin(s.welcome))
	mux.HandleFunc("GET /profile", s.requireLogin(s.profileForm))
	mux.HandleFunc("POST /profile", s.requireLogin(s.profileSubmit))
	mux.HandleFunc("POST /profile/photo", s.requireLogin(s.profilePhoto))
	mux.HandleFunc("POST /profile/photo/delete", s.requireLogin(s.profilePhotoDelete))
	mux.HandleFunc("POST /profile/photo/gravatar", s.requireLogin(s.profileGravatar))
	mux.HandleFunc("POST /profile/delete", s.requireLogin(s.profileDelete))
	mux.HandleFunc("GET "+openSlotsPath, s.requireLogin(s.openSlots))
	mux.HandleFunc("GET /slots/{id}", s.requireLogin(s.slotPage))
	mux.HandleFunc("POST /slots/{id}/signup", s.requireLogin(s.slotSignup))
	mux.HandleFunc("POST /slots/{id}/unsignup", s.requireLogin(s.slotUnsignup))
	mux.HandleFunc("POST /slots/{id}/waitlist", s.requireLogin(s.slotWaitlistJoin))
	mux.HandleFunc("POST /slots/{id}/waitlist/delete", s.requireLogin(s.slotWaitlistLeave))
	mux.HandleFunc("POST /tasks/{id}/toggle", s.requireLogin(s.taskToggle))

	// Slot-level management (by id; pole-level management lives under the pole URL)
	mux.HandleFunc("POST /manage/locations/{id}/delete", s.requireLogin(s.manageLocationDelete))
	mux.HandleFunc("POST /manage/messages/{id}/delete", s.requireLogin(s.manageMessageDelete))
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
	mux.HandleFunc("GET /admin/events/new", s.requireAdmin(s.adminEventNew))
	mux.HandleFunc("POST /admin/events", s.requireAdmin(s.adminEventCreate))
	mux.HandleFunc("GET /admin/events/{event}", s.requireEventAdmin(s.adminEvent))
	mux.HandleFunc("POST /admin/events/{event}", s.requireEventAdmin(s.adminEventUpdate))
	mux.HandleFunc("POST /admin/events/{event}/image", s.requireEventAdmin(s.adminEventImage))
	mux.HandleFunc("POST /admin/events/{event}/image/delete", s.requireEventAdmin(s.adminEventImageDelete))
	mux.HandleFunc("POST /admin/events/{event}/delete", s.requireAdmin(s.adminEventDelete))
	mux.HandleFunc("POST /admin/events/{event}/poles", s.requireEventAdmin(s.adminPoleCreate))
	mux.HandleFunc("POST /admin/events/{event}/messages", s.requireEventAdmin(s.adminEventMessage))
	mux.HandleFunc("GET /admin/events/{event}/volunteers", s.requireEventAdmin(s.adminVolunteers))
	mux.HandleFunc("GET /admin/events/{event}/volunteers.csv", s.requireEventAdmin(s.adminVolunteersCSV))
	mux.HandleFunc("GET /admin/events/{event}/planning", s.requireEventAdmin(s.adminPlanning))
	mux.HandleFunc("POST /admin/events/{event}/duplicate", s.requireAdmin(s.adminEventDuplicate))
	mux.HandleFunc("POST /admin/events/{event}/staff", s.requireAdmin(s.adminStaffAdd))
	mux.HandleFunc("POST /admin/events/{event}/staff/{uid}/delete", s.requireAdmin(s.adminStaffRemove))
	mux.HandleFunc("POST /admin/poles/{id}/delete", s.requirePoleEventAdmin(s.adminPoleDelete))
	mux.HandleFunc("POST /admin/poles/{id}/managers", s.requirePoleEventAdmin(s.adminManagerAdd))
	mux.HandleFunc("POST /admin/poles/{id}/managers/{uid}/delete", s.requirePoleEventAdmin(s.adminManagerRemove))
	mux.HandleFunc("POST /admin/poles/{id}/copy", s.requirePoleEventAdmin(s.adminPoleCopy))
	mux.HandleFunc("GET /admin/users", s.requireAdmin(s.adminUsers))
	mux.HandleFunc("POST /admin/users/{id}/admin", s.requireAdmin(s.adminUserToggleAdmin))
	mux.HandleFunc("POST /admin/users/{id}/delete", s.requireAdmin(s.adminUserDelete))
	mux.HandleFunc("POST /admin/users/{id}/join", s.requireLogin(s.adminUserJoinPole))
	mux.HandleFunc("POST /admin/users/new", s.requireLogin(s.adminUserCreate))

	// Pretty URLs: /{event} and /{event}/{pole}/... Every other route above has a
	// literal first segment, so it is more specific and wins over these.
	mux.HandleFunc("GET /{event}", s.requireLogin(s.eventHome))
	mux.HandleFunc("GET /{event}/{pole}", s.requireLogin(s.polePage))
	mux.HandleFunc("GET /{event}/{pole}/{action}", s.requireLogin(s.poleDispatch))
	mux.HandleFunc("POST /{event}/{pole}/{action}", s.requireLogin(s.poleDispatch))
	mux.HandleFunc("GET /{event}/{pole}/{action}/{rest...}", s.requireLogin(s.poleDispatch))
	mux.HandleFunc("POST /{event}/{pole}/{action}/{rest...}", s.requireLogin(s.poleDispatch))

	// Cross-origin protection covers every state-changing request (CSRF).
	cop := http.NewCrossOriginProtection()
	return s.withUser(cop.Handler(mux))
}

// poleDispatch routes /{event}/{pole}/{action}[/{rest}] to the pole handlers.
func (s *Server) poleDispatch(w http.ResponseWriter, r *http.Request) {
	action, rest := r.PathValue("action"), r.PathValue("rest")
	get, post := r.Method == http.MethodGet, r.Method == http.MethodPost
	switch {
	case action == "join" && rest == "" && get:
		s.poleInvite(w, r)
	case action == "join" && rest == "" && post:
		s.poleJoin(w, r)
	case action == "leave" && rest == "" && post:
		s.poleLeave(w, r)
	case action == "manage":
		switch {
		case rest == "" && get:
			s.managePole(w, r)
		case rest == "" && post:
			s.managePoleUpdate(w, r)
		case rest == "key" && post:
			s.managePoleKey(w, r)
		case rest == "image" && post:
			s.managePoleImage(w, r)
		case rest == "image/delete" && post:
			s.managePoleImageDelete(w, r)
		case rest == "members" && post:
			s.manageMemberAdd(w, r)
		case rest == "users" && get:
			s.manageUserSearch(w, r)
		case rest == "locations" && post:
			s.manageLocationCreate(w, r)
		case rest == "messages" && post:
			s.managePoleMessage(w, r)
		case rest == "slots/new" && get:
			s.manageSlotNew(w, r)
		case rest == "slots" && post:
			s.manageSlotCreate(w, r)
		case strings.HasPrefix(rest, "members/") && strings.HasSuffix(rest, "/delete") && post:
			r.SetPathValue("uid", strings.TrimSuffix(strings.TrimPrefix(rest, "members/"), "/delete"))
			s.manageMemberRemove(w, r)
		default:
			s.notFound(w, r)
		}
	default:
		s.notFound(w, r)
	}
}

// polePathID resolves /{event}/{pole} to a pole id (0 when unknown).
func (s *Server) polePathID(r *http.Request) int64 {
	p, err := s.DB.GetPoleBySlug(r.PathValue("event"), r.PathValue("pole"))
	if err != nil {
		return 0
	}
	return p.ID
}

// --- context / auth helpers ---------------------------------------------

type ctxKey int

const userKey ctxKey = 1

func (s *Server) withUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if u, err := s.DB.SessionUser(c.Value); err == nil {
				// Fills in the profile picture of an account that has
				// none, off the request goroutine.
				s.Avatars.Refresh(u)
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

// canManage tells whether the user may administer the pole: its managers,
// the staff of its event and administrators.
func (s *Server) canManage(u *db.User, poleID int64) bool {
	return u != nil && (u.IsAdmin || s.DB.IsPoleManager(poleID, u.ID) || s.DB.IsPoleStaff(poleID, u.ID))
}

// isEventAdmin tells whether the user runs the event: administrators, and the
// staff of that event. Staff have every right inside their event except
// deleting it or creating other editions; accounts, administrator rights and
// the site settings stay with administrators.
func (s *Server) isEventAdmin(u *db.User, eventID int64) bool {
	return u != nil && (u.IsAdmin || s.DB.IsEventStaff(eventID, u.ID))
}

// requireEventAdmin guards the /admin/events/{event}/… pages that the staff of
// that event may use.
func (s *Server) requireEventAdmin(h http.HandlerFunc) http.HandlerFunc {
	return s.requireLogin(func(w http.ResponseWriter, r *http.Request) {
		ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !s.isEventAdmin(currentUser(r), ev.ID) {
			s.forbidden(w, r)
			return
		}
		h(w, r)
	})
}

// requirePoleEventAdmin guards the /admin/poles/{id}/… actions, allowed to
// whoever runs the pole's event.
func (s *Server) requirePoleEventAdmin(h http.HandlerFunc) http.HandlerFunc {
	return s.requireLogin(func(w http.ResponseWriter, r *http.Request) {
		pole, err := s.DB.GetPole(pathID(r, "id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !s.isEventAdmin(currentUser(r), pole.EventID) {
			s.forbidden(w, r)
			return
		}
		h(w, r)
	})
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
	data["SourceURL"] = s.DB.GetSetting("source_url", "")
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

// dropImage unlinks an uploaded file once no row points at it any more.
// Duplicating an event or copying a pole makes two rows share one file, so
// clearing one of them must not take the bytes away from the other.
func (s *Server) dropImage(name string) {
	if !s.DB.ImageInUse(name) {
		s.Uploads.Remove(name)
	}
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

// safePath tells whether a user-supplied "back"/"next" value can be used as a
// redirect target. It must be a rooted path on this site: a leading "//"
// (or "/\") is a protocol-relative URL and would send the visitor off-site.
func safePath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, `/\`)
}

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

// userInitials returns one or two uppercase letters for the avatar of an
// account with no picture, derived from the name or, failing that, from the
// e-mail. It takes a value so that templates can pass either a db.User or a
// *db.User.
func userInitials(u db.User) string {
	source := strings.TrimSpace(u.Name)
	if source == "" {
		source = strings.SplitN(u.Email, "@", 2)[0]
		source = strings.NewReplacer(".", " ", "-", " ", "_", " ", "+", " ").Replace(source)
	}
	var out []rune
	for _, word := range strings.Fields(source) {
		for _, r := range word {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}
