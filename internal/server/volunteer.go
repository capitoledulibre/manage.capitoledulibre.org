package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"manage/internal/store"
)

// The volunteer space: editions, poles, slots and a volunteer's own
// planning, under /volunteers/. Every edition-level page sits below
// /volunteers/{event}/ so that its routes never overlap the few site-wide
// ones (/volunteers/login, /volunteers/profile…).

func (s *Server) volunteerRoutes(mux *http.ServeMux) {
	mux.Handle("GET /volunteers/login", s.wrap(s.volunteerLogin))
	mux.Handle("GET /calendar/{token}", s.wrap(s.calendarFeed))
	mux.Handle("GET /volunteer-images/{file}", s.wrap(s.volunteerImage))

	vol := func(pattern string, h handlerFunc) { mux.Handle(pattern, s.wrap(s.requireVolunteer(h))) }

	vol("GET /volunteers/{$}", s.volunteerHome)
	vol("GET /volunteers/welcome", s.volunteerWelcome)
	vol("GET /volunteers/profile", s.volunteerProfile)
	vol("POST /volunteers/profile", s.volunteerProfileSave)
	vol("POST /volunteers/profile/delete", s.volunteerLeave)

	vol("GET /volunteers/{event}", s.eventHome)
	vol("GET /volunteers/{event}/slots", s.openSlots)
	vol("GET /volunteers/{event}/me", s.myPlanning)
	vol("GET /volunteers/{event}/me.ics", s.myPlanningICS)
	vol("POST /volunteers/{event}/me/calendar/reset", s.calendarReset)

	vol("GET /volunteers/{event}/{pole}", s.polePage)
	vol("GET /volunteers/{event}/{pole}/join", s.poleInvite)
	vol("POST /volunteers/{event}/{pole}/join", s.poleJoin)
	vol("POST /volunteers/{event}/{pole}/leave", s.poleLeave)

	vol("GET /volunteers/{event}/{pole}/slots/{slot}", s.slotPage)
	vol("POST /volunteers/{event}/{pole}/slots/{slot}/signup", s.slotSignup)
	vol("POST /volunteers/{event}/{pole}/slots/{slot}/unsignup", s.slotUnsignup)
	vol("POST /volunteers/{event}/{pole}/slots/{slot}/waitlist", s.slotWaitlistJoin)
	vol("POST /volunteers/{event}/{pole}/slots/{slot}/waitlist/delete", s.slotWaitlistLeave)
	vol("POST /volunteers/{event}/{pole}/tasks/{task}/toggle", s.taskToggle)

	s.poleManageRoutes(mux)
	s.coordinationRoutes(mux)
}

// requireVolunteer lets in a signed-in person. A staff member signed in
// through the staff login only is their own person here: the staff are
// volunteers too. Anyone else goes to the volunteer login, and comes back.
func (s *Server) requireVolunteer(h handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		c := rc(r)

		if c.Person == nil && c.Admin != nil {
			p, err := s.store.GetPerson(r.Context(), c.Admin.PersonID)
			if err != nil {
				return err
			}

			c.Person = p
		}

		if c.Person == nil {
			to := "/volunteers/login?next=" + url.QueryEscape(r.URL.RequestURI())
			if isHX(r) {
				w.Header().Set("HX-Redirect", to)
				w.WriteHeader(http.StatusUnauthorized)

				return nil
			}

			http.Redirect(w, r, to, http.StatusSeeOther)

			return nil
		}

		return h(w, r)
	}
}

func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// --- rights ---------------------------------------------------------------

// coordinates tells whether the person runs the volunteers of an edition:
// the staff, and the edition's coordinators.
func (s *Server) coordinates(r *http.Request, eventID int64) (bool, error) {
	c := rc(r)
	if c.Admin != nil {
		return true, nil
	}

	return s.store.IsEventCoordinator(r.Context(), eventID, c.Person.ID)
}

