package web

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
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
		"DefaultPoleName": s.DB.GetSetting("default_pole_name", "Organisation"),
		"SourceURL":       s.DB.GetSetting("source_url", ""),
	})
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
		s.Uploads.Remove(s.DB.GetSetting("org_logo", ""))
		s.DB.SetSetting("org_logo", name)
		s.flash(w, "Logo mis à jour.")
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminLogoDelete(w http.ResponseWriter, r *http.Request) {
	s.Uploads.Remove(s.DB.GetSetting("org_logo", ""))
	s.DB.SetSetting("org_logo", "")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
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
		s.flash(w, "Indiquez un nom et des dates valides pour la nouvel événement.")
		http.Redirect(w, r, src.AdminURL()+"?tab=settings", http.StatusSeeOther)
		return
	}
	id, err := s.DB.DuplicateEvent(src.ID, name, startsOn, endsOn, s.DB.GetSetting("default_pole_name", "Organisation"))
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
	id, err := s.DB.CreateEvent(name, startsOn, endsOn, s.formTimePtr(r, "freeze_at"), s.DB.GetSetting("default_pole_name", "Organisation"))
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
	events, _ := s.DB.ListEvents()
	s.render(w, r, "admin_event", M{
		"Event": ev, "Coverage": coverage, "Stats": stats, "Messages": msgs, "Modes": db.Modes, "Events": events,
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
	if err := s.DB.DeleteEvent(pathID(r, "id")); err != nil {
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
	s.render(w, r, "admin_volunteers", M{"Event": ev, "Volunteers": vols})
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

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.DB.ListUsers()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := M{"Users": users}
	if ev, err := s.currentEvent(currentUser(r)); err == nil {
		data["Event"] = ev
		data["UserPoles"], _ = s.DB.UserPoles(ev.ID)
		data["Poles"], _ = s.DB.ListPoles(ev.ID)
	}
	s.render(w, r, "admin_users", data)
}

func (s *Server) adminUserToggleAdmin(w http.ResponseWriter, r *http.Request) {
	u, err := s.DB.GetUser(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if u.IsAdmin {
		if n, _ := s.DB.CountAdmins(); n <= 1 {
			s.flash(w, "Impossible de retirer le dernier administrateur.")
			http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
			return
		}
	}
	if err := s.DB.SetAdmin(u.ID, !u.IsAdmin); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *Server) adminUserDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.DB.GetUser(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if u.ID == currentUser(r).ID {
		s.flash(w, "Vous ne pouvez pas supprimer votre propre compte depuis cette page.")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	if err := s.DB.DeleteUser(u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Compte supprimé.")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
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
	if err := s.DB.JoinPole(pole.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, u.DisplayName()+" a été ajouté·e au pôle « "+pole.Name+" ».")
	back := r.FormValue("back")
	if back == "" || !strings.HasPrefix(back, "/") {
		back = "/admin/users"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
