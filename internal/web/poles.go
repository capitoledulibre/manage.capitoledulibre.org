package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"benevoles/internal/db"
	"benevoles/internal/reminders"
)

// poleContext loads a pole with its event and checks the user may see it.
func (s *Server) poleContext(w http.ResponseWriter, r *http.Request, poleID int64) (db.Pole, db.Event, bool) {
	u := currentUser(r)
	pole, err := s.DB.GetPole(poleID)
	if err != nil {
		s.fail(w, r, err)
		return pole, db.Event{}, false
	}
	ed, err := s.DB.GetEvent(pole.EventID)
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
	pole, ed, ok := s.poleContext(w, r, s.polePathID(r))
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
	// One query for the whole page rather than one per card.
	onWaitlist, _ := s.DB.UserWaitlistInPole(pole.ID, u.ID)
	if onWaitlist == nil {
		onWaitlist = map[int64]bool{}
	}
	s.render(w, r, "pole", M{
		"OnWaitlist": onWaitlist,
		"Pole":       pole, "Event": ed, "Member": member, "CanManage": manage,
		"Locked": ed.SignupsLocked(pole.FreezeAt, time.Now()), "Deadline": ed.SignupDeadline(pole.FreezeAt),
		"Days": groupSlots(slots, s.Cfg.Timezone), "Locations": locations, "Filter": filter, "OnlyOpen": onlyOpen,
		"Messages": msgs, "Managers": managers, "Mine": mine, "Missing": missing, "Total": len(slots),
		"Error": r.URL.Query().Get("error"),
	})
}

// poleInvite shows the invitation page reached through a private pole's link.
func (s *Server) poleInvite(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	pole, ed, ok := s.poleContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	if s.DB.IsPoleMember(pole.ID, u.ID) {
		http.Redirect(w, r, pole.URL(), http.StatusSeeOther)
		return
	}
	key := r.URL.Query().Get("key")
	valid := pole.IsOpen() || (pole.IsPrivate() && key != "" && key == pole.AccessKey)
	managers, _ := s.DB.ListPoleManagers(pole.ID)
	s.render(w, r, "join", M{"Pole": pole, "Event": ed, "Key": key, "Valid": valid, "Managers": managers,
		"Closed": ed.Status != db.StatusOpen})
}

