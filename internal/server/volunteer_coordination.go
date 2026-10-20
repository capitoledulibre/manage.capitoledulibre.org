package server

import (
	"encoding/csv"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"manage/internal/files"
	"manage/internal/i18n"
	"manage/internal/store"
)

// The coordination of an edition's volunteers, for its coordinators and the
// staff: every pole, the global planning, the volunteer list, edition-wide
// messages and the volunteer settings. Creating, renaming or deleting an
// edition stays in the staff administration, shared with the partners.

func (s *Server) coordinationRoutes(mux *router) {
	c := func(pattern string, h handlerFunc) {
		mux.Handle(pattern, s.wrap(s.requireVolunteer(s.requireCoordination(h))))
	}

	const base = "/{event}/volunteers"

	c("GET "+base, s.coordination)
	c("POST "+base+"/settings", s.coordinationSettings)
	c("POST "+base+"/logo", s.coordinationLogo)
	c("POST "+base+"/logo/delete", s.coordinationLogoDelete)
	c("POST "+base+"/copy", s.coordinationCopy)
	c("POST "+base+"/coordinators", s.coordinatorAdd)
	c("POST "+base+"/coordinators/{person}/delete", s.coordinatorRemove)
	c("POST "+base+"/poles", s.coordinationPoleCreate)
	c("POST "+base+"/poles/{pole}/delete", s.coordinationPoleDelete)
	c("POST "+base+"/poles/{pole}/copy", s.coordinationPoleCopy)
	c("POST "+base+"/messages", s.coordinationMessage)
	c("POST "+base+"/messages/{message}/delete", s.coordinationMessageDelete)
	c("GET "+base+"/list", s.coordinationVolunteers)
	c("GET "+base+"/list.csv", s.coordinationVolunteersCSV)
	c("GET "+base+"/signups.csv", s.coordinationSignupsCSV)
	c("GET "+base+"/logistics.csv", s.coordinationLogisticsCSV)
	c("POST "+base+"/remind", s.coordinationRemind)
	c("POST "+base+"/list", s.coordinationVolunteerAdd)
	c("POST "+base+"/list/join", s.coordinationVolunteersJoin)
	c("GET "+base+"/{person}", s.coordinationVolunteer)
	c("POST "+base+"/{person}/join", s.coordinationVolunteerJoin)
	c("POST "+base+"/{person}", s.coordinationVolunteerSave)
}

// requireCoordination lets in the coordinators of the edition of the path,
// and the staff.
func (s *Server) requireCoordination(h handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		e, err := s.store.GetEventBySlug(r.Context(), r.PathValue("event"))
		if err != nil {
			return err
		}

		if !act(r).Coordinates(e.ID) {
			return errForbidden
		}

		return h(w, r)
	}
}

// coordinationEvent loads the edition of the path; requireCoordination has
// checked the rights.
func (s *Server) coordinationEvent(r *http.Request) (*store.Event, error) {
	return s.store.GetEventBySlug(r.Context(), r.PathValue("event"))
}

// editionURL is the coordination of an edition's volunteers.
func editionURL(e *store.Event) string { return "/" + e.Slug + "/volunteers" }

// volunteerHomeURL is an edition's home page in the volunteer space.
func volunteerHomeURL(e *store.Event) string { return "/" + e.Slug + "/" }

func (s *Server) redirectCoordination(w http.ResponseWriter, r *http.Request, e *store.Event, tab string) error {
	to := editionURL(e)
	if tab != "" {
		to += "?tab=" + tab
	}

	http.Redirect(w, r, to, http.StatusSeeOther)

	return nil
}

