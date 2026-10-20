package server

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"manage/internal/files"
	"manage/internal/i18n"
	"manage/internal/store"
)

// Pole management, for the people who run a pole: its managers, the
// coordinators of its edition and the staff. Everything sits below
// /{event}/poles/{pole}/manage.

const maxPoleImageSize = 2 << 20

func (s *Server) poleManageRoutes(mux *router) {
	m := func(pattern string, h handlerFunc) {
		mux.Handle(pattern, s.wrap(s.requireVolunteer(h)))
	}

	const base = "/{event}/poles/{pole}/manage"

	m("POST "+base, s.managePoleSave)
	m("POST "+base+"/key", s.managePoleKey)
	m("POST "+base+"/image", s.managePoleImage)
	m("POST "+base+"/image/delete", s.managePoleImageDelete)
	m("GET "+base+"/people", s.managePeopleSearch)
	m("POST "+base+"/members", s.manageMemberAdd)
	m("POST "+base+"/members/{person}/delete", s.manageMemberRemove)
	m("POST "+base+"/members/{person}/signup", s.manageMemberSignup)
	m("POST "+base+"/managers", s.manageManagerAdd)
	m("POST "+base+"/managers/{person}/delete", s.manageManagerRemove)
	m("POST "+base+"/locations", s.manageLocationAdd)
	m("POST "+base+"/locations/{location}/delete", s.manageLocationDelete)
	m("POST "+base+"/messages", s.managePoleMessage)
	m("POST "+base+"/messages/{message}/delete", s.manageMessageDelete)
	m("GET "+base+"/slots/new", s.manageSlotNew)
	m("POST "+base+"/slots", s.manageSlotCreate)
	m("GET "+base+"/slots/{slot}", s.manageSlotEdit)
	m("POST "+base+"/slots/{slot}", s.manageSlotSave)
	m("POST "+base+"/slots/{slot}/delete", s.manageSlotDelete)
	m("GET "+base+"/slots/bulk", s.manageSlotsBulk)
	m("POST "+base+"/slots/bulk/delete", s.manageSlotsBulkDelete)
	m("POST "+base+"/slots/{slot}/tasks", s.manageTaskAdd)
	m("POST "+base+"/slots/{slot}/messages", s.manageSlotMessage)
	m("POST "+base+"/slots/{slot}/volunteers", s.manageSlotAddVolunteer)
	m("GET "+base+"/slots/{slot}/picker", s.manageSlotPicker)
	m("POST "+base+"/slots/{slot}/volunteers/{person}/delete", s.manageSlotRemoveVolunteer)
	m("POST "+base+"/tasks/{task}/delete", s.manageTaskDelete)
	m("POST "+base+"/days/copy", s.manageDayCopy)
	m("GET "+base+"/signups.csv", s.manageSignupsCSV)

	s.generateRoutes(m)
}

// managedPole loads the pole of the path for someone who runs it.
func (s *Server) managedPole(r *http.Request) (*poleView, error) {
	v, err := s.volunteerPole(r)
	if err != nil {
		return nil, err
	}

	if !v.Manage {
		return nil, errForbidden
	}

	return v, nil
}

// managedSlot loads the slot of the path, in a pole the person runs.
func (s *Server) managedSlot(r *http.Request) (*poleView, *store.Slot, error) {
	v, err := s.managedPole(r)
	if err != nil {
		return nil, nil, err
	}

	sl, err := s.store.GetSlotByUID(r.Context(), v.Event.ID, r.PathValue("slot"), 0)
	if err != nil {
		return nil, nil, err
	}

	if sl.PoleID != v.Pole.ID {
		return nil, nil, errNotFound
	}

	return v, sl, nil
}

// pathPerson loads the person of the path.
func (s *Server) pathPerson(r *http.Request) (*store.Person, error) {
	return s.store.GetPersonByUID(r.Context(), r.PathValue("person"))
}

// redirectTab goes back to a tab of the pole's page after an action.
func (s *Server) redirectTab(w http.ResponseWriter, r *http.Request, p *store.Pole, tab string) error {
	to := p.URL()
	if tab != "" {
		to += "?tab=" + tab
	}

	http.Redirect(w, r, to, http.StatusSeeOther)

	return nil
}

// --- the management page ----------------------------------------------------

