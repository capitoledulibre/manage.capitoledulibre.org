package server

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"manage/internal/files"
	"manage/internal/i18n"
	"manage/internal/store"
)

// The coordination of an edition's volunteers, for its coordinators and the
// staff: every pole, the global planning, the volunteer list, edition-wide
// messages and the volunteer settings. Creating, renaming or deleting an
// edition stays in the staff administration, shared with the partners.

func (s *Server) coordinationRoutes(mux *http.ServeMux) {
	c := func(pattern string, h handlerFunc) {
		mux.Handle(pattern, s.wrap(s.requireVolunteer(s.requireCoordination(h))))
	}

	const base = "/volunteers/{event}/edition"

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
	c("GET "+base+"/volunteers", s.coordinationVolunteers)
	c("GET "+base+"/volunteers.csv", s.coordinationVolunteersCSV)
	c("GET "+base+"/signups.csv", s.coordinationSignupsCSV)
	c("GET "+base+"/logistics.csv", s.coordinationLogisticsCSV)
	c("POST "+base+"/remind", s.coordinationRemind)
	c("POST "+base+"/volunteers", s.coordinationVolunteerAdd)
	c("POST "+base+"/volunteers/{person}/join", s.coordinationVolunteerJoin)
}

// requireCoordination lets in the coordinators of the edition of the path,
// and the staff.
func (s *Server) requireCoordination(h handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		e, err := s.store.GetEventBySlug(r.Context(), r.PathValue("event"))
		if err != nil {
			return err
		}

		ok, err := s.coordinates(r, e.ID)
		if err != nil {
			return err
		}

		if !ok {
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

func editionURL(e *store.Event) string { return "/volunteers/" + e.Slug + "/edition" }

func (s *Server) redirectCoordination(w http.ResponseWriter, r *http.Request, e *store.Event, tab string) error {
	to := editionURL(e)
	if tab != "" {
		to += "?tab=" + tab
	}

	http.Redirect(w, r, to, http.StatusSeeOther)

	return nil
}

// staffOnly refuses anyone but the staff.
func staffOnly(r *http.Request) error {
	if rc(r).Admin == nil {
		return errForbidden
	}

	return nil
}

func (s *Server) coordination(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	coverage, err := s.store.ListPoleCoverage(ctx, e.ID)
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
		ok, err := s.coordinates(r, x.ID)
		if err != nil {
			return err
		}

		if ok {
			targets = append(targets, x)
		}

		if x.ID != e.ID {
			others = append(others, x)
		}
	}

	tab := r.URL.Query().Get("tab")
	if tab != "messages" && tab != "settings" && tab != "history" && tab != "logistics" {
		tab = "poles"
	}

	var (
		history []*store.LogEntry
		logi    *logistics
	)

	switch tab {
	case "history":
		if history, err = s.store.ListEventLog(ctx, e.ID); err != nil {
			return err
		}
	case "logistics":
		if logi, err = s.logistics(r, e); err != nil {
			return err
		}
	}

	p := s.page(r, "vol.coord.title")
	p.TitleText = i18n.T(p.Lang, "vol.coord.title_event", e.Name)
	p.D = map[string]any{
		"Event": e, "Tab": tab, "Coverage": coverage, "Stats": stats, "Messages": messages,
		"Coordinators": coordinators, "Targets": targets, "Others": others, "Staff": rc(r).Admin != nil,
		"Modes": store.PoleModes, "Statuses": []string{store.VolunteersDraft, store.VolunteersOpen, store.VolunteersFrozen, store.VolunteersArchived},
		"MaxImage": maxPoleImageSize, "History": history, "Logistics": logi,
	}

	return s.render(w, r, "volunteer/edition", p)
}

// --- settings ---------------------------------------------------------------

func (s *Server) coordinationSettings(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	before := e.VolunteerStatus
	e.VolunteerStatus = r.FormValue("status")
	e.VolunteerFreezeAt = formTimePtr(r, "freeze_at")
	e.TshirtSizes, e.Meals = r.FormValue("tshirt_sizes"), r.FormValue("meals")

	if err := s.store.SaveVolunteerSettings(r.Context(), e); err != nil {
		if errors.Is(err, store.ErrBadInput) {
			return errBadInput
		}

		return err
	}

	if e.VolunteerStatus != before {
		s.logVol(r, &store.LogEntry{Action: store.LogStatus, EventID: e.ID, Title: e.VolunteerStatus})
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

	if err := s.store.SetEventLogo(r.Context(), e.ID, name); err != nil {
		return err
	}

	s.dropFile(r, e.LogoFile)
	s.flash(w, "flash.logo_saved")

	return s.redirectCoordination(w, r, e, "settings")
}

func (s *Server) coordinationLogoDelete(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	if err := s.store.SetEventLogo(r.Context(), e.ID, ""); err != nil {
		return err
	}

	s.dropFile(r, e.LogoFile)

	return s.redirectCoordination(w, r, e, "settings")
}

// coordinationCopy prepares the edition from another one: its poles
// (settings, places, leads), its coordinators and its logo. The staff's
// call, as it brings people along.
func (s *Server) coordinationCopy(w http.ResponseWriter, r *http.Request) error {
	if err := staffOnly(r); err != nil {
		return err
	}

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	src, err := s.store.GetEventBySlug(r.Context(), r.FormValue("from"))
	if err != nil {
		return err
	}

	if err := s.store.CopyVolunteerSide(r.Context(), src.ID, e.ID); err != nil {
		if errors.Is(err, store.ErrBadInput) {
			return errBadInput
		}

		return err
	}

	s.flash(w, "flash.vol.edition_copied", src.Name)

	return s.redirectCoordination(w, r, e, "")
}

// --- coordinators: the staff's call ---------------------------------------

func (s *Server) coordinatorAdd(w http.ResponseWriter, r *http.Request) error {
	if err := staffOnly(r); err != nil {
		return err
	}

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	email := store.NormalizeEmail(r.FormValue("email"))
	if !validEmail(email) {
		s.flash(w, "flash.invalid_email")

		return s.redirectCoordination(w, r, e, "settings")
	}

	p, err := s.store.EnsureVolunteer(r.Context(), email, "", "", rc(r).Lang)
	if err != nil {
		return err
	}

	if err := s.store.AddEventCoordinator(r.Context(), e.ID, p.ID); err != nil {
		return err
	}

	in := &store.LogEntry{Action: store.LogCoordinatorIn, EventID: e.ID}
	in.SetSubject(p)
	s.logVol(r, in)

	s.flash(w, "flash.vol.coordinator_added", p.Label())

	return s.redirectCoordination(w, r, e, "settings")
}

func (s *Server) coordinatorRemove(w http.ResponseWriter, r *http.Request) error {
	if err := staffOnly(r); err != nil {
		return err
	}

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	p, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	if err := s.store.RemoveEventCoordinator(r.Context(), e.ID, p.ID); err != nil {
		return err
	}

	out := &store.LogEntry{Action: store.LogCoordinatorOut, EventID: e.ID}
	out.SetSubject(p)
	s.logVol(r, out)

	s.flash(w, "flash.vol.coordinator_removed", p.Label())

	return s.redirectCoordination(w, r, e, "settings")
}

// --- poles ------------------------------------------------------------------

func (s *Server) coordinationPoleCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 100 {
		return s.redirectCoordination(w, r, e, "")
	}

	poles, err := s.store.ListPoles(ctx, e.ID)
	if err != nil {
		return err
	}

	p := &store.Pole{EventID: e.ID, Name: name, AccessMode: r.FormValue("access_mode"), Icon: strings.TrimSpace(r.FormValue("icon")), Position: len(poles)}
	if err := s.store.CreatePole(ctx, p); err != nil {
		return err
	}

	if email := store.NormalizeEmail(r.FormValue("lead")); validEmail(email) {
		lead, err := s.store.EnsureVolunteer(ctx, email, "", "", rc(r).Lang)
		if err != nil {
			return err
		}

		if err := s.store.AddPoleManager(ctx, p.ID, lead.ID); err != nil {
			return err
		}
	}

	if p, err = s.store.GetPole(ctx, p.ID); err != nil {
		return err
	}

	s.logVol(r, logPole(store.LogPoleCreated, p, nil))
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

	if p.IsDefault {
		s.flash(w, "vol.err.default_pole_delete")

		return s.redirectCoordination(w, r, e, "")
	}

	if err := s.store.DeletePole(r.Context(), p.ID); err != nil {
		return err
	}

	// The pole is gone: the entry keeps its name, at the edition's level.
	s.logVol(r, &store.LogEntry{Action: store.LogPoleDeleted, EventID: e.ID, PoleName: p.Name})

	s.dropFile(r, p.ImageFile)
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

		if ok, err := s.coordinates(r, target.ID); err != nil || !ok {
			return cmpErr(err, errForbidden)
		}
	}

	copied, err := s.store.CopyPole(r.Context(), p.ID, target.ID)
	if err != nil {
		return err
	}

	s.logVol(r, logPole(store.LogPoleCreated, copied, nil))
	s.flash(w, "flash.vol.pole_copied", p.Name)
	http.Redirect(w, r, copied.URL()+"?tab=settings", http.StatusSeeOther)

	return nil
}

