package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"benevoles/internal/db"
)

// manageContext loads a pole and checks the user may manage it.
func (s *Server) manageContext(w http.ResponseWriter, r *http.Request, poleID int64) (db.Pole, db.Edition, bool) {
	u := currentUser(r)
	pole, err := s.DB.GetPole(poleID)
	if err != nil {
		s.fail(w, r, err)
		return pole, db.Edition{}, false
	}
	if !s.canManage(u, pole.ID) {
		s.forbidden(w, r)
		return pole, db.Edition{}, false
	}
	ed, err := s.DB.GetEdition(pole.EditionID)
	if err != nil {
		s.fail(w, r, err)
		return pole, ed, false
	}
	return pole, ed, true
}

// manageSlotContext loads a slot and checks the user may manage its pole.
func (s *Server) manageSlotContext(w http.ResponseWriter, r *http.Request, slotID int64) (db.Slot, db.Pole, db.Edition, bool) {
	slot, err := s.DB.GetSlot(slotID, currentUser(r).ID)
	if err != nil {
		s.fail(w, r, err)
		return slot, db.Pole{}, db.Edition{}, false
	}
	pole, ed, ok := s.manageContext(w, r, slot.PoleID)
	return slot, pole, ed, ok
}

func (s *Server) managePole(w http.ResponseWriter, r *http.Request) {
	pole, ed, ok := s.manageContext(w, r, pathID(r, "id"))
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
	s.render(w, r, "manage_pole", M{
		"CanAddMembers": u.IsAdmin || pole.ManagersCanAdd(), "AllUsers": allUsers,
		"Pole": pole, "Edition": ed, "Days": groupSlots(slots, s.Cfg.Timezone), "Locations": locations,
		"Messages": msgs, "Members": members, "Managers": managers, "Volunteers": volunteers,
		"Capacity": capacity, "Filled": filled, "Short": short, "SlotCount": len(slots),
		"Tab": r.URL.Query().Get("tab"),
	})
}