func (s *Server) coordination(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	coverage, _, err := s.poleCoverage(ctx, act(r), e)
	if err != nil {
		return err
	}

	stats, err := s.store.GetVolunteerStats(ctx, e.ID)
	if err != nil {
		return err
	}

	messages, err := s.store.ListEventMessages(ctx, e.ID)
	if err != nil {
		return err
	}

	coordinators, err := s.store.ListEventCoordinators(ctx, e.ID)
	if err != nil {
		return err
	}

	// A pole is copied into an edition the person also coordinates.
	all, err := s.store.ListEvents(ctx)
	if err != nil {
		return err
	}

	var targets, others []*store.Event

	for _, x := range all {
		ok := act(r).Coordinates(x.ID)
		if ok {
			targets = append(targets, x)
		}

		if ok && x.ID != e.ID {
			others = append(others, x)
		}
	}

	tab := r.URL.Query().Get("tab")
	if tab != "messages" && tab != "settings" && tab != "history" && tab != "logistics" {
		tab = "poles"
	}

	var (
		history []*store.LogEntry
		logMore bool
		logi    *logistics
	)

	lp := logPage(r)

	switch tab {
	case "history":
		if history, logMore, err = s.store.ListEventLog(ctx, e.ID, lp); err != nil {
			return err
		}
	case "logistics":
		if logi, err = s.logisticsOf(ctx, act(r), e); err != nil {
			return err
		}
	}

	p := s.page(r, "vol.coord.title")
	p.TitleText = i18n.T(p.Lang, "vol.coord.title_event", e.Name)
	p.D = map[string]any{
		"Event": e, "Tab": tab, "Coverage": coverage, "Stats": stats, "Messages": messages,
		"PastFreeze":   e.VolunteerStatus == store.VolunteersOpen && e.SignupsLocked(nil, time.Now()),
		"CanCopy":      onlyDefaultPole(coverage),
		"Coordinators": coordinators, "Targets": targets, "Others": others, "Staff": act(r).Staff,
		"Modes": store.PoleModes, "Statuses": []string{store.VolunteersDraft, store.VolunteersOpen, store.VolunteersFrozen, store.VolunteersArchived},
		"MaxImage": maxPoleImageSize, "History": history, "Logistics": logi,
		"Log": logView{Page: lp, More: logMore, Base: editionURL(e) + "?tab=history"},
	}

	return s.render(w, r, "volunteer/edition", p)
}

// --- settings ---------------------------------------------------------------

func (s *Server) coordinationSettings(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	set := volunteerSettings{Status: r.FormValue("status"), FreezeAt: formTimePtr(r, "freeze_at"),
		TshirtSizes: r.FormValue("tshirt_sizes"), Meals: r.FormValue("meals")}

	if err := s.saveVolunteerSettings(r.Context(), act(r), e, set); err != nil {
		return err
	}

	s.flash(w, "flash.saved")

	return s.redirectCoordination(w, r, e, "settings")
}

func (s *Server) coordinationLogo(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	name, err := s.upload(w, r, "logo", files.KindLogo, maxPoleImageSize)
	if err != nil {
		var ue uploadError
		if errors.As(err, &ue) {
			s.flash(w, string(ue))

			return s.redirectCoordination(w, r, e, "settings")
		}

		return err
	}

	if err := s.setEditionLogo(r.Context(), act(r), e, name); err != nil {
		return err
	}

	s.flash(w, "flash.logo_saved")

	return s.redirectCoordination(w, r, e, "settings")
}

func (s *Server) coordinationLogoDelete(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	if err := s.setEditionLogo(r.Context(), act(r), e, ""); err != nil {
		return err
	}

	return s.redirectCoordination(w, r, e, "settings")
}

// coordinationCopy prepares the edition from another one the person also
// coordinates (the staff, any): its poles, coordination and logo, and on
// request its slots, moved to the new dates.
func (s *Server) coordinationCopy(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	src, err := s.store.GetEventBySlug(r.Context(), r.FormValue("from"))
	if err != nil {
		return err
	}

	withSlots := r.FormValue("slots") == "1"

	err = s.copyEdition(r.Context(), act(r), e, src, withSlots)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)

		return s.redirectCoordination(w, r, e, "settings")
	}

	if err != nil {
		return err
	}

	if withSlots {
		s.flash(w, "flash.vol.edition_copied_slots", src.Name)
	} else {
		s.flash(w, "flash.vol.edition_copied", src.Name)
	}

	return s.redirectCoordination(w, r, e, "")
}

// onlyDefaultPole tells whether an edition has no pole of its own yet: the
// only one it can be prepared from another into.
func onlyDefaultPole(coverage []*store.PoleCoverage) bool {
	for _, c := range coverage {
		if !c.Pole.IsDefault {
			return false
		}
	}

	return true
}

// editionShift moves a time of edition from to the same day of edition
// to: the first day onto the first day, and so on, at the same clock time
// in Paris across a change of daylight saving time. Nil when either has
// no dates.
func editionShift(from, to *store.Event) func(time.Time) time.Time {
	if from.StartsOn == nil || to.StartsOn == nil {
		return nil
	}

	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }
	days := int(day(*to.StartsOn).Sub(day(*from.StartsOn)).Hours() / 24)

	return func(t time.Time) time.Time {
		t = t.In(paris)

		return time.Date(t.Year(), t.Month(), t.Day()+days, t.Hour(), t.Minute(), 0, 0, paris)
	}
}

// --- coordinators: the staff's call ---------------------------------------

