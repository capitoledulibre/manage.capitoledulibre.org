package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"manage/internal/store"
)

// OAuth 2.1 for the MCP server, as its specification asks, so that an
// assistant (claude.ai's connectors, say) gets a token by sending its user
// here rather than having them paste one:
//
//   - /.well-known/oauth-protected-resource tells where to authorize
//     (RFC 9728), /.well-known/oauth-authorization-server how (RFC 8414);
//   - /oauth/register registers the assistant as a public client (RFC 7591),
//     which proves itself with PKCE (S256) instead of a secret;
//   - /oauth/authorize has the person log in as usual and agree, read only
//     or with changes, then sends a one-time code back to the assistant;
//   - /oauth/token trades the code for an access token (an hour) and a
//     refresh token (90 days), renewed together on each refresh.
//
// A grant is an API token (store.APIToken) carrying the client's name: it
// shows with the personal tokens, on the profile it was granted from, and
// is revoked there the same way.

const (
	oauthCodeTTL    = 10 * time.Minute
	oauthAccessTTL  = time.Hour
	oauthRefreshTTL = 90 * 24 * time.Hour
)

func (s *Server) oauthRoutes(mux *router) {
	mux.Handle("GET /.well-known/oauth-protected-resource", cors(http.HandlerFunc(s.oauthResource)))
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp", cors(http.HandlerFunc(s.oauthResource)))
	mux.Handle("GET /.well-known/oauth-authorization-server", cors(http.HandlerFunc(s.oauthMetadata)))
	mux.Handle("/oauth/register", cors(http.HandlerFunc(s.oauthRegister)))
	mux.Handle("/oauth/token", cors(http.HandlerFunc(s.oauthToken)))
	mux.Handle("GET /oauth/authorize", s.wrap(s.oauthAuthorize))
	mux.Handle("POST /oauth/authorize", s.wrap(s.oauthDecide))
}

// cors lets an assistant running in a browser reach the API and the OAuth
// endpoints: none of them takes the site's cookies.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", "*")
		hd.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Protocol-Version, Mcp-Session-Id")
		hd.Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id")
		hd.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)

			return
		}

		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// oauthError answers an OAuth error (RFC 6749 §5.2).
func oauthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

func (s *Server) resourceMetadataURL() string {
	return s.cfg.BaseURL + "/.well-known/oauth-protected-resource"
}

func (s *Server) oauthResource(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.cfg.BaseURL + "/mcp",
		"resource_name":            s.site.Name,
		"authorization_servers":    []string{s.cfg.BaseURL},
		"scopes_supported":         []string{"read", "write"},
		"bearer_methods_supported": []string{"header"},
	})
}

func (s *Server) oauthMetadata(w http.ResponseWriter, _ *http.Request) {
	base := s.cfg.BaseURL

	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         base,
		"authorization_endpoint":                         base + "/oauth/authorize",
		"token_endpoint":                                 base + "/oauth/token",
		"registration_endpoint":                          base + "/oauth/register",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"scopes_supported":                               []string{"read", "write"},
		"authorization_response_iss_parameter_supported": true,
	})
}

// validRedirectURI tells whether a client may be sent back to an address:
// https, the machine itself over http (a desktop assistant), or an app's
// own scheme (reverse domain, RFC 8252); never with a fragment.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 500 || u.Fragment != "" || u.Scheme == "" {
		return false
	}

	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		host := u.Hostname()

		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	case "javascript", "data", "file", "vbscript":
		return false
	}

	return strings.Contains(u.Scheme, ".")
}