// --- messages ---------------------------------------------------------------

func (s *Server) coordinationMessage(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		return s.redirectCoordination(w, r, e, "messages")
	}

	if err := s.store.CreateMessage(ctx, &store.Message{EventID: e.ID, Body: body}, rc(r).Person.ID); err != nil {
		return err
	}

	// A message nobody comes back to read is a message nobody got, so the
	// author decides whether it is worth an e-mail.
	if r.FormValue("notify") != "1" {
		s.flash(w, "flash.vol.message_posted")

		return s.redirectCoordination(w, r, e, "messages")
	}

	volunteers, err := s.store.ListEventVolunteers(ctx, e.ID)
	if err != nil {
		return err
	}

	recipients := make([]*store.Person, 0, len(volunteers))
	for _, v := range volunteers {
		recipients = append(recipients, v.Person)
	}

	s.logVol(r, &store.LogEntry{Action: store.LogMessagePosted, EventID: e.ID, Count: len(recipients)})

	name := e.Name
	s.notifyMessage(recipients, e.Slug, func(lang string) string { return i18n.T(lang, "mail.vol.scope_event", name) },
		body, s.cfg.BaseURL+"/volunteers/"+e.Slug)
	s.flashCount(w, "flash.vol.message_sent", len(recipients))

	return s.redirectCoordination(w, r, e, "messages")
}

