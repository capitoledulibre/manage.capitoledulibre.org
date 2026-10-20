package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
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
	// maxLoginEmails limits login emails per address and quarter of an
	// hour, as the login page says.
	maxLoginEmails   = 5
	loginEmailWindow = 15 * time.Minute
	// loginsPerIP limits the login requests of one client per quarter of an
	// hour: enough for a family or an office behind one address.
	loginsPerIP = 30
)

func (s *Server) publicRoutes(mux *router) {
	mux.Handle("GET /{$}", s.wrap(s.loginPage))
	mux.Handle("GET /partners/login", s.wrap(s.partnerLogin))
	mux.Handle("POST /login", s.wrap(s.loginSubmit))
	mux.Handle("GET /auth/{token}", s.wrap(s.magicConfirm))
	mux.Handle("POST /auth/{token}", s.wrap(s.magicConsume))
	mux.Handle("POST /logout", s.wrap(s.logout))
	mux.Handle("GET /lang/{lang}", s.wrap(s.switchLang))
	mux.Handle("GET /theme/{theme}", s.wrap(s.switchTheme))
	mux.Handle("GET /jobs", s.wrap(s.publicJobs))
	mux.Handle("GET /privacy", s.wrap(s.privacy))
	mux.Handle("GET /logos/{file}", s.wrap(s.publicLogo))
	mux.Handle("GET /avatars/{file}", s.wrap(s.avatar))
	mux.Handle("GET /{event}/api/partners.json", s.wrap(s.exportJSON))
}

// privacy is the personal data notice, linked from every page.
func (s *Server) privacy(w http.ResponseWriter, r *http.Request) error {
	p := s.page(r, "privacy.title")
	p.D = map[string]any{"Gravatar": s.gravatar.Enabled()}

	return s.render(w, r, "public/privacy", p)
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
			http.Redirect(w, r, "/choose", http.StatusSeeOther)

			return nil
		}

		if ok, err := s.volunteerSpaceOpen(r, c.Person); ok || err != nil {
			if err == nil {
				http.Redirect(w, r, "/volunteers/", http.StatusSeeOther)
			}

			return err
		}

		p := s.page(r, "home.title")
		p.D = c.Person

		return s.render(w, r, "public/home", p)
	}

	// Someone not signed in picks their door: partner, volunteer or staff.
	p := s.page(r, "door.title")
	p.D = map[string]any{"Staff": s.oidc != nil || s.devAdminLogin(r)}

	return s.render(w, r, "public/door", p)
}

// partnerLogin is the login form of the partner space.
func (s *Server) partnerLogin(w http.ResponseWriter, r *http.Request) error {
	next := safeRedirect(r.URL.Query().Get("next"), "")

	if rc(r).Person != nil {
		http.Redirect(w, r, safeRedirect(next, "/"), http.StatusSeeOther)

		return nil
	}

	p := s.page(r, "login.title")
	p.D = map[string]any{"Next": next}

	return s.render(w, r, "public/login", p)
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) error {
	email := store.NormalizeEmail(r.FormValue("email"))
	// next is where to go once signed in; the volunteer login form sets it.
	next := safeRedirect(r.FormValue("next"), "")
	p := s.page(r, "login.title")

	refuse := func(key string) error {
		if volunteerPath(next) {
			p.Title = "vol.login.title"
			d := s.loginContext(r, next)
			d["Error"], d["Email"] = key, email
			p.D = d

			return s.render(w, r, "volunteer/login", p)
		}

		p.D = map[string]any{"Error": key, "Email": email, "Next": next}

		return s.render(w, r, "public/login", p)
	}

	if !validEmail(email) {
		return refuse("login.invalid_email")
	}

	// Anyone can ask for a link while an edition takes volunteers: one
	// client cannot make the server send e-mails to many addresses.
	if !s.loginByIP.Allow(rateKey(s.clientIP(r))) {
		slog.Warn("login requests rate limited", "ip", s.clientIP(r))
		w.WriteHeader(http.StatusTooManyRequests)

		return refuse("login.too_many")
	}

	// The response is identical whether the email is known or not, so the
	// form cannot be used to discover who is known; the link goes out off
	// the request so that the time to answer does not tell either.
	lang := rc(r).Lang

	s.background(func() {
		if err := s.sendLoginLink(context.Background(), email, lang, next); err != nil {
			slog.Error("send login link", "email", email, "err", err)
		}
	})

	p.D = map[string]any{"Email": email, "Volunteer": volunteerPath(next), "Next": next}

	return s.render(w, r, "public/login_sent", p)
}

func (s *Server) sendLoginLink(ctx context.Context, email, lang, next string) error {
	// Known people get a link. So does anyone while an edition takes
	// volunteers: signing in is how a newcomer volunteers, and the person is
	// only created once the link is used, so a mistyped address leaves
	// nothing behind. The answer on screen is the same either way.
	person, err := s.store.PersonByEmail(ctx, email)

	switch {
	case errors.Is(err, store.ErrNotFound):
		if _, err := s.store.CurrentVolunteerEvent(ctx); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}

			return err
		}
	case err != nil:
		return err
	default:
		lang = person.Lang
	}

	n, err := s.store.CountRecentMagicTokens(ctx, email, loginEmailWindow)
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

	link := s.cfg.BaseURL + "/auth/" + token
	if next != "" {
		link += "?next=" + url.QueryEscape(next)
	}

	return s.mailer.Send(pmail.Message{
		To:      []string{email},
		Subject: i18n.T(lang, "mail.login.subject"),
		Body:    i18n.T(lang, "mail.login.body", link, int(loginTokenTTL.Minutes())),
	})
}