// managesPole tells whether the person runs a pole: its managers, and
// whoever coordinates its edition.
func (s *Server) managesPole(r *http.Request, p *store.Pole) (bool, error) {
	if ok, err := s.coordinates(r, p.EventID); ok || err != nil {
		return ok, err
	}

	return s.store.IsPoleManager(r.Context(), p.ID, rc(r).Person.ID)
}

// --- loading what a path names ---------------------------------------------

// volunteerEvent loads the edition of the path. A draft or archived
// edition is only shown to the people preparing it: its coordinators and
// pole managers, and the staff.
func (s *Server) volunteerEvent(r *http.Request) (*store.Event, error) {
	ctx := r.Context()

	e, err := s.store.GetEventBySlug(ctx, r.PathValue("event"))
	if err != nil {
		return nil, err
	}

	if e.VisibleToVolunteers() {
		return e, nil
	}

	if ok, err := s.coordinates(r, e.ID); ok || err != nil {
		return e, err
	}

	managed, err := s.store.ManagedPoleIDs(ctx, e.ID, rc(r).Person.ID)
	if err != nil {
		return nil, err
	}

	if len(managed) == 0 {
		return nil, errNotFound
	}

	return e, nil
}

// poleView is what a page knows of a pole and of the person looking at it.
type poleView struct {
	Event  *store.Event
	Pole   *store.Pole
	Member bool
	Manage bool
	Locked bool
}

// volunteerPole loads the pole of the path and the person's place in it. A
// pole of an edition hidden from volunteers is only shown to whoever runs
// it, and a restricted pole only to its members and to whoever runs it.
func (s *Server) volunteerPole(r *http.Request) (*poleView, error) {
	ctx := r.Context()

	e, err := s.store.GetEventBySlug(ctx, r.PathValue("event"))
	if err != nil {
		return nil, err
	}

	p, err := s.store.GetPoleBySlug(ctx, e.ID, r.PathValue("pole"))
	if err != nil {
		return nil, err
	}

	v := &poleView{Event: e, Pole: p, Locked: e.SignupsLocked(p.FreezeAt, time.Now())}

	if v.Manage, err = s.managesPole(r, p); err != nil {
		return nil, err
	}

	if v.Member, err = s.store.IsPoleMember(ctx, p.ID, rc(r).Person.ID); err != nil {
		return nil, err
	}

	if !v.Manage && (!e.VisibleToVolunteers() || (p.AccessMode == store.PoleRestricted && !v.Member)) {
		return nil, errNotFound
	}

	return v, nil
}

// currentVolunteerEvent is the edition the volunteer space opens on: the
// one taking sign-ups. Before any opens, the staff and the coordinators see
// the one they are preparing.
func (s *Server) currentVolunteerEvent(r *http.Request) (*store.Event, error) {
	ctx := r.Context()

	e, err := s.store.CurrentVolunteerEvent(ctx)
	if !errors.Is(err, store.ErrNotFound) {
		return e, err
	}

	if rc(r).Admin != nil {
		return s.store.CurrentEvent(ctx)
	}

	coordinated, err := s.store.ListCoordinatedEvents(ctx, rc(r).Person.ID)
	if err != nil {
		return nil, err
	}

	if len(coordinated) == 0 {
		return nil, store.ErrNotFound
	}

	return coordinated[0], nil
}

// --- edition pages ----------------------------------------------------------

// volunteerHome opens the edition of the moment.
func (s *Server) volunteerHome(w http.ResponseWriter, r *http.Request) error {
	e, err := s.currentVolunteerEvent(r)
	if errors.Is(err, store.ErrNotFound) {
		return s.render(w, r, "volunteer/no_event", s.page(r, "vol.no_event.title"))
	}

	if err != nil {
		return err
	}

	http.Redirect(w, r, "/volunteers/"+e.Slug, http.StatusSeeOther)

	return nil
}

// poleCard is a pole on the edition's home page.
type poleCard struct {
	Pole     *store.Pole
	Coverage *store.PoleCoverage
	Member   bool
	Manage   bool
	Missing  int
}

