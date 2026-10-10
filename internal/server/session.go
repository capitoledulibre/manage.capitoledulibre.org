package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"manage/internal/store"
)

const (
	managerCookie = "manage_session"
	adminCookie   = "manage_admin"
	langCookie    = "manage_lang"
	flashCookie   = "manage_flash"
	themeCookie   = "manage_theme"
	// returnCookie remembers the admin page to go back to after viewing
	// the partner area as a partner.
	returnCookie = "manage_admin_return"

	managerSessionTTL = 30 * 24 * time.Hour
	adminSessionTTL   = 12 * time.Hour
)

// reqCtx holds the per-request identity and preferences.
type reqCtx struct {
	Lang string

	managerToken string
	Manager      *store.Manager

	adminToken string
	Admin      *store.Admin

	// Org is the organization whose dashboard is being viewed: the
	// manager's, or the one a staff member impersonates.
	Org           *store.Organization
	Impersonating bool
}

type ctxKey struct{}

func rc(r *http.Request) *reqCtx {
	return r.Context().Value(ctxKey{}).(*reqCtx)
}

// Actor describes who performs an action, for the activity log.
func (c *reqCtx) Actor() string {
	switch {
	case c.Admin != nil && (c.Impersonating || c.Manager == nil):
		return "staff:" + c.Admin.Email
	case c.Manager != nil:
		return c.Manager.Email
	}

	return "system"
}

func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)

			return
		}

		c := &reqCtx{}
		ctx := r.Context()

		if err := s.loadSessions(ctx, r, c); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		c.Lang = detectLang(r, c.Manager)

		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKey{}, c)))
	})
}

func (s *Server) loadSessions(ctx context.Context, r *http.Request, c *reqCtx) error {
	if ck, err := r.Cookie(adminCookie); err == nil {
		sess, err := s.store.GetSession(ctx, ck.Value, store.SessionAdmin)

		switch {
		case err == nil && sess.AdminID != nil:
			admin, err := s.store.GetAdmin(ctx, *sess.AdminID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}

			if admin != nil {
				c.Admin, c.adminToken = admin, ck.Value

				if sess.ImpersonateOrgID != nil {
					org, err := s.store.GetOrganization(ctx, *sess.ImpersonateOrgID)
					if err != nil && !errors.Is(err, store.ErrNotFound) {
						return err
					}

					if org != nil {
						c.Org, c.Impersonating = org, true
					}
				}
			}
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return err
		}
	}

	if ck, err := r.Cookie(managerCookie); err == nil {
		sess, err := s.store.GetSession(ctx, ck.Value, store.SessionManager)

		switch {
		case err == nil && sess.ManagerID != nil:
			m, err := s.store.GetManager(ctx, *sess.ManagerID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}

			if m != nil {
				c.Manager, c.managerToken = m, ck.Value

				if c.Org == nil {
					org, err := s.store.GetOrganization(ctx, m.OrgID)
					if err != nil {
						return err
					}

					c.Org = org
				}
			}
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return err
		}
	}

	return nil
}

// detectLang picks the display language: the explicit choice (cookie), then
// the manager's email language, then the browser preference.
func detectLang(r *http.Request, m *store.Manager) string {
	if ck, err := r.Cookie(langCookie); err == nil && validLang(ck.Value) {
		return ck.Value
	}

	if m != nil && validLang(m.Lang) {
		return m.Lang
	}

	if al := strings.ToLower(r.Header.Get("Accept-Language")); strings.HasPrefix(al, "en") {
		return "en"
	}

	return "fr"
}

func validLang(l string) bool { return l == "fr" || l == "en" }

// Color themes. "system" follows the operating system preference.
const (
	themeSystem = "system"
	themeLight  = "light"
	themeDark   = "dark"
)

func validTheme(t string) bool { return t == themeSystem || t == themeLight || t == themeDark }

func detectTheme(r *http.Request) string {
	if ck, err := r.Cookie(themeCookie); err == nil && validTheme(ck.Value) {
		return ck.Value
	}

	return themeSystem
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode})
}

// flash stores a one-shot message key displayed on the next page.
func (s *Server) flash(w http.ResponseWriter, key string) {
	s.setCookie(w, flashCookie, key, time.Minute)
}

func (s *Server) popFlash(w http.ResponseWriter, r *http.Request) string {
	ck, err := r.Cookie(flashCookie)
	if err != nil {
		return ""
	}

	s.clearCookie(w, flashCookie)

	return ck.Value
}
