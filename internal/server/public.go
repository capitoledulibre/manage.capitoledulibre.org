package server

import (
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode"

	"manage/internal/i18n"
	pmail "manage/internal/mail"
	"manage/internal/store"
)

const (
	loginTokenTTL  = 30 * time.Minute
	inviteTokenTTL = 7 * 24 * time.Hour
	// maxLoginEmails limits login emails per address and hour.
	maxLoginEmails = 5
)

func (s *Server) publicRoutes(mux *http.ServeMux) {
	mux.Handle("GET /{$}", s.wrap(s.loginPage))
	mux.Handle("POST /login", s.wrap(s.loginSubmit))
	mux.Handle("GET /auth/{token}", s.wrap(s.magicConfirm))
	mux.Handle("POST /auth/{token}", s.wrap(s.magicConsume))
	mux.Handle("POST /logout", s.wrap(s.logout))
	mux.Handle("GET /lang/{lang}", s.wrap(s.switchLang))
	mux.Handle("GET /theme/{theme}", s.wrap(s.switchTheme))
	mux.Handle("GET /jobs", s.wrap(s.publicJobs))
	mux.Handle("GET /logos/{file}", s.wrap(s.publicLogo))
	mux.Handle("GET /avatars/{file}", s.wrap(s.avatar))
	mux.Handle("GET /api/partners.json", s.wrap(s.exportJSON))
}

// loginPage is the home page: the login form, or where a logged-in person
// goes next. A partner contact goes to their partner space (or picks an
// organization when none is selected); someone with no space yet is told so
// instead of being shown the login form again.
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) error {
	c := rc(r)

	switch {
	case c.Manager != nil:
		http.Redirect(w, r, "/partners/", http.StatusSeeOther)

		return nil
	case c.Person != nil:
		managers, err := s.store.ManagersByEmail(r.Context(), c.Person.Email)
		if err != nil {
			return err
		}

		if len(managers) > 0 {
			http.Redirect(w, r, "/partners/choose", http.StatusSeeOther)

			return nil
		}

		p := s.page(r, "home.title")
		p.D = c.Person

		return s.render(w, r, "public/home", p)
	}

	return s.render(w, r, "public/login", s.page(r, "login.title"))
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) error {
	email := store.NormalizeEmail(r.FormValue("email"))
	p := s.page(r, "login.title")

	if !validEmail(email) {
		p.D = map[string]any{"Error": "login.invalid_email", "Email": email}

		return s.render(w, r, "public/login", p)
	}

	// The response is identical whether the email is known or not, so the
	// form cannot be used to discover managers.
	if err := s.sendLoginLink(r, email, rc(r).Lang); err != nil {
		slog.Error("send login link", "email", email, "err", err)
	}

	p.D = map[string]any{"Email": email}

	return s.render(w, r, "public/login_sent", p)
}

func (s *Server) sendLoginLink(r *http.Request, email, lang string) error {
	ctx := r.Context()

	// Only known people get a link; the answer on screen is the same either
	// way, so the form does not tell who is known.
	person, err := s.store.PersonByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	n, err := s.store.CountRecentMagicTokens(ctx, email, time.Hour)
	if err != nil {
		return err
	}

	if n >= maxLoginEmails {
		slog.Warn("login link rate limited", "email", email)

		return nil
	}

	token, err := s.store.CreateMagicToken(ctx, email, loginTokenTTL)
	if err != nil {
		return err
	}

	lang = person.Lang

	return s.mailer.Send(pmail.Message{
		To:      []string{email},
		Subject: i18n.T(lang, "mail.login.subject"),
		Body:    i18n.T(lang, "mail.login.body", s.cfg.BaseURL+"/auth/"+token, int(loginTokenTTL.Minutes())),
	})
}

// sendInvite emails a long-lived magic link to a newly added manager.
func (s *Server) sendInvite(r *http.Request, m *store.Manager, org *store.Organization, inviter string) error {
	token, err := s.store.CreateMagicToken(r.Context(), m.Email, inviteTokenTTL)
	if err != nil {
		return err
	}

	return s.mailer.Send(pmail.Message{
		To:      []string{m.Email},
		Subject: i18n.T(m.Lang, "mail.invite.subject", org.Name),
		Body:    i18n.T(m.Lang, "mail.invite.body", inviter, org.Name, s.cfg.BaseURL+"/auth/"+token, s.cfg.BaseURL),
	})
}

// magicConfirm shows a confirmation button instead of logging in directly:
// mail scanners that pre-fetch links would otherwise burn the token.
func (s *Server) magicConfirm(w http.ResponseWriter, r *http.Request) error {
	p := s.page(r, "login.title")

	email, err := s.store.PeekMagicToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		p.D = map[string]any{"Error": "login.link_invalid"}

		return s.render(w, r, "public/login", p)
	}

	if err != nil {
		return err
	}

	p.D = map[string]any{"Email": email, "Token": r.PathValue("token")}

	return s.render(w, r, "public/login_confirm", p)
}