// formTime reads a datetime-local field, in the event's time zone.
func formTime(r *http.Request, name string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02T15:04", strings.TrimSpace(r.FormValue(name)), paris)

	return t, err == nil
}

func formTimePtr(r *http.Request, name string) *time.Time {
	t, ok := formTime(r, name)
	if !ok {
		return nil
	}

	return &t
}

func (s *Server) managePoleSave(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	set := poleSettings{Name: r.FormValue("name"), Description: r.FormValue("description"), Icon: r.FormValue("icon"),
		DefaultTasks: r.FormValue("default_tasks"), FreezeAt: formTimePtr(r, "freeze_at"), AccessMode: r.FormValue("access_mode")}

	if r.Form.Has("slug") {
		slug := r.FormValue("slug")
		set.Slug = &slug
	}

	if err := s.savePole(r.Context(), act(r), v, set); err != nil {
		return err
	}

	s.flash(w, "flash.vol.pole_saved")

	return s.redirectTab(w, r, v.Pole, "settings")
}

func (s *Server) managePoleKey(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if err := s.regenerateKey(r.Context(), act(r), v); err != nil {
		return err
	}

	s.flash(w, "flash.vol.key_regenerated")

	return s.redirectTab(w, r, v.Pole, "members")
}

func (s *Server) managePoleImage(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	name, err := s.upload(w, r, "image", files.KindLogo, maxPoleImageSize)
	if err != nil {
		var ue uploadError
		if errors.As(err, &ue) {
			s.flash(w, string(ue))

			return s.redirectTab(w, r, v.Pole, "settings")
		}

		return err
	}

	if err := s.setPoleImage(r.Context(), act(r), v, name); err != nil {
		return err
	}

	s.flash(w, "flash.vol.image_saved")

	return s.redirectTab(w, r, v.Pole, "settings")
}

func (s *Server) managePoleImageDelete(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if err := s.setPoleImage(r.Context(), act(r), v, ""); err != nil {
		return err
	}

	return s.redirectTab(w, r, v.Pole, "settings")
}

// dropFile removes a stored image once nothing uses it any more. Copying a
// pole or an edition makes two rows share one file, so clearing one must
// not take the bytes away from the other.
func (s *Server) dropFile(ctx context.Context, file string) {
	if file == "" {
		return
	}

	used, err := s.store.FileInUse(ctx, file)
	if err != nil || used {
		return
	}

	_ = s.files.Remove(file)
}

// --- members --------------------------------------------------------------

// managePeopleSearch answers the live search of the "add members" box.
func (s *Server) managePeopleSearch(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	d, err := s.memberSearch(r, v, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		return err
	}

	return s.fragment(w, "member_search", d)
}

func (s *Server) memberSearch(r *http.Request, v *poleView, q string) (map[string]any, error) {
	var people []*store.Person

	if len([]rune(q)) >= 2 {
		var err error
		if people, err = s.store.SearchPeopleNotInPole(r.Context(), v.Pole.ID, q, 8); err != nil {
			return nil, err
		}
	}

	return map[string]any{"Lang": rc(r).Lang, "Pole": v.Pole, "People": people, "Q": q, "Fiche": v.Fiche}, nil
}

// manageMemberAdd adds people to a pole, by address: one per line, bare or
// as "Name <address>". Those without an account get one.
func (s *Server) manageMemberAdd(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	var people []newMember

	for _, raw := range strings.FieldsFunc(r.FormValue("emails"), func(c rune) bool { return c == '\n' || c == ',' || c == ';' }) {
		addr, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil || !validEmail(store.NormalizeEmail(addr.Address)) {
			continue
		}

		people = append(people, newMember{Email: addr.Address, Name: strings.TrimSpace(addr.Name)})
	}

	done, err := s.addMembers(ctx, act(r), v, people, rc(r).Lang)
	if err != nil {
		return err
	}

	added := len(done)

	if isHX(r) {
		d, err := s.memberSearch(r, v, strings.TrimSpace(r.FormValue("q")))
		if err != nil {
			return err
		}

		if d["Members"], err = s.store.ListPoleMembers(ctx, v.Pole.ID); err != nil {
			return err
		}

		if d["Slots"], err = s.upcomingPoleSlots(r, v.Pole.ID); err != nil {
			return err
		}

		d["Added"], d["Announce"] = added, plural(rc(r).Lang, "vol.manage.added", added)

		return s.fragment(w, "member_added", d)
	}

	if added == 0 {
		s.flash(w, "vol.err.no_address")
	} else {
		s.flashCount(w, "flash.vol.members_added", added)
	}

	return s.redirectTab(w, r, v.Pole, "members")
}