func (s *Server) eventHome(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	e, err := s.volunteerEvent(r)
	if err != nil {
		return err
	}

	coverage, err := s.store.ListPoleCoverage(ctx, e.ID)
	if err != nil {
		return err
	}

	members, err := s.store.MemberPoleIDs(ctx, e.ID, person.ID)
	if err != nil {
		return err
	}

	managed, err := s.store.ManagedPoleIDs(ctx, e.ID, person.ID)
	if err != nil {
		return err
	}

	coordinator, err := s.coordinates(r, e.ID)
	if err != nil {
		return err
	}

	var cards []*poleCard

	for _, c := range coverage {
		id := c.Pole.ID
		// Restricted poles stay invisible to the people who are not in them.
		if c.Pole.AccessMode == store.PoleRestricted && !members[id] && !managed[id] && !coordinator {
			continue
		}

		cards = append(cards, &poleCard{Pole: c.Pole, Coverage: c, Member: members[id],
			Manage: coordinator || managed[id], Missing: max(c.Capacity-c.Filled, 0)})
	}

	mine, err := s.store.ListPersonSlots(ctx, e.ID, person.ID)
	if err != nil {
		return err
	}

	var upcoming []*store.Slot

	for _, sl := range mine {
		if sl.EndsAt.After(time.Now()) && len(upcoming) < 3 {
			upcoming = append(upcoming, sl)
		}
	}

	messages, err := s.store.ListEventMessages(ctx, e.ID)
	if err != nil {
		return err
	}

	waiting, err := s.upcomingWaitlist(r, e.ID)
	if err != nil {
		return err
	}

	prev, returning := s.returningPoles(r, e, cards)

	p := s.page(r, "vol.home.title")
	p.D = map[string]any{
		"Event": e, "Poles": cards, "Messages": messages, "MySlots": mine, "Upcoming": upcoming,
		"Joined": len(members) > 0, "Waiting": waiting, "PrevEvent": prev, "Returning": returning,
		"Coordinator": coordinator, "FirstName": firstName(person.Name, person.Email),
	}

	return s.render(w, r, "volunteer/home", p)
}

// returningPoles finds, for a volunteer who has joined nothing yet this
// time, the poles they were in at the last edition they took part in. Coming
// back once a year, most people want the same team again. Suggestions stop
// at the first join: from then on the volunteer has found their way.
func (s *Server) returningPoles(r *http.Request, e *store.Event, cards []*poleCard) (*store.Event, []*poleCard) {
	for _, c := range cards {
		if c.Member && !c.Pole.IsDefault {
			return nil, nil
		}
	}

	person := rc(r).Person

	prev, slugs, err := s.store.PreviousPoleSlugs(r.Context(), e.ID, person.ID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("previous poles", "person", person.UID, "err", err)
		}

		return nil, nil
	}

	var out []*poleCard

	for _, slug := range slugs {
		for _, c := range cards {
			if c.Pole.Slug == slug && !c.Pole.IsDefault {
				out = append(out, c)
			}
		}
	}

	if len(out) == 0 {
		return nil, nil
	}

	return prev, out
}

// upcomingWaitlist returns the slots still to come the person waits for.
func (s *Server) upcomingWaitlist(r *http.Request, eventID int64) ([]*store.Slot, error) {
	all, err := s.store.ListPersonWaitlist(r.Context(), eventID, rc(r).Person.ID)
	if err != nil {
		return nil, err
	}

	var out []*store.Slot

	for _, sl := range all {
		if sl.StartsAt.After(time.Now()) {
			out = append(out, sl)
		}
	}

	return out, nil
}

// slotDay is the slots of one day.
type slotDay struct {
	Day   string
	Slots []*store.Slot
}

func groupByDay(slots []*store.Slot) []*slotDay {
	var out []*slotDay

	for _, sl := range slots {
		key := dayKey(sl.StartsAt)
		if len(out) == 0 || out[len(out)-1].Day != key {
			out = append(out, &slotDay{Day: key})
		}

		out[len(out)-1].Slots = append(out[len(out)-1].Slots, sl)
	}

	return out
}