func (s *Server) managePoleUpdate(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name != "" {
		pole.Name = name
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
	s.flash(w, "Pôle enregistré.")
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=settings", pole.ID), http.StatusSeeOther)
}

func (s *Server) managePoleKey(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	if _, err := s.DB.RegenerateAccessKey(pole.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Nouveau lien d'invitation généré. L'ancien lien ne fonctionne plus.")
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=members", pole.ID), http.StatusSeeOther)
}

func (s *Server) managePoleImage(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, pathID(r, "id"))
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
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=settings", pole.ID), http.StatusSeeOther)
}

func (s *Server) managePoleImageDelete(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	s.Uploads.Remove(pole.Image)
	if err := s.DB.SetPoleImage(pole.ID, ""); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=settings", pole.ID), http.StatusSeeOther)
}

// --- members ------------------------------------------------------------

// manageUserSearch answers the live search of the "add members" box.
func (s *Server) manageUserSearch(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, pathID(r, "id"))
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
	pole, _, ok := s.manageContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	if !pole.ManagersCanAdd() && !currentUser(r).IsAdmin {
		s.forbidden(w, r)
		return
	}
	added := 0
	for _, raw := range strings.FieldsFunc(r.FormValue("emails"), func(c rune) bool { return c == '\n' || c == ',' || c == ';' || c == ' ' }) {
		email := strings.ToLower(strings.TrimSpace(raw))
		if !strings.Contains(email, "@") {
			continue
		}
		u, err := s.DB.GetOrCreateUser(email)
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
		s.flash(w, fmt.Sprintf("%d bénévoles ajoutés au pôle.", added))
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=members", pole.ID), http.StatusSeeOther)
}

func (s *Server) manageMemberRemove(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	if pole.IsDefault {
		s.flash(w, "Impossible de retirer quelqu'un du pôle par défaut.")
	} else if err := s.DB.LeavePole(pole.ID, pathID(r, "uid")); err != nil {
		s.serverError(w, r, err)
		return
	} else {
		s.flash(w, "Bénévole retiré·e du pôle et de ses créneaux.")
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=members", pole.ID), http.StatusSeeOther)
}

// --- locations ----------------------------------------------------------

func (s *Server) manageLocationCreate(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, pathID(r, "id"))
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
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=settings", pole.ID), http.StatusSeeOther)
}

func (s *Server) manageLocationDelete(w http.ResponseWriter, r *http.Request) {
	loc, err := s.DB.GetLocation(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, _, ok := s.manageContext(w, r, loc.PoleID); !ok {
		return
	}
	if err := s.DB.DeleteLocation(loc.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=settings", loc.PoleID), http.StatusSeeOther)
}

// --- messages -----------------------------------------------------------

func (s *Server) managePoleMessage(w http.ResponseWriter, r *http.Request) {
	pole, _, ok := s.manageContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if body != "" {
		if _, err := s.DB.CreateMessage(pole.EditionID, pole.ID, 0, currentUser(r).ID, body); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=messages", pole.ID), http.StatusSeeOther)
}

func (s *Server) manageSlotMessage(w http.ResponseWriter, r *http.Request) {
	slot, pole, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if body != "" {
		if _, err := s.DB.CreateMessage(pole.EditionID, pole.ID, slot.ID, currentUser(r).ID, body); err != nil {
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
	if back == "" || !strings.HasPrefix(back, "/") {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// --- slots --------------------------------------------------------------

func (s *Server) manageSlotNew(w http.ResponseWriter, r *http.Request) {
	pole, ed, ok := s.manageContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	locations, _ := s.DB.ListLocations(pole.ID)
	start, _ := time.ParseInLocation("2006-01-02", ed.StartsOn, s.Cfg.Timezone)
	start = start.Add(9 * time.Hour)
	s.render(w, r, "slot_form", M{
		"Pole": pole, "Edition": ed, "Locations": locations,
		"Slot": db.Slot{StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 1},
		"New":  true, "Tasks": pole.DefaultTasks,
	})
}

func (s *Server) manageSlotCreate(w http.ResponseWriter, r *http.Request) {
	pole, ed, ok := s.manageContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	locations, _ := s.DB.ListLocations(pole.ID)
	slot := db.Slot{PoleID: pole.ID, Title: strings.TrimSpace(r.FormValue("title")), Capacity: formInt(r, "capacity", 1), LocationID: formInt64(r, "location_id")}
	var okStart, okEnd bool
	slot.StartsAt, okStart = s.formTime(r, "starts_at")
	slot.EndsAt, okEnd = s.formTime(r, "ends_at")
	renderErr := func(msg string) {
		s.render(w, r, "slot_form", M{"Pole": pole, "Edition": ed, "Locations": locations, "Slot": slot, "New": true, "Error": msg,
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
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d", pole.ID), http.StatusSeeOther)
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
	s.render(w, r, "slot_form", M{
		"Pole": pole, "Edition": ed, "Locations": locations, "Slot": slot,
		"TaskList": tasks, "Volunteers": volunteers, "Messages": msgs, "Members": members,
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
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d", pole.ID), http.StatusSeeOther)
}

func (s *Server) manageSlotDelete(w http.ResponseWriter, r *http.Request) {
	slot, pole, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	if err := s.DB.DeleteSlot(slot.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Créneau supprimé.")
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d", pole.ID), http.StatusSeeOther)
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

func (s *Server) manageSlotAddVolunteer(w http.ResponseWriter, r *http.Request) {
	slot, pole, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	if !strings.Contains(email, "@") {
		http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d?error=Adresse+e-mail+invalide.", slot.ID), http.StatusSeeOther)
		return
	}
	u, err := s.DB.GetOrCreateUser(email)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.DB.JoinPole(pole.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.DB.ForceSignup(slot.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, u.DisplayName()+" a été ajouté(e) au créneau.")
	http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d#volunteers", slot.ID), http.StatusSeeOther)
}

func (s *Server) manageSlotRemoveVolunteer(w http.ResponseWriter, r *http.Request) {
	slot, _, _, ok := s.manageSlotContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	if err := s.DB.Unsignup(slot.ID, pathID(r, "uid")); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/slots/%d#volunteers", slot.ID), http.StatusSeeOther)
}