func (s *Server) coordinatorAdd(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	p, err := s.appointCoordinator(r.Context(), act(r), e, r.FormValue("email"), rc(r).Lang)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)

		return s.redirectCoordination(w, r, e, "settings")
	}

	if err != nil {
		return err
	}

	s.flash(w, "flash.vol.coordinator_added", p.Label())

	return s.redirectCoordination(w, r, e, "settings")
}

func (s *Server) coordinatorRemove(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	p, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	if err := s.dismissCoordinator(r.Context(), act(r), e, p); err != nil {
		return err
	}

	s.flash(w, "flash.vol.coordinator_removed", p.Label())

	return s.redirectCoordination(w, r, e, "settings")
}

// --- poles ------------------------------------------------------------------

func (s *Server) coordinationPoleCreate(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	p, err := s.createPole(r.Context(), act(r), e, newPole{Name: r.FormValue("name"), AccessMode: r.FormValue("access_mode"),
		Icon: r.FormValue("icon"), Lead: r.FormValue("lead")}, rc(r).Lang)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)

		return s.redirectCoordination(w, r, e, "")
	}

	if err != nil {
		return err
	}

	s.flash(w, "flash.vol.pole_created", p.Name)
	http.Redirect(w, r, p.URL()+"?tab=settings", http.StatusSeeOther)

	return nil
}

// coordinationPole loads the pole of the path in the edition of the path.
func (s *Server) coordinationPole(r *http.Request, e *store.Event) (*store.Pole, error) {
	return s.store.GetPoleBySlug(r.Context(), e.ID, r.PathValue("pole"))
}

func (s *Server) coordinationPoleDelete(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	p, err := s.coordinationPole(r, e)
	if err != nil {
		return err
	}

	err = s.deletePole(r.Context(), act(r), e, p)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)

		return s.redirectCoordination(w, r, e, "")
	}

	if err != nil {
		return err
	}

	s.flash(w, "flash.vol.pole_deleted", p.Name)

	return s.redirectCoordination(w, r, e, "")
}

func (s *Server) coordinationPoleCopy(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	p, err := s.coordinationPole(r, e)
	if err != nil {
		return err
	}

	target := e

	if slug := r.FormValue("to"); slug != "" && slug != e.Slug {
		if target, err = s.store.GetEventBySlug(r.Context(), slug); err != nil {
			return err
		}
	}

	copied, err := s.copyPole(r.Context(), act(r), p, target)
	if err != nil {
		return err
	}

	s.flash(w, "flash.vol.pole_copied", p.Name)
	http.Redirect(w, r, copied.URL()+"?tab=settings", http.StatusSeeOther)

	return nil
}

// --- messages ---------------------------------------------------------------

func (s *Server) coordinationMessage(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	sent, err := s.postNotice(r.Context(), act(r), noticeScope{Event: e}, r.FormValue("body"), r.FormValue("notify") == "1")
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)

		return s.redirectCoordination(w, r, e, "messages")
	}

	if err != nil {
		return err
	}

	s.flashNotice(w, r.FormValue("notify") == "1", sent)

	return s.redirectCoordination(w, r, e, "messages")
}

// flashNotice says a notice was posted, or how many it was e-mailed to.
func (s *Server) flashNotice(w http.ResponseWriter, notified bool, sent int) {
	if notified {
		s.flashCount(w, "flash.vol.message_sent", sent)
	} else {
		s.flash(w, "flash.vol.message_posted")
	}
}

func (s *Server) coordinationMessageDelete(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	if err := s.deleteNotice(r.Context(), act(r), noticeScope{Event: e}, r.PathValue("message")); err != nil {
		return err
	}

	return s.redirectCoordination(w, r, e, "messages")
}

// --- volunteers -------------------------------------------------------------

func (s *Server) coordinationVolunteers(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	volunteers, err := s.store.ListEventVolunteers(ctx, e.ID)
	if err != nil {
		return err
	}

	poles, err := s.store.ListPoles(ctx, e.ID)
	if err != nil {
		return err
	}

	// The volunteers yet to pick a slot: the ones to write to before the
	// event.
	var idle []*store.EventVolunteer

	for _, v := range volunteers {
		if v.Idle() {
			idle = append(idle, v)
		}
	}

	onlyIdle := r.URL.Query().Get("filter") == "idle"

	shown := volunteers
	if onlyIdle {
		shown = idle
	}

	// One pole's volunteers, which both filters combine with.
	var inPole *store.Pole

	for _, p := range poles {
		if p.Slug == r.URL.Query().Get("pole") {
			inPole = p
		}
	}

	if inPole != nil {
		var kept []*store.EventVolunteer

		for _, v := range shown {
			if slices.Contains(v.Poles, inPole.Name) {
				kept = append(kept, v)
			}
		}

		shown = kept
	}

	p := s.page(r, "vol.coord.volunteers")
	p.D = map[string]any{"Event": e, "Volunteers": shown, "Total": len(volunteers), "Idle": len(idle), "OnlyIdle": onlyIdle,
		"Open": !e.SignupsLocked(nil, time.Now()), "Poles": poles, "InPole": inPole}

	return s.render(w, r, "volunteer/volunteers", p)
}