// openSlots answers "where am I needed?": many volunteers want to help on a
// given day more than in a given pole. Only slots the volunteer can take
// are listed: poles they belong to, and public poles, which a sign-up joins
// on the way.
func (s *Server) openSlots(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	e, err := s.volunteerEvent(r)
	if err != nil {
		return err
	}

	poles, err := s.store.ListPoles(ctx, e.ID)
	if err != nil {
		return err
	}

	members, err := s.store.MemberPoleIDs(ctx, e.ID, person.ID)
	if err != nil {
		return err
	}

	now := time.Now()
	takeable := map[int64]*store.Pole{}

	for _, p := range poles {
		if (members[p.ID] || p.IsOpen()) && !e.SignupsLocked(p.FreezeAt, now) {
			takeable[p.ID] = p
		}
	}

	slots, err := s.store.ListOpenSlots(ctx, e.ID, person.ID, now)
	if err != nil {
		return err
	}

	type card struct {
		Slot   *store.Slot
		Pole   *store.Pole
		Member bool
	}

	type day struct {
		Day   string
		Cards []card
	}

	var (
		days    []*day
		dayKeys []string
	)

	filter := r.URL.Query().Get("day")

	for _, sl := range slots {
		p, ok := takeable[sl.PoleID]
		if !ok {
			continue
		}

		key := dayKey(sl.StartsAt)
		if len(dayKeys) == 0 || dayKeys[len(dayKeys)-1] != key {
			dayKeys = append(dayKeys, key)
		}

		if filter != "" && key != filter {
			continue
		}

		if len(days) == 0 || days[len(days)-1].Day != key {
			days = append(days, &day{Day: key})
		}

		days[len(days)-1].Cards = append(days[len(days)-1].Cards, card{Slot: sl, Pole: p, Member: members[p.ID]})
	}

	back := "/volunteers/" + e.Slug + "/slots"
	if filter != "" {
		back += "?day=" + url.QueryEscape(filter)
	}

	p := s.page(r, "vol.open.title")
	p.D = map[string]any{"Event": e, "Days": days, "DayKeys": dayKeys, "Filter": filter, "Back": back,
		"Locked": e.VolunteerStatus != store.VolunteersOpen}

	return s.render(w, r, "volunteer/open_slots", p)
}

func (s *Server) myPlanning(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	e, err := s.volunteerEvent(r)
	if err != nil {
		return err
	}

	slots, err := s.store.ListPersonSlots(ctx, e.ID, person.ID)
	if err != nil {
		return err
	}

	waiting, err := s.upcomingWaitlist(r, e.ID)
	if err != nil {
		return err
	}

	// The subscription secret is created on the first visit of this page,
	// so a person who never opens it never gets one.
	token, err := s.store.CalendarToken(ctx, person.ID)
	if err != nil {
		return err
	}

	p := s.page(r, "vol.me.title")
	p.D = map[string]any{"Event": e, "Days": groupByDay(slots), "Today": dayKey(time.Now()), "Waiting": waiting,
		"Count": len(slots), "CalendarURL": s.cfg.BaseURL + "/calendar/" + token + ".ics"}

	return s.render(w, r, "volunteer/me", p)
}