func (s *Server) poleJoin(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	pole, ed, ok := s.poleContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	if ed.Status != db.StatusOpen && !s.canManage(u, pole.ID) {
		s.flash(w, "Les inscriptions à cet événement sont fermées.")
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
	http.Redirect(w, r, pole.URL(), http.StatusSeeOther)
}

func (s *Server) poleLeave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	pole, ed, ok := s.poleContext(w, r, s.polePathID(r))
	if !ok {
		return
	}
	if pole.IsDefault {
		s.flash(w, "Ce pôle regroupe tous les bénévoles et ne peut pas être quitté.")
		http.Redirect(w, r, pole.URL(), http.StatusSeeOther)
		return
	}
	if ed.SignupsLocked(pole.FreezeAt, time.Now()) {
		s.flash(w, "Les inscriptions sont closes : contactez un responsable pour vous désengager.")
		http.Redirect(w, r, pole.URL(), http.StatusSeeOther)
		return
	}
	// Leaving cancels every slot the volunteer had in the pole: each is a
	// withdrawal, for the waiting list and, close to the day, the managers.
	mine, _ := s.DB.UserSlotIDsInPole(pole.ID, u.ID)
	if err := s.DB.LeavePole(pole.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	var late []db.Slot
	for _, id := range mine {
		s.announceFreePlace(pole, id)
		if slot, err := s.DB.GetSlot(id, 0); err == nil && isLate(slot) {
			late = append(late, slot)
		}
	}
	s.notifyLateWithdrawal(*u, pole, late)
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
	// Whom to call when something comes up: for the people of the pole only.
	var contacts []db.User
	if member || manage {
		contacts = s.poleContacts(pole.ID)
	}
	s.render(w, r, "slot", M{
		"Waiting": s.DB.OnWaitlist(slot.ID, u.ID),
		"Slot":    slot, "Pole": pole, "Event": ed, "Member": member, "CanManage": manage,
		"Locked":     ed.SignupsLocked(pole.FreezeAt, time.Now()),
		"Volunteers": volunteers, "Tasks": tasks, "Messages": msgs, "Contacts": contacts,
		"CanTick": slot.Mine || manage,
		"Error":   r.URL.Query().Get("error"),
	})
}

// poleContacts returns the managers a volunteer of the pole can reach: those
// who gave a name. Their phone number is only shown when they chose to share
// it (SharePhone); the profile otherwise promises it stays with managers.
func (s *Server) poleContacts(poleID int64) []db.User {
	managers, _ := s.DB.ListPoleManagers(poleID)
	var out []db.User
	for _, m := range managers {
		if strings.TrimSpace(m.Name) != "" {
			out = append(out, m)
		}
	}
	return out
}

// backTo returns the page to come back to after a slot action, defaulting to
// the slot's own page. Only same-site paths are honoured.
func backTo(r *http.Request, slotID int64) string {
	if back := r.FormValue("back"); safePath(back) {
		return back
	}
	return fmt.Sprintf("/slots/%d", slotID)
}

// cardData builds the data of the slot_card partial. Back has to be carried
// through: the card re-rendered by an htmx swap keeps the forms that need it.
// It also tells which page the card sits on: the open-slots list mixes every
// pole, so its cards name theirs.
func (s *Server) cardData(r *http.Request, slot db.Slot, pole db.Pole, ed db.Event, member bool) M {
	back := backTo(r, slot.ID)
	data := M{
		"Slot": slot, "Pole": pole, "Member": member, "Back": back,
		"Locked":   ed.SignupsLocked(pole.FreezeAt, time.Now()),
		"ShowPole": back == openSlotsPath || strings.HasPrefix(back, openSlotsPath+"?"),
	}
	// Only a full slot offers the waiting list, so only then is the lookup worth it.
	if u := currentUser(r); u != nil && member && slot.IsFull() && !slot.Mine {
		data["Waiting"] = s.DB.OnWaitlist(slot.ID, u.ID)
	}
	return data
}

// respondSlot answers a signup action: fragment for htmx, redirect otherwise.
// announce is the sentence pushed to the live region (htmx) or to the flash
// (plain form post) on success, so both paths confirm what happened.
func (s *Server) respondSlot(w http.ResponseWriter, r *http.Request, data M, errMsg, announce string) {
	if isHX(r) {
		if errMsg != "" {
			data["Error"] = errMsg
			data["Announce"] = errMsg
		} else if announce != "" {
			data["Announce"] = announce
		}
		s.fragment(w, "slot_card", data)
		return
	}
	if errMsg != "" {
		s.flash(w, errMsg)
	} else if announce != "" {
		s.flash(w, announce)
	}
	back, _ := data["Back"].(string)
	if !safePath(back) {
		back = "/slots/" + r.PathValue("id")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// slotLabel names a slot in a sentence: "samedi de 14:00 à 18:00".
func (s *Server) slotLabel(slot db.Slot) string {
	loc := s.Cfg.Timezone
	return fmt.Sprintf("%s de %s à %s", reminders.FormatDay(slot.StartsAt, loc),
		slot.StartsAt.In(loc).Format("15:04"), slot.EndsAt.In(loc).Format("15:04"))
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
	data := s.cardData(r, slot, pole, ed, member)
	// Anyone may join a public pole, so asking for a separate "join" click
	// before the signup only added a step. Other poles still need a key or a
	// manager.
	autoJoin := !member && pole.IsOpen()
	if !member && !autoJoin {
		s.respondSlot(w, r, data, "Ce pôle est sur invitation : demandez au responsable de vous ajouter.", "")
		return
	}
	if ed.SignupsLocked(pole.FreezeAt, time.Now()) {
		s.respondSlot(w, r, data, "Les inscriptions sont closes sur ce créneau.", "")
		return
	}
	if r.FormValue("force") != "1" {
		overlaps, _ := s.DB.OverlappingSlots(u.ID, pole.ID, slot.ID, slot.StartsAt, slot.EndsAt)
		if len(overlaps) > 0 {
			data["Overlaps"] = overlaps
			if isHX(r) {
				data["Announce"] = "Attention : ce créneau chevauche un créneau où vous êtes déjà inscrit·e. Confirmez pour vous inscrire quand même."
				s.fragment(w, "slot_card", data)
				return
			}
			// Non-htmx fallback: show the confirmation on the slot page.
			s.render(w, r, "slot_confirm", M{"Slot": slot, "Pole": pole, "Overlaps": overlaps, "Back": backTo(r, slot.ID)})
			return
		}
	}
	// Joining comes after the overlap check, so that cancelling at that
	// point leaves the volunteer where they were.
	if autoJoin {
		if err := s.DB.JoinPole(pole.ID, u.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		member = true
	}
	okSignup, err := s.DB.Signup(slot.ID, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	slot, _ = s.DB.GetSlot(slot.ID, u.ID)
	data = s.cardData(r, slot, pole, ed, member)
	if !okSignup {
		s.respondSlot(w, r, data, "Ce créneau est déjà complet : personne d'autre n'est attendu.", "")
		return
	}
	done := "Inscription enregistrée : " + pole.Name + ", " + s.slotLabel(slot) + "."
	if autoJoin {
		done = "Vous avez rejoint le pôle « " + pole.Name + " ». " + done
	}
	s.respondSlot(w, r, data, "", done)
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
		s.respondSlot(w, r, s.cardData(r, slot, pole, ed, member), "Les inscriptions sont closes : contactez un responsable pour vous désengager.", "")
		return
	}
	if err := s.DB.Unsignup(slot.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	slot, _ = s.DB.GetSlot(slot.ID, u.ID)
	s.announceFreePlace(pole, slot.ID)
	if isLate(slot) {
		s.notifyLateWithdrawal(*u, pole, []db.Slot{slot})
	}
	s.respondSlot(w, r, s.cardData(r, slot, pole, ed, member), "",
		"Désinscription enregistrée : "+s.slotLabel(slot)+". Vous pouvez vous réinscrire sur ce créneau.")
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
		done, total := 0, len(tasks)
		for _, t := range tasks {
			if t.Done() {
				done++
			}
		}
		s.fragment(w, "task_list", M{"Tasks": tasks, "CanTick": true, "Slot": slot,
			"Announce": fmt.Sprintf("Tâches : %d sur %d faites.", done, total)})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/slots/%d", slot.ID), http.StatusSeeOther)
}

// --- waiting list --------------------------------------------------------

// announceFreePlace tells the people waiting for a slot that a place opened.
// It is a no-op when the slot is full again (someone was added at the same
// time) or when nobody is waiting, so callers can invoke it unconditionally
// after any action that might free a seat.
func (s *Server) announceFreePlace(pole db.Pole, slotID int64) {
	slot, err := s.DB.GetSlot(slotID, 0)
	if err != nil || slot.IsFull() {
		return
	}
	waiting, err := s.DB.ListWaitlist(slotID)
	if err != nil || len(waiting) == 0 {
		return
	}
	s.notifyPlaceFreed(waiting, pole, slot)
}

// slotWaitlistJoin registers the volunteer's interest in a full slot.
func (s *Server) slotWaitlistJoin(w http.ResponseWriter, r *http.Request) {
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
	data := s.cardData(r, slot, pole, ed, member)
	if !member {
		s.respondSlot(w, r, data, "Rejoignez d'abord le pôle.", "")
		return
	}
	if ed.SignupsLocked(pole.FreezeAt, time.Now()) {
		s.respondSlot(w, r, data, "Les inscriptions sont closes.", "")
		return
	}
	if err := s.DB.JoinWaitlist(slot.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	data["Waiting"] = true
	s.respondSlot(w, r, data, "",
		"Vous serez prévenu·e par e-mail si une place se libère sur ce créneau.")
}

// slotWaitlistLeave cancels that interest.
func (s *Server) slotWaitlistLeave(w http.ResponseWriter, r *http.Request) {
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
	if err := s.DB.LeaveWaitlist(slot.ID, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	member := s.DB.IsPoleMember(pole.ID, u.ID)
	data := s.cardData(r, slot, pole, ed, member)
	s.respondSlot(w, r, data, "", "Vous ne serez plus prévenu·e pour ce créneau.")
}

// openSlotsPath lists what still needs volunteers, every pole together.
const openSlotsPath = "/slots"

// openSlots answers "where am I needed?": many volunteers want to help on a
// given day more than in a given pole, and the pole-first path made them
// open each pole in turn to find a hole. Only slots the volunteer can
// actually take are listed: poles they belong to, and public poles, which a
// signup joins on the way.
func (s *Server) openSlots(w http.ResponseWriter, r *http.Request) {
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
	poles, err := s.DB.ListPoles(ev.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	members, _ := s.DB.MemberPoleIDs(ev.ID, u.ID)
	now := time.Now()
	takeable := map[int64]db.Pole{}
	for _, p := range poles {
		if (members[p.ID] || p.IsOpen()) && !ev.SignupsLocked(p.FreezeAt, now) {
			takeable[p.ID] = p
		}
	}
	slots, err := s.DB.ListOpenSlots(ev.ID, u.ID, now)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	type card struct {
		Slot   db.Slot
		Pole   db.Pole
		Member bool
	}
	type day struct {
		Day   string
		Cards []card
	}
	var days []day
	var dayKeys []string
	filter := r.URL.Query().Get("day")
	for _, sl := range slots {
		p, ok := takeable[sl.PoleID]
		if !ok {
			continue
		}
		key := sl.Day(s.Cfg.Timezone)
		if len(dayKeys) == 0 || dayKeys[len(dayKeys)-1] != key {
			dayKeys = append(dayKeys, key)
		}
		if filter != "" && key != filter {
			continue
		}
		if len(days) == 0 || days[len(days)-1].Day != key {
			days = append(days, day{Day: key})
		}
		days[len(days)-1].Cards = append(days[len(days)-1].Cards, card{Slot: sl.Slot, Pole: p, Member: members[p.ID]})
	}
	back := openSlotsPath
	if filter != "" {
		back += "?day=" + url.QueryEscape(filter)
	}
	s.render(w, r, "open_slots", M{
		"Event": ev, "Days": days, "DayKeys": dayKeys, "Filter": filter, "Back": back,
		"Locked": ev.Status != db.StatusOpen,
	})
}
