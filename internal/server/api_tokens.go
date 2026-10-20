package server

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

// The personal tokens of the API (MCP): made, listed and revoked by their
// owner, from their profile. A staff member makes theirs from the staff
// profile, and they carry the staff's rights; the coordinators and the
// pole managers make theirs from "My profile". A token is shown once.

// apiTokens is the token section of a profile page: the tokens, the one
// just made (shown once), and where the forms post.
type apiTokens struct {
	Tokens   any
	NewToken string
	Base     string
	MCPURL   string
}

func (s *Server) apiTokensOf(r *http.Request, personID int64, base, newToken string) (*apiTokens, error) {
	tokens, err := s.store.ListAPITokens(r.Context(), personID)
	if err != nil {
		return nil, err
	}

	return &apiTokens{Tokens: tokens, NewToken: newToken, Base: base, MCPURL: s.cfg.BaseURL + "/mcp"}, nil
}

// tokenName is the name typed for a token, "MCP" when blank.
func tokenName(r *http.Request) string {
	name := strings.TrimSpace(r.FormValue("name"))

	switch {
	case name == "":
		return "MCP"
	case utf8.RuneCountInString(name) > 60:
		return string([]rune(name)[:60])
	}

	return name
}

// meTokenCreate makes a token for a coordinator or a pole manager, and
// shows it on their profile.
func (s *Server) meTokenCreate(w http.ResponseWriter, r *http.Request) error {
	a := act(r)
	if a.Staff || !a.HasRoles() {
		return errForbidden
	}

	token, _, err := s.store.CreateAPIToken(r.Context(), a.Person.ID, nil, tokenName(r), r.FormValue("writes") == "yes", apiTokenTTL)
	if err != nil {
		return err
	}

	return s.renderVolunteerProfile(w, r, token)
}

func (s *Server) meTokenDelete(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.DeleteAPIToken(r.Context(), rc(r).Person.ID, r.PathValue("token")); err != nil {
		return err
	}

	s.flash(w, "flash.api.revoked")
	http.Redirect(w, r, "/me#api", http.StatusSeeOther)

	return nil
}

// adminTokenCreate makes a staff token, and shows it on the staff profile.
func (s *Server) adminTokenCreate(w http.ResponseWriter, r *http.Request) error {
	admin := rc(r).Admin

	token, _, err := s.store.CreateAPIToken(r.Context(), admin.PersonID, &admin.ID, tokenName(r), r.FormValue("writes") == "yes", apiTokenTTL)
	if err != nil {
		return err
	}

	return s.renderAdminProfile(w, r, token)
}

func (s *Server) adminTokenDelete(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.DeleteAPIToken(r.Context(), rc(r).Admin.PersonID, r.PathValue("token")); err != nil {
		return err
	}

	s.flash(w, "flash.api.revoked")
	http.Redirect(w, r, "/admin/profile#api", http.StatusSeeOther)

	return nil
}
