package web

import (
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strings"

	"benevoles/internal/db"
)

// loginContext names what the visitor was heading for before being asked to
// log in. An invitation link is the most common first contact with the
// application, and a bare "Connexion" form gave no clue about which pole or
// which event was involved, so the destination is resolved and displayed.
func (s *Server) loginContext(next string) M {
	data := M{"Next": next}
	if path := next; path != "" {
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 2 {
			if pole, err := s.DB.GetPoleBySlug(parts[0], parts[1]); err == nil && pole.AccessMode != db.ModeRestricted {
				if ev, err := s.DB.GetEvent(pole.EventID); err == nil && ev.IsVisibleToVolunteers() {
					data["Pole"] = pole
					data["Event"] = ev
					data["Managers"], _ = s.DB.ListPoleManagers(pole.ID)
					return data
				}
			}
		}
	}
	// No specific destination: name the event currently taking sign-ups, so a
	// visitor arriving on a bare /login knows what they are signing up for.
	if ev, err := s.DB.CurrentEvent(); err == nil && ev.IsVisibleToVolunteers() {
		data["Event"] = ev
	}
	return data
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", s.loginContext(r.URL.Query().Get("next")))
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	data := s.loginContext(r.FormValue("next"))
	data["Email"] = email
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		data["Error"] = "Cette adresse e-mail ne semble pas valide."
		s.render(w, r, "login", data)
		return
	}
	if !s.loginByIP.Allow(clientIP(r)) || !s.loginByEmail.Allow(email) {
		w.WriteHeader(http.StatusTooManyRequests)
		data["Error"] = "Trop de demandes de connexion. Vérifiez votre boîte de réception, puis réessayez dans un quart d'heure."
		s.render(w, r, "login", data)
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
	if next != "" && safePath(next) {
		link += "?next=" + next
	}
	org := s.DB.GetSetting("org_name", "Bénévoles")
	body := fmt.Sprintf("Bonjour,\n\nPour vous connecter à l'espace bénévoles %s, cliquez sur ce lien (valable %d minutes) :\n\n%s\n\nSi vous n'avez pas demandé cette connexion, ignorez simplement ce message.\n",
		org, int(magicLinkTTL.Minutes()), link)
	if err := s.Mailer.Send(email, fmt.Sprintf("[%s] Votre lien de connexion", org), body); err != nil {
		// The mail server being down is not the volunteer's fault, and a bare
		// 500 page gave them no idea of what to do next.
		log.Printf("login link to %s: %v", email, err)
		w.WriteHeader(http.StatusServiceUnavailable)
		data["Error"] = "Le lien de connexion n'a pas pu être envoyé. Réessayez dans quelques minutes ; si le problème continue, prévenez l'équipe d'organisation."
		s.render(w, r, "login", data)
		return
	}
	sent := s.loginContext(next)
	sent["Email"] = email
	if s.Cfg.DevMode {
		sent["Link"] = link
	}
	s.render(w, r, "login_sent", sent)
}

// invalidLink is the page of a login link that expired or was already used.
func (s *Server) invalidLink(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "error", M{"Title": "Lien invalide", "Message": "Ce lien de connexion a expiré ou a déjà été utilisé. Demandez-en un nouveau.", "ShowLogin": true})
}

// authConfirm is where the e-mailed link lands. It only checks the token and
// asks for one click: mail security gateways (Outlook Safe Links, Mimecast…)
// open every link of an incoming message before the recipient does, and when
// that visit logged in, it spent the token, so the volunteer's own click ended
// on "link expired". Nothing runs the form on its own either, since some of
// these scanners execute the page's scripts.
func (s *Server) authConfirm(w http.ResponseWriter, r *http.Request) {
	userID, err := s.DB.PeekMagicLink(r.PathValue("token"))
	if err != nil {
		s.invalidLink(w, r)
		return
	}
	u, err := s.DB.GetUser(userID)
	if err != nil {
		s.invalidLink(w, r)
		return
	}
	next := r.URL.Query().Get("next")
	if !safePath(next) {
		next = ""
	}
	data := s.loginContext(next)
	data["Account"] = u.Email
	data["Token"] = r.PathValue("token")
	s.render(w, r, "auth_confirm", data)
}

// authToken opens the session, from the confirmation page's button.
func (s *Server) authToken(w http.ResponseWriter, r *http.Request) {
	userID, err := s.DB.ConsumeMagicLink(r.PathValue("token"))
	if err != nil {
		s.invalidLink(w, r)
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
	next := r.FormValue("next")
	if !safePath(next) {
		next = "/"
	}
	if u.Name == "" {
		next = "/welcome?next=" + url.QueryEscape(next)
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