func (s *Server) manageMemberRemove(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	who, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	err = s.removeMember(r.Context(), act(r), v, who)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)

		return s.redirectTab(w, r, v.Pole, "members")
	}

	if err != nil {
		return err
	}

	s.flash(w, "flash.vol.member_removed", who.Label())

	return s.redirectTab(w, r, v.Pole, "members")
}

// --- pole managers: the coordination's call -------------------------------

func (s *Server) manageManagerAdd(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	p, err := s.appointManager(r.Context(), act(r), v, r.FormValue("email"), rc(r).Lang)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)

		return s.redirectTab(w, r, v.Pole, "members")
	}

	if err != nil {
		return err
	}

	s.flash(w, "flash.vol.manager_added", p.Label())

	return s.redirectTab(w, r, v.Pole, "members")
}

func (s *Server) manageManagerRemove(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	who, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	if err := s.dismissManager(r.Context(), act(r), v, who); err != nil {
		return err
	}

	s.flash(w, "flash.vol.manager_removed", who.Label())

	return s.redirectTab(w, r, v.Pole, "members")
}

// flashCount flashes key+".none", ".one" or ".other" after n, with n as
// the first argument of the text.
func (s *Server) flashCount(w http.ResponseWriter, key string, n int, args ...string) {
	switch {
	case n == 0 && i18n.Has(key+".none"):
		key += ".none"
	case n == 1:
		key += ".one"
	default:
		key += ".other"
	}

	s.flash(w, key, append([]string{strconv.Itoa(n)}, args...)...)
}

// cmpErr returns err when set, otherwise fallback.
func cmpErr(err, fallback error) error {
	if err != nil {
		return err
	}

	return fallback
}

// --- locations --------------------------------------------------------------

func (s *Server) manageLocationAdd(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	// Several names at once, one per line.
	if err := s.addPlaces(r.Context(), act(r), v, nonBlankLines(r.FormValue("names"))); err != nil {
		return err
	}

	return s.redirectTab(w, r, v.Pole, "settings")
}

func (s *Server) manageLocationDelete(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if err := s.deletePlace(r.Context(), act(r), v, r.PathValue("location")); err != nil {
		return err
	}

	return s.redirectTab(w, r, v.Pole, "settings")
}

// --- messages ---------------------------------------------------------------

func (s *Server) managePoleMessage(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	notify := r.FormValue("notify") == "1"

	sent, err := s.postNotice(r.Context(), act(r), noticeScope{Event: v.Event, Pole: v.Pole}, r.FormValue("body"), notify)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)

		return s.redirectTab(w, r, v.Pole, "messages")
	}

	if err != nil {
		return err
	}

	s.flashNotice(w, notify, sent)

	return s.redirectTab(w, r, v.Pole, "messages")
}