func (s *Server) coordinationVolunteersCSV(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	volunteers, err := s.store.ListEventVolunteers(r.Context(), e.ID)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="volunteers-`+e.Slug+`.csv"`)

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"name", "email", "phone", "poles", "slots", "minutes"})

	for _, v := range volunteers {
		_ = cw.Write([]string{csvSafe(v.Person.Name), csvSafe(v.Person.Email), csvSafe(v.Person.Phone), csvSafe(strings.Join(v.Poles, ", ")),
			strconv.Itoa(v.SlotCount), strconv.Itoa(v.Minutes)})
	}

	cw.Flush()

	return cw.Error()
}

// csvSafe keeps a spreadsheet from reading a cell typed by a volunteer as a
// formula.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}

	return v
}

// coordinationVolunteerAdd registers a volunteer on their behalf, in a pole
// or in the edition's default pole.
func (s *Server) coordinationVolunteerAdd(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	back := editionURL(e) + "/list"

	var pole *store.Pole

	if slug := r.FormValue("pole"); slug != "" {
		if pole, err = s.store.GetPoleBySlug(ctx, e.ID, slug); err != nil {
			return err
		}
	}

	p, pole, err := s.registerVolunteer(ctx, act(r), e, registration{Email: r.FormValue("email"), Name: r.FormValue("name"), Phone: r.FormValue("phone")},
		pole, rc(r).Lang)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	s.flash(w, "flash.vol.volunteer_registered", p.Label(), pole.Name)
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

// coordinationVolunteerJoin puts a volunteer in a pole, whatever its access
// mode.
func (s *Server) coordinationVolunteerJoin(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	who, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	pole, err := s.store.GetPoleBySlug(r.Context(), e.ID, r.FormValue("pole"))
	if err != nil {
		return err
	}

	if err := s.putInPole(r.Context(), act(r), e, pole, []*store.Person{who}); err != nil {
		return err
	}

	s.flash(w, "flash.vol.member_joined", who.Label(), pole.Name)
	http.Redirect(w, r, safeRedirect(r.FormValue("back"), editionURL(e)+"/list"), http.StatusSeeOther)

	return nil
}

// coordinationVolunteer is one volunteer of the edition, as the
// coordination needs them: their poles, slots and waiting list, their
// logistics answers and what happened to them.
func (s *Server) coordinationVolunteer(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	who, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	f, err := s.volunteerFileOf(ctx, act(r), e, who)
	if err != nil {
		return err
	}

	poles, err := s.store.ListPoles(ctx, e.ID)
	if err != nil {
		return err
	}

	p := s.page(r, "vol.coord.volunteers")
	p.TitleText = who.Label()
	p.D = map[string]any{"Event": e, "V": f.Volunteer, "Slots": f.Slots, "Waiting": f.Waiting, "History": f.History, "Poles": poles, "Details": f.Details,
		"Staff": act(r).Staff}

	return s.render(w, r, "volunteer/volunteer", p)
}

// coordinationVolunteerSave corrects a volunteer's name and phone, from
// their page: a typo, a number given by phone. The address stays the
// staff's, since changing it logs the person out.
func (s *Server) coordinationVolunteerSave(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	who, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	if err := s.correctVolunteer(r.Context(), act(r), e, who, r.FormValue("name"), r.FormValue("phone")); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, editionURL(e)+"/"+who.UID, http.StatusSeeOther)

	return nil
}

// coordinationVolunteersJoin adds the volunteers ticked in the list to a
// pole at once.
func (s *Server) coordinationVolunteersJoin(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	pole, err := s.store.GetPoleBySlug(ctx, e.ID, r.FormValue("pole"))
	if err != nil {
		return err
	}

	var people []*store.Person

	for _, uid := range r.Form["person"] {
		who, err := s.store.GetPersonByUID(ctx, uid)
		if err != nil {
			return err
		}

		people = append(people, who)
	}

	if err := s.putInPole(ctx, act(r), e, pole, people); err != nil {
		return err
	}

	s.flashCount(w, "flash.vol.members_joined", len(people), pole.Name)
	http.Redirect(w, r, editionURL(e)+"/list", http.StatusSeeOther)

	return nil
}