// rateKey is the client a rate limit counts: an IPv4 address, or the /64
// of an IPv6 one, since a single machine commonly owns a whole /64 and
// would otherwise get a fresh allowance per address.
func rateKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() || a.Is4In6() {
		return ip
	}

	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}

	return p.String()
}

// clientIP returns the address of the caller. X-Forwarded-For is only read
// when the connection comes from a trusted proxy: from anyone else it is
// whatever they chose to write. Each proxy appends the address it saw, so
// the client is the rightmost entry that is not a trusted proxy; the
// leftmost ones are the client's to forge.
func (s *Server) clientIP(r *http.Request) string {
	peer := r.RemoteAddr
	if ap, err := netip.ParseAddrPort(peer); err == nil {
		peer = ap.Addr().Unmap().String()
	}

	if !s.trustedProxy(peer) {
		return peer
	}

	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}

		if !s.trustedProxy(hop) {
			return hop
		}

		peer = hop
	}

	return peer
}

// trustedProxy tells whether an address belongs to MANAGE_TRUSTED_PROXIES.
func (s *Server) trustedProxy(addr string) bool {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}

	a = a.Unmap()

	for _, p := range s.cfg.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}

	return false
}

// volunteerSpaceOpen tells whether the volunteer space has something for a
// person: an edition taking volunteers, or a part they already play in one.
func (s *Server) volunteerSpaceOpen(r *http.Request, p *store.Person) (bool, error) {
	if _, err := s.store.CurrentVolunteerEvent(r.Context()); !errors.Is(err, store.ErrNotFound) {
		return err == nil, err
	}

	return s.store.IsVolunteer(r.Context(), p.ID)
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

	p.D = map[string]any{"Email": email, "Token": r.PathValue("token"), "Next": safeRedirect(r.URL.Query().Get("next"), "")}

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

	// A newcomer's person is created now that the address is proven.
	person, err := s.store.PersonByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		person, err = s.store.EnsureVolunteer(ctx, email, "", "", rc(r).Lang)
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

	next := safeRedirect(r.FormValue("next"), "")

	// A volunteer without a name is asked for it first: it is what the pole
	// managers see.
	if person.Name == "" && (volunteerPath(next) || (next == "" && len(managers) == 0)) {
		http.Redirect(w, r, "/welcome?next="+url.QueryEscape(safeRedirect(next, "/volunteers/")), http.StatusSeeOther)

		return nil
	}

	switch {
	case next != "":
		http.Redirect(w, r, next, http.StatusSeeOther)
	case len(managers) > 1:
		http.Redirect(w, r, "/choose", http.StatusSeeOther)
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

// avatar serves a profile picture: to the staff; a volunteer's to the
// signed-in people, who meet them in poles and slots; a staff member's to
// the partners whose partnership they follow.
func (s *Server) avatar(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	file := r.PathValue("file")
	c := rc(r)

	owner, err := s.store.PersonByAvatarFile(ctx, file)
	if err != nil {
		return err
	}

	allowed := c.Admin != nil

	if !allowed && c.Person != nil {
		if allowed, err = s.store.IsVolunteer(ctx, owner.ID); err != nil {
			return err
		}

		allowed = allowed || owner.ID == c.Person.ID
	}

	if a, err := s.store.AdminByAvatarFile(ctx, file); !allowed && err == nil && c.Org != nil {
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

// partnerPagePath matches the pages of a partner's space and of the staff's
// partners, below an edition's slug.
var partnerPagePath = regexp.MustCompile(`^/[^/]+/partners?(/|$)`)

// volunteerPath tells whether a path is in the volunteer space: the way in,
// the profile, or an edition's pages but its partners'. The login says
// so and comes back there.
func volunteerPath(path string) bool {
	switch {
	case strings.HasPrefix(path, "/volunteers/"), path == "/me", path == "/welcome":
		return true
	case path == "", path == "/", strings.HasPrefix(path, "/admin"), strings.HasPrefix(path, "/partners/"),
		strings.HasPrefix(path, "/choose"), partnerPagePath.MatchString(path):
		return false
	}

	return strings.Count(strings.SplitN(path, "?", 2)[0], "/") >= 2
}

// safeRedirect keeps next when it is a path of this site, else returns
// fallback. A backslash or a control character is refused outright:
// browsers read "/\host" as "//host", and http.Redirect cleans "/./\host"
// into it.
func safeRedirect(next, fallback string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") ||
		strings.ContainsFunc(next, func(r rune) bool { return r == '\\' || unicode.IsControl(r) }) {
		return fallback
	}

	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return fallback
	}

	return next
}