func (s *Server) manageSlotMessage(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	notify := r.FormValue("notify") == "1"
	back := sl.ManageURL() + "#messages"

	sent, err := s.postNotice(r.Context(), act(r), noticeScope{Event: v.Event, Pole: v.Pole, Slot: sl}, r.FormValue("body"), notify)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	s.flashNotice(w, notify, sent)
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

// manageMessageDelete removes a notice of the pole or of one of its slots.
func (s *Server) manageMessageDelete(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if err := s.deleteNotice(r.Context(), act(r), noticeScope{Event: v.Event, Pole: v.Pole}, r.PathValue("message")); err != nil {
		return err
	}

	http.Redirect(w, r, safeRedirect(r.FormValue("back"), v.Pole.URL()+"?tab=messages"), http.StatusSeeOther)

	return nil
}

// --- slots ------------------------------------------------------------------

// slotForm is what the slot form shows.
type slotForm struct {
	V         *poleView
	Slot      *store.Slot
	New       bool
	Locations []*store.Location
	// Location is the UID of the slot's location, "" for none.
	Location     string
	Tasks        string
	RepeatUntil  string
	AllLocations bool
	Error        string
	// Outside is set when the slot falls outside the dates of the edition,
	// a frequent slip of the day: the form asks for a confirmation.
	Outside bool
}

// outsideEdition tells whether a slot starts or ends on a day outside the
// dates of its edition. Without dates there is nothing to check.
func outsideEdition(e *store.Event, sl *store.Slot) bool {
	if e.StartsOn == nil || e.EndsOn == nil {
		return false
	}

	first, last := e.StartsOn.Format(time.DateOnly), e.EndsOn.Format(time.DateOnly)
	start, end := dayKey(sl.StartsAt), dayKey(sl.EndsAt.Add(-time.Minute))

	return start < first || end > last
}

func (s *Server) renderSlotForm(w http.ResponseWriter, r *http.Request, f *slotForm, extra map[string]any) error {
	locations, err := s.store.ListLocations(r.Context(), f.V.Pole.ID)
	if err != nil {
		return err
	}

	f.Locations = locations

	for _, l := range locations {
		if l.ID == f.Slot.LocationID {
			f.Location = l.UID
		}
	}

	title := "vol.slot_form.edit"
	if f.New {
		title = "vol.slot_form.new"
	}

	d := map[string]any{"F": f}
	for k, v := range extra {
		d[k] = v
	}

	p := s.page(r, title)
	p.D = d

	return s.render(w, r, "volunteer/slot_form", p)
}

func (s *Server) manageSlotNew(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	day := time.Now().In(paris)
	if v.Event.StartsOn != nil {
		day = *v.Event.StartsOn
	}

	start := time.Date(day.Year(), day.Month(), day.Day(), 9, 0, 0, 0, paris)

	return s.renderSlotForm(w, r, &slotForm{V: v, New: true, Tasks: v.Pole.DefaultTasks,
		Slot: &store.Slot{StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 1}}, nil)
}

// slotDraftFromForm reads the fields of the slot form.
func slotDraftFromForm(r *http.Request) slotDraft {
	d := slotDraft{Title: r.FormValue("title"), Capacity: formInt(r, "capacity"), Location: r.FormValue("location")}

	start, okStart := formTime(r, "starts_at")
	end, okEnd := formTime(r, "ends_at")

	if okStart && okEnd {
		d.StartsAt, d.EndsAt = start, end
	}

	return d
}

// slotRefusal is the error and confirmation a slot form shows again after
// a refusal.
func slotRefusal(rf *refusal) (msg string, outside bool) {
	if rf.Key == outsideKey {
		return "", true
	}

	return rf.Key, false
}

// manageSlotCreate creates a slot, or a series of them: consecutive slots
// of the same length up to a time, for one location or for each of them.
func (s *Server) manageSlotCreate(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	series := slotSeries{AllLocations: r.FormValue("all_locations") == "1", Tasks: nonBlankLines(r.FormValue("tasks"))}
	if until, ok := formTime(r, "repeat_until"); ok {
		series.Until = until
	}

	created, typed, err := s.createSlots(r.Context(), act(r), v, slotDraftFromForm(r), series, r.FormValue("outside_ok") == "yes")
	if rf, ok := refused(err); ok {
		msg, outside := slotRefusal(rf)

		return s.renderSlotForm(w, r, &slotForm{V: v, New: true, Slot: typed, Error: msg, Outside: outside, Tasks: r.FormValue("tasks"),
			RepeatUntil: r.FormValue("repeat_until"), AllLocations: series.AllLocations}, nil)
	}

	if err != nil {
		return err
	}

	s.flashCount(w, "flash.vol.slots_created", created)

	return s.redirectTab(w, r, v.Pole, "")
}

// logSlotsCreated records slots created in a pole, by hand or generated.
func (s *Server) logSlotsCreated(ctx context.Context, actor *store.Person, p *store.Pole, n int) {
	if n == 0 {
		return
	}

	e := logPole(store.LogSlotsCreated, p, nil)
	e.Count = n
	s.logAs(ctx, actor, e)
}

func (s *Server) manageSlotEdit(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	errKey := r.URL.Query().Get("error")
	if !strings.HasPrefix(errKey, "vol.err.") || !i18n.Has(errKey) {
		errKey = ""
	}

	extra, err := s.slotEditExtras(r, v, sl)
	if err != nil {
		return err
	}

	return s.renderSlotForm(w, r, &slotForm{V: v, Slot: sl, Error: errKey}, extra)
}

// slotEditExtras reads what the edit page shows next to the form: tasks,
// volunteers, messages, the members not on the slot, the waiting list.
func (s *Server) slotEditExtras(r *http.Request, v *poleView, sl *store.Slot) (map[string]any, error) {
	ctx := r.Context()

	tasks, err := s.store.ListTasks(ctx, sl.ID)
	if err != nil {
		return nil, err
	}

	volunteers, err := s.store.ListSlotVolunteers(ctx, sl.ID)
	if err != nil {
		return nil, err
	}

	messages, err := s.store.ListSlotMessages(ctx, sl.ID)
	if err != nil {
		return nil, err
	}

	members, err := s.store.ListPoleMembers(ctx, v.Pole.ID)
	if err != nil {
		return nil, err
	}

	waiting, err := s.store.ListWaitlist(ctx, sl.ID)
	if err != nil {
		return nil, err
	}

	// Offer the members who are not on the slot already.
	on := map[int64]bool{}
	for _, p := range volunteers {
		on[p.ID] = true
	}

	var available []*store.PoleMember

	for _, m := range members {
		if !on[m.Person.ID] {
			available = append(available, m)
		}
	}

	return map[string]any{
		"Tasks": tasks, "Volunteers": volunteers, "Messages": messages, "Available": available, "Waiting": waiting,
		"CanAddMembers": act(r).CanAddMembers(v.Pole),
	}, nil
}

func (s *Server) manageSlotSave(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	notified, typed, err := s.updateSlot(r.Context(), act(r), v, sl, slotDraftFromForm(r), r.FormValue("outside_ok") == "yes")
	if rf, ok := refused(err); ok {
		msg, outside := slotRefusal(rf)

		// The form comes back with what was typed.
		extra, err := s.slotEditExtras(r, v, sl)
		if err != nil {
			return err
		}

		return s.renderSlotForm(w, r, &slotForm{V: v, Slot: typed, Error: msg, Outside: outside}, extra)
	}

	if err != nil {
		return err
	}

	s.flashCount(w, "flash.vol.slot_saved", notified)

	return s.redirectTab(w, r, v.Pole, "")
}

func (s *Server) manageSlotDelete(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	done, err := s.deleteSlots(r.Context(), act(r), v, []*store.Slot{sl})
	if err != nil {
		return err
	}

	s.flashCount(w, "flash.vol.slot_deleted", done.Signups)

	return s.redirectTab(w, r, v.Pole, "")
}

// manageDayCopy copies the slots of a day of the pole to another day: a
// two-day event is often the same day twice.
func (s *Server) manageDayCopy(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	from, _ := time.ParseInLocation(time.DateOnly, r.FormValue("from"), paris)
	to, _ := time.ParseInLocation(time.DateOnly, r.FormValue("to"), paris)

	created, skipped, err := s.copyDay(r.Context(), act(r), v, from, to)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)

		return s.redirectTab(w, r, v.Pole, "")
	}

	if err != nil {
		return err
	}

	if skipped > 0 {
		s.flashCount(w, "flash.vol.day_copied_skip", created, dayName(rc(r).Lang, to), strconv.Itoa(skipped))
	} else {
		s.flashCount(w, "flash.vol.day_copied", created, dayName(rc(r).Lang, to))
	}

	return s.redirectTab(w, r, v.Pole, "")
}