// calendarFor renders the slots of a person in an edition as an iCalendar
// document. UIDs are stable per slot and person, so a calendar that reads
// the feed again updates its entries instead of duplicating them.
func (s *Server) calendarFor(r *http.Request, person *store.Person, e *store.Event) (string, error) {
	slots, err := s.store.ListPersonSlots(r.Context(), e.ID, person.ID)
	if err != nil {
		return "", err
	}

	esc := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`).Replace

	var b strings.Builder

	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//manage//volunteers//FR\r\nCALSCALE:GREGORIAN\r\n")
	fmt.Fprintf(&b, "X-WR-CALNAME:%s\r\n", esc(e.Name))
	// Subscribed calendars should read the feed again regularly.
	b.WriteString("X-PUBLISHED-TTL:PT1H\r\nREFRESH-INTERVAL;VALUE=DURATION:PT1H\r\n")

	stamp := time.Now().UTC().Format("20060102T150405Z")

	for _, sl := range slots {
		summary := sl.PoleName
		if sl.Title != "" {
			summary += " – " + sl.Title
		}

		b.WriteString("BEGIN:VEVENT\r\n")
		fmt.Fprintf(&b, "UID:%s-%s@manage\r\nDTSTAMP:%s\r\n", sl.UID, person.UID, stamp)
		fmt.Fprintf(&b, "DTSTART:%s\r\nDTEND:%s\r\n", sl.StartsAt.UTC().Format("20060102T150405Z"), sl.EndsAt.UTC().Format("20060102T150405Z"))
		fmt.Fprintf(&b, "SUMMARY:%s\r\n", esc(summary))

		if sl.LocationName != "" {
			fmt.Fprintf(&b, "LOCATION:%s\r\n", esc(sl.LocationName))
		}

		fmt.Fprintf(&b, "URL:%s%s\r\n", s.cfg.BaseURL, sl.URL())
		b.WriteString("END:VEVENT\r\n")
	}

	b.WriteString("END:VCALENDAR\r\n")

	return b.String(), nil
}

const emptyCalendar = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//manage//volunteers//FR\r\nEND:VCALENDAR\r\n"

func writeCalendar(w http.ResponseWriter, body string) error {
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="volunteering.ics"`)
	_, err := w.Write([]byte(body))

	return err
}

// myPlanningICS downloads the planning once.
func (s *Server) myPlanningICS(w http.ResponseWriter, r *http.Request) error {
	e, err := s.volunteerEvent(r)
	if err != nil {
		return err
	}

	body, err := s.calendarFor(r, rc(r).Person, e)
	if err != nil {
		return err
	}

	return writeCalendar(w, body)
}

// calendarFeed is the subscription endpoint. It carries its own secret
// instead of a session, because a calendar application polls it on its own:
// a downloaded file goes stale the moment a manager moves a slot.
func (s *Server) calendarFeed(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	token, ok := strings.CutSuffix(r.PathValue("token"), ".ics")
	if !ok {
		return errNotFound
	}

	person, err := s.store.PersonByCalendarToken(ctx, token)
	if err != nil {
		return err
	}

	e, err := s.store.CurrentVolunteerEvent(ctx)
	if errors.Is(err, store.ErrNotFound) {
		// No edition running: a valid empty calendar keeps the subscription
		// alive between two editions.
		return writeCalendar(w, emptyCalendar)
	}

	if err != nil {
		return err
	}

	body, err := s.calendarFor(r, person, e)
	if err != nil {
		return err
	}

	return writeCalendar(w, body)
}

// calendarReset issues a new subscription secret, which cuts off every
// calendar subscribed with the old one.
func (s *Server) calendarReset(w http.ResponseWriter, r *http.Request) error {
	e, err := s.volunteerEvent(r)
	if err != nil {
		return err
	}

	if _, err := s.store.ResetCalendarToken(r.Context(), rc(r).Person.ID); err != nil {
		return err
	}

	s.flash(w, "flash.vol.calendar_reset")
	http.Redirect(w, r, "/volunteers/"+e.Slug+"/me", http.StatusSeeOther)

	return nil
}

// --- profile -----------------------------------------------------------------

// volunteerWelcome is the first screen of a new person: their name, the
// phone as an option, then on to where they were heading.
func (s *Server) volunteerWelcome(w http.ResponseWriter, r *http.Request) error {
	next := safeRedirect(r.URL.Query().Get("next"), "/volunteers/")

	if rc(r).Person.Name != "" {
		http.Redirect(w, r, next, http.StatusSeeOther)

		return nil
	}

	p := s.page(r, "vol.welcome.title")
	p.D = s.loginContext(r, next)

	return s.render(w, r, "volunteer/welcome", p)
}

