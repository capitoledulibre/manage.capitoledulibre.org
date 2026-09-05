package web

import (
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"benevoles/internal/db"
)

// manageContext loads a pole and checks the user may manage it.
func (s *Server) manageContext(w http.ResponseWriter, r *http.Request, poleID int64) (db.Pole, db.Event, bool) {
	u := currentUser(r)
	pole, err := s.DB.GetPole(poleID)
	if err != nil {
		s.fail(w, r, err)
		return pole, db.Event{}, false
	}
	if !s.canManage(u, pole.ID) {
		s.forbidden(w, r)
		return pole, db.Event{}, false
	}
	ed, err := s.DB.GetEvent(pole.EventID)
	if err != nil {
		s.fail(w, r, err)
		return pole, ed, false
	}
	return pole, ed, true
}

// manageSlotContext loads a slot and checks the user may manage its pole.
func (s *Server) manageSlotContext(w http.ResponseWriter, r *http.Request, slotID int64) (db.Slot, db.Pole, db.Event, bool) {
	slot, err := s.DB.GetSlot(slotID, currentUser(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return slot, db.Pole{}, db.Event{}, false
	}
	pole, ed, ok := s.manageContext(w, r, slot.PoleID)
	return slot, pole, ed, ok
}

func (s *Server) managePole(w http.ResponseWriter, r *http.Request) {
	pole, ed, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	slots, err := s.DB.ListSlots(pole.ID, currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	locations, _ := s.DB.ListLocations(pole.ID)
	msgs, _ := s.DB.ListPoleMessages(pole.ID)
	members, _ := s.DB.ListPoleMembers(pole.ID)
	managers, _ := s.DB.ListPoleManagers(pole.ID)
	volunteers := map[int64][]db.User{}
	var capacity, filled, short int
	for _, sl := range slots {
		volunteers[sl.ID], _ = s.DB.ListSlotVolunteers(sl.ID)
		capacity += sl.Capacity
		filled += sl.Filled
		if !sl.IsFull() {
			short++
		}
	}
	u := currentUser(r)
	var allUsers []db.User
	if u.IsAdmin && r.URL.Query().Get("tab") == "members" {
		allUsers, _ = s.DB.ListUsers()
	}
	// A slot with people waiting is a slot worth enlarging: show it.
	waiting, _ := s.DB.WaitlistCounts(pole.ID)
	if waiting == nil {
		waiting = map[int64]int{}
	}
	s.render(w, r, "manage_pole", M{
		"CanAddMembers": u.IsAdmin || pole.ManagersCanAdd(), "AllUsers": allUsers, "Waiting": waiting,
		"Pole": pole, "Event": ed, "Days": groupSlots(slots, s.Cfg.Timezone), "Locations": locations,
		"Messages": msgs, "Members": members, "Managers": managers, "Volunteers": volunteers,
		"Capacity": capacity, "Filled": filled, "Short": short, "SlotCount": len(slots),
		"Tab": r.URL.Query().Get("tab"),
	})
}

func (s *Server) managePoleUpdate(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name != "" {
		pole.Name = name
	}
	if r.Form.Has("slug") && !pole.IsDefault {
		pole.Slug = strings.TrimSpace(r.FormValue("slug"))
	}
	pole.Description = strings.TrimSpace(r.FormValue("description"))
	pole.Icon = strings.TrimSpace(r.FormValue("icon"))
	pole.DefaultTasks = strings.TrimSpace(r.FormValue("default_tasks"))
	if mode := r.FormValue("access_mode"); mode != "" && !pole.IsDefault {
		valid := false
		for _, m := range db.Modes {
			valid = valid || m == mode
		}
		if valid && (mode != db.ModeRestricted || currentUser(r).IsAdmin || pole.AccessMode == db.ModeRestricted) {
			pole.AccessMode = mode
		}
	}
	pole.FreezeAt = s.formTimePtr(r, "freeze_at")
	if err := s.DB.UpdatePole(pole); err != nil {
		s.serverError(w, r, err)
		return
	}
	pole, _ = s.DB.GetPole(pole.ID) // slug may have been normalised
	s.flash(w, "Pôle enregistré.")
	http.Redirect(w, r, pole.ManageURL()+"?tab=settings", http.StatusSeeOther)
}

func (s *Server) managePoleKey(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	if _, err := s.DB.RegenerateAccessKey(pole.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Nouveau lien d'invitation généré. L'ancien lien ne fonctionne plus.")
	http.Redirect(w, r, pole.ManageURL()+"?tab=members", http.StatusSeeOther)
}

func (s *Server) managePoleImage(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	name, err := s.Uploads.Save(r, "image")
	if err != nil {
		s.flash(w, "Image refusée : PNG, JPEG, GIF ou WebP, 2 Mo maximum.")
	} else if name != "" {
		s.Uploads.Remove(pole.Image)
		if err := s.DB.SetPoleImage(pole.ID, name); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.flash(w, "Image du pôle mise à jour.")
	}
	http.Redirect(w, r, pole.ManageURL()+"?tab=settings", http.StatusSeeOther)
}

func (s *Server) managePoleImageDelete(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	s.Uploads.Remove(pole.Image)
	if err := s.DB.SetPoleImage(pole.ID, ""); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, pole.ManageURL()+"?tab=settings", http.StatusSeeOther)
}

// --- members ------------------------------------------------------------

// manageUserSearch answers the live search of the "add members" box.
func (s *Server) manageUserSearch(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var users []db.User
	if len(q) >= 2 {
		users, _ = s.DB.SearchUsersNotInPole(pole.ID, q, 8)
	}
	s.fragment(w, "user_search", M{"Pole": pole, "Users": users, "Q": q, "CanAdd": currentUser(r).IsAdmin || pole.ManagersCanAdd()})
}

func (s *Server) manageMemberAdd(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	if !pole.ManagersCanAdd() && !currentUser(r).IsAdmin {
		s.forbidden(w, r)
		return
	}
	added := 0
	for _, raw := range strings.FieldsFunc(r.FormValue("emails"), func(c rune) bool { return c == '\n' || c == ',' || c == ';' }) {
		// Accept "Prénom Nom <adresse>" as well as a bare address.
		addr, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		u, err := s.DB.EnsureVolunteer(strings.ToLower(addr.Address), addr.Name, "")
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.DB.JoinPole(pole.ID, u.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		added++
	}
	if isHX(r) {
		// Refresh the search results and, out of band, the members table.
		q := strings.TrimSpace(r.FormValue("q"))
		var users []db.User
		if len(q) >= 2 {
			users, _ = s.DB.SearchUsersNotInPole(pole.ID, q, 8)
		}
		members, _ := s.DB.ListPoleMembers(pole.ID)
		s.fragment(w, "member_added", M{"Pole": pole, "Users": users, "Q": q, "CanAdd": true, "Added": added, "Members": members, "User": currentUser(r)})
		return
	}
	if added == 0 {
		s.flash(w, "Aucune adresse e-mail valide.")
	} else if added == 1 {
		s.flash(w, "Bénévole ajouté·e au pôle.")
	} else {
		s.flash(w, fmt.Sprintf("%d bénévoles ajouté·es au pôle.", added))
	}
	http.Redirect(w, r, pole.ManageURL()+"?tab=members", http.StatusSeeOther)
}

func (s *Server) manageMemberRemove(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	if pole.IsDefault {
		s.flash(w, "Impossible de retirer quelqu'un du pôle par défaut.")
	} else {
		// Count the signups before they are cancelled, so the e-mail can say
		// exactly what the person is losing.
		u, _ := s.DB.GetUser(pathID(r, "uid"))
		freed, _ := s.DB.UserSlotIDsInPole(pole.ID, pathID(r, "uid"))
		if err := s.DB.LeavePole(pole.ID, pathID(r, "uid")); err != nil {
			s.serverError(w, r, err)
			return
		}
		if u.ID != 0 {
			s.notifyPoleRemoved(u, pole, len(freed))
		}
		for _, slotID := range freed {
			s.announceFreePlace(pole, slotID)
		}
		s.flash(w, "Bénévole retiré·e du pôle et de ses créneaux, et prévenu·e par e-mail.")
	}
	http.Redirect(w, r, pole.ManageURL()+"?tab=members", http.StatusSeeOther)
}

// --- locations ----------------------------------------------------------

func (s *Server) manageLocationCreate(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	// Accept several names at once, one per line.
	for _, line := range strings.Split(r.FormValue("names"), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			if _, err := s.DB.CreateLocation(pole.ID, name); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
	}
	http.Redirect(w, r, pole.ManageURL()+"?tab=settings", http.StatusSeeOther)
}

func (s *Server) manageLocationDelete(w http.ResponseWriter, r *http.Request) {
	loc, err := s.DB.GetLocation(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pole, _, ok := s.manageContext(w, r, loc.PoleID)
	if !ok {
		return
	}
	if err := s.DB.DeleteLocation(loc.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, pole.ManageURL()+"?tab=settings", http.StatusSeeOther)
}

// --- messages -----------------------------------------------------------

func (s *Server) managePoleMessage(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if body != "" {
		if _, err := s.DB.CreateMessage(pole.EventID, pole.ID, 0, currentUser(r).ID, body); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, pole.ManageURL()+"?tab=messages", http.StatusSeeOther)
}

func (s *Server) manageSlotMessage(w http.ResponseWriter, r *http.Request) {
	slot, pole, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if body != "" {
		if _, err := s.DB.CreateMessage(pole.EventID, pole.ID, slot.ID, currentUser(r).ID, body); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d", slot.ID), http.StatusSeeOther)
}

func (s *Server) manageMessageDelete(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	msg, err := s.DB.GetMessage(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	allowed := u.IsAdmin || (msg.PoleID != 0 && s.canManage(u, msg.PoleID))
	if !allowed {
		s.forbidden(w, r)
		return
	}
	if err := s.DB.DeleteMessage(msg.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	back := r.FormValue("back")
	if !safePath(back) {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// --- slots --------------------------------------------------------------

func (s *Server) manageSlotNew(w http.ResponseWriter, r *http.Request) {
	pole, ed, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	locations, _ := s.DB.ListLocations(pole.ID)
	start, _ := time.ParseInLocation("2006-01-02", ed.StartsOn, s.Cfg.Timezone)
	start = start.Add(9 * time.Hour)
	s.render(w, r, "slot_form", M{
		"Pole": pole, "Event": ed, "Locations": locations,
		"Slot": db.Slot{StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 1},
		"New":  true, "Tasks": pole.DefaultTasks,
	})
}

func (s *Server) manageSlotCreate(w http.ResponseWriter, r *http.Request) {
	pole, ed, ok := s.manageContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	locations, _ := s.DB.ListLocations(pole.ID)
	slot := db.Slot{PoleID: pole.ID, Title: strings.TrimSpace(r.FormValue("title")), Capacity: formInt(r, "capacity", 1), LocationID: formInt64(r, "location_id")}
	var okStart, okEnd bool
	slot.StartsAt, okStart = s.formTime(r, "starts_at")
	slot.EndsAt, okEnd = s.formTime(r, "ends_at")
	renderErr := func(msg string) {
		s.render(w, r, "slot_form", M{"Pole": pole, "Event": ed, "Locations": locations, "Slot": slot, "New": true, "Error": msg,
			"Tasks": r.FormValue("tasks"), "RepeatUntil": r.FormValue("repeat_until"), "AllLocations": r.FormValue("all_locations") == "1"})
	}
	if !okStart || !okEnd || !slot.EndsAt.After(slot.StartsAt) {
		renderErr("Indiquez un début et une fin, la fin devant être après le début.")
		return
	}
	if slot.Capacity < 1 {
		renderErr("Le nombre de bénévoles doit être au moins 1.")
		return
	}
	// Expand into a series when asked.
	duration := slot.EndsAt.Sub(slot.StartsAt)
	starts := []time.Time{slot.StartsAt}
	if until, ok := s.formTime(r, "repeat_until"); ok {
		for t := slot.StartsAt.Add(duration); t.Add(duration).Before(until.Add(time.Minute)) && len(starts) < 200; t = t.Add(duration) {
			starts = append(starts, t)
		}
	}
	locIDs := []int64{slot.LocationID}
	if r.FormValue("all_locations") == "1" && len(locations) > 0 {
		locIDs = locIDs[:0]
		for _, l := range locations {
			locIDs = append(locIDs, l.ID)
		}
	}
	var taskLabels []string
	for _, line := range strings.Split(r.FormValue("tasks"), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			taskLabels = append(taskLabels, l)
		}
	}
	created := 0
	for _, locID := range locIDs {
		for _, st := range starts {
			sl := slot
			sl.LocationID = locID
			sl.StartsAt = st
			sl.EndsAt = st.Add(duration)
			id, err := s.DB.CreateSlot(sl)
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			for _, label := range taskLabels {
				s.DB.CreateTask(id, label)
			}
			created++
		}
	}
	if created == 1 {
		s.flash(w, "Créneau créé.")
	} else {
		s.flash(w, fmt.Sprintf("%d créneaux créés.", created))
	}
	http.Redirect(w, r, pole.ManageURL(), http.StatusSeeOther)
}

func (s *Server) manageSlotEdit(w http.ResponseWriter, r *http.Request) {
	slot, pole, ed, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	locations, _ := s.DB.ListLocations(pole.ID)
	tasks, _ := s.DB.ListTasks(slot.ID)
	volunteers, _ := s.DB.ListSlotVolunteers(slot.ID)
	msgs, _ := s.DB.ListSlotMessages(slot.ID)
	members, _ := s.DB.ListPoleMembers(pole.ID)
	// Only offer people who are not on the slot already.
	onSlot := map[int64]bool{}
	for _, v := range volunteers {
		onSlot[v.ID] = true
	}
	var available []db.PoleMember
	for _, m := range members {
		if !onSlot[m.User.ID] {
			available = append(available, m)
		}
	}
	waiting, _ := s.DB.ListWaitlist(slot.ID)
	s.render(w, r, "slot_form", M{
		"Pole": pole, "Event": ed, "Locations": locations, "Slot": slot,
		"TaskList": tasks, "Volunteers": volunteers, "Messages": msgs,
		"Members": members, "Available": available, "Waiting": waiting,
		"Error": r.URL.Query().Get("error"),
	})
}

func (s *Server) manageSlotUpdate(w http.ResponseWriter, r *http.Request) {
	slot, pole, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	slot.Title = strings.TrimSpace(r.FormValue("title"))
	slot.Capacity = formInt(r, "capacity", slot.Capacity)
	slot.LocationID = formInt64(r, "location_id")
	var okStart, okEnd bool
	slot.StartsAt, okStart = s.formTime(r, "starts_at")
	slot.EndsAt, okEnd = s.formTime(r, "ends_at")
	if !okStart || !okEnd || !slot.EndsAt.After(slot.StartsAt) || slot.Capacity < 1 {
		http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d?error=Vérifiez+les+horaires+et+le+nombre+de+bénévoles.", slot.ID), http.StatusSeeOther)
		return
	}
	if err := s.DB.UpdateSlot(slot); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Créneau enregistré.")
	http.Redirect(w, r, pole.ManageURL(), http.StatusSeeOther)
}

func (s *Server) manageSlotDelete(w http.ResponseWriter, r *http.Request) {
	slot, pole, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	// Read the volunteers before the cascade removes their signups.
	volunteers, _ := s.DB.ListSlotVolunteers(slot.ID)
	waiting, _ := s.DB.ListWaitlist(slot.ID)
	if err := s.DB.DeleteSlot(slot.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.notifySlotDeleted(volunteers, pole, slot)
	s.notifySlotDeleted(waiting, pole, slot)
	msg := "Créneau supprimé."
	if n := len(volunteers); n == 1 {
		msg = "Créneau supprimé. Le bénévole inscrit a été prévenu par e-mail."
	} else if n > 1 {
		msg = fmt.Sprintf("Créneau supprimé. Les %d bénévoles inscrits ont été prévenus par e-mail.", n)
	}
	s.flash(w, msg)
	http.Redirect(w, r, pole.ManageURL(), http.StatusSeeOther)
}

// --- tasks --------------------------------------------------------------

func (s *Server) manageTaskCreate(w http.ResponseWriter, r *http.Request) {
	slot, _, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	for _, line := range strings.Split(r.FormValue("labels"), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			if _, err := s.DB.CreateTask(slot.ID, l); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d#tasks", slot.ID), http.StatusSeeOther)
}

func (s *Server) manageTaskDelete(w http.ResponseWriter, r *http.Request) {
	task, err := s.DB.GetTask(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, _, _, ok := s.manageSlotContext(w, r, task.SlotID); !ok {
		return
	}
	if err := s.DB.DeleteTask(task.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d#tasks", task.SlotID), http.StatusSeeOther)
}

// --- volunteers on a slot -----------------------------------------------

// manageSlotAddVolunteer puts someone on a slot. Two distinct intents, two
// distinct form fields: picking an existing volunteer from the pole (the
// common case) or deliberately inviting someone who has no account yet.
// The previous single free-text field was labelled "name or e-mail" but
// required an e-mail, and any typo silently created a permanent account.
func (s *Server) manageSlotAddVolunteer(w http.ResponseWriter, r *http.Request) {
	slot, pole, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	slotErr := func(msg string) {
		http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d?error=%s#volunteers", slot.ID, url.QueryEscape(msg)), http.StatusSeeOther)
	}
	var u db.User
	if uid := formInt64(r, "user_id"); uid != 0 {
		var err error
		if u, err = s.DB.GetUser(uid); err != nil {
			s.fail(w, r, err)
			return
		}
	} else {
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		addr, err := mail.ParseAddress(email)
		if err != nil || addr.Address != email {
			slotErr("Indiquez une adresse e-mail valide pour inviter quelqu'un qui n'a pas encore de compte.")
			return
		}
		if u, err = s.DB.EnsureVolunteer(email, strings.TrimSpace(r.FormValue("name")), ""); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if err := s.DB.JoinPole(pole.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.DB.ForceSignup(slot.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, u.DisplayName()+" a été ajouté·e au créneau et au pôle « "+pole.Name+" ».")
	http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d#volunteers", slot.ID), http.StatusSeeOther)
}

func (s *Server) manageSlotRemoveVolunteer(w http.ResponseWriter, r *http.Request) {
	slot, pole, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	u, _ := s.DB.GetUser(pathID(r, "uid"))
	if err := s.DB.Unsignup(slot.ID, pathID(r, "uid")); err != nil {
		s.serverError(w, r, err)
		return
	}
	if u.ID != 0 {
		s.notifySignupRemoved(u, pole, slot)
		s.flash(w, u.DisplayName()+" a été retiré·e de ce créneau et prévenu·e par e-mail. La personne reste dans le pôle.")
	}
	s.announceFreePlace(pole, slot.ID)
	http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d#volunteers", slot.ID), http.StatusSeeOther)
}
