package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"manage/internal/store"
)

const (
	personCookie = "manage_session"
	adminCookie  = "manage_admin"
	langCookie   = "manage_lang"
	flashCookie  = "manage_flash"
	themeCookie  = "manage_theme"
	// returnCookie remembers the admin page to go back to after viewing
	// the partner area as a partner.
	returnCookie = "manage_admin_return"

	// A person session slides: volunteers come back a few times a year,
	// and a fixed expiry sent them through the e-mail link again right
	// before the event. It is renewed at most once a day, so page views do
	// not write to the database.
	personSessionTTL  = 90 * 24 * time.Hour
	sessionRenewEvery = 24 * time.Hour
	adminSessionTTL   = 12 * time.Hour
)

// reqCtx holds the per-request identity and preferences.
type reqCtx struct {
	Lang string

	// Person is who logged in with an e-mail link. Manager is set when that
	// person is looking at one of the partner organizations they manage.
	personToken string
	// renewPerson is set when the person session was pushed back and its
	// cookie must follow.
	renewPerson bool
	Person      *store.Person
	Manager     *store.Manager

	adminToken string
	Admin      *store.Admin

	// Org is the organization whose dashboard is being viewed: the
	// manager's, or the one a staff member impersonates.
	Org           *store.Organization
	Impersonating bool

	// area is the space of the page, set by the access check of its route
	// ("admin", "partner", "volunteer"): the URLs alone no longer tell, an
	// edition's pages of every kind sitting below its slug.
	area string
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

		c.Lang = detectLang(r, c.Person)

		if c.renewPerson {
			s.setCookie(w, personCookie, c.personToken, personSessionTTL)
		}

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

	if ck, err := r.Cookie(personCookie); err == nil {
		sess, err := s.store.GetSession(ctx, ck.Value, store.SessionPerson)

		switch {
		case err == nil:
			person, err := s.store.GetPerson(ctx, sess.PersonID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}

			if person == nil {
				return nil
			}

			c.Person, c.personToken = person, ck.Value

			if time.Until(sess.ExpiresAt) < personSessionTTL-sessionRenewEvery {
				if err := s.store.ExtendSession(ctx, ck.Value, personSessionTTL); err != nil {
					return err
				}

				c.renewPerson = true
			}

			if sess.ManagerID == nil {
				return nil
			}

			m, err := s.store.GetManager(ctx, *sess.ManagerID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}

			// The organization must still be this person's: a contact moved
			// to another address belongs to someone else now.
			if m == nil || m.PersonID != person.ID {
				return nil
			}

			c.Manager = m

			if c.Org == nil {
				org, err := s.store.GetOrganization(ctx, m.OrgID)
				if err != nil {
					return err
				}

				c.Org = org
			}
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
	}

	return nil
}

// detectLang picks the display language: the explicit choice (cookie), then
// the person's e-mail language, then the browser preference.
func detectLang(r *http.Request, p *store.Person) string {
	if ck, err := r.Cookie(langCookie); err == nil && validLang(ck.Value) {
		return ck.Value
	}

	if p != nil && validLang(p.Lang) {
		return p.Lang
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

// flash stores a one-shot message displayed on the next page: an i18n key
// and the arguments of its text.
func (s *Server) flash(w http.ResponseWriter, key string, args ...string) {
	s.setCookie(w, flashCookie, url.QueryEscape(strings.Join(append([]string{key}, args...), "\x1f")), time.Minute)
}

func (s *Server) popFlash(w http.ResponseWriter, r *http.Request) (string, []string) {
	ck, err := r.Cookie(flashCookie)
	if err != nil {
		return "", nil
	}

	s.clearCookie(w, flashCookie)

	v, err := url.QueryUnescape(ck.Value)
	if err != nil {
		return "", nil
	}

	parts := strings.Split(v, "\x1f")

	return parts[0], parts[1:]
}