func (s *Server) magicConsume(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	email, err := s.store.ConsumeMagicToken(ctx, r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		p := s.page(r, "login.title")
		p.D = map[string]any{"Error": "login.link_invalid"}

		return s.render(w, r, "public/login", p)
	}

	if err != nil {
		return err
	}

	person, err := s.store.PersonByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		p := s.page(r, "login.title")
		p.D = map[string]any{"Error": "login.link_invalid"}

		return s.render(w, r, "public/login", p)
	}

	if err != nil {
		return err
	}

	managers, err := s.store.ManagersByEmail(ctx, email)
	if err != nil {
		return err
	}

	if c := rc(r); c.personToken != "" {
		_ = s.store.DeleteSession(ctx, c.personToken)
	}

	// One session for the person; a partner contact starts on their first
	// organization, and picks another one when they manage several.
	sess := &store.Session{Kind: store.SessionPerson, PersonID: person.ID}
	if len(managers) > 0 {
		sess.ManagerID = &managers[0].ID
	}

	token, err := s.store.CreateSession(ctx, sess, personSessionTTL)
	if err != nil {
		return err
	}

	_ = s.store.TouchPersonLogin(ctx, person.ID)
	s.setCookie(w, personCookie, token, personSessionTTL)

	switch {
	case len(managers) > 1:
		http.Redirect(w, r, "/partners/choose", http.StatusSeeOther)
	case len(managers) == 1:
		http.Redirect(w, r, "/partners/", http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}

	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if c := rc(r); c.personToken != "" {
		if err := s.store.DeleteSession(r.Context(), c.personToken); err != nil {
			return err
		}
	}

	s.clearCookie(w, personCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)

	return nil
}

func (s *Server) switchLang(w http.ResponseWriter, r *http.Request) error {
	lang := r.PathValue("lang")
	if !validLang(lang) {
		return errNotFound
	}

	// A GET only changes the display language (cookie). The language of a
	// manager's emails is changed on the profile page, with a POST, so a
	// third-party site cannot change it.
	s.setCookie(w, langCookie, lang, 365*24*time.Hour)

	http.Redirect(w, r, safeRedirect(r.URL.Query().Get("next"), "/"), http.StatusSeeOther)

	return nil
}

func (s *Server) switchTheme(w http.ResponseWriter, r *http.Request) error {
	theme := r.PathValue("theme")
	if !validTheme(theme) {
		return errNotFound
	}

	s.setCookie(w, themeCookie, theme, 365*24*time.Hour)
	http.Redirect(w, r, safeRedirect(r.URL.Query().Get("next"), "/"), http.StatusSeeOther)

	return nil
}

func (s *Server) publicJobs(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	event, err := s.store.CurrentEvent(ctx)
	if errors.Is(err, store.ErrNotFound) {
		p := s.page(r, "jobs.title")
		p.D = map[string]any{"Groups": nil}

		return s.render(w, r, "public/jobs", p)
	}

	if err != nil {
		return err
	}

	jobs, err := s.store.ListPublicJobs(ctx, event.ID)
	if err != nil {
		return err
	}

	// Group offers by organization, keeping the level ordering.
	type group struct {
		Org   *store.Organization
		Level string
		Jobs  []*store.JobOffer
	}

	var groups []*group

	index := map[int64]*group{}

	for _, j := range jobs {
		g, ok := index[j.Org.ID]
		if !ok {
			g = &group{Org: j.Org, Level: j.Level}
			index[j.Org.ID] = g
			groups = append(groups, g)
		}

		g.Jobs = append(g.Jobs, j.Job)
	}

	p := s.page(r, "jobs.title")
	p.D = map[string]any{"Event": event, "Groups": groups}

	return s.render(w, r, "public/jobs", p)
}

// publicLogo serves the current logos of organizations that are (or were)
// confirmed partners. Logos of prospects are only visible to the staff and
// to the organization itself.
func (s *Server) publicLogo(w http.ResponseWriter, r *http.Request) error {
	file := r.PathValue("file")

	org, err := s.store.OrganizationByLogoFile(r.Context(), file)
	if err != nil {
		return err
	}

	confirmed, err := s.store.OrganizationHasConfirmedPartnership(r.Context(), org.ID)
	if err != nil {
		return err
	}

	c := rc(r)

	switch {
	case confirmed:
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("Access-Control-Allow-Origin", "*")
	case c.Admin != nil || (c.Org != nil && c.Org.ID == org.ID):
		w.Header().Set("Cache-Control", "private, no-cache")
	default:
		return errNotFound
	}

	s.files.Serve(w, r, file, "")

	return nil
}

// avatar serves a staff avatar to the staff, and to partners whose
// partnership is followed by that staff member.
func (s *Server) avatar(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	file := r.PathValue("file")
	c := rc(r)

	a, err := s.store.AdminByAvatarFile(ctx, file)
	if err != nil {
		return err
	}

	allowed := c.Admin != nil

	if !allowed && c.Org != nil {
		partnerships, err := s.store.ListOrgPartnerships(ctx, c.Org.ID)
		if err != nil {
			return err
		}

		for _, p := range partnerships {
			if p.VisibleToPartner() && p.OwnerAdminID != nil && *p.OwnerAdminID == a.ID {
				allowed = true
			}
		}
	}

	if !allowed {
		return errNotFound
	}

	w.Header().Set("Cache-Control", "private, max-age=3600")
	s.files.Serve(w, r, file, "")

	return nil
}

// validEmail accepts a bare address (no display name), rejecting control
// characters such as CR/LF that could end up in email headers.
func validEmail(email string) bool {
	if len(email) > 254 || strings.IndexFunc(email, unicode.IsControl) >= 0 || strings.ContainsAny(email, " <>,;\"") {
		return false
	}

	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return false
	}

	at := strings.LastIndex(email, "@")

	return at > 0 && at < len(email)-3 && strings.Contains(email[at:], ".")
}

// safeRedirect only allows local paths, to avoid open redirects.
func safeRedirect(next, fallback string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return fallback
	}

	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return fallback
	}

	return next
}