// oauthRegister registers a client (RFC 7591): a public one, whatever it
// asked, since it proves itself with PKCE.
func (s *Server) oauthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)

		return
	}

	if !s.registerByIP.Allow(rateKey(s.clientIP(r))) {
		oauthError(w, http.StatusTooManyRequests, "invalid_request", "too many registrations, try again later")

		return
	}

	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body is not valid JSON")

		return
	}

	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 5 || slices.ContainsFunc(req.RedirectURIs, func(u string) bool { return !validRedirectURI(u) }) {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URIs must be https, http on localhost, or an app scheme")

		return
	}

	name := strings.TrimSpace(req.ClientName)

	switch {
	case name == "":
		name = "Assistant"
	case utf8.RuneCountInString(name) > 80:
		name = string([]rune(name)[:80])
	}

	client, err := s.store.RegisterOAuthClient(r.Context(), name, req.RedirectURIs)
	if err != nil {
		slog.Error("oauth register", "err", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "")

		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  client.ClientID,
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"client_id_issued_at":        client.CreatedAt.Unix(),
	})
}

// authzRequest is an authorization request, checked.
type authzRequest struct {
	Client      *store.OAuthClient
	RedirectURI string
	State       string
	Challenge   string
	// Writes is whether the client asks to change things too.
	Writes bool
}

// errBadClient is a request whose client or address cannot be trusted:
// it is answered here, never sent back to that address.
var errBadClient = errors.New("unknown client or redirect address")

// authzRequestOf checks an authorization request. errBadClient when the
// client or its address cannot be trusted; else, a refusal to send back
// to the client as an OAuth error code.
func (s *Server) authzRequestOf(r *http.Request) (*authzRequest, string, error) {
	q := r.Form

	client, err := s.store.GetOAuthClient(r.Context(), q.Get("client_id"))
	if err != nil {
		return nil, "", errBadClient
	}

	req := &authzRequest{Client: client, RedirectURI: q.Get("redirect_uri"), State: q.Get("state"), Challenge: q.Get("code_challenge")}

	if !slices.Contains(client.RedirectURIs, req.RedirectURI) {
		return nil, "", errBadClient
	}

	switch {
	case q.Get("response_type") != "code":
		return req, "unsupported_response_type", nil
	case q.Get("code_challenge_method") != "S256" || len(req.Challenge) < 43 || len(req.Challenge) > 128:
		return req, "invalid_request", nil
	}

	req.Writes = slices.Contains(strings.Fields(q.Get("scope")), "write")

	return req, "", nil
}

// sendBack redirects to the client with the parameters of its answer.
func (s *Server) sendBack(w http.ResponseWriter, r *http.Request, req *authzRequest, params url.Values) {
	u, _ := url.Parse(req.RedirectURI)
	q := u.Query()

	for k, v := range params {
		q[k] = v
	}

	if req.State != "" {
		q.Set("state", req.State)
	}

	q.Set("iss", s.cfg.BaseURL)
	u.RawQuery = q.Encode()

	http.Redirect(w, r, u.String(), http.StatusFound)
}

// grantor is who grants access: a staff member (with their staff rights),
// or a person who runs something. Nil when nobody is signed in.
type grantor struct {
	Person  *store.Person
	AdminID *int64
	// Runs is false for someone who runs nothing: an assistant would read
	// nothing for them.
	Runs bool
}

func (s *Server) grantorOf(r *http.Request) (*grantor, error) {
	c := rc(r)

	if c.Admin != nil {
		p, err := s.store.GetPerson(r.Context(), c.Admin.PersonID)
		if err != nil {
			return nil, err
		}

		return &grantor{Person: p, AdminID: &c.Admin.ID, Runs: true}, nil
	}

	if c.Person == nil {
		return nil, nil
	}

	a, err := s.actor(r)
	if err != nil {
		return nil, err
	}

	return &grantor{Person: c.Person, Runs: a.HasRoles()}, nil
}

// oauthPage shows a step of the authorization: the login, the consent, or
// why it cannot go on.
func (s *Server) oauthPage(w http.ResponseWriter, r *http.Request, status int, d map[string]any) error {
	p := s.page(r, "oauth.title")
	p.D = d

	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}

	return s.render(w, r, "public/oauth", p)
}

