package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"benevoles/internal/db"
)

// poleContext loads a pole with its edition and checks the user may see it.
func (s *Server) poleContext(w http.ResponseWriter, r *http.Request, poleID int64) (db.Pole, db.Edition, bool) {
	u := currentUser(r)
	pole, err := s.DB.GetPole(poleID)
	if err != nil {
		s.fail(w, r, err)
		return pole, db.Edition{}, false
	}
	ed, err := s.DB.GetEdition(pole.EditionID)
	if err != nil {
		s.fail(w, r, err)
		return pole, ed, false
	}
	if !ed.IsVisibleToVolunteers() && !s.canManage(u, pole.ID) {
		s.notFound(w, r)
		return pole, ed, false
	}
	return pole, ed, true
}

type slotDay struct {
	Day   string
	Slots []db.Slot
}

func groupSlots(slots []db.Slot, loc *time.Location) []slotDay {
	var out []slotDay
	for _, sl := range slots {
		key := sl.Day(loc)
		if len(out) == 0 || out[len(out)-1].Day != key {
			out = append(out, slotDay{Day: key})
		}
		out[len(out)-1].Slots = append(out[len(out)-1].Slots, sl)
	}
	return out
}

func (s *Server) polePage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	pole, ed, ok := s.poleContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	member := s.DB.IsPoleMember(pole.ID, u.ID)
	manage := s.canManage(u, pole.ID)
	slots, err := s.DB.ListSlots(pole.ID, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	locations, _ := s.DB.ListLocations(pole.ID)
	filter := formInt64(r, "location")
	if filter != 0 {
		var kept []db.Slot
		for _, sl := range slots {
			if sl.LocationID == filter {
				kept = append(kept, sl)
			}
		}
		slots = kept
	}
	onlyOpen := r.URL.Query().Get("open") == "1"
	if onlyOpen {
		var kept []db.Slot
		for _, sl := range slots {
			if !sl.IsFull() || sl.Mine {
				kept = append(kept, sl)
			}
		}
		slots = kept
	}
	msgs, _ := s.DB.ListPoleMessages(pole.ID)
	managers, _ := s.DB.ListPoleManagers(pole.ID)
	var mine, missing int
	for _, sl := range slots {
		if sl.Mine {
			mine++
		}
		missing += sl.Missing()
	}
	s.render(w, r, "pole", M{
		"Pole": pole, "Edition": ed, "Member": member, "CanManage": manage,
		"Locked": ed.SignupsLocked(pole.FreezeAt, time.Now()),
		"Days":   groupSlots(slots, s.Cfg.Timezone), "Locations": locations, "Filter": filter, "OnlyOpen": onlyOpen,
		"Messages": msgs, "Managers": managers, "Mine": mine, "Missing": missing, "Total": len(slots),
		"Error": r.URL.Query().Get("error"),
	})
}

// poleInvite shows the invitation page reached through a private pole's link.
func (s *Server) poleInvite(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	pole, ed, ok := s.poleContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	if s.DB.IsPoleMember(pole.ID, u.ID) {
		http.Redirect(w, r, fmt.Sprintf("/poles/%d", pole.ID), http.StatusSeeOther)
		return
	}
	key := r.URL.Query().Get("key")
	valid := pole.IsOpen() || (pole.IsPrivate() && key != "" && key == pole.AccessKey)
	managers, _ := s.DB.ListPoleManagers(pole.ID)
	s.render(w, r, "join", M{"Pole": pole, "Edition": ed, "Key": key, "Valid": valid, "Managers": managers,
		"Closed": ed.Status != db.StatusOpen})
}

