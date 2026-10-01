package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"partners/internal/export"
	"partners/internal/store"
)

// exportJSON serves the partner list used by the website build. It requires
// the export token (as a Bearer token or "token" parameter) or a staff
// session.
func (s *Server) exportJSON(w http.ResponseWriter, r *http.Request) error {
	if !s.exportAuthorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="partners"`)

		return errStatus(http.StatusUnauthorized)
	}

	ctx := r.Context()

	var (
		e   *store.Event
		err error
	)

	if slug := r.URL.Query().Get("event"); slug != "" {
		e, err = s.store.GetEventBySlug(ctx, slug)
	} else {
		e, err = s.store.CurrentEvent(ctx)
	}

	if err != nil {
		return err
	}

	doc, err := export.Build(ctx, s.store, s.cfg.BaseURL, e)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(doc)
}

func (s *Server) exportAuthorized(r *http.Request) bool {
	if rc(r).Admin != nil {
		return true
	}

	if s.cfg.ExportToken == "" {
		return false
	}

	token := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimPrefix(h, "Bearer ")
	}

	return subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.ExportToken)) == 1
}