// --- tasks ------------------------------------------------------------------

func (s *Server) manageTaskAdd(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	if err := s.addTasks(r.Context(), act(r), v, sl, nonBlankLines(r.FormValue("labels"))); err != nil {
		return err
	}

	http.Redirect(w, r, sl.ManageURL()+"#tasks", http.StatusSeeOther)

	return nil
}

func (s *Server) manageTaskDelete(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	sl, err := s.deleteTask(r.Context(), act(r), v, r.PathValue("task"))
	if err != nil {
		return err
	}

	http.Redirect(w, r, sl.ManageURL()+"#tasks", http.StatusSeeOther)

	return nil
}

// --- volunteers on a slot ---------------------------------------------------

// manageSlotPicker is the "sign someone up" form of a slot card, fetched
// when it opens: the pole's members not on the slot, or an address. The
// card comes back to the page it is on.
func (s *Server) manageSlotPicker(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	d, err := s.slotEditExtras(r, v, sl)
	if err != nil {
		return err
	}

	d["Lang"], d["Slot"], d["Back"] = rc(r).Lang, sl, safeRedirect(r.URL.Query().Get("back"), sl.URL())

	return s.fragment(w, "slot_picker", d)
}

// manageSlotAddVolunteer puts people on a slot, even a full one: whoever
// runs the pole decides. Two intents, two fields: picking members of the
// pole (the common case), or deliberately inviting someone by address. A
// single "name or e-mail" box turned every typo into an account.
func (s *Server) manageSlotAddVolunteer(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	back := safeRedirect(r.FormValue("back"), sl.ManageURL()+"#volunteers")

	var people []*store.Person

	if _, invite := r.PostForm["email"]; invite {
		email := store.NormalizeEmail(r.FormValue("email"))
		if !validEmail(email) {
			http.Redirect(w, r, sl.ManageURL()+"?error=vol.err.invite_email#volunteers", http.StatusSeeOther)

			return nil
		}

		who, err := s.store.EnsureVolunteer(ctx, email, strings.TrimSpace(r.FormValue("name")), "", rc(r).Lang)
		if err != nil {
			return err
		}

		people = append(people, who)
	} else {
		seen := map[string]bool{}

		for _, uid := range r.PostForm["person"] {
			if uid == "" || seen[uid] {
				continue
			}

			seen[uid] = true

			who, err := s.store.GetPersonByUID(ctx, uid)
			if err != nil {
				return err
			}

			people = append(people, who)
		}
	}

	if len(people) == 0 {
		s.flash(w, "vol.err.pick_member")
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	// Signing someone up makes them a member: on a restricted pole, only the
	// coordination brings in new people, as in the members tab.
	done, err := s.signUpOthers(ctx, act(r), v, sl, people)
	if err != nil {
		return err
	}

	s.flashPlaced(w, v, done)
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

// flashPlaced says who was signed up by whoever runs the pole, warning of
// those already on another slot at that time.
func (s *Server) flashPlaced(w http.ResponseWriter, v *poleView, done []placed) {
	if len(done) == 1 {
		if done[0].Also != "" {
			s.flash(w, "flash.vol.volunteer_added_overlap", done[0].Person.Label(), done[0].Also)
		} else {
			s.flash(w, "flash.vol.volunteer_added", done[0].Person.Label(), v.Pole.Name)
		}

		return
	}

	var names, also []string

	for _, p := range done {
		names = append(names, p.Person.Label())

		if p.Also != "" {
			also = append(also, p.Person.Label()+" : "+p.Also)
		}
	}

	if len(also) > 0 {
		s.flash(w, "flash.vol.volunteers_added_overlap", strings.Join(names, ", "), strings.Join(also, " ; "))
	} else {
		s.flash(w, "flash.vol.volunteers_added", strings.Join(names, ", "))
	}
}

// upcomingPoleSlots are the slots of a pole still to come, the ones a
// member can be put on.
func (s *Server) upcomingPoleSlots(r *http.Request, poleID int64) ([]*store.Slot, error) {
	all, err := s.store.ListSlots(r.Context(), poleID, 0)
	if err != nil {
		return nil, err
	}

	var out []*store.Slot

	for _, sl := range all {
		if sl.EndsAt.After(time.Now()) {
			out = append(out, sl)
		}
	}

	return out, nil
}

// manageMemberSignup puts a member on a slot of the pole from the members
// list: filling the gaps of those on no slot took a trip to each slot.
func (s *Server) manageMemberSignup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	who, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	if member, err := s.store.IsPoleMember(ctx, v.Pole.ID, who.ID); err != nil {
		return err
	} else if !member {
		return errNotFound
	}

	sl, err := s.store.GetSlotByUID(ctx, v.Event.ID, r.FormValue("slot"), 0)
	if err != nil {
		return err
	}

	if sl.PoleID != v.Pole.ID {
		return errNotFound
	}

	done, err := s.signUpOthers(ctx, act(r), v, sl, []*store.Person{who})
	if err != nil {
		return err
	}

	s.flashPlaced(w, v, done)

	return s.redirectTab(w, r, v.Pole, "members")
}

func (s *Server) manageSlotRemoveVolunteer(w http.ResponseWriter, r *http.Request) error {
	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	who, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	if err := s.takeOff(r.Context(), act(r), v, sl, who); err != nil {
		return err
	}

	s.flash(w, "flash.vol.volunteer_removed", who.Label())
	http.Redirect(w, r, safeRedirect(r.FormValue("back"), sl.ManageURL()+"#volunteers"), http.StatusSeeOther)

	return nil
}