func (s *Server) poleJoin(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	pole, ed, ok := s.poleContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	if ed.Status != db.StatusOpen && !s.canManage(u, pole.ID) {
		s.flash(w, "Les inscriptions à cette édition sont fermées.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	switch {
	case pole.IsOpen():
	case pole.IsPrivate():
		if strings.TrimSpace(r.FormValue("key")) != pole.AccessKey {
			s.flash(w, "Ce lien d'invitation n'est pas valable pour le pôle « "+pole.Name+" ». Demandez-en un nouveau au responsable.")
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	default:
		s.flash(w, "Le pôle « "+pole.Name+" » est sur invitation : un responsable doit vous ajouter.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := s.DB.JoinPole(pole.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Vous avez rejoint le pôle « "+pole.Name+" ». Choisissez vos créneaux !")
	http.Redirect(w, r, fmt.Sprintf("/poles/%d", pole.ID), http.StatusSeeOther)
}

func (s *Server) poleLeave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	pole, ed, ok := s.poleContext(w, r, pathID(r, "id"))
	if !ok {
		return
	}
	if pole.IsDefault {
		s.flash(w, "Ce pôle regroupe tous les bénévoles et ne peut pas être quitté.")
		http.Redirect(w, r, "/poles/"+r.PathValue("id"), http.StatusSeeOther)
		return
	}
	if ed.SignupsLocked(pole.FreezeAt, time.Now()) {
		s.flash(w, "Le planning est gelé : contactez un responsable pour vous désengager.")
		http.Redirect(w, r, "/poles/"+r.PathValue("id"), http.StatusSeeOther)
		return
	}
	if err := s.DB.LeavePole(pole.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Vous avez quitté le pôle « "+pole.Name+" ».")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- slots --------------------------------------------------------------

func (s *Server) slotPage(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	slot, err := s.DB.GetSlot(pathID(r, "id"), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pole, ed, ok := s.poleContext(w, r, slot.PoleID)
	if !ok {
		return
	}
	member := s.DB.IsPoleMember(pole.ID, u.ID)
	manage := s.canManage(u, pole.ID)
	volunteers, _ := s.DB.ListSlotVolunteers(slot.ID)
	tasks, _ := s.DB.ListTasks(slot.ID)
	msgs, _ := s.DB.ListSlotMessages(slot.ID)
	s.render(w, r, "slot", M{
		"Slot": slot, "Pole": pole, "Edition": ed, "Member": member, "CanManage": manage,
		"Locked":     ed.SignupsLocked(pole.FreezeAt, time.Now()),
		"Volunteers": volunteers, "Tasks": tasks, "Messages": msgs,
		"CanTick": slot.Mine || manage,
		"Error":   r.URL.Query().Get("error"),
	})
}

// cardData builds the data of the slot_card partial.
func (s *Server) cardData(slot db.Slot, pole db.Pole, ed db.Edition, member bool) M {
	return M{
		"Slot": slot, "Pole": pole, "Member": member,
		"Locked": ed.SignupsLocked(pole.FreezeAt, time.Now()),
	}
}

// respondSlot answers a signup action: fragment for htmx, redirect otherwise.
func (s *Server) respondSlot(w http.ResponseWriter, r *http.Request, data M, errMsg string) {
	if isHX(r) {
		if errMsg != "" {
			data["Error"] = errMsg
		}
		s.fragment(w, "slot_card", data)
		return
	}
	back := r.FormValue("back")
	if back == "" || !strings.HasPrefix(back, "/") {
		back = "/slots/" + r.PathValue("id")
	}
	if errMsg != "" {
		s.flash(w, errMsg)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) slotSignup(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	slot, err := s.DB.GetSlot(pathID(r, "id"), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pole, ed, ok := s.poleContext(w, r, slot.PoleID)
	if !ok {
		return
	}
	member := s.DB.IsPoleMember(pole.ID, u.ID)
	data := s.cardData(slot, pole, ed, member)
	if !member {
		s.respondSlot(w, r, data, "Rejoignez d'abord le pôle pour vous inscrire.")
		return
	}
	if ed.SignupsLocked(pole.FreezeAt, time.Now()) {
		s.respondSlot(w, r, data, "Le planning est gelé, les inscriptions sont fermées.")
		return
	}
	if r.FormValue("force") != "1" {
		overlaps, _ := s.DB.OverlappingSlots(u.ID, pole.ID, slot.ID, slot.StartsAt, slot.EndsAt)
		if len(overlaps) > 0 {
			data["Overlaps"] = overlaps
			if isHX(r) {
				s.fragment(w, "slot_card", data)
				return
			}
			// Non-htmx fallback: show the confirmation on the slot page.
			s.render(w, r, "slot_confirm", M{"Slot": slot, "Pole": pole, "Overlaps": overlaps, "Back": r.FormValue("back")})
			return
		}
	}
	okSignup, err := s.DB.Signup(slot.ID, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	slot, _ = s.DB.GetSlot(slot.ID, u.ID)
	data = s.cardData(slot, pole, ed, member)
	if !okSignup {
		s.respondSlot(w, r, data, "Ce créneau est déjà complet.")
		return
	}
	s.respondSlot(w, r, data, "")
}

func (s *Server) slotUnsignup(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	slot, err := s.DB.GetSlot(pathID(r, "id"), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	pole, ed, ok := s.poleContext(w, r, slot.PoleID)
	if !ok {
		return
	}
	member := s.DB.IsPoleMember(pole.ID, u.ID)
	if ed.SignupsLocked(pole.FreezeAt, time.Now()) {
		s.respondSlot(w, r, s.cardData(slot, pole, ed, member), "Le planning est gelé : contactez un responsable pour vous désengager.")
		return
	}
	if err := s.DB.Unsignup(slot.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	slot, _ = s.DB.GetSlot(slot.ID, u.ID)
	s.respondSlot(w, r, s.cardData(slot, pole, ed, member), "")
}

func (s *Server) taskToggle(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	task, err := s.DB.GetTask(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slot, err := s.DB.GetSlot(task.SlotID, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !slot.Mine && !s.canManage(u, slot.PoleID) {
		s.forbidden(w, r)
		return
	}
	if err := s.DB.ToggleTask(task.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	if isHX(r) {
		tasks, _ := s.DB.ListTasks(slot.ID)
		s.fragment(w, "task_list", M{"Tasks": tasks, "CanTick": true, "Slot": slot})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/slots/%d", slot.ID), http.StatusSeeOther)
}
