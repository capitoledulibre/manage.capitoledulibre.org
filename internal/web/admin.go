package web

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"benevoles/internal/db"
)

func (s *Server) adminHome(w http.ResponseWriter, r *http.Request) {
	events, err := s.DB.ListEvents()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	users, _ := s.DB.ListUsers()
	s.render(w, r, "admin", M{
		"Events": events, "UserCount": len(users), "OrgLogo": s.DB.GetSetting("org_logo", ""),
		"OrgName":         s.DB.GetSetting("org_name", "Bénévoles"),
		"DefaultPoleName": s.defaultPoleName(),
		"SourceURL":       s.DB.GetSetting("source_url", ""),
	})
}

// defaultPoleName names the pole created with every event, which holds the
// shifts that belong to no team and includes every volunteer. "Organisation"
// read as "reserved for organisers"; "Général" says nothing about who is in
// it, and the organiser can narrow it down per event.
func (s *Server) defaultPoleName() string {
	return s.DB.GetSetting("default_pole_name", "Général")
}

func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	if v := strings.TrimSpace(r.FormValue("org_name")); v != "" {
		s.DB.SetSetting("org_name", v)
	}
	if v := strings.TrimSpace(r.FormValue("default_pole_name")); v != "" {
		s.DB.SetSetting("default_pole_name", v)
	}
	if r.Form.Has("source_url") {
		v := strings.TrimSpace(r.FormValue("source_url"))
		if v == "" || strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://") {
			s.DB.SetSetting("source_url", v)
		}
	}
	s.flash(w, "Paramètres enregistrés.")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminLogo(w http.ResponseWriter, r *http.Request) {
	name, err := s.Uploads.Save(r, "logo")
	if err != nil {
		s.flash(w, "Image refusée : PNG, JPEG, GIF ou WebP, 2 Mo maximum.")
	} else if name != "" {
		old := s.DB.GetSetting("org_logo", "")
		s.DB.SetSetting("org_logo", name)
		s.dropImage(old)
		s.flash(w, "Logo mis à jour.")
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminLogoDelete(w http.ResponseWriter, r *http.Request) {
	old := s.DB.GetSetting("org_logo", "")
	s.DB.SetSetting("org_logo", "")
	s.dropImage(old)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// adminEventImage sets the logo shown next to the name of one edition.
func (s *Server) adminEventImage(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name, err := s.Uploads.Save(r, "image")
	if err != nil {
		s.flash(w, "Image refusée : PNG, JPEG, GIF ou WebP, 2 Mo maximum.")
	} else if name != "" {
		if err := s.DB.SetEventImage(ev.ID, name); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.dropImage(ev.Image)
		s.flash(w, "Logo de l'événement mis à jour.")
	}
	http.Redirect(w, r, ev.AdminURL()+"?tab=settings", http.StatusSeeOther)
}

func (s *Server) adminEventImageDelete(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.DB.SetEventImage(ev.ID, ""); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.dropImage(ev.Image)
	http.Redirect(w, r, ev.AdminURL()+"?tab=settings", http.StatusSeeOther)
}

// planningRow is one line of the global planning grid: a pole, or a pole location.
type planningRow struct {
	Pole     db.Pole
	Location string
	Slots    []db.MySlot
	Capacity int
	Filled   int
}

type planningDay struct {
	Day      string
	Rows     []planningRow
	Capacity int
	Filled   int
}

func (s *Server) adminPlanning(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slots, err := s.DB.ListEventSlots(ev.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	poles, _ := s.DB.ListPoles(ev.ID)
	poleByID := map[int64]db.Pole{}
	for _, p := range poles {
		poleByID[p.ID] = p
	}
	onlyShort := r.URL.Query().Get("short") == "1"
	var days []planningDay
	for _, sl := range slots {
		if onlyShort && sl.IsFull() {
			continue
		}
		key := sl.Day(s.Cfg.Timezone)
		if len(days) == 0 || days[len(days)-1].Day != key {
			days = append(days, planningDay{Day: key})
		}
		d := &days[len(days)-1]
		var row *planningRow
		for i := range d.Rows {
			if d.Rows[i].Pole.ID == sl.PoleID && d.Rows[i].Location == sl.LocationName {
				row = &d.Rows[i]
				break
			}
		}
		if row == nil {
			d.Rows = append(d.Rows, planningRow{Pole: poleByID[sl.PoleID], Location: sl.LocationName})
			row = &d.Rows[len(d.Rows)-1]
		}
		row.Slots = append(row.Slots, sl)
		row.Capacity += sl.Capacity
		row.Filled += sl.Filled
		d.Capacity += sl.Capacity
		d.Filled += sl.Filled
	}
	// Keep the pole order of the event inside each day.
	order := map[int64]int{}
	for i, p := range poles {
		order[p.ID] = i
	}
	for i := range days {
		sort.SliceStable(days[i].Rows, func(a, b int) bool {
			ra, rb := days[i].Rows[a], days[i].Rows[b]
			if ra.Pole.ID != rb.Pole.ID {
				return order[ra.Pole.ID] < order[rb.Pole.ID]
			}
			return ra.Location < rb.Location
		})
	}
	s.render(w, r, "admin_planning", M{"Event": ev, "Days": days, "OnlyShort": onlyShort})
}

func (s *Server) adminEventDuplicate(w http.ResponseWriter, r *http.Request) {
	src, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	startsOn, endsOn := r.FormValue("starts_on"), r.FormValue("ends_on")
	if name == "" || startsOn == "" || endsOn == "" || endsOn < startsOn {
		s.flash(w, "Indiquez un nom et des dates valides pour le nouvel événement.")
		http.Redirect(w, r, src.AdminURL()+"?tab=settings", http.StatusSeeOther)
		return
	}
	id, err := s.DB.DuplicateEvent(src.ID, name, startsOn, endsOn, s.defaultPoleName())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	created, _ := s.DB.GetEvent(id)
	s.flash(w, "Événement créé en brouillon avec les pôles, emplacements et responsables de « "+src.Name+" ». Les créneaux sont à recréer.")
	http.Redirect(w, r, created.AdminURL(), http.StatusSeeOther)
}

func (s *Server) adminPoleCopy(w http.ResponseWriter, r *http.Request) {
	pole, err := s.DB.GetPole(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	target := formInt64(r, "event_id")
	if target == 0 {
		target = pole.EventID
	}
	if _, err := s.DB.GetEvent(target); err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.isEventAdmin(currentUser(r), target) {
		s.forbidden(w, r)
		return
	}
	id, err := s.DB.CopyPole(pole.ID, target)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	copied, _ := s.DB.GetPole(id)
	s.flash(w, "Pôle « "+pole.Name+" » copié : réglages, emplacements et responsables. Les créneaux et bénévoles ne sont pas repris.")
	http.Redirect(w, r, copied.ManageURL()+"?tab=settings", http.StatusSeeOther)
}

// --- events -----------------------------------------------------------

func (s *Server) adminEventNew(w http.ResponseWriter, r *http.Request) {
	year := time.Now().Year()
	s.render(w, r, "event_form", M{"Event": db.Event{Name: fmt.Sprintf("%s %d", s.DB.GetSetting("org_name", ""), year), Status: db.StatusDraft}, "New": true})
}

func (s *Server) adminEventCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	startsOn, endsOn := r.FormValue("starts_on"), r.FormValue("ends_on")
	if name == "" || startsOn == "" || endsOn == "" || endsOn < startsOn {
		s.render(w, r, "event_form", M{"Event": db.Event{Name: name, StartsOn: startsOn, EndsOn: endsOn}, "New": true, "Error": "Nom et dates sont obligatoires, la fin ne peut pas précéder le début."})
		return
	}
	id, err := s.DB.CreateEvent(name, startsOn, endsOn, s.formTimePtr(r, "freeze_at"), s.defaultPoleName())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	created, _ := s.DB.GetEvent(id)
	s.flash(w, "Événement créé. Ajoutez maintenant les pôles et leurs responsables.")
	http.Redirect(w, r, created.AdminURL(), http.StatusSeeOther)
}

func (s *Server) adminEvent(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	coverage, err := s.DB.ListPoleCoverage(ev.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	stats, _ := s.DB.GetEventStats(ev.ID)
	msgs, _ := s.DB.ListEventMessages(ev.ID)
	u := currentUser(r)
	// A pole can only be copied into an event the user also runs.
	all, _ := s.DB.ListEvents()
	var events []db.Event
	for _, e := range all {
		if s.isEventAdmin(u, e.ID) {
			events = append(events, e)
		}
	}
	coordinators, _ := s.DB.ListEventCoordinators(ev.ID)
	var allUsers []db.User
	if u.IsAdmin && r.URL.Query().Get("tab") == "settings" {
		allUsers, _ = s.DB.ListUsers()
	}
	s.render(w, r, "admin_event", M{
		"Event": ev, "Coverage": coverage, "Stats": stats, "Messages": msgs, "Modes": db.Modes, "Events": events,
		"Coordinators": coordinators, "AllUsers": allUsers,
		"NextYear": time.Now().Year() + 1,
		"Statuses": []string{db.StatusDraft, db.StatusOpen, db.StatusFrozen, db.StatusArchived},
		"Tab":      r.URL.Query().Get("tab"),
	})
}

func (s *Server) adminEventUpdate(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if name := strings.TrimSpace(r.FormValue("name")); name != "" {
		ev.Name = name
	}
	if r.Form.Has("slug") {
		ev.Slug = strings.TrimSpace(r.FormValue("slug"))
	}
	if st := r.FormValue("status"); st != "" {
		ev.Status = st
	}
	if v := r.FormValue("starts_on"); v != "" {
		ev.StartsOn = v
	}
	if v := r.FormValue("ends_on"); v != "" {
		ev.EndsOn = v
	}
	ev.FreezeAt = s.formTimePtr(r, "freeze_at")
	if err := s.DB.UpdateEvent(ev); err != nil {
		s.serverError(w, r, err)
		return
	}
	ev, _ = s.DB.GetEvent(ev.ID)
	s.flash(w, "Événement enregistré.")
	http.Redirect(w, r, ev.AdminURL()+"?tab=settings", http.StatusSeeOther)
}

func (s *Server) adminEventDelete(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.DB.DeleteEvent(ev.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Événement supprimé.")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminEventMessage(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if body := strings.TrimSpace(r.FormValue("body")); body != "" {
		if _, err := s.DB.CreateMessage(ev.ID, 0, 0, currentUser(r).ID, body); err != nil {
			s.serverError(w, r, err)
			return
		}
		// A message nobody comes back to read is a message nobody got, so the
		// author decides whether it is worth an e-mail.
		if r.FormValue("notify") == "1" {
			vols, _ := s.DB.ListEventVolunteers(ev.ID)
			recipients := make([]db.User, 0, len(vols))
			for _, v := range vols {
				recipients = append(recipients, v.User)
			}
			s.notifyMessage(recipients, "les bénévoles de « "+ev.Name+" »", body, s.Cfg.BaseURL+ev.URL())
			s.flash(w, fmt.Sprintf("Message publié et envoyé à %d bénévole(s).", len(recipients)))
		} else {
			s.flash(w, "Message publié sur la page d'accueil de l'événement.")
		}
	}
	http.Redirect(w, r, ev.AdminURL()+"?tab=messages", http.StatusSeeOther)
}

// --- poles --------------------------------------------------------------

func (s *Server) adminPoleCreate(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, ev.AdminURL(), http.StatusSeeOther)
		return
	}
	poles, _ := s.DB.ListPoles(ev.ID)
	id, err := s.DB.CreatePole(db.Pole{EventID: ev.ID, Name: name, AccessMode: r.FormValue("access_mode"), Icon: strings.TrimSpace(r.FormValue("icon")), Position: len(poles)})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if email := strings.ToLower(strings.TrimSpace(r.FormValue("manager_email"))); strings.Contains(email, "@") {
		if u, err := s.DB.GetOrCreateUser(email); err == nil {
			s.DB.AddPoleManager(id, u.ID)
		}
	}
	created, _ := s.DB.GetPole(id)
	s.flash(w, "Pôle « "+name+" » créé.")
	http.Redirect(w, r, created.ManageURL()+"?tab=settings", http.StatusSeeOther)
}

func (s *Server) adminPoleDelete(w http.ResponseWriter, r *http.Request) {
	pole, err := s.DB.GetPole(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if pole.IsDefault {
		s.flash(w, "Le pôle par défaut ne peut pas être supprimé.")
	} else if err := s.DB.DeletePole(pole.ID); err != nil {
		s.serverError(w, r, err)
		return
	} else {
		s.flash(w, "Pôle supprimé.")
	}
	ev, _ := s.DB.GetEvent(pole.EventID)
	http.Redirect(w, r, ev.AdminURL(), http.StatusSeeOther)
}

func (s *Server) adminManagerAdd(w http.ResponseWriter, r *http.Request) {
	pole, err := s.DB.GetPole(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	if !strings.Contains(email, "@") {
		s.flash(w, "Adresse e-mail invalide.")
	} else {
		u, err := s.DB.GetOrCreateUser(email)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.DB.AddPoleManager(pole.ID, u.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.flash(w, u.DisplayName()+" est maintenant responsable du pôle.")
	}
	http.Redirect(w, r, pole.ManageURL()+"?tab=members", http.StatusSeeOther)
}

func (s *Server) adminManagerRemove(w http.ResponseWriter, r *http.Request) {
	pole, err := s.DB.GetPole(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.DB.RemovePoleManager(pole.ID, pathID(r, "uid")); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, pole.ManageURL()+"?tab=members", http.StatusSeeOther)
}

// --- volunteers ---------------------------------------------------------

func (s *Server) adminVolunteers(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	vols, err := s.DB.ListEventVolunteers(ev.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	poles, _ := s.DB.ListPoles(ev.ID)
	s.render(w, r, "admin_volunteers", M{"Event": ev, "Volunteers": vols, "Poles": poles})
}

func (s *Server) adminVolunteersCSV(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	vols, err := s.DB.ListEventVolunteers(ev.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="benevoles.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"name", "email", "phone", "poles", "slots"})
	for _, v := range vols {
		cw.Write([]string{v.User.Name, v.User.Email, v.User.Phone, strings.Join(v.Poles, ", "), fmt.Sprint(v.SlotCount)})
	}
	cw.Flush()
}

// --- users --------------------------------------------------------------

// usersPerPage bounds the account list. Accounts are kept from one event to
// the next, so the table grows for ever and used to be rendered whole and
// filtered in the browser.
const usersPerPage = 50

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := formInt(r, "page", 1)
	if page < 1 {
		page = 1
	}
	total, err := s.DB.CountUsers(q)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	offset := (page - 1) * usersPerPage
	if offset > total {
		page, offset = 1, 0
	}
	users, err := s.DB.SearchUsers(q, usersPerPage, offset)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// Row actions redirect back to the exact list the administrator was
	// looking at, search and page included.
	back := "/admin/users"
	if q != "" || page > 1 {
		back += "?" + url.Values{"q": {q}, "page": {strconv.Itoa(page)}}.Encode()
	}
	data := M{
		"Users": users, "Q": q, "Page": page, "Total": total, "Back": back,
		"From": offset + 1, "To": offset + len(users),
		"HasPrev": page > 1, "HasNext": offset+len(users) < total,
	}
	if ev, err := s.currentEvent(currentUser(r)); err == nil {
		data["Event"] = ev
		data["UserPoles"], _ = s.DB.UserPoles(ev.ID)
		data["Poles"], _ = s.DB.ListPoles(ev.ID)
	}
	s.render(w, r, "admin_users", data)
}

// usersBack keeps the search and the page the administrator was looking at.
func usersBack(r *http.Request) string {
	if back := r.FormValue("back"); safePath(back) && strings.HasPrefix(back, "/admin/users") {
		return back
	}
	return "/admin/users"
}

func (s *Server) adminUserDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.DB.GetUser(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if u.ID == currentUser(r).ID {
		s.flash(w, "Vous ne pouvez pas supprimer votre propre compte depuis cette page.")
		http.Redirect(w, r, usersBack(r), http.StatusSeeOther)
		return
	}
	if err := s.DB.DeleteUser(u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.dropImage(u.Avatar)
	s.flash(w, "Compte de "+u.DisplayName()+" supprimé.")
	http.Redirect(w, r, usersBack(r), http.StatusSeeOther)
}

// adminUserJoinPole adds a user to a pole from the user lists, bypassing the access mode.
func (s *Server) adminUserJoinPole(w http.ResponseWriter, r *http.Request) {
	u, err := s.DB.GetUser(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pole, err := s.DB.GetPole(formInt64(r, "pole_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.isEventAdmin(currentUser(r), pole.EventID) {
		s.forbidden(w, r)
		return
	}
	if err := s.DB.JoinPole(pole.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, u.DisplayName()+" a été ajouté·e au pôle « "+pole.Name+" ».")
	back := r.FormValue("back")
	if !safePath(back) {
		back = "/admin/users"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// adminUserCreate registers a volunteer on their behalf (no self sign-up
// needed) and optionally puts them in a pole.
func (s *Server) adminUserCreate(w http.ResponseWriter, r *http.Request) {
	if !s.canRegisterVolunteer(r) {
		s.forbidden(w, r)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	back := r.FormValue("back")
	if !safePath(back) {
		back = "/admin/users"
	}
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		s.flash(w, "Adresse e-mail invalide.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	u, err := s.DB.EnsureVolunteer(email, strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("phone")))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	msg := u.DisplayName() + " est enregistré·e"
	switch poleID, eventID := formInt64(r, "pole_id"), formInt64(r, "event_id"); {
	case poleID != 0:
		pole, err := s.DB.GetPole(poleID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.DB.JoinPole(pole.ID, u.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		msg += " et ajouté·e au pôle « " + pole.Name + " »"
	case eventID != 0:
		// No pole chosen: still attach the person to the event through its
		// default pole so they show up in the event's volunteer list.
		ev, err := s.DB.GetEvent(eventID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		def, err := s.DB.DefaultPole(ev.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if err := s.DB.JoinPole(def.ID, u.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		msg += " comme bénévole de « " + ev.Name + " », sans pôle pour l'instant"
	default:
		msg += ". Sans événement ni pôle, la personne n'apparaît que dans Admin → Comptes"
	}
	s.flash(w, msg+". Elle se connectera simplement avec son adresse e-mail.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// canRegisterVolunteer tells whether the user may create an account on
// someone's behalf with this form: administrators always, event coordinators when
// the account lands in their event (through a pole of it, or the event itself).
func (s *Server) canRegisterVolunteer(r *http.Request) bool {
	u := currentUser(r)
	if u.IsAdmin {
		return true
	}
	if poleID := formInt64(r, "pole_id"); poleID != 0 {
		pole, err := s.DB.GetPole(poleID)
		return err == nil && s.isEventAdmin(u, pole.EventID)
	}
	if eventID := formInt64(r, "event_id"); eventID != 0 {
		return s.isEventAdmin(u, eventID)
	}
	return false
}

// --- coordinators -------------------------------------------------------

// adminCoordinatorAdd makes someone coordinator of an event. Only administrators
// grant it: it gives every right inside the event.
func (s *Server) adminCoordinatorAdd(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		s.flash(w, "Adresse e-mail invalide.")
	} else {
		u, err := s.DB.GetOrCreateUser(email)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.DB.AddEventCoordinator(ev.ID, u.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.flash(w, u.DisplayName()+" fait maintenant partie de la coordination de « "+ev.Name+" ».")
	}
	http.Redirect(w, r, ev.AdminURL()+"?tab=settings", http.StatusSeeOther)
}

func (s *Server) adminCoordinatorRemove(w http.ResponseWriter, r *http.Request) {
	ev, err := s.DB.GetEventBySlug(r.PathValue("event"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.DB.RemoveEventCoordinator(ev.ID, pathID(r, "uid")); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Retiré·e de la coordination. La personne reste bénévole de l'événement.")
	http.Redirect(w, r, ev.AdminURL()+"?tab=settings", http.StatusSeeOther)
}
