// Package server implements the HTTP application: public pages, partner
// dashboard and staff administration.
package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"manage/internal/config"
	"manage/internal/files"
	"manage/internal/gravatar"
	"manage/internal/i18n"
	"manage/internal/mail"
	"manage/internal/ratelimit"
	"manage/internal/store"
	"manage/web"
)

type Server struct {
	cfg    *config.Config
	store  *store.Store
	files  *files.Store
	mailer mail.Sender
	tmpl   *templates
	oidc   *oidcClient
	logo   *brandLogo
	icons  *siteIcons
	site   Site
	// gravatar fills in missing profile pictures.
	gravatar *gravatar.Fetcher
	// loginByIP limits the login links one client asks for.
	loginByIP *ratelimit.Limiter
	// bg tracks the work done off the request (e-mails, reminders), which a
	// shutdown waits for before the database closes.
	bg sync.WaitGroup
}

// background runs f off the request, tracked for Wait.
func (s *Server) background(f func()) { s.bg.Go(f) }

// Start runs the periodic jobs until ctx is done.
func (s *Server) Start(ctx context.Context) {
	s.background(func() { s.RunReminders(ctx) })
	s.background(func() { s.RunVolunteerReminders(ctx) })
}

// Wait blocks until the work started off the requests is over: once the
// HTTP server has shut down, so that no e-mail is lost to a restart.
func (s *Server) Wait() {
	s.bg.Wait()
	s.gravatar.Wait()
}

func New(ctx context.Context, cfg *config.Config, st *store.Store, fs *files.Store, mailer mail.Sender) (*Server, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}

	if cfg.SiteName == "" {
		cfg.SiteName = config.DefaultSiteName
	}

	logo, err := loadBrandLogo(cfg.SiteLogo)
	if err != nil {
		return nil, err
	}

	i18n.SetSite(cfg.SiteName)

	s := &Server{cfg: cfg, store: st, files: fs, mailer: mailer, tmpl: tmpl, logo: logo, site: newSite(cfg, logo)}

	if s.icons, err = newSiteIcons(s.site, logo); err != nil {
		return nil, err
	}

	gravatarURL := cfg.GravatarURL
	if gravatarURL == "" {
		gravatarURL = gravatar.Endpoint
	}

	s.gravatar = gravatar.New(st, fs, cfg.Gravatar, gravatarURL)
	s.loginByIP = ratelimit.New(loginsPerIP, 15*time.Minute)

	if cfg.OIDCEnabled() {
		s.oidc, err = newOIDCClient(ctx, cfg)
		if err != nil {
			return nil, err
		}
	}

	return s, nil
}

// handlerFunc is an HTTP handler that may return an error, rendered as an
// error page by ServeHTTP.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// httpError carries an HTTP status code.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func errStatus(status int) error { return &httpError{status: status, msg: http.StatusText(status)} }

var (
	errNotFound  = errStatus(http.StatusNotFound)
	errForbidden = errStatus(http.StatusForbidden)
	errBadInput  = errStatus(http.StatusBadRequest)
)

func (s *Server) wrap(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}

		status := http.StatusInternalServerError

		var he *httpError

		switch {
		case errors.As(err, &he):
			status = he.status
		case errors.Is(err, store.ErrNotFound):
			status = http.StatusNotFound
		default:
			slog.Error("request failed", "method", r.Method, "path", logPath(r), "err", err)
		}

		w.Header().Del("Content-Disposition")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)

		p := s.page(r, "error.title")
		if p.Area == "admin" {
			p.Title = "Erreur"
		}

		p.D = map[string]any{"Status": status}

		if err := s.tmpl.render(w, "error", p); err != nil {
			slog.Error("render error page", "err", err)
		}
	})
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	protection := http.NewCrossOriginProtection()
	// The API takes a token, never the site's cookies: a request from
	// another origin cannot act on anyone's behalf there.
	protection.AddInsecureBypassPattern("/mcp")

	var h http.Handler = s.routes()
	h = limitBodies(h)
	h = s.withSession(h)
	h = protection.Handler(h)
	h = securityHeaders(h, s.cfg.SecureCookies())
	h = logRequests(h)

	return h
}

// router holds the routes in two muxes: the site's own pages, and the pages
// of an edition, below its slug (/{event}/…). A request goes to the
// editions' when none of the site's own matches: the patterns of both
// would otherwise conflict (/static/… against /{event}/…), and a word of the
// site can be no edition's slug (store.ReservedSlug).
type router struct {
	main, editions *http.ServeMux
}

func newRouter() *router {
	rt := &router{main: http.NewServeMux(), editions: http.NewServeMux()}
	rt.main.Handle("/", rt.editions)

	return rt
}

// Handle registers a pattern on the mux it belongs to.
func (rt *router) Handle(pattern string, h http.Handler) {
	if _, path, _ := strings.Cut(pattern, " "); path == "/{event}" || strings.HasPrefix(path, "/{event}/") {
		rt.editions.Handle(pattern, h)

		return
	}

	rt.main.Handle(pattern, h)
}

func (rt *router) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	rt.Handle(pattern, http.HandlerFunc(h))
}

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) { rt.main.ServeHTTP(w, r) }

// pattern is the route a request reaches, empty for none.
func (rt *router) pattern(r *http.Request) string {
	if _, p := rt.main.Handler(r); p != "" && p != "/" {
		return p
	}

	_, p := rt.editions.Handler(r)

	return p
}

// routes registers every route of the application.
func (s *Server) routes() *router {
	mux := newRouter()

	static, _ := fs.Sub(web.Static, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /brand/logo", s.serveBrandLogo)
	mux.HandleFunc("GET /icon.svg", s.serveIconSVG)
	// iOS asks for these at the root, whatever the page says.
	mux.HandleFunc("GET /apple-touch-icon.png", s.serveTouchIcon)
	mux.HandleFunc("GET /apple-touch-icon-precomposed.png", s.serveTouchIcon)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	s.publicRoutes(mux)
	s.partnerRoutes(mux)
	s.volunteerRoutes(mux)
	s.adminRoutes(mux)
	s.mcpRoutes(mux)

	return mux
}

// Request bodies: a form fits in maxFormBody; an upload in maxUploadBody,
// the largest file accepted (a PDF) with room for the other fields. Each
// upload handler then applies its own, smaller, limit.
const (
	maxFormBody   = 1 << 20
	maxUploadBody = maxPDFSize + 1<<20
)

// limitBodies bounds every request body. Without it, any form handler
// reading a field of a multipart body would spool an unbounded file to disk
// first, for anyone and before any rate limit.
func limitBodies(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxFormBody)
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			limit = maxUploadBody
		}

		r.Body = http.MaxBytesReader(w, r.Body, limit)

		h.ServeHTTP(w, r)
	})
}

// securityHeaders sets the browser security policy. HSTS is only sent when
// the application is served over HTTPS.
func securityHeaders(h http.Handler, https bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; "+
			"form-action 'self'; frame-ancestors 'none'; base-uri 'self'; object-src 'none'")
		hd.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")

		if https {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}

		h.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// secretPaths are the paths whose last segment is a secret: a login link,
// a calendar subscription. Logs keep the prefix only, or anyone reading
// them could use the link.
var secretPaths = []string{"/auth/", "/calendar/"}

// logPath is the path of a request as logs show it.
func logPath(r *http.Request) string {
	for _, prefix := range secretPaths {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return prefix + "…"
		}
	}

	return r.URL.Path
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)

		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}

		slog.Info("http", "method", r.Method, "path", logPath(r), "status", rec.status, "duration", time.Since(start).Round(time.Millisecond))
	})
}