func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadInput
	}

	req, refusal, err := s.authzRequestOf(r)
	if errors.Is(err, errBadClient) {
		return s.oauthPage(w, r, http.StatusBadRequest, map[string]any{"Mode": "bad"})
	}

	if err != nil {
		return err
	}

	if refusal != "" {
		s.sendBack(w, r, req, url.Values{"error": {refusal}})

		return nil
	}

	g, err := s.grantorOf(r)
	if err != nil {
		return err
	}

	next := url.QueryEscape(r.URL.RequestURI())
	d := map[string]any{"Client": req.Client.Name, "Mode": "consent", "Req": req, "Form": r.Form,
		"StaffLogin": "/admin/login?next=" + next, "VolunteerLogin": "/volunteers/login?next=" + next}

	if u, err := url.Parse(req.RedirectURI); err == nil {
		d["Host"] = u.Host

		// Agreeing sends the browser on to the assistant: its address is a
		// destination the consent form may lead to.
		origin := u.Scheme + ":"
		if u.Host != "" {
			origin = u.Scheme + "://" + u.Host
		}

		w.Header().Set("Content-Security-Policy", contentSecurityPolicy(origin))
	}

	switch {
	case g == nil:
		d["Mode"] = "login"
	case !g.Runs:
		d["Mode"] = "noroles"
	default:
		d["Staff"] = g.AdminID != nil
		d["Who"] = g.Person.Label()
	}

	return s.oauthPage(w, r, http.StatusOK, d)
}

// oauthDecide records the person's answer, and sends it to the client.
func (s *Server) oauthDecide(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return errBadInput
	}

	req, refusal, err := s.authzRequestOf(r)
	if errors.Is(err, errBadClient) {
		return s.oauthPage(w, r, http.StatusBadRequest, map[string]any{"Mode": "bad"})
	}

	if err != nil {
		return err
	}

	if refusal != "" {
		s.sendBack(w, r, req, url.Values{"error": {refusal}})

		return nil
	}

	g, err := s.grantorOf(r)
	if err != nil {
		return err
	}

	if g == nil || !g.Runs {
		return errForbidden
	}

	if r.PostForm.Get("decision") != "allow" {
		s.sendBack(w, r, req, url.Values{"error": {"access_denied"}})

		return nil
	}

	code, err := s.store.CreateOAuthCode(r.Context(), &store.OAuthGrant{ClientID: req.Client.ID, PersonID: g.Person.ID, AdminID: g.AdminID,
		RedirectURI: req.RedirectURI, Challenge: req.Challenge, Writes: req.Writes && r.PostForm.Get("writes") == "yes"}, oauthCodeTTL)
	if err != nil {
		return err
	}

	s.sendBack(w, r, req, url.Values{"code": {code}})

	return nil
}

// pkceMatches tells whether a code verifier is the one behind a challenge.
func pkceMatches(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}

	sum := sha256.Sum256([]byte(verifier))

	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// oauthToken trades a code, or a refresh token, for tokens.
func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)

		return
	}

	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "")

		return
	}

	ctx := r.Context()

	client, err := s.store.GetOAuthClient(ctx, r.PostForm.Get("client_id"))
	if err != nil {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")

		return
	}

	var (
		access, refresh string
		writes          bool
	)

	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		g, err := s.store.ConsumeOAuthCode(ctx, r.PostForm.Get("code"))
		if err != nil || g.ClientID != client.ID || g.RedirectURI != r.PostForm.Get("redirect_uri") ||
			!pkceMatches(r.PostForm.Get("code_verifier"), g.Challenge) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "the code is invalid, expired or already used")

			return
		}

		if access, refresh, err = s.store.IssueOAuthTokens(ctx, client, g, oauthAccessTTL, oauthRefreshTTL); err != nil {
			slog.Error("oauth token", "err", err)
			oauthError(w, http.StatusInternalServerError, "server_error", "")

			return
		}

		writes = g.Writes
	case "refresh_token":
		if access, refresh, writes, err = s.store.RefreshOAuthTokens(ctx, client, r.PostForm.Get("refresh_token"), oauthAccessTTL, oauthRefreshTTL); err != nil {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token is invalid, expired or revoked")

			return
		}
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")

		return
	}

	scope := "read"
	if writes {
		scope = "read write"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         scope,
	})
}
