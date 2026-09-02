package web

import (
	"errors"
	"fmt"
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
	s.render(w, r, "home", M{
		"Event": ev, "Poles": poles, "Messages": msgs,
		"MySlots": mySlots, "Upcoming": upcoming, "Joined": len(members) > 0,
		"Error": r.URL.Query().Get("error"),
	})
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
	s.render(w, r, "me", M{"Event": ev, "Days": groupMySlots(slots, s.Cfg.Timezone), "Today": today, "Count": len(slots)})
}

func (s *Server) myPlanningICS(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ev, err := s.currentEvent(u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slots, err := s.DB.ListUserSlots(ev.ID, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
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
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="benevolat.ics"`)
	w.Write([]byte(b.String()))
}

func (s *Server) profileForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "profile", M{"Next": r.URL.Query().Get("next")})
}

func (s *Server) profileSubmit(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	name := strings.TrimSpace(r.FormValue("name"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	if name == "" {
		s.render(w, r, "profile", M{"Error": "Le nom est obligatoire.", "Next": r.FormValue("next")})
		return
	}
	if err := s.DB.UpdateProfile(u.ID, name, phone); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Profil enregistré.")
	next := r.FormValue("next")
	if next == "" || !strings.HasPrefix(next, "/") {
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
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
