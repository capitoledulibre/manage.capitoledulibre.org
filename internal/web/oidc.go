package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"benevoles/internal/config"
)

// The staff (site-wide administrators) log in through OpenID Connect, as on
// the partners dashboard. The rights come with the session this login opens
// and end with it: the same address logged in by e-mail link is a volunteer.
// Volunteers, pole managers and event coordinators keep the e-mail link.

const (
	oidcStateCookie = "oidc"
	// adminSessionTTL is short on purpose: an administrator session can do
	// anything, so it does not outlive a working day.
	adminSessionTTL = 12 * time.Hour
)

type oidcClient struct {
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
}

func newOIDCClient(ctx context.Context, cfg config.Config) (*oidcClient, error) {
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

// staffLogin tells whether a staff login is offered at all.
func (s *Server) staffLogin() bool { return s.oidc != nil || s.Cfg.DevAdminLogin() }

// adminNext is where to go once logged in as staff: a same-site path, the
// administration home by default.
func adminNext(next string) string {
	if next == "" || !safePath(next) {
		return "/admin"
	}
	return next
}

// adminLogin starts the authorization code flow, with PKCE, or shows the
// development form when OIDC is not configured.
func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	next := adminNext(r.URL.Query().Get("next"))
	if s.oidc == nil {
		if !s.Cfg.DevAdminLogin() {
			s.notFound(w, r)
			return
		}
		s.render(w, r, "admin_login", M{"Next": next, "Restricted": len(s.Cfg.AdminEmails) > 0})
		return
	}
	state, nonce := randomHex(), randomHex()
	verifier := oauth2.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: strings.Join([]string{state, nonce, verifier, url.QueryEscape(next)}, "|"),
		Path: "/admin", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.Cfg.Secure,
		MaxAge: int((10 * time.Minute).Seconds()),
	})
	http.Redirect(w, r, s.oidc.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

// adminCallback is where the identity provider sends the browser back.
func (s *Server) adminCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		s.notFound(w, r)
		return
	}
	ck, err := r.Cookie(oidcStateCookie)
	if err != nil {
		s.badLoginRequest(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/admin", MaxAge: -1, HttpOnly: true})
	parts := strings.Split(ck.Value, "|")
	if len(parts) != 4 || parts[0] != r.FormValue("state") {
		s.badLoginRequest(w, r)
		return
	}
	nonce, verifier := parts[1], parts[2]
	next, _ := url.QueryUnescape(parts[3])
	if e := r.FormValue("error"); e != "" {
		log.Printf("oidc error: %s (%s)", e, r.FormValue("error_description"))
		s.forbidden(w, r)
		return
	}
	ctx := r.Context()
	tok, err := s.oidc.oauth.Exchange(ctx, r.FormValue("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		s.serverError(w, r, fmt.Errorf("oidc exchange: %w", err))
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		s.serverError(w, r, errors.New("oidc: no id_token in the response"))
		return
	}
	idToken, err := s.oidc.verifier.Verify(ctx, rawID)
	if err != nil {
		log.Printf("oidc: invalid id_token: %v", err)
		s.forbidden(w, r)
		return
	}
	if idToken.Nonce != nonce {
		s.badLoginRequest(w, r)
		return
	}
	var claims struct {
		Email         string   `json:"email"`
		EmailVerified *bool    `json:"email_verified"`
		Name          string   `json:"name"`
		Groups        []string `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		s.serverError(w, r, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" || (claims.EmailVerified != nil && !*claims.EmailVerified) || !s.adminAllowed(email, claims.Groups) {
		log.Printf("staff login refused for %q", email)
		s.forbidden(w, r)
		return
	}
	s.startAdminSession(w, r, email, claims.Name, adminNext(next))
}

// badLoginRequest answers a callback that does not belong to a login this
// browser started: an expired, replayed or forged one.
func (s *Server) badLoginRequest(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	s.render(w, r, "error", M{"Title": "Connexion interrompue", "Message": "Cette connexion a expiré ou n'a pas été demandée depuis ce navigateur. Recommencez depuis le début."})
}

// adminAllowed tells whether an identity may administer the site.
func (s *Server) adminAllowed(email string, groups []string) bool {
	if slices.Contains(s.Cfg.AdminEmails, email) {
		return true
	}
	return s.Cfg.AdminGroup != "" && slices.Contains(groups, s.Cfg.AdminGroup)
}

// adminDevLogin logs in, without a password, any address listed in
// ADMIN_EMAILS, or any address when the list is empty. config.DevAdminLogin
// says when it is available.
func (s *Server) adminDevLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc != nil || !s.Cfg.DevAdminLogin() {
		s.notFound(w, r)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email ||
		(len(s.Cfg.AdminEmails) > 0 && !slices.Contains(s.Cfg.AdminEmails, email)) {
		s.forbidden(w, r)
		return
	}
	s.startAdminSession(w, r, email, "", adminNext(r.FormValue("next")))
}

// startAdminSession opens an administrator session on the account of that
// address, creating the account when the person never came as a volunteer.
func (s *Server) startAdminSession(w http.ResponseWriter, r *http.Request, email, name, next string) {
	u, err := s.DB.GetOrCreateUser(email)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if u.Name == "" && strings.TrimSpace(name) != "" {
		if err := s.DB.UpdateProfile(u.ID, strings.TrimSpace(name), u.Phone); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	// The volunteer session this browser may have had gives way.
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.DB.DeleteSession(c.Value)
	}
	sid, err := s.DB.CreateSession(u.ID, adminSessionTTL, true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.setSessionCookie(w, sid, adminSessionTTL)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// randomHex returns 32 random hexadecimal characters, for state and nonce.
func randomHex() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