func (s *Server) coordinationMessageDelete(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	m, err := s.store.GetMessageByUID(r.Context(), e.ID, r.PathValue("message"))
	if err != nil {
		return err
	}

	if err := s.store.DeleteMessage(r.Context(), m.ID); err != nil {
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

	p := s.page(r, "vol.coord.volunteers")
	p.D = map[string]any{"Event": e, "Volunteers": shown, "Total": len(volunteers), "Idle": len(idle), "OnlyIdle": onlyIdle,
		"Open": e.VolunteerStatus == store.VolunteersOpen, "Poles": poles}

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

	back := "/volunteers/" + e.Slug + "/edition/volunteers"

	email := store.NormalizeEmail(r.FormValue("email"))
	if !validEmail(email) {
		s.flash(w, "flash.invalid_email")
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	pole, err := s.store.DefaultPole(ctx, e.ID)
	if err != nil {
		return err
	}

	if slug := r.FormValue("pole"); slug != "" {
		if pole, err = s.store.GetPoleBySlug(ctx, e.ID, slug); err != nil {
			return err
		}
	}

	p, err := s.store.EnsureVolunteer(ctx, email, strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("phone")), rc(r).Lang)
	if err != nil {
		return err
	}

	if err := s.store.JoinPole(ctx, pole.ID, p.ID); err != nil {
		return err
	}

	s.logVol(r, logPole(store.LogRegistered, pole, p))
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

	if err := s.store.JoinPole(r.Context(), pole.ID, who.ID); err != nil {
		return err
	}

	s.logVol(r, logPole(store.LogMemberAdded, pole, who))
	s.flash(w, "flash.vol.member_joined", who.Label(), pole.Name)
	http.Redirect(w, r, "/volunteers/"+e.Slug+"/edition/volunteers", http.StatusSeeOther)

	return nil
}