func (s *Server) volunteerProfile(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c := rc(r)

	manager, err := s.store.ManagesAnyPole(ctx, c.Person.ID)
	if err != nil {
		return err
	}

	// A partner contact or a staff member keeps their account when they
	// stop volunteering.
	contacts, err := s.store.ManagersByEmail(ctx, c.Person.Email)
	if err != nil {
		return err
	}

	p := s.page(r, "vol.profile.title")
	p.D = map[string]any{"Next": r.URL.Query().Get("next"), "PoleManager": manager, "OtherRoles": len(contacts) > 0 || c.Admin != nil}

	return s.render(w, r, "volunteer/profile", p)
}

func (s *Server) volunteerProfileSave(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person
	welcome := r.FormValue("welcome") == "1"
	next := safeRedirect(r.FormValue("next"), "/volunteers/")

	name := strings.TrimSpace(r.FormValue("name"))
	phone := strings.TrimSpace(r.FormValue("phone"))

	if name == "" || len(name) > 200 || len(phone) > 40 {
		if welcome {
			p := s.page(r, "vol.welcome.title")
			d := s.loginContext(r, next)
			d["Error"] = "vol.profile.name_required"
			p.D = d

			return s.render(w, r, "volunteer/welcome", p)
		}

		manager, err := s.store.ManagesAnyPole(ctx, person.ID)
		if err != nil {
			return err
		}

		p := s.page(r, "vol.profile.title")
		p.D = map[string]any{"Next": r.FormValue("next"), "PoleManager": manager, "Error": "vol.profile.name_required"}

		return s.render(w, r, "volunteer/profile", p)
	}

	if err := s.store.UpdateVolunteerProfile(ctx, person.ID, name, phone); err != nil {
		return err
	}

	// Only the profile page of a pole manager carries the choice; the
	// welcome form leaves it as it is.
	if r.FormValue("share_phone_shown") == "1" {
		if err := s.store.SetSharePhone(ctx, person.ID, r.FormValue("share_phone") == "1"); err != nil {
			return err
		}
	}

	if !welcome {
		s.flash(w, "flash.vol.profile_saved")
	}

	http.Redirect(w, r, next, http.StatusSeeOther)

	return nil
}

// volunteerLeave ends the volunteer side of a person. Someone who is only
// a volunteer leaves altogether, logged out; a partner contact or a staff
// member keeps their account for that.
func (s *Server) volunteerLeave(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c := rc(r)
	person := c.Person

	if r.FormValue("confirm") != "yes" {
		return errBadInput
	}

	ids, deleted, err := s.store.LeaveVolunteering(ctx, person.ID)
	if err != nil {
		return err
	}

	// Every place freed goes to the waiting list.
	for _, id := range ids {
		// The slot is still there; only the sign-up went.
		sl, err := s.store.GetSlot(ctx, id, 0)
		if err != nil {
			continue
		}

		if p, err := s.store.GetPole(ctx, sl.PoleID); err == nil {
			s.announceFreePlace(r, p, sl.ID)
		}
	}

	if !deleted {
		s.flash(w, "flash.vol.left_volunteering")
		http.Redirect(w, r, "/", http.StatusSeeOther)

		return nil
	}

	s.dropFile(r, person.AvatarFile)
	s.clearCookie(w, personCookie)
	s.flash(w, "flash.vol.account_deleted")
	http.Redirect(w, r, "/volunteers/login", http.StatusSeeOther)

	return nil
}

// --- login --------------------------------------------------------------------

// loginContext names what a visitor was heading for before being asked to
// sign in. An invitation link is the most common first contact with the
// volunteer space, and a bare form gave no clue about which pole or edition
// was involved.
func (s *Server) loginContext(r *http.Request, next string) map[string]any {
	ctx := r.Context()
	d := map[string]any{"Next": next}

	path, _, _ := strings.Cut(next, "?")
	parts := strings.Split(strings.Trim(path, "/"), "/")

	if len(parts) >= 3 && parts[0] == "volunteers" {
		if e, err := s.store.GetEventBySlug(ctx, parts[1]); err == nil && e.VisibleToVolunteers() {
			if p, err := s.store.GetPoleBySlug(ctx, e.ID, parts[2]); err == nil && p.AccessMode != store.PoleRestricted && !p.IsDefault {
				managers, err := s.store.ListPoleManagers(ctx, p.ID)
				if err != nil {
					slog.Error("pole managers", "pole", p.UID, "err", err)
				}

				d["Event"], d["Pole"], d["Managers"] = e, p, named(managers)

				return d
			}
		}
	}

	// No pole in sight: name the edition taking sign-ups, so a visitor
	// knows what they are signing up for.
	if e, err := s.store.CurrentVolunteerEvent(ctx); err == nil {
		d["Event"] = e
	}

	return d
}

