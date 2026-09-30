package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"benevoles/internal/db"
)

// currentEvent returns the event to display to the user. Admins see the
// most recent event even if it is a draft.
func (s *Server) currentEvent(u *db.User) (db.Event, error) {
	ev, err := s.DB.CurrentEvent()
	if err == nil || !u.IsAdmin || !errors.Is(err, db.ErrNotFound) {
		return ev, err
	}
	eds, err := s.DB.ListEvents()
	if err != nil {
		return ev, err
	}
	if len(eds) == 0 {
		return ev, db.ErrNotFound
	}
	return eds[0], nil
}

type poleView struct {
	db.Pole
	Member   bool
	Manager  bool
	Slots    int
	Missing  int
	Coverage db.PoleCoverage
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ev, err := s.currentEvent(u)
	if errors.Is(err, db.ErrNotFound) {
		s.render(w, r, "no_event", nil)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderHome(w, r, ev)
}

// eventHome shows a specific event (/{event}); drafts are for managers and admins.
func (s *Server) eventHome(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ev.IsVisibleToVolunteers() && !u.IsAdmin {
		if managed, _ := s.DB.ManagedPoleIDs(ev.ID, u.ID); len(managed) == 0 {
			s.notFound(w, r)
			return
		}
	}
	s.renderHome(w, r, ev)
}

func (s *Server) renderHome(w http.ResponseWriter, r *http.Request, ev db.Event) {
	u := currentUser(r)
	coverage, err := s.DB.ListPoleCoverage(ev.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	members, _ := s.DB.MemberPoleIDs(ev.ID, u.ID)
	managed, _ := s.DB.ManagedPoleIDs(ev.ID, u.ID)
	var poles []poleView
	for _, c := range coverage {
		// Restricted poles stay invisible to people who are not in them.
		if c.Pole.AccessMode == db.ModeRestricted && !members[c.Pole.ID] && !u.IsAdmin && !managed[c.Pole.ID] {
			continue
		}
		poles = append(poles, poleView{
			Pole: c.Pole, Member: members[c.Pole.ID], Manager: u.IsAdmin || managed[c.Pole.ID],
			Slots: c.Slots, Missing: c.Capacity - c.Filled, Coverage: c,
		})
	}
	mySlots, _ := s.DB.ListUserSlots(ev.ID, u.ID)
	var upcoming []db.MySlot
	for _, sl := range mySlots {
		if sl.EndsAt.After(time.Now()) {
			upcoming = append(upcoming, sl)
		}
	}
	if len(upcoming) > 3 {
		upcoming = upcoming[:3]
	}
	msgs, _ := s.DB.ListEventMessages(ev.ID)
	prevEvent, returning := s.returningPoles(ev, u, poles)
	s.render(w, r, "home", M{
		"Event": ev, "Poles": poles, "Messages": msgs,
		"MySlots": mySlots, "Upcoming": upcoming, "Joined": len(members) > 0,
		"PrevEvent": prevEvent, "Returning": returning,
		"Error": r.URL.Query().Get("error"),
	})
}

// returningPoles finds, for a volunteer who has not joined anything yet this
// year, the poles they were in at the previous event they took part in. Coming
// back once a year, most people want the same team again, and the full list of
// poles made them look for it. Suggestions stop at the first join: from then
// on the volunteer has found their way.
func (s *Server) returningPoles(ev db.Event, u *db.User, poles []poleView) (db.Event, []poleView) {
	for _, p := range poles {
		if p.Member && !p.IsDefault {
			return db.Event{}, nil
		}
	}
	prev, slugs, err := s.DB.PreviousPoleSlugs(ev.ID, u.ID)
	if err != nil {
		if !errors.Is(err, db.ErrNotFound) {
			log.Printf("previous poles of user %d: %v", u.ID, err)
		}
		return db.Event{}, nil
	}
	var out []poleView
	for _, slug := range slugs {
		for _, p := range poles {
			if p.Slug == slug && !p.IsDefault {
				out = append(out, p)
			}
		}
	}
	return prev, out
}

// dayGroup groups slots by local day for display.
type dayGroup struct {
	Day   string
	Slots []db.MySlot
}

func groupMySlots(slots []db.MySlot, loc *time.Location) []dayGroup {
	var out []dayGroup
	for _, sl := range slots {
		key := sl.Day(loc)
		if len(out) == 0 || out[len(out)-1].Day != key {
			out = append(out, dayGroup{Day: key})
		}
		out[len(out)-1].Slots = append(out[len(out)-1].Slots, sl)
	}
	return out
}

func (s *Server) myPlanning(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ev, err := s.currentEvent(u)
	if errors.Is(err, db.ErrNotFound) {
		s.render(w, r, "no_event", nil)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	slots, err := s.DB.ListUserSlots(ev.ID, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	today := time.Now().In(s.Cfg.Timezone).Format("2006-01-02")
	// The subscription secret is created on first visit to this page, so an
	// account that never opens it never gets one.
	token, err := s.DB.CalendarToken(u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "me", M{"Event": ev, "Days": groupMySlots(slots, s.Cfg.Timezone), "Today": today,
		"Count": len(slots), "CalendarURL": fmt.Sprintf("%s/calendar/%s.ics", s.Cfg.BaseURL, token)})
}

// calendarFor renders one volunteer's slots for an event as an iCalendar
// document. UIDs are stable per (slot, user) so a calendar that re-reads the
// feed updates its entries instead of duplicating them.
func (s *Server) calendarFor(u db.User, ev db.Event) (string, error) {
	slots, err := s.DB.ListUserSlots(ev.ID, u.ID)
	if err != nil {
		return "", err
	}
	org := s.DB.GetSetting("org_name", "Bénévoles")
	esc := func(v string) string {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, ";", `\;`)
		v = strings.ReplaceAll(v, ",", `\,`)
		return strings.ReplaceAll(v, "\n", `\n`)
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//benevoles//FR\r\nCALSCALE:GREGORIAN\r\n")
	fmt.Fprintf(&b, "X-WR-CALNAME:%s – %s\r\n", esc(org), esc(ev.Name))
	// Hint for subscribed calendars: this feed is worth re-reading regularly.
	b.WriteString("X-PUBLISHED-TTL:PT1H\r\nREFRESH-INTERVAL;VALUE=DURATION:PT1H\r\n")
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, sl := range slots {
		summary := sl.PoleName
		if sl.Title != "" {
			summary += " – " + sl.Title
		}
		b.WriteString("BEGIN:VEVENT\r\n")
		fmt.Fprintf(&b, "UID:slot-%d-%d@benevoles\r\nDTSTAMP:%s\r\n", sl.ID, u.ID, stamp)
		fmt.Fprintf(&b, "DTSTART:%s\r\nDTEND:%s\r\n", sl.StartsAt.UTC().Format("20060102T150405Z"), sl.EndsAt.UTC().Format("20060102T150405Z"))
		fmt.Fprintf(&b, "SUMMARY:%s\r\n", esc(summary))
		if sl.LocationName != "" {
			fmt.Fprintf(&b, "LOCATION:%s\r\n", esc(sl.LocationName))
		}
		fmt.Fprintf(&b, "URL:%s/slots/%d\r\n", s.Cfg.BaseURL, sl.ID)
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return b.String(), nil
}

func writeCalendar(w http.ResponseWriter, body, filename string) {
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Write([]byte(body))
}

// myPlanningICS downloads the planning once.
func (s *Server) myPlanningICS(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ev, err := s.currentEvent(u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body, err := s.calendarFor(*u, ev)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeCalendar(w, body, "benevolat.ics")
}

// calendarFeed is the subscription endpoint. It carries its own secret instead
// of a session, because a calendar application polls it on its own: a
// downloaded file goes stale the moment a manager moves a slot.
func (s *Server) calendarFeed(w http.ResponseWriter, r *http.Request) {
	u, err := s.DB.UserByCalendarToken(strings.TrimSuffix(r.PathValue("token"), ".ics"))
	if err != nil {
		s.notFound(w, r)
		return
	}
	ev, err := s.DB.CurrentEvent()
	if err != nil {
		// No event running: answer an empty but valid calendar rather than an
		// error, so the subscription stays alive between two editions.
		writeCalendar(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//benevoles//FR\r\nEND:VCALENDAR\r\n", "benevolat.ics")
		return
	}
	body, err := s.calendarFor(u, ev)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeCalendar(w, body, "benevolat.ics")
}

// calendarReset issues a new subscription secret, which stops every calendar
// subscribed with the old one.
func (s *Server) calendarReset(w http.ResponseWriter, r *http.Request) {
	if _, err := s.DB.ResetCalendarToken(currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Nouveau lien d'abonnement généré. L'ancien ne fonctionne plus : réabonnez vos agendas.")
	http.Redirect(w, r, "/me", http.StatusSeeOther)
}

// welcome is the first screen of a new account. The profile page mixes the
// name with the photo, Gravatar and account deletion, which is a lot to read
// for someone who came to pick a shift: this asks for the name alone, the
// phone as an option, and goes on to where the volunteer was heading.
func (s *Server) welcome(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	if !safePath(next) {
		next = "/"
	}
	if currentUser(r).Name != "" {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	data := s.loginContext(next)
	data["Next"] = next
	s.render(w, r, "welcome", data)
}

func (s *Server) profileForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "profile", M{"Next": r.URL.Query().Get("next"), "Gravatar": s.Cfg.Gravatar})
}

// profilePhoto stores the picture a volunteer uploaded. It wins over the
// Gravatar fallback, which is what AvatarUpload records.
func (s *Server) profilePhoto(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	name, err := s.Uploads.Save(r, "photo")
	switch {
	case err != nil:
		s.flash(w, "Image refusée : PNG, JPEG, GIF ou WebP, 2 Mo maximum.")
	case name == "":
		s.flash(w, "Choisissez un fichier image.")
	default:
		old, err := s.DB.SetAvatar(u.ID, name, db.AvatarUpload)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		s.dropImage(old)
		s.flash(w, "Photo de profil enregistrée.")
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

// profilePhotoDelete removes the picture and remembers the choice, so that the
// Gravatar fallback does not hand a face back on the next page view.
func (s *Server) profilePhotoDelete(w http.ResponseWriter, r *http.Request) {
	old, err := s.DB.SetAvatar(currentUser(r).ID, "", db.AvatarOptOut)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.dropImage(old)
	s.flash(w, "Photo retirée : vos initiales sont affichées à la place.")
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

// profileGravatar re-enables the fallback and looks the address up right away,
// so the volunteer sees the outcome of their click instead of a promise.
func (s *Server) profileGravatar(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if !s.Cfg.Gravatar {
		s.flash(w, "La recherche Gravatar est désactivée sur ce serveur.")
		http.Redirect(w, r, "/profile", http.StatusSeeOther)
		return
	}
	old, err := s.DB.SetAvatar(u.ID, "", db.AvatarNone)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.dropImage(old)
	switch found, err := s.Avatars.Lookup(u.ID); {
	case err != nil:
		log.Printf("gravatar lookup for %s: %v", u.Email, err)
		s.flash(w, "Gravatar est injoignable pour le moment. Réessayez plus tard.")
	case found:
		s.flash(w, "Avatar Gravatar récupéré.")
	default:
		s.flash(w, "Aucun avatar n'est associé à "+u.Email+" sur gravatar.com.")
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

func (s *Server) profileSubmit(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	name := strings.TrimSpace(r.FormValue("name"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	if name == "" {
		page, data := "profile", M{"Gravatar": s.Cfg.Gravatar}
		if r.FormValue("welcome") == "1" {
			page, data = "welcome", s.loginContext(r.FormValue("next"))
		}
		data["Error"] = "Indiquez votre nom pour continuer."
		data["Next"] = r.FormValue("next")
		s.render(w, r, page, data)
		return
	}
	if err := s.DB.UpdateProfile(u.ID, name, phone); err != nil {
		s.serverError(w, r, err)
		return
	}
	if r.FormValue("welcome") != "1" {
		s.flash(w, "Profil enregistré.")
	}
	next := r.FormValue("next")
	if !safePath(next) {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) profileDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u.IsAdmin {
		s.flash(w, "Un administrateur ne peut pas supprimer son propre compte. Retirez d'abord le rôle administrateur.")
		http.Redirect(w, r, "/profile", http.StatusSeeOther)
		return
	}
	if err := s.DB.DeleteUser(u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	// The picture lives on disk, not in the database: delete it too.
	s.dropImage(u.Avatar)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
