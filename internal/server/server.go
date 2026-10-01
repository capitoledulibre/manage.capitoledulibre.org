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
	"time"

	"partners/internal/config"
	"partners/internal/files"
	"partners/internal/i18n"
	"partners/internal/mail"
	"partners/internal/store"
	"partners/web"
)

type Server struct {
	cfg    *config.Config
	store  *store.Store
	files  *files.Store
	mailer mail.Sender
	tmpl   *templates
	oidc   *oidcClient
	logo   *brandLogo
	site   Site
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
			slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
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
	mux := http.NewServeMux()

	static, _ := fs.Sub(web.Static, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /brand/logo", s.serveBrandLogo)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	s.publicRoutes(mux)
	s.partnerRoutes(mux)
	s.adminRoutes(mux)

	protection := http.NewCrossOriginProtection()

	var h http.Handler = mux
	h = s.withSession(h)
	h = protection.Handler(h)
	h = securityHeaders(h)
	h = logRequests(h)

	return h
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'; base-uri 'self'")
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

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)

		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}

		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start).Round(time.Millisecond))
	})
}
