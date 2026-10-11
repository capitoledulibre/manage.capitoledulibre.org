package server

import (
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"manage/internal/files"
	"manage/internal/i18n"
	"manage/internal/store"
)

// Pole management, for the people who run a pole: its managers, the
// coordinators of its edition and the staff. Everything sits below
// /volunteers/{event}/{pole}/manage.

const maxPoleImageSize = 2 << 20

func (s *Server) poleManageRoutes(mux *http.ServeMux) {
	m := func(pattern string, h handlerFunc) {
		mux.Handle(pattern, s.wrap(s.requireVolunteer(h)))
	}

	const base = "/volunteers/{event}/{pole}/manage"

	m("GET "+base, s.managePole)
	m("POST "+base, s.managePoleSave)
	m("POST "+base+"/key", s.managePoleKey)
	m("POST "+base+"/image", s.managePoleImage)
	m("POST "+base+"/image/delete", s.managePoleImageDelete)
	m("GET "+base+"/people", s.managePeopleSearch)
	m("POST "+base+"/members", s.manageMemberAdd)
	m("POST "+base+"/members/{person}/delete", s.manageMemberRemove)
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
	m("POST "+base+"/slots/{slot}/tasks", s.manageTaskAdd)
	m("POST "+base+"/slots/{slot}/messages", s.manageSlotMessage)
	m("POST "+base+"/slots/{slot}/volunteers", s.manageSlotAddVolunteer)
	m("POST "+base+"/slots/{slot}/volunteers/{person}/delete", s.manageSlotRemoveVolunteer)
	m("POST "+base+"/tasks/{task}/delete", s.manageTaskDelete)
	m("POST "+base+"/days/copy", s.manageDayCopy)

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

func (s *Server) redirectTab(w http.ResponseWriter, r *http.Request, p *store.Pole, tab string) error {
	to := p.ManageURL()
	if tab != "" {
		to += "?tab=" + tab
	}

	http.Redirect(w, r, to, http.StatusSeeOther)

	return nil
}

// --- the management page ----------------------------------------------------

func (s *Server) managePole(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	slots, err := s.store.ListSlots(ctx, v.Pole.ID, 0)
	if err != nil {
		return err
	}

	volunteers := map[int64][]*store.Person{}

	var capacity, filled, short int

	urgent := false

	for _, sl := range slots {
		if volunteers[sl.ID], err = s.store.ListSlotVolunteers(ctx, sl.ID); err != nil {
			return err
		}

		capacity += sl.Capacity
		filled += sl.Filled

		if !sl.IsFull() {
			short++
		}

		urgent = urgent || (!sl.IsFull() && isLate(sl))
	}

	locations, err := s.store.ListLocations(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	messages, err := s.store.ListPoleMessages(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	members, err := s.store.ListPoleMembers(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	managers, err := s.store.ListPoleManagers(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	// A slot with people waiting is a slot worth enlarging: show it.
	waiting, err := s.store.WaitlistCounts(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	coordinator, err := s.coordinates(r, v.Event.ID)
	if err != nil {
		return err
	}

	tab := r.URL.Query().Get("tab")
	if tab != "members" && tab != "messages" && tab != "settings" && tab != "history" {
		tab = "planning"
	}

	var history []*store.LogEntry

	if tab == "history" {
		if history, err = s.store.ListPoleLog(ctx, v.Pole.ID); err != nil {
			return err
		}
	}

	p := s.page(r, "vol.manage.title")
	p.TitleText = i18n.T(p.Lang, "vol.manage.title_pole", v.Pole.Name)
	p.D = map[string]any{
		"V": v, "Tab": tab, "Days": groupByDay(slots), "Volunteers": volunteers, "Waiting": waiting, "Urgent": urgent,
		"Locations": locations, "Messages": messages, "Members": members, "Managers": managers,
		"Coordinator": coordinator, "CanAddMembers": coordinator || v.Pole.ManagersCanAdd(),
		"SlotCount": len(slots), "Capacity": capacity, "Filled": filled, "Short": short,
		"InviteURL": s.cfg.BaseURL + v.Pole.URL() + "/join?key=" + url.QueryEscape(v.Pole.AccessKey),
		"Modes":     store.PoleModes, "MaxImage": maxPoleImageSize, "History": history,
	}

	return s.render(w, r, "volunteer/manage", p)
}

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

	p := v.Pole

	if name := strings.TrimSpace(r.FormValue("name")); name != "" {
		p.Name = name
	}

	if r.Form.Has("slug") && !p.IsDefault {
		p.Slug = strings.TrimSpace(r.FormValue("slug"))
	}

	p.Description = strings.TrimSpace(r.FormValue("description"))
	p.Icon = strings.TrimSpace(r.FormValue("icon"))
	p.DefaultTasks = strings.TrimSpace(r.FormValue("default_tasks"))
	p.FreezeAt = formTimePtr(r, "freeze_at")

	if mode := r.FormValue("access_mode"); mode != "" && !p.IsDefault && store.ValidPoleMode(mode) {
		// Only the coordination makes a pole restricted: it hides the pole
		// from everyone else.
		coordinator, err := s.coordinates(r, v.Event.ID)
		if err != nil {
			return err
		}

		if mode != store.PoleRestricted || coordinator || p.AccessMode == store.PoleRestricted {
			p.AccessMode = mode
		}
	}

	if err := s.store.UpdatePole(r.Context(), p); err != nil {
		return err
	}

	s.flash(w, "flash.vol.pole_saved")

	return s.redirectTab(w, r, p, "settings")
}

func (s *Server) managePoleKey(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if _, err := s.store.RegenerateAccessKey(r.Context(), v.Pole.ID); err != nil {
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

	if err := s.store.SetPoleImage(r.Context(), v.Pole.ID, name); err != nil {
		return err
	}

	s.dropFile(r, v.Pole.ImageFile)
	s.flash(w, "flash.vol.image_saved")

	return s.redirectTab(w, r, v.Pole, "settings")
}

func (s *Server) managePoleImageDelete(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if err := s.store.SetPoleImage(r.Context(), v.Pole.ID, ""); err != nil {
		return err
	}

	s.dropFile(r, v.Pole.ImageFile)

	return s.redirectTab(w, r, v.Pole, "settings")
}

// dropFile removes a stored image once nothing uses it any more. Copying a
// pole or an edition makes two rows share one file, so clearing one must
// not take the bytes away from the other.
func (s *Server) dropFile(r *http.Request, file string) {
	if file == "" {
		return
	}

	used, err := s.store.FileInUse(r.Context(), file)
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

	return map[string]any{"Lang": rc(r).Lang, "Pole": v.Pole, "People": people, "Q": q}, nil
}

// manageMemberAdd adds people to a pole, by address: one per line, bare or
// as "Name <address>". Those without an account get one.
func (s *Server) manageMemberAdd(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	coordinator, err := s.coordinates(r, v.Event.ID)
	if err != nil {
		return err
	}

	if !v.Pole.ManagersCanAdd() && !coordinator {
		return errForbidden
	}

	added := 0

	for _, raw := range strings.FieldsFunc(r.FormValue("emails"), func(c rune) bool { return c == '\n' || c == ',' || c == ';' }) {
		addr, err := mail.ParseAddress(strings.TrimSpace(raw))
		if err != nil || !validEmail(store.NormalizeEmail(addr.Address)) {
			continue
		}

		p, err := s.store.EnsureVolunteer(ctx, addr.Address, strings.TrimSpace(addr.Name), "", rc(r).Lang)
		if err != nil {
			return err
		}

		if err := s.store.JoinPole(ctx, v.Pole.ID, p.ID); err != nil {
			return err
		}

		s.logVol(r, logPole(store.LogMemberAdded, v.Pole, p))

		added++
	}

	if isHX(r) {
		d, err := s.memberSearch(r, v, strings.TrimSpace(r.FormValue("q")))
		if err != nil {
			return err
		}

		if d["Members"], err = s.store.ListPoleMembers(ctx, v.Pole.ID); err != nil {
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
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if v.Pole.IsDefault {
		s.flash(w, "vol.err.default_pole")

		return s.redirectTab(w, r, v.Pole, "members")
	}

	who, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	// Read the sign-ups before they go, so the e-mail says what is lost.
	freed, err := s.store.PersonSlotsInPole(ctx, v.Pole.ID, who.ID)
	if err != nil {
		return err
	}

	if err := s.store.LeavePole(ctx, v.Pole.ID, who.ID); err != nil {
		return err
	}

	removed := logPole(store.LogMemberRemoved, v.Pole, who)
	removed.Count = len(freed)
	s.logVol(r, removed)

	s.notifyPoleRemoved(who, v.Pole, len(freed))

	for _, sl := range freed {
		s.announceFreePlace(r, v.Pole, sl.ID)
	}

	s.flash(w, "flash.vol.member_removed", who.Label())

	return s.redirectTab(w, r, v.Pole, "members")
}

// --- pole managers: the coordination's call -------------------------------

func (s *Server) manageManagerAdd(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if ok, err := s.coordinates(r, v.Event.ID); err != nil || !ok {
		return cmpErr(err, errForbidden)
	}

	email := store.NormalizeEmail(r.FormValue("email"))
	if !validEmail(email) {
		s.flash(w, "flash.invalid_email")

		return s.redirectTab(w, r, v.Pole, "members")
	}

	p, err := s.store.EnsureVolunteer(ctx, email, "", "", rc(r).Lang)
	if err != nil {
		return err
	}

	if err := s.store.AddPoleManager(ctx, v.Pole.ID, p.ID); err != nil {
		return err
	}

	s.logVol(r, logPole(store.LogLeadAdded, v.Pole, p))
	s.flash(w, "flash.vol.manager_added", p.Label())

	return s.redirectTab(w, r, v.Pole, "members")
}

func (s *Server) manageManagerRemove(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if ok, err := s.coordinates(r, v.Event.ID); err != nil || !ok {
		return cmpErr(err, errForbidden)
	}

	who, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	if err := s.store.RemovePoleManager(r.Context(), v.Pole.ID, who.ID); err != nil {
		return err
	}

	s.logVol(r, logPole(store.LogLeadRemoved, v.Pole, who))

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
	for line := range strings.SplitSeq(r.FormValue("names"), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			if _, err := s.store.CreateLocation(r.Context(), v.Pole.ID, name); err != nil {
				return err
			}
		}
	}

	return s.redirectTab(w, r, v.Pole, "settings")
}

func (s *Server) manageLocationDelete(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	l, err := s.store.GetLocationByUID(r.Context(), v.Pole.ID, r.PathValue("location"))
	if err != nil {
		return err
	}

	if err := s.store.DeleteLocation(r.Context(), l.ID); err != nil {
		return err
	}

	return s.redirectTab(w, r, v.Pole, "settings")
}

// --- messages ---------------------------------------------------------------

func (s *Server) managePoleMessage(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		return s.redirectTab(w, r, v.Pole, "messages")
	}

	if err := s.store.CreateMessage(ctx, &store.Message{EventID: v.Event.ID, PoleID: v.Pole.ID, Body: body}, rc(r).Person.ID); err != nil {
		return err
	}

	if r.FormValue("notify") != "1" {
		s.flash(w, "flash.vol.message_posted")

		return s.redirectTab(w, r, v.Pole, "messages")
	}

	members, err := s.store.ListPoleMembers(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	recipients := make([]*store.Person, 0, len(members))
	for _, m := range members {
		recipients = append(recipients, m.Person)
	}

	posted := logPole(store.LogMessagePosted, v.Pole, nil)
	posted.Count = len(recipients)
	s.logVol(r, posted)

	pole := v.Pole
	s.notifyMessage(recipients, pole.EventSlug, func(lang string) string { return i18n.T(lang, "mail.vol.scope_pole", pole.Name) },
		body, s.poleURL(pole))
	s.flashCount(w, "flash.vol.message_sent", len(recipients))

	return s.redirectTab(w, r, v.Pole, "messages")
}

func (s *Server) manageSlotMessage(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	body := strings.TrimSpace(r.FormValue("body"))
	if body != "" {
		if err := s.store.CreateMessage(ctx, &store.Message{EventID: v.Event.ID, PoleID: v.Pole.ID, SlotID: sl.ID, Body: body}, rc(r).Person.ID); err != nil {
			return err
		}

		if r.FormValue("notify") == "1" {
			volunteers, err := s.store.ListSlotVolunteers(ctx, sl.ID)
			if err != nil {
				return err
			}

			s.notifyMessage(volunteers, sl.EventSlug, func(lang string) string {
				return i18n.T(lang, "mail.vol.scope_slot", slotWhen(lang, sl.StartsAt, sl.EndsAt))
			}, body, s.slotURL(sl))
			s.flashCount(w, "flash.vol.message_sent", len(volunteers))
		} else {
			s.flash(w, "flash.vol.message_posted")
		}
	}

	http.Redirect(w, r, sl.ManageURL()+"#messages", http.StatusSeeOther)

	return nil
}

// manageMessageDelete removes a notice of the pole or of one of its slots.
func (s *Server) manageMessageDelete(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	m, err := s.store.GetMessageByUID(r.Context(), v.Event.ID, r.PathValue("message"))
	if err != nil {
		return err
	}

	if m.PoleID != v.Pole.ID {
		return errNotFound
	}

	if err := s.store.DeleteMessage(r.Context(), m.ID); err != nil {
		return err
	}

	http.Redirect(w, r, safeRedirect(r.FormValue("back"), v.Pole.ManageURL()+"?tab=messages"), http.StatusSeeOther)

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

// slotFromForm reads the fields of the slot form into sl.
func (s *Server) slotFromForm(r *http.Request, v *poleView, sl *store.Slot) (string, error) {
	sl.Title = strings.TrimSpace(r.FormValue("title"))
	sl.Capacity = formInt(r, "capacity")
	sl.LocationID = 0

	if uid := r.FormValue("location"); uid != "" {
		l, err := s.store.GetLocationByUID(r.Context(), v.Pole.ID, uid)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", err
		}

		if l != nil {
			sl.LocationID, sl.LocationName = l.ID, l.Name
		}
	}

	var okStart, okEnd bool

	sl.StartsAt, okStart = formTime(r, "starts_at")
	sl.EndsAt, okEnd = formTime(r, "ends_at")

	switch {
	case !okStart || !okEnd || !sl.EndsAt.After(sl.StartsAt):
		return "vol.err.slot_times", nil
	case sl.Capacity < 1 || sl.Capacity > 500:
		return "vol.err.slot_capacity", nil
	}

	return "", nil
}

// manageSlotCreate creates a slot, or a series of them: consecutive slots
// of the same length up to a time, for one location or for each of them.
func (s *Server) manageSlotCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	sl := &store.Slot{PoleID: v.Pole.ID}

	msg, err := s.slotFromForm(r, v, sl)
	if err != nil {
		return err
	}

	outside := msg == "" && outsideEdition(v.Event, sl) && r.FormValue("outside_ok") != "yes"

	if msg != "" || outside {
		return s.renderSlotForm(w, r, &slotForm{V: v, New: true, Slot: sl, Error: msg, Outside: outside, Tasks: r.FormValue("tasks"),
			RepeatUntil: r.FormValue("repeat_until"), AllLocations: r.FormValue("all_locations") == "1"}, nil)
	}

	length := sl.EndsAt.Sub(sl.StartsAt)
	starts := []time.Time{sl.StartsAt}

	if until, ok := formTime(r, "repeat_until"); ok {
		for t := sl.StartsAt.Add(length); !t.Add(length).After(until) && len(starts) < 200; t = t.Add(length) {
			starts = append(starts, t)
		}
	}

	locIDs := []int64{sl.LocationID}

	if r.FormValue("all_locations") == "1" {
		locations, err := s.store.ListLocations(ctx, v.Pole.ID)
		if err != nil {
			return err
		}

		if len(locations) > 0 {
			locIDs = locIDs[:0]
			for _, l := range locations {
				locIDs = append(locIDs, l.ID)
			}
		}
	}

	var labels []string

	for line := range strings.SplitSeq(r.FormValue("tasks"), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			labels = append(labels, l)
		}
	}

	created := 0

	for _, locID := range locIDs {
		for _, start := range starts {
			n := &store.Slot{PoleID: v.Pole.ID, LocationID: locID, Title: sl.Title, Capacity: sl.Capacity, StartsAt: start, EndsAt: start.Add(length)}
			if err := s.store.CreateSlot(ctx, n); err != nil {
				return err
			}

			for _, label := range labels {
				if _, err := s.store.CreateTask(ctx, n.ID, label); err != nil {
					return err
				}
			}

			created++
		}
	}

	s.logSlotsCreated(r, v.Pole, created)
	s.flashCount(w, "flash.vol.slots_created", created)

	return s.redirectTab(w, r, v.Pole, "")
}

// logSlotsCreated records slots created in a pole, by hand or generated.
func (s *Server) logSlotsCreated(r *http.Request, p *store.Pole, n int) {
	if n == 0 {
		return
	}

	e := logPole(store.LogSlotsCreated, p, nil)
	e.Count = n
	s.logVol(r, e)
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
	}, nil
}

func (s *Server) manageSlotSave(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	before := *sl

	msg, err := s.slotFromForm(r, v, sl)
	if err != nil {
		return err
	}

	outside := msg == "" && outsideEdition(v.Event, sl) && !outsideEdition(v.Event, &before) && r.FormValue("outside_ok") != "yes"

	if msg != "" || outside {
		// The form comes back with what was typed.
		extra, err := s.slotEditExtras(r, v, &before)
		if err != nil {
			return err
		}

		return s.renderSlotForm(w, r, &slotForm{V: v, Slot: sl, Error: msg, Outside: outside}, extra)
	}

	if err := s.store.UpdateSlot(ctx, sl); err != nil {
		return err
	}

	after, err := s.store.GetSlot(ctx, sl.ID, 0)
	if err != nil {
		return err
	}

	notified := 0
	moved := !before.StartsAt.Equal(after.StartsAt) || !before.EndsAt.Equal(after.EndsAt) || before.LocationID != after.LocationID

	if moved {
		s.logVol(r, logSlot(store.LogSlotMoved, v.Pole, after, nil))
	} else {
		s.logVol(r, logSlot(store.LogSlotUpdated, v.Pole, after, nil))
	}

	if moved {
		volunteers, err := s.store.ListSlotVolunteers(ctx, sl.ID)
		if err != nil {
			return err
		}

		s.notifySlotChanged(volunteers, v.Pole, &before, after)
		notified = len(volunteers)
	}

	// More places on a slot that was full: the waiting list wants to know.
	if before.IsFull() && !after.IsFull() {
		s.announceFreePlace(r, v.Pole, sl.ID)
	}

	s.flashCount(w, "flash.vol.slot_saved", notified)

	return s.redirectTab(w, r, v.Pole, "")
}

func (s *Server) manageSlotDelete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	// Read who is on it before the cascade removes the sign-ups.
	volunteers, err := s.store.ListSlotVolunteers(ctx, sl.ID)
	if err != nil {
		return err
	}

	waiting, err := s.store.ListWaitlist(ctx, sl.ID)
	if err != nil {
		return err
	}

	if err := s.store.DeleteSlot(ctx, sl.ID); err != nil {
		return err
	}

	deleted := logSlot(store.LogSlotDeleted, v.Pole, sl, nil)
	deleted.Count = len(volunteers)
	s.logVol(r, deleted)

	s.notifySlotDeleted(append(volunteers, waiting...), v.Pole, sl)
	s.flashCount(w, "flash.vol.slot_deleted", len(volunteers))

	return s.redirectTab(w, r, v.Pole, "")
}

// manageDayCopy copies the slots of a day of the pole to another day: same
// hours, places, titles, places wanted and tasks, nobody signed up. A
// two-day event is often the same day twice.
func (s *Server) manageDayCopy(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	from, err1 := time.ParseInLocation(time.DateOnly, r.FormValue("from"), paris)
	to, err2 := time.ParseInLocation(time.DateOnly, r.FormValue("to"), paris)

	if err1 != nil || err2 != nil || from.Equal(to) {
		s.flash(w, "vol.err.day_copy")

		return s.redirectTab(w, r, v.Pole, "")
	}

	slots, err := s.store.ListSlots(ctx, v.Pole.ID, 0)
	if err != nil {
		return err
	}

	created := 0

	for _, sl := range slots {
		if dayKey(sl.StartsAt) != from.Format(time.DateOnly) {
			continue
		}

		// The clock time is kept across a change of daylight saving time.
		start := sl.StartsAt.In(paris)
		at := time.Date(to.Year(), to.Month(), to.Day(), start.Hour(), start.Minute(), 0, 0, paris)

		n := &store.Slot{PoleID: v.Pole.ID, LocationID: sl.LocationID, Title: sl.Title, Capacity: sl.Capacity,
			StartsAt: at, EndsAt: at.Add(sl.EndsAt.Sub(sl.StartsAt))}
		if err := s.store.CreateSlot(ctx, n); err != nil {
			return err
		}

		if err := s.store.CopyTasks(ctx, sl.ID, n.ID); err != nil {
			return err
		}

		created++
	}

	s.logSlotsCreated(r, v.Pole, created)
	s.flashCount(w, "flash.vol.day_copied", created, dayName(rc(r).Lang, to))

	return s.redirectTab(w, r, v.Pole, "")
}

// --- tasks ------------------------------------------------------------------

func (s *Server) manageTaskAdd(w http.ResponseWriter, r *http.Request) error {
	_, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	for line := range strings.SplitSeq(r.FormValue("labels"), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			if _, err := s.store.CreateTask(r.Context(), sl.ID, l); err != nil {
				return err
			}
		}
	}

	http.Redirect(w, r, sl.ManageURL()+"#tasks", http.StatusSeeOther)

	return nil
}

func (s *Server) manageTaskDelete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	task, err := s.store.GetTaskByUID(ctx, r.PathValue("task"))
	if err != nil {
		return err
	}

	sl, err := s.store.GetSlot(ctx, task.SlotID, 0)
	if err != nil {
		return err
	}

	if sl.PoleID != v.Pole.ID {
		return errNotFound
	}

	if err := s.store.DeleteTask(ctx, task.ID); err != nil {
		return err
	}

	http.Redirect(w, r, sl.ManageURL()+"#tasks", http.StatusSeeOther)

	return nil
}

// --- volunteers on a slot ---------------------------------------------------

// manageSlotAddVolunteer puts someone on a slot, even a full one. Two
// intents, two fields: picking a member of the pole (the common case), or
// deliberately inviting someone by address. A single "name or e-mail" box
// turned every typo into an account.
func (s *Server) manageSlotAddVolunteer(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, sl, err := s.managedSlot(r)
	if err != nil {
		return err
	}

	var who *store.Person

	if uid := r.FormValue("person"); uid != "" {
		if who, err = s.store.GetPersonByUID(ctx, uid); err != nil {
			return err
		}
	} else {
		email := store.NormalizeEmail(r.FormValue("email"))
		if !validEmail(email) {
			http.Redirect(w, r, sl.ManageURL()+"?error=vol.err.invite_email#volunteers", http.StatusSeeOther)

			return nil
		}

		if who, err = s.store.EnsureVolunteer(ctx, email, strings.TrimSpace(r.FormValue("name")), "", rc(r).Lang); err != nil {
			return err
		}
	}

	if err := s.store.JoinPole(ctx, v.Pole.ID, who.ID); err != nil {
		return err
	}

	if err := s.store.ForceSignup(ctx, sl.ID, who.ID); err != nil {
		return err
	}

	s.logVol(r, logSlot(store.LogSignupAdded, v.Pole, sl, who))

	s.flash(w, "flash.vol.volunteer_added", who.Label(), v.Pole.Name)
	http.Redirect(w, r, sl.ManageURL()+"#volunteers", http.StatusSeeOther)

	return nil
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

	if err := s.store.Unsignup(r.Context(), sl.ID, who.ID); err != nil {
		return err
	}

	s.logVol(r, logSlot(store.LogSignupRemoved, v.Pole, sl, who))
	s.notifySignupRemoved(who, v.Pole, sl)
	s.announceFreePlace(r, v.Pole, sl.ID)
	s.flash(w, "flash.vol.volunteer_removed", who.Label())
	http.Redirect(w, r, sl.ManageURL()+"#volunteers", http.StatusSeeOther)

	return nil
}
