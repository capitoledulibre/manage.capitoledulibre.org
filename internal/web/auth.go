package web

import (
	"fmt"
	"net/http"
	"net/mail"
	"strings"
)

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", M{"Next": r.URL.Query().Get("next")})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		s.render(w, r, "login", M{"Error": "Cette adresse e-mail ne semble pas valide.", "Email": email})
		return
	}
	if !s.loginByIP.Allow(clientIP(r)) || !s.loginByEmail.Allow(email) {
		w.WriteHeader(http.StatusTooManyRequests)
		s.render(w, r, "login", M{"Error": "Trop de demandes de connexion. Vérifiez votre boîte de réception, puis réessayez dans un quart d'heure.", "Email": email})
		return
	}
	u, err := s.DB.GetOrCreateUser(email)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tok, err := s.DB.CreateMagicLink(u.ID, magicLinkTTL)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	link := fmt.Sprintf("%s/auth/%s", s.Cfg.BaseURL, tok)
	next := r.FormValue("next")
	if next != "" && strings.HasPrefix(next, "/") {
		link += "?next=" + next
	}
	org := s.DB.GetSetting("org_name", "Bénévoles")
	body := fmt.Sprintf("Bonjour,\n\nPour vous connecter à l'espace bénévoles %s, cliquez sur ce lien (valable %d minutes) :\n\n%s\n\nSi vous n'avez pas demandé cette connexion, ignorez simplement ce message.\n",
		org, int(magicLinkTTL.Minutes()), link)
	if err := s.Mailer.Send(email, fmt.Sprintf("[%s] Votre lien de connexion", org), body); err != nil {
		s.serverError(w, r, err)
		return
	}
	data := M{"Email": email}
	if s.Cfg.DevMode {
		data["Link"] = link
	}
	s.render(w, r, "login_sent", data)
}

func (s *Server) authToken(w http.ResponseWriter, r *http.Request) {
	userID, err := s.DB.ConsumeMagicLink(r.PathValue("token"))
	if err != nil {
		s.render(w, r, "error", M{"Title": "Lien invalide", "Message": "Ce lien de connexion a expiré ou a déjà été utilisé. Demandez-en un nouveau.", "ShowLogin": true})
		return
	}
	sid, err := s.DB.CreateSession(userID, sessionTTL)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: sid, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.Cfg.Secure, MaxAge: int(sessionTTL.Seconds()),
	})
	u, _ := s.DB.GetUser(userID)
	next := r.URL.Query().Get("next")
	if next == "" || !strings.HasPrefix(next, "/") {
		next = "/"
	}
	if u.Name == "" {
		s.flash(w, "Bienvenue ! Indiquez votre nom pour que les responsables puissent vous identifier.")
		next = "/profile?next=" + next
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.DB.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
