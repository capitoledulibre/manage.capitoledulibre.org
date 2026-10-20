package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"manage/internal/config"
	"manage/internal/store"
)

const oidcStateCookie = "manage_oidc"

// adminNextCookie keeps where a staff login comes back to: the page that
// asked for it (an assistant's authorization, say).
const adminNextCookie = "manage_admin_next"

type oidcClient struct {
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
}

func newOIDCClient(ctx context.Context, cfg *config.Config) (*oidcClient, error) {
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}

	scopes := []string{oidc.ScopeOpenID, "email", "profile"}
	if cfg.AdminGroup != "" {
		scopes = append(scopes, "groups")
	}

	return &oidcClient{
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}),
		oauth: oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.BaseURL + "/admin/callback",
			Scopes:       scopes,
		},
	}, nil
}

// adminLogin starts the OIDC authorization code flow (with PKCE), or shows
// the development login form when OIDC is disabled.
func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) error {
	if next := r.URL.Query().Get("next"); next != "" {
		s.setCookie(w, adminNextCookie, safeRedirect(next, "/admin/"), 15*time.Minute)
	}

	if s.oidc == nil {
		if !s.devAdminLogin(r) {
			return errNotFound
		}

		p := s.page(r, "Connexion staff")

		return s.render(w, r, "admin/login_dev", p)
	}

	state, _ := store.NewToken()
	nonce, _ := store.NewToken()
	verifier := oauth2.GenerateVerifier()

	s.setCookie(w, oidcStateCookie, state+"|"+nonce+"|"+verifier, 10*time.Minute)

	url := s.oidc.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, url, http.StatusFound)

	return nil
}

func (s *Server) adminCallback(w http.ResponseWriter, r *http.Request) error {
	if s.oidc == nil {
		return errNotFound
	}

	ctx := r.Context()

	ck, err := r.Cookie(oidcStateCookie)
	if err != nil {
		return errStatus(http.StatusBadRequest)
	}

	s.clearCookie(w, oidcStateCookie)

	parts := strings.Split(ck.Value, "|")
	if len(parts) != 3 || parts[0] != r.FormValue("state") {
		return errStatus(http.StatusBadRequest)
	}

	nonce, verifier := parts[1], parts[2]

	if e := r.FormValue("error"); e != "" {
		slog.Warn("oidc error", "error", e, "description", r.FormValue("error_description"))

		return errForbidden
	}

	tok, err := s.oidc.oauth.Exchange(ctx, r.FormValue("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		return fmt.Errorf("oidc exchange: %w", err)
	}

	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return errors.New("oidc: no id_token in response")
	}

	idToken, err := s.oidc.verifier.Verify(ctx, rawID)
	if err != nil {
		slog.Warn("oidc: invalid id_token", "err", err)

		return errForbidden
	}

	if idToken.Nonce != nonce {
		return errStatus(http.StatusBadRequest)
	}

	var claims struct {
		Email         string   `json:"email"`
		EmailVerified *bool    `json:"email_verified"`
		Name          string   `json:"name"`
		Groups        []string `json:"groups"`
	}

	if err := idToken.Claims(&claims); err != nil {
		return err
	}

	email := store.NormalizeEmail(claims.Email)
	if email == "" || (claims.EmailVerified != nil && !*claims.EmailVerified) || !s.adminAllowed(email, claims.Groups) {
		slog.Warn("admin login refused", "email", email)

		return errForbidden
	}

	return s.startAdminSession(w, r, email, claims.Name)
}

// staffEmailAllowed tells whether someone may still be staff, when the
// configuration alone tells: a list of addresses, and no group (a group
// is only known at login, from the identity provider). A token of someone
// taken off the list stops working at once; otherwise, at its end.
func (s *Server) staffEmailAllowed(email string) bool {
	if s.cfg.AdminGroup != "" || len(s.cfg.AdminEmails) == 0 {
		return true
	}

	return slices.Contains(s.cfg.AdminEmails, store.NormalizeEmail(email))
}

func (s *Server) adminAllowed(email string, groups []string) bool {
	if slices.Contains(s.cfg.AdminEmails, email) {
		return true
	}

	return s.cfg.AdminGroup != "" && slices.Contains(groups, s.cfg.AdminGroup)
}

// devAdminLogin tells whether the password-less staff login is offered to
// this request: config.DevAdminLogin, and a request from the machine itself.
// The configured base URL alone is not enough: a server listening on every
// interface with a forgotten MANAGE_BASE_URL would open the admin area to
// its network. The peer address is read as is, never from X-Forwarded-For.
func (s *Server) devAdminLogin(r *http.Request) bool {
	if !s.cfg.DevAdminLogin() {
		return false
	}

	ap, err := netip.ParseAddrPort(r.RemoteAddr)

	return err == nil && ap.Addr().Unmap().IsLoopback()
}

// adminDevLogin logs in any email listed in MANAGE_ADMIN_EMAILS (or any
// email when the list is empty). See config.DevAdminLogin for when it is
// available.
func (s *Server) adminDevLogin(w http.ResponseWriter, r *http.Request) error {
	if s.oidc != nil || !s.devAdminLogin(r) {
		return errNotFound
	}

	email := store.NormalizeEmail(r.FormValue("email"))
	if !validEmail(email) || (len(s.cfg.AdminEmails) > 0 && !slices.Contains(s.cfg.AdminEmails, email)) {
		return errForbidden
	}

	return s.startAdminSession(w, r, email, "")
}

func (s *Server) startAdminSession(w http.ResponseWriter, r *http.Request, email, name string) error {
	ctx := r.Context()

	admin, err := s.store.UpsertAdmin(ctx, email, name)
	if err != nil {
		return err
	}

	if c := rc(r); c.adminToken != "" {
		_ = s.store.DeleteSession(ctx, c.adminToken)
	}

	token, err := s.store.CreateSession(ctx, &store.Session{Kind: store.SessionAdmin, AdminID: &admin.ID, PersonID: admin.PersonID}, adminSessionTTL)
	if err != nil {
		return err
	}

	s.setCookie(w, adminCookie, token, adminSessionTTL)

	to := "/admin/"
	if ck, err := r.Cookie(adminNextCookie); err == nil {
		to = safeRedirect(ck.Value, to)
		s.clearCookie(w, adminNextCookie)
	}

	http.Redirect(w, r, to, http.StatusSeeOther)

	return nil
}