// volunteerLogin is the login form of the volunteer space. Signing in from
// it creates the account of a newcomer.
func (s *Server) volunteerLogin(w http.ResponseWriter, r *http.Request) error {
	next := safeRedirect(r.URL.Query().Get("next"), "/volunteers/")

	if rc(r).Person != nil {
		http.Redirect(w, r, next, http.StatusSeeOther)

		return nil
	}

	p := s.page(r, "vol.login.title")
	d := s.loginContext(r, next)
	d["Email"] = r.URL.Query().Get("email")
	p.D = d

	return s.render(w, r, "volunteer/login", p)
}

// named keeps the people who gave a name. Volunteer pages list pole
// managers by name; one who never filled theirs in would otherwise be shown
// by e-mail address to every volunteer and, on the login page reached
// through an invitation link, to anyone holding the link.
func named(people []*store.Person) []*store.Person {
	var out []*store.Person

	for _, p := range people {
		if strings.TrimSpace(p.Name) != "" {
			out = append(out, p)
		}
	}

	return out
}

// volunteerImage serves the images of poles and the logos of editions.
// They are not secret (a logo is on the event's website, a pole picture is
// decoration) but only files used as such are served: not a partner's
// document, nor a person's avatar.
func (s *Server) volunteerImage(w http.ResponseWriter, r *http.Request) error {
	file := r.PathValue("file")

	ok, err := s.store.VolunteerImageInUse(r.Context(), file)
	if err != nil {
		return err
	}

	if !ok {
		return errNotFound
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	s.files.Serve(w, r, file, "")

	return nil
}

// --- navigation ---------------------------------------------------------------

// volunteerNav is the menu of the volunteer space: the edition being
// looked at, and the poles the person runs in it.
type volunteerNav struct {
	Event       *store.Event
	Poles       []*store.Pole
	Coordinator bool
}

func (s *Server) volunteerNav(r *http.Request) *volunteerNav {
	nav := &volunteerNav{}

	var err error

	if slug := r.PathValue("event"); slug != "" {
		nav.Event, err = s.store.GetEventBySlug(r.Context(), slug)
	} else {
		nav.Event, err = s.currentVolunteerEvent(r)
	}

	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("volunteer menu", "err", err)
		}

		return nav
	}

	if nav.Poles, err = s.store.ListManagedPoles(r.Context(), nav.Event.ID, rc(r).Person.ID); err != nil {
		slog.Error("volunteer menu", "err", err)
	}

	if nav.Coordinator, err = s.coordinates(r, nav.Event.ID); err != nil {
		slog.Error("volunteer menu", "err", err)
	}

	return nav
}

// space is another space of the signed-in person, linked from their menu.
type space struct {
	Label string // i18n key
	URL   string
}

// spaces lists the spaces of the signed-in person other than current: a
// partner contact who volunteers, or the other way round, goes from one to
// the other through their menu.
func (s *Server) spaces(r *http.Request, current string) []space {
	c := rc(r)
	if c.Person == nil {
		return nil
	}

	var out []space

	if current != "partner" {
		if managers, err := s.store.ManagersByEmail(r.Context(), c.Person.Email); err == nil && len(managers) > 0 {
			out = append(out, space{Label: "space.partners", URL: "/partners/"})
		}
	}

	if current != "volunteer" {
		if ok, err := s.store.IsVolunteer(r.Context(), c.Person.ID); err == nil && ok {
			out = append(out, space{Label: "space.volunteers", URL: "/volunteers/"})
		}
	}

	return out
}
