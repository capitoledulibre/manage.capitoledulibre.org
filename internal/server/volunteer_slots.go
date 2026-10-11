package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"manage/internal/i18n"
	"manage/internal/store"
)

// --- poles ----------------------------------------------------------------

func (s *Server) polePage(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	v, err := s.volunteerPole(r)
	if err != nil {
		return err
	}

	slots, err := s.store.ListSlots(ctx, v.Pole.ID, person.ID)
	if err != nil {
		return err
	}

	locations, err := s.store.ListLocations(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	var filter *store.Location

	if uid := r.URL.Query().Get("location"); uid != "" {
		for _, l := range locations {
			if l.UID == uid {
				filter = l
			}
		}
	}

	onlyOpen := r.URL.Query().Get("open") == "1"
	total := len(slots)

	var kept []*store.Slot

	for _, sl := range slots {
		if (filter == nil || sl.LocationID == filter.ID) && (!onlyOpen || !sl.IsFull() || sl.Mine) {
			kept = append(kept, sl)
		}
	}

	var mine, missing int

	for _, sl := range slots {
		if sl.Mine {
			mine++
		}

		missing += sl.Missing()
	}

	messages, err := s.store.ListPoleMessages(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	managers, err := s.store.ListPoleManagers(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	// One query for the whole page rather than one per card.
	waiting, err := s.store.PersonWaitlistInPole(ctx, v.Pole.ID, person.ID)
	if err != nil {
		return err
	}

	filterUID := ""
	if filter != nil {
		filterUID = filter.UID
	}

	p := s.page(r, "vol.pole.title")
	p.D = map[string]any{
		"V": v, "Days": groupByDay(kept), "Locations": locations, "Filter": filterUID, "OnlyOpen": onlyOpen,
		"Messages": messages, "Managers": named(managers), "Mine": mine, "Missing": missing, "Total": total,
		"Deadline": v.Event.SignupDeadline(v.Pole.FreezeAt), "Waiting": waiting, "Back": r.URL.RequestURI(),
	}

	return s.render(w, r, "volunteer/pole", p)
}

// poleInvite is the page an invitation link of a private pole leads to.
func (s *Server) poleInvite(w http.ResponseWriter, r *http.Request) error {
	v, err := s.volunteerPole(r)
	if err != nil {
		return err
	}

	if v.Member {
		http.Redirect(w, r, v.Pole.URL(), http.StatusSeeOther)

		return nil
	}

	managers, err := s.store.ListPoleManagers(r.Context(), v.Pole.ID)
	if err != nil {
		return err
	}

	key := r.URL.Query().Get("key")

	p := s.page(r, "vol.join.title")
	p.D = map[string]any{"V": v, "Key": key, "Managers": named(managers),
		"Valid":  v.Pole.IsOpen() || (v.Pole.IsPrivate() && key != "" && key == v.Pole.AccessKey),
		"Closed": v.Event.VolunteerStatus != store.VolunteersOpen}

	return s.render(w, r, "volunteer/join", p)
}

func (s *Server) poleJoin(w http.ResponseWriter, r *http.Request) error {
	v, err := s.volunteerPole(r)
	if err != nil {
		return err
	}

	home := "/volunteers/" + v.Event.Slug

	if v.Event.VolunteerStatus != store.VolunteersOpen && !v.Manage {
		s.flash(w, "vol.err.closed")
		http.Redirect(w, r, home, http.StatusSeeOther)

		return nil
	}

	switch {
	case v.Pole.IsOpen():
	case v.Pole.IsPrivate() && strings.TrimSpace(r.FormValue("key")) == v.Pole.AccessKey:
	case v.Pole.IsPrivate():
		s.flash(w, "vol.err.bad_invite", v.Pole.Name)
		http.Redirect(w, r, home, http.StatusSeeOther)

		return nil
	default:
		s.flash(w, "vol.err.invitation_only_pole", v.Pole.Name)
		http.Redirect(w, r, home, http.StatusSeeOther)

		return nil
	}

	if err := s.store.JoinPole(r.Context(), v.Pole.ID, rc(r).Person.ID); err != nil {
		return err
	}

	s.flash(w, "flash.vol.joined", v.Pole.Name)
	http.Redirect(w, r, v.Pole.URL(), http.StatusSeeOther)

	return nil
}

func (s *Server) poleLeave(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	v, err := s.volunteerPole(r)
	if err != nil {
		return err
	}

	if v.Pole.IsDefault {
		s.flash(w, "vol.err.default_pole")
		http.Redirect(w, r, v.Pole.URL(), http.StatusSeeOther)

		return nil
	}

	if v.Locked {
		s.flash(w, "vol.err.locked_unsignup")
		http.Redirect(w, r, v.Pole.URL(), http.StatusSeeOther)

		return nil
	}

	// Leaving cancels every slot the volunteer had in the pole: each is a
	// withdrawal, for the waiting list and, close to the day, the managers.
	mine, err := s.store.PersonSlotsInPole(ctx, v.Pole.ID, person.ID)
	if err != nil {
		return err
	}

	if err := s.store.LeavePole(ctx, v.Pole.ID, person.ID); err != nil {
		return err
	}

	var late []*store.Slot

	for _, sl := range mine {
		s.announceFreePlace(r, v.Pole, sl.ID)

		if after, err := s.store.GetSlot(ctx, sl.ID, 0); err == nil && isLate(after) {
			late = append(late, after)
		}
	}

	s.notifyLateWithdrawal(r, person, v.Pole, late)
	s.flash(w, "flash.vol.left", v.Pole.Name)
	http.Redirect(w, r, "/volunteers/"+v.Event.Slug, http.StatusSeeOther)

	return nil
}

// --- slots ----------------------------------------------------------------

// volunteerSlot loads the slot of the path, with its pole.
func (s *Server) volunteerSlot(r *http.Request) (*poleView, *store.Slot, error) {
	v, err := s.volunteerPole(r)
	if err != nil {
		return nil, nil, err
	}

	sl, err := s.store.GetSlotByUID(r.Context(), v.Event.ID, r.PathValue("slot"), rc(r).Person.ID)
	if err != nil {
		return nil, nil, err
	}

	if sl.PoleID != v.Pole.ID {
		return nil, nil, errNotFound
	}

	return v, sl, nil
}

func (s *Server) slotPage(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	v, sl, err := s.volunteerSlot(r)
	if err != nil {
		return err
	}

	volunteers, err := s.store.ListSlotVolunteers(ctx, sl.ID)
	if err != nil {
		return err
	}

	tasks, err := s.store.ListTasks(ctx, sl.ID)
	if err != nil {
		return err
	}

	messages, err := s.store.ListSlotMessages(ctx, sl.ID)
	if err != nil {
		return err
	}

	waiting, err := s.store.OnWaitlist(ctx, sl.ID, person.ID)
	if err != nil {
		return err
	}

	// Whom to call when something comes up: for the people of the pole only.
	var contacts []*store.Person

	if v.Member || v.Manage {
		managers, err := s.store.ListPoleManagers(ctx, v.Pole.ID)
		if err != nil {
			return err
		}

		contacts = named(managers)
	}

	p := s.page(r, "vol.slot.title")
	p.D = map[string]any{
		"V": v, "Slot": sl, "Volunteers": volunteers, "Tasks": tasks, "Messages": messages, "Contacts": contacts,
		"Card":    s.slotCard(r, v, sl, waiting),
		"CanTick": sl.Mine || v.Manage,
	}

	return s.render(w, r, "volunteer/slot", p)
}

// slotCardData is what the slot_card partial shows. Back is carried through
// the forms so a card re-rendered by htmx keeps it; ShowPole names the pole
// on the pages that mix every pole.
type slotCardData struct {
	Lang     string
	Slot     *store.Slot
	Pole     *store.Pole
	Member   bool
	Locked   bool
	Waiting  bool
	Back     string
	ShowPole bool
	// Compact leaves out the time and place, which the slot's own page
	// already shows as its heading.
	Compact  bool
	Error    string
	Announce string
	Overlaps []*store.Slot
}

func (s *Server) slotCard(r *http.Request, v *poleView, sl *store.Slot, waiting bool) *slotCardData {
	back := safeRedirect(r.FormValue("back"), sl.URL())

	return &slotCardData{
		Lang: rc(r).Lang, Slot: sl, Pole: v.Pole, Member: v.Member, Locked: v.Locked, Waiting: waiting, Back: back,
		ShowPole: strings.HasPrefix(back, "/volunteers/"+v.Event.Slug+"/slots"),
		Compact:  back == sl.URL(),
	}
}

// respondSlot answers a slot action: the card for htmx, a redirect back to
// the page otherwise. The outcome is announced either way: in the live
// region of the card, or as a flash message.
func (s *Server) respondSlot(w http.ResponseWriter, r *http.Request, card *slotCardData, errKey, doneKey string, args ...string) error {
	lang := rc(r).Lang

	if isHX(r) {
		switch {
		case errKey != "":
			card.Error = i18n.T(lang, errKey, anys(args)...)
			card.Announce = card.Error
		case doneKey != "":
			card.Announce = i18n.T(lang, doneKey, anys(args)...)
		}

		return s.fragment(w, "slot_card", card)
	}

	switch {
	case errKey != "":
		s.flash(w, errKey, args...)
	case doneKey != "":
		s.flash(w, doneKey, args...)
	}

	http.Redirect(w, r, card.Back, http.StatusSeeOther)

	return nil
}

func (s *Server) slotSignup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person
	lang := rc(r).Lang

	v, sl, err := s.volunteerSlot(r)
	if err != nil {
		return err
	}

	card := s.slotCard(r, v, sl, false)

	// Anyone may join a public pole, so a sign-up joins it on the way.
	// Other poles take a key or a manager.
	autoJoin := !v.Member && v.Pole.IsOpen()

	switch {
	case !v.Member && !autoJoin:
		return s.respondSlot(w, r, card, "vol.err.invitation_only", "")
	case v.Locked:
		return s.respondSlot(w, r, card, "vol.err.locked", "")
	}

	if r.FormValue("force") != "1" {
		overlaps, err := s.store.OverlappingSlots(ctx, person.ID, v.Pole.ID, sl.ID, sl.StartsAt, sl.EndsAt)
		if err != nil {
			return err
		}

		if len(overlaps) > 0 {
			card.Overlaps = overlaps

			if isHX(r) {
				card.Announce = i18n.T(lang, "vol.overlap.announce")

				return s.fragment(w, "slot_card", card)
			}

			p := s.page(r, "vol.overlap.title")
			p.D = card

			return s.render(w, r, "volunteer/slot_confirm", p)
		}
	}

	// Joining comes after the overlap check, so that cancelling at that
	// point leaves the volunteer where they were.
	if autoJoin {
		if err := s.store.JoinPole(ctx, v.Pole.ID, person.ID); err != nil {
			return err
		}

		v.Member = true
	}

	ok, err := s.store.Signup(ctx, sl.ID, person.ID)
	if err != nil {
		return err
	}

	if sl, err = s.store.GetSlot(ctx, sl.ID, person.ID); err != nil {
		return err
	}

	card = s.slotCard(r, v, sl, false)

	if !ok {
		return s.respondSlot(w, r, card, "vol.err.full", "")
	}

	when := slotWhen(lang, sl.StartsAt, sl.EndsAt)
	if autoJoin {
		return s.respondSlot(w, r, card, "", "vol.done.joined_signup", v.Pole.Name, when)
	}

	return s.respondSlot(w, r, card, "", "vol.done.signup", v.Pole.Name, when)
}

func (s *Server) slotUnsignup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	v, sl, err := s.volunteerSlot(r)
	if err != nil {
		return err
	}

	if v.Locked {
		return s.respondSlot(w, r, s.slotCard(r, v, sl, false), "vol.err.locked_unsignup", "")
	}

	if err := s.store.Unsignup(ctx, sl.ID, person.ID); err != nil {
		return err
	}

	if sl, err = s.store.GetSlot(ctx, sl.ID, person.ID); err != nil {
		return err
	}

	s.announceFreePlace(r, v.Pole, sl.ID)

	if isLate(sl) {
		s.notifyLateWithdrawal(r, person, v.Pole, []*store.Slot{sl})
	}

	return s.respondSlot(w, r, s.slotCard(r, v, sl, false), "", "vol.done.unsignup", slotWhen(rc(r).Lang, sl.StartsAt, sl.EndsAt))
}

// slotWaitlistJoin records that the volunteer wants a full slot.
func (s *Server) slotWaitlistJoin(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.volunteerSlot(r)
	if err != nil {
		return err
	}

	card := s.slotCard(r, v, sl, false)

	switch {
	case !v.Member:
		return s.respondSlot(w, r, card, "vol.err.join_first", "")
	case v.Locked:
		return s.respondSlot(w, r, card, "vol.err.locked", "")
	}

	if err := s.store.JoinWaitlist(r.Context(), sl.ID, rc(r).Person.ID); err != nil {
		return err
	}

	card.Waiting = true

	return s.respondSlot(w, r, card, "", "vol.done.waitlist")
}

func (s *Server) slotWaitlistLeave(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.volunteerSlot(r)
	if err != nil {
		return err
	}

	if err := s.store.LeaveWaitlist(r.Context(), sl.ID, rc(r).Person.ID); err != nil {
		return err
	}

	return s.respondSlot(w, r, s.slotCard(r, v, sl, false), "", "vol.done.waitlist_left")
}

// taskToggle ticks a task of the checklist of a slot: for the people on the
// slot, and whoever runs the pole.
func (s *Server) taskToggle(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	v, err := s.volunteerPole(r)
	if err != nil {
		return err
	}

	task, err := s.store.GetTaskByUID(ctx, r.PathValue("task"))
	if err != nil {
		return err
	}

	sl, err := s.store.GetSlot(ctx, task.SlotID, person.ID)
	if err != nil {
		return err
	}

	if sl.PoleID != v.Pole.ID {
		return errNotFound
	}

	if !sl.Mine && !v.Manage {
		return errForbidden
	}

	if err := s.store.ToggleTask(ctx, task.ID, person.ID); err != nil {
		return err
	}

	if !isHX(r) {
		http.Redirect(w, r, sl.URL(), http.StatusSeeOther)

		return nil
	}

	tasks, err := s.store.ListTasks(ctx, sl.ID)
	if err != nil {
		return err
	}

	done := 0

	for _, tk := range tasks {
		if tk.Done() {
			done++
		}
	}

	lang := rc(r).Lang

	return s.fragment(w, "task_list", map[string]any{
		"Lang": lang, "Tasks": tasks, "CanTick": true, "Slot": sl,
		"Announce": i18n.T(lang, "vol.tasks.announce", done, len(tasks)),
	})
}

// --- notifications of withdrawals -----------------------------------------

// lateWithdrawal is how close to its start a withdrawal is worth telling the
// managers about: earlier, the waiting list and the other volunteers have
// time to fill the hole; later, someone has to find a replacement.
const lateWithdrawal = 48 * time.Hour

// isLate tells whether a slot starts within lateWithdrawal from now.
func isLate(sl *store.Slot) bool {
	d := time.Until(sl.StartsAt)

	return d > 0 && d < lateWithdrawal
}

// announceFreePlace tells the people waiting for a slot that a place
// opened. Nothing happens when the slot is full again or nobody waits, so
// callers invoke it after any action that might free a place.
func (s *Server) announceFreePlace(r *http.Request, pole *store.Pole, slotID int64) {
	ctx := r.Context()

	sl, err := s.store.GetSlot(ctx, slotID, 0)
	if err != nil || sl.IsFull() {
		return
	}

	waiting, err := s.store.ListWaitlist(ctx, slotID)
	if err != nil || len(waiting) == 0 {
		return
	}

	s.notifyPlaceFreed(waiting, pole, sl)
}

// slotURL is the absolute address of a slot page, for e-mails.
func (s *Server) slotURL(sl *store.Slot) string { return s.cfg.BaseURL + sl.URL() }

// poleURL is the absolute address of a pole page, for e-mails.
func (s *Server) poleURL(p *store.Pole) string { return fmt.Sprintf("%s%s", s.cfg.BaseURL, p.URL()) }
