package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"manage/internal/i18n"
	"manage/internal/store"
)

// --- poles ----------------------------------------------------------------

// poleTabs are the sections of a pole's page: its slots for everyone, the
// rest for whoever runs it.
var poleTabs = []string{"slots", "members", "messages", "settings", "history"}

// polePage is the one page of a pole. A volunteer sees its slots, with who
// is on them, and joins or leaves it; whoever runs it finds the same slots
// with the phone numbers and the links to edit them, and the members,
// messages, settings and history in tabs of the same page.
func (s *Server) polePage(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.volunteerPole(r)
	if err != nil {
		return err
	}

	tab := r.URL.Query().Get("tab")
	if !v.Manage || !slices.Contains(poleTabs, tab) {
		tab = "slots"
	}

	// The pole's notices, as its slots' (slotPage): for its people, not for
	// a visitor of a private or invitation pole (a door code, a meeting
	// point).
	var messages []*store.Message

	if v.Member || v.Pole.IsOpen() || v.Manage {
		if messages, err = s.store.ListPoleMessages(ctx, v.Pole.ID); err != nil {
			return err
		}
	}

	managers, err := s.store.ListPoleManagers(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	locations, err := s.store.ListLocations(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	p := s.page(r, "vol.pole.title")
	p.TitleText = v.Pole.Name
	d := map[string]any{
		"V": v, "Tab": tab, "Tabs": poleTabs, "Messages": messages, "Managers": managers, "Leads": named(managers),
		"Locations": locations,
	}
	p.D = d

	switch tab {
	case "slots":
		if err := s.poleSlots(w, r, v, locations, d); err != nil {
			return err
		}
	case "members":
		members, err := s.poleMembers(ctx, act(r), v)
		if err != nil {
			return err
		}

		slots, err := s.upcomingPoleSlots(r, v.Pole.ID)
		if err != nil {
			return err
		}

		d["Members"], d["CanAppoint"], d["Slots"] = members, act(r).CanAppointManagers(v.Pole), slots
		d["CanAddMembers"] = act(r).CanAddMembers(v.Pole)
		d["InviteURL"] = s.cfg.BaseURL + v.Pole.URL() + "/join?key=" + url.QueryEscape(v.Pole.AccessKey)
	case "messages":
		members, err := s.store.ListPoleMembers(ctx, v.Pole.ID)
		if err != nil {
			return err
		}

		d["Recipients"] = len(members)
	case "settings":
		d["CanRestrict"], d["Modes"], d["MaxImage"] = act(r).CanRestrict(v.Pole), store.PoleModes, maxPoleImageSize
	case "history":
		lp := logPage(r)

		history, more, err := s.store.ListPoleLog(ctx, v.Pole.ID, lp)
		if err != nil {
			return err
		}

		d["History"], d["Log"] = history, logView{Page: lp, More: more, Base: v.Pole.URL() + "?tab=history"}
	}

	return s.render(w, r, "volunteer/pole", p)
}

// poleSlots fills in the slots tab: the cards, filtered by place or to the
// slots still to fill, by day, then grouped by time or by role.
func (s *Server) poleSlots(w http.ResponseWriter, r *http.Request, v *poleView, locations []*store.Location, d map[string]any) error {
	ctx := r.Context()
	person := rc(r).Person

	slots, err := s.store.ListSlots(ctx, v.Pole.ID, person.ID)
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

	var mine, missing int

	for _, sl := range slots {
		if sl.Mine {
			mine++
		}

		missing += sl.Missing()
	}

	// The slots are grouped by the time they run, or by the role they
	// staff, as last chosen.
	layout := ""
	if multiRole(slots, v.Pole) {
		layout = s.chosenLayout(w, r)
	}

	filterUID := ""
	if filter != nil {
		filterUID = filter.UID
	}

	// One query each for the whole page rather than one per card.
	waiting, err := s.store.PersonWaitlistInPole(ctx, v.Pole.ID, person.ID)
	if err != nil {
		return err
	}

	people, err := s.store.ListEventSignups(ctx, v.Event.ID, v.Pole.ID)
	if err != nil {
		return err
	}

	tasks, err := s.store.ListEventTasks(ctx, v.Event.ID, v.Pole.ID)
	if err != nil {
		return err
	}

	var counts map[int64]int

	if v.Manage {
		if counts, err = s.store.WaitlistCounts(ctx, v.Pole.ID); err != nil {
			return err
		}
	}

	// Who is on a slot shows to those who may take one: the members, anyone
	// on an open pole, and whoever runs it.
	showPeople := v.Member || v.Pole.IsOpen() || v.Manage
	// The layout always rides in back, even the default one: a card that
	// htmx re-renders reads from it what its group already shows.
	back := poleSlotsURL(v.Pole, filterUID, onlyOpen, layout)
	lang := rc(r).Lang

	var days []*slotDay

	for _, sl := range slots {
		if (filter != nil && sl.LocationID != filter.ID) || (onlyOpen && sl.IsFull() && !sl.Mine) {
			continue
		}

		key := dayKey(sl.StartsAt)
		if len(days) == 0 || days[len(days)-1].Day != key {
			days = append(days, &slotDay{Day: key})
		}

		days[len(days)-1].Cards = append(days[len(days)-1].Cards, &slotCardData{
			Lang: lang, Slot: sl, Pole: v.Pole, Member: v.Member, Locked: v.Locked, Waiting: waiting[sl.ID], Back: back,
			ShowPeople: showPeople, People: people[sl.ID], Manage: v.Manage, WaitCount: counts[sl.ID],
			Tasks: tasks[sl.ID], CanTick: sl.Mine || v.Manage, Fiche: v.Fiche,
			NoTime: layout == layoutTime, NoRole: layout == layoutRole,
		})
	}

	for _, day := range days {
		day.Groups = groupSlots(day.Cards, layout)
	}

	if layout != "" {
		d["Layouts"] = []planLink{
			{Label: "vol.pole.layout_time", URL: poleSlotsURL(v.Pole, filterUID, onlyOpen, layoutTime), On: layout == layoutTime},
			{Label: "vol.pole.layout_role", URL: poleSlotsURL(v.Pole, filterUID, onlyOpen, layoutRole), On: layout == layoutRole},
		}
	}

	if len(locations) > 0 {
		links := []planLink{{Label: "vol.pole.all_locations", URL: poleSlotsURL(v.Pole, "", onlyOpen, layout), On: filter == nil}}
		for _, l := range locations {
			links = append(links, planLink{Name: l.Name, URL: poleSlotsURL(v.Pole, l.UID, onlyOpen, layout), On: l.UID == filterUID})
		}

		d["LocationLinks"] = links
	}

	d["Days"], d["Layout"], d["OnlyOpen"] = days, layout, onlyOpen
	d["OpenURL"] = poleSlotsURL(v.Pole, filterUID, !onlyOpen, layout)
	d["Mine"], d["Missing"], d["Total"] = mine, missing, len(slots)
	d["Deadline"] = v.Event.SignupDeadline(v.Pole.FreezeAt)

	return nil
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

	err = s.joinPole(r.Context(), act(r), v, strings.TrimSpace(r.FormValue("key")))
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, volunteerHomeURL(v.Event), http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	s.flash(w, "flash.vol.joined", v.Pole.Name)
	http.Redirect(w, r, v.Pole.URL(), http.StatusSeeOther)

	return nil
}

func (s *Server) poleLeave(w http.ResponseWriter, r *http.Request) error {
	v, err := s.volunteerPole(r)
	if err != nil {
		return err
	}

	err = s.leavePole(r.Context(), act(r), v)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, v.Pole.URL(), http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	s.flash(w, "flash.vol.left", v.Pole.Name)
	http.Redirect(w, r, volunteerHomeURL(v.Event), http.StatusSeeOther)

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

	// Who is on the slot, and its messages, show to those who may take it,
	// as on the pole's page: not to a visitor of a private or invitation
	// pole.
	var (
		volunteers []*store.Person
		messages   []*store.Message
	)

	showPeople := v.Member || v.Pole.IsOpen() || v.Manage

	if showPeople {
		if volunteers, err = s.store.ListSlotVolunteers(ctx, sl.ID); err != nil {
			return err
		}

		if messages, err = s.store.ListSlotMessages(ctx, sl.ID); err != nil {
			return err
		}
	}

	tasks, err := s.store.ListTasks(ctx, sl.ID)
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
		"V": v, "Slot": sl, "Volunteers": volunteers, "ShowPeople": showPeople, "Tasks": tasks, "Messages": messages, "Contacts": contacts,
		"Card":    s.slotCard(r, v, sl, waiting),
		"CanTick": sl.Mine || v.Manage,
	}

	return s.render(w, r, "volunteer/slot", p)
}

// slotCardData is what the slot_card partial shows. Back is carried through
// the forms so a card re-rendered by htmx keeps it.
type slotCardData struct {
	Lang    string
	Slot    *store.Slot
	Pole    *store.Pole
	Member  bool
	Locked  bool
	Waiting bool
	Back    string
	// Compact leaves out the time and place, which the slot's own page
	// already shows as its heading.
	Compact bool
	// NoTime and NoRole leave out what the group of the card, on the
	// pole's page, already shows as its heading.
	NoTime bool
	NoRole bool
	// Group is the group of a card re-rendered by htmx, whose heading is
	// updated along with it.
	Group    *slotGroup
	Error    string
	Announce string
	Overlaps []*store.Slot
	// ShowPeople lists who is signed up, on the planning; Manage adds
	// their numbers and a link to edit the slot, for whoever runs the pole.
	ShowPeople bool
	People     []*store.Person
	Manage     bool
	// WaitCount is the people waiting for a place, for whoever runs the
	// pole: a slot with a queue is a slot worth enlarging.
	WaitCount int
	// Tasks is the checklist, folded on the card; CanTick lets the people
	// on the slot and whoever runs the pole tick it from there.
	Tasks   []*store.SlotTask
	CanTick bool
	// OpenTasks unfolds the checklist, where it is what the card is for.
	OpenTasks bool
	// Fiche leads the names to the volunteers' pages, for the
	// coordination.
	Fiche string
}

func (s *Server) slotCard(r *http.Request, v *poleView, sl *store.Slot, waiting bool) *slotCardData {
	back := safeRedirect(r.FormValue("back"), sl.URL())

	card := &slotCardData{
		Lang: rc(r).Lang, Slot: sl, Pole: v.Pole, Member: v.Member, Locked: v.Locked, Waiting: waiting, Back: back,
		Compact: back == sl.URL(), Fiche: v.Fiche,
	}

	// A card of the planning or of the pole's page shows who is on the
	// slot, which an action on it has just changed.
	page, _, _ := strings.Cut(back, "?")

	// Swapped into a group, the card leaves out what the group's heading
	// shows, and brings that heading's figures up to date.
	if isHX(r) {
		s.groupCard(r, v, card)
	}

	if (page == planningURL(v.Event) || page == v.Pole.URL()) && (v.Member || v.Pole.IsOpen() || v.Manage) {
		people, err := s.store.ListSlotVolunteers(r.Context(), sl.ID)
		if err != nil {
			slog.Error("slot card", "slot", sl.UID, "err", err)
		}

		card.ShowPeople, card.People, card.Manage = true, people, v.Manage

		tasks, err := s.store.ListTasks(r.Context(), sl.ID)
		if err != nil {
			slog.Error("slot card", "slot", sl.UID, "err", err)
		}

		card.Tasks, card.CanTick = tasks, sl.Mine || v.Manage

		if v.Manage {
			counts, err := s.store.WaitlistCounts(r.Context(), v.Pole.ID)
			if err != nil {
				slog.Error("slot card", "slot", sl.UID, "err", err)
			}

			card.WaitCount = counts[sl.ID]
		}
	}

	return card
}

// groupCard fits a card re-rendered by htmx to the group it sits in, on
// the page it goes back to, with the group's figures after the action.
func (s *Server) groupCard(r *http.Request, v *poleView, card *slotCardData) {
	layout := cardLayout(card.Back, v.Event, v.Pole)
	if layout == "" {
		return
	}

	var personID int64
	if p := rc(r).Person; p != nil {
		personID = p.ID
	}

	slots, err := s.store.ListSlots(r.Context(), v.Pole.ID, personID)
	if err != nil {
		slog.Error("slot card", "slot", card.Slot.UID, "err", err)

		return
	}

	if !multiRole(slots, v.Pole) {
		return
	}

	card.NoTime, card.NoRole = layout == layoutTime, layout == layoutRole

	key := groupKey(card.Slot, v.Pole, layout)
	g := &slotGroup{ID: groupID(v.Pole, key)}

	for _, sl := range slots {
		if groupKey(sl, v.Pole, layout) == key {
			g.Missing += sl.Missing()
			g.Mine = g.Mine || sl.Mine
		}
	}

	card.Group = g
}

// respondSlot answers a slot action: the card for htmx, a redirect back to
// the page otherwise. The outcome is announced either way: in the live
// region of the card, or as a flash message.
func (s *Server) respondSlot(w http.ResponseWriter, r *http.Request, card *slotCardData, errKey, doneKey string, args ...string) error {
	lang := rc(r).Lang

	if isHX(r) {
		// On the slot's own page, its people, places and tasks change
		// along with the card: reload it, with the outcome as its flash.
		if card.Compact && errKey == "" && doneKey != "" {
			s.flash(w, doneKey, args...)
			w.Header().Set("HX-Redirect", card.Back)

			return nil
		}

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
	lang := rc(r).Lang

	v, sl, err := s.volunteerSlot(r)
	if err != nil {
		return err
	}

	out, err := s.signUp(r.Context(), act(r), v, sl, r.FormValue("force") == "1")
	if rf, ok := refused(err); ok {
		if out != nil {
			sl = out.Slot
		}

		return s.respondSlot(w, r, s.slotCard(r, v, sl, false), rf.Key, "", rf.Args...)
	}

	if err != nil {
		return err
	}

	card := s.slotCard(r, v, out.Slot, false)

	if out.Overlaps != nil {
		card.Overlaps = out.Overlaps

		if isHX(r) {
			card.Announce = i18n.T(lang, "vol.overlap.announce")

			return s.fragment(w, "slot_card", card)
		}

		p := s.page(r, "vol.overlap.title")
		p.D = card

		return s.render(w, r, "volunteer/slot_confirm", p)
	}

	when := slotWhen(lang, out.Slot.StartsAt, out.Slot.EndsAt)
	if out.Joined {
		// Joining changes more than the card: the page's "not in this pole
		// yet" block and the other cards. htmx reloads the whole page, with
		// the outcome as its flash message.
		if isHX(r) {
			s.flash(w, "vol.done.joined_signup", v.Pole.Name, when)
			w.Header().Set("HX-Redirect", card.Back)

			return nil
		}

		return s.respondSlot(w, r, card, "", "vol.done.joined_signup", v.Pole.Name, when)
	}

	return s.respondSlot(w, r, card, "", "vol.done.signup", v.Pole.Name, when)
}

func (s *Server) slotUnsignup(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.volunteerSlot(r)
	if err != nil {
		return err
	}

	after, err := s.unsignUp(r.Context(), act(r), v, sl)
	if rf, ok := refused(err); ok {
		return s.respondSlot(w, r, s.slotCard(r, v, sl, false), rf.Key, "", rf.Args...)
	}

	if err != nil {
		return err
	}

	return s.respondSlot(w, r, s.slotCard(r, v, after, false), "", "vol.done.unsignup", slotWhen(rc(r).Lang, after.StartsAt, after.EndsAt))
}

// slotWaitlistJoin records that the volunteer wants a full slot.
func (s *Server) slotWaitlistJoin(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.volunteerSlot(r)
	if err != nil {
		return err
	}

	card := s.slotCard(r, v, sl, false)

	joined, err := s.joinWaitlist(r.Context(), act(r), v, sl)
	if rf, ok := refused(err); ok {
		return s.respondSlot(w, r, card, rf.Key, "", rf.Args...)
	}

	if err != nil {
		return err
	}

	// Joining changes the whole page, as for a sign-up.
	if joined && isHX(r) {
		s.flash(w, "vol.done.waitlist")
		w.Header().Set("HX-Redirect", card.Back)

		return nil
	}

	card.Waiting = true

	return s.respondSlot(w, r, card, "", "vol.done.waitlist")
}

func (s *Server) slotWaitlistLeave(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.volunteerSlot(r)
	if err != nil {
		return err
	}

	if err := s.leaveWaitlist(r.Context(), act(r), v, sl); err != nil {
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

	// The history says who did what, and who took it back.
	action := store.LogTaskDone
	if task.Done() {
		action = store.LogTaskUndone
	}

	entry := logSlot(action, v.Pole, sl, nil)
	entry.Title = task.Label
	s.logVol(r, entry)

	back := safeRedirect(r.FormValue("back"), sl.URL())

	if !isHX(r) {
		http.Redirect(w, r, back, http.StatusSeeOther)

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

	data := map[string]any{
		"Lang": lang, "Tasks": tasks, "CanTick": true, "Slot": sl,
		"Announce": i18n.T(lang, "vol.tasks.announce", done, len(tasks)),
	}

	// On a card, the folded checklist comes back open, with its state.
	if r.FormValue("view") == "card" {
		data["View"], data["Back"], data["Open"] = "card", back, true

		return s.fragment(w, "task_block", data)
	}

	return s.fragment(w, "task_list", data)
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
func (s *Server) announceFreePlace(ctx context.Context, pole *store.Pole, slotID int64) {
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
