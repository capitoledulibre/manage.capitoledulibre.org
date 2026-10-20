package server

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"manage/internal/access"
	"manage/internal/i18n"
	"manage/internal/store"
)

// The coordination of an edition's volunteers: its settings, its poles, its
// coordinators, its people. As for the sign-ups, each operation checks the
// actor's rights itself (the coordination of that edition, or the staff),
// does the change, tells whom it concerns and writes the history.

// coordinates refuses an actor who does not coordinate the edition.
func coordinates(a *access.Actor, e *store.Event) error {
	if !a.Coordinates(e.ID) {
		return errForbidden
	}

	return nil
}

// --- the edition --------------------------------------------------------------

// volunteerSettings are the volunteer side's settings of an edition.
type volunteerSettings struct {
	Status      string
	FreezeAt    *time.Time
	TshirtSizes string
	Meals       string
}

// saveVolunteerSettings changes the volunteer side's settings of an edition;
// a change of status goes in the history.
func (s *Server) saveVolunteerSettings(ctx context.Context, a *access.Actor, e *store.Event, set volunteerSettings) error {
	if err := coordinates(a, e); err != nil {
		return err
	}

	before := e.VolunteerStatus
	e.VolunteerStatus, e.VolunteerFreezeAt, e.TshirtSizes, e.Meals = set.Status, set.FreezeAt, set.TshirtSizes, set.Meals

	if err := s.store.SaveVolunteerSettings(ctx, e); err != nil {
		if errors.Is(err, store.ErrBadInput) {
			return errBadInput
		}

		return err
	}

	if e.VolunteerStatus != before {
		s.logAs(ctx, a.Person, &store.LogEntry{Action: store.LogStatus, EventID: e.ID, Title: e.VolunteerStatus})
	}

	return nil
}

// setEditionLogo sets the logo file of the edition's volunteer side, ""
// to take it off, and drops the former one.
func (s *Server) setEditionLogo(ctx context.Context, a *access.Actor, e *store.Event, file string) error {
	if err := coordinates(a, e); err != nil {
		return err
	}

	if err := s.store.SetEventLogo(ctx, e.ID, file); err != nil {
		return err
	}

	s.dropFile(ctx, e.LogoFile)
	e.LogoFile = file

	return nil
}

// copyEdition prepares an edition from another the actor also coordinates:
// its poles (settings, places, managers), its coordinators and its logo,
// and if withSlots its slots, moved to the new dates.
func (s *Server) copyEdition(ctx context.Context, a *access.Actor, e, src *store.Event, withSlots bool) error {
	if err := coordinates(a, e); err != nil {
		return err
	}

	if err := coordinates(a, src); err != nil {
		return err
	}

	var shift func(time.Time) time.Time

	if withSlots {
		if shift = editionShift(src, e); shift == nil {
			return refuse("vol.err.copy_no_dates")
		}
	}

	err := s.store.CopyVolunteerSide(ctx, src.ID, e.ID, shift)

	switch {
	case errors.Is(err, store.ErrBadInput):
		return errBadInput
	case errors.Is(err, store.ErrDuplicate):
		return refuse("vol.err.copy_not_empty")
	}

	return err
}

// appointCoordinator makes someone, by address, a coordinator of the
// edition: the staff's call. They get an account if they have none, in
// lang.
func (s *Server) appointCoordinator(ctx context.Context, a *access.Actor, e *store.Event, email, lang string) (*store.Person, error) {
	if !a.CanAppointCoordinators() {
		return nil, errForbidden
	}

	email = store.NormalizeEmail(email)
	if !validEmail(email) {
		return nil, refuse("flash.invalid_email")
	}

	p, err := s.store.EnsureVolunteer(ctx, email, "", "", lang)
	if err != nil {
		return nil, err
	}

	if err := s.store.AddEventCoordinator(ctx, e.ID, p.ID); err != nil {
		return nil, err
	}

	in := &store.LogEntry{Action: store.LogCoordinatorIn, EventID: e.ID}
	in.SetSubject(p)
	s.logAs(ctx, a.Person, in)

	return p, nil
}

// dismissCoordinator takes someone off the coordinators of the edition.
func (s *Server) dismissCoordinator(ctx context.Context, a *access.Actor, e *store.Event, who *store.Person) error {
	if !a.CanAppointCoordinators() {
		return errForbidden
	}

	if err := s.store.RemoveEventCoordinator(ctx, e.ID, who.ID); err != nil {
		return err
	}

	out := &store.LogEntry{Action: store.LogCoordinatorOut, EventID: e.ID}
	out.SetSubject(who)
	s.logAs(ctx, a.Person, out)

	return nil
}

// remind writes to the volunteers yet to pick a slot ("idle": not once
// sign-ups close), or to those on a slot who have not answered the
// logistics ("details": while it is asked). It returns how many.
func (s *Server) remind(ctx context.Context, a *access.Actor, e *store.Event, audience string) (int, error) {
	if err := coordinates(a, e); err != nil {
		return 0, err
	}

	var people []*store.Person

	switch audience {
	case "idle":
		if e.SignupsLocked(nil, time.Now()) {
			return 0, errBadInput
		}

		volunteers, err := s.store.ListEventVolunteers(ctx, e.ID)
		if err != nil {
			return 0, err
		}

		for _, v := range volunteers {
			if v.Idle() {
				people = append(people, v.Person)
			}
		}
	case "details":
		if !e.DetailsOpen(time.Now()) {
			return 0, errBadInput
		}

		l, err := s.logistics(ctx, e)
		if err != nil {
			return 0, err
		}

		for _, line := range l.Pending() {
			people = append(people, line.Volunteer.Person)
		}
	default:
		return 0, errBadInput
	}

	planning := s.cfg.BaseURL + planningURL(e) + "?view=open"
	home := s.cfg.BaseURL + volunteerHomeURL(e)

	s.notifyPeople(people, func(lang string) string { return volSubject(lang, "mail.vol.remind_"+audience+".subject") },
		func(p *store.Person) string {
			link := home
			if audience == "idle" {
				link = planning
			}

			return s.volMail(p, e.Slug, i18n.T(p.Lang, "mail.vol.remind_"+audience+".body", e.Name, link))
		})

	s.logAs(ctx, a.Person, &store.LogEntry{Action: store.LogReminded, EventID: e.ID, Title: audience, Count: len(people)})

	return len(people), nil
}

// --- poles ----------------------------------------------------------------------

// newPole is a pole to create, with the address of its first manager, if
// any.
type newPole struct {
	Name, AccessMode, Icon, Lead string
}

// createPole creates a pole in the edition, last of its list, with its
// first manager (who gets an account if they have none, in lang).
func (s *Server) createPole(ctx context.Context, a *access.Actor, e *store.Event, n newPole, lang string) (*store.Pole, error) {
	if err := coordinates(a, e); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(n.Name)
	if name == "" || len(name) > 100 {
		return nil, refuse("vol.err.pole_name")
	}

	poles, err := s.store.ListPoles(ctx, e.ID)
	if err != nil {
		return nil, err
	}

	p := &store.Pole{EventID: e.ID, Name: name, AccessMode: n.AccessMode, Icon: strings.TrimSpace(n.Icon), Position: len(poles)}
	if err := s.store.CreatePole(ctx, p); err != nil {
		return nil, err
	}

	if email := store.NormalizeEmail(n.Lead); validEmail(email) {
		lead, err := s.store.EnsureVolunteer(ctx, email, "", "", lang)
		if err != nil {
			return nil, err
		}

		if err := s.store.AddPoleManager(ctx, p.ID, lead.ID); err != nil {
			return nil, err
		}
	}

	if p, err = s.store.GetPole(ctx, p.ID); err != nil {
		return nil, err
	}

	s.logAs(ctx, a.Person, logPole(store.LogPoleCreated, p, nil))

	return p, nil
}

// deletePole deletes a pole of the edition, with its slots; not the default
// one, which is everyone's.
func (s *Server) deletePole(ctx context.Context, a *access.Actor, e *store.Event, p *store.Pole) error {
	if err := coordinates(a, e); err != nil {
		return err
	}

	if p.EventID != e.ID {
		return errNotFound
	}

	if p.IsDefault {
		return refuse("vol.err.default_pole_delete")
	}

	if err := s.store.DeletePole(ctx, p.ID); err != nil {
		return err
	}

	// The pole is gone: the entry keeps its name, at the edition's level.
	s.logAs(ctx, a.Person, &store.LogEntry{Action: store.LogPoleDeleted, EventID: e.ID, PoleName: p.Name})
	s.dropFile(ctx, p.ImageFile)

	return nil
}

// copyPole copies a pole into an edition the actor coordinates, its own or
// another.
func (s *Server) copyPole(ctx context.Context, a *access.Actor, p *store.Pole, target *store.Event) (*store.Pole, error) {
	if !a.Coordinates(p.EventID) || !a.Coordinates(target.ID) {
		return nil, errForbidden
	}

	copied, err := s.store.CopyPole(ctx, p.ID, target.ID)
	if err != nil {
		return nil, err
	}

	s.logAs(ctx, a.Person, logPole(store.LogPoleCreated, copied, nil))

	return copied, nil
}

// --- people -------------------------------------------------------------------

// registration is a volunteer registered by the coordination on their
// behalf.
type registration struct {
	Email, Name, Phone string
}

// registerVolunteer registers a volunteer on their behalf, in a pole of the
// edition or, without one, in its default pole. They get an account if
// they have none, in lang. It returns them, and the pole.
func (s *Server) registerVolunteer(ctx context.Context, a *access.Actor, e *store.Event, reg registration, pole *store.Pole, lang string) (*store.Person, *store.Pole, error) {
	if err := coordinates(a, e); err != nil {
		return nil, nil, err
	}

	email := store.NormalizeEmail(reg.Email)
	if !validEmail(email) {
		return nil, nil, refuse("flash.invalid_email")
	}

	if pole == nil {
		var err error
		if pole, err = s.store.DefaultPole(ctx, e.ID); err != nil {
			return nil, nil, err
		}
	} else if pole.EventID != e.ID {
		return nil, nil, errNotFound
	}

	p, err := s.store.EnsureVolunteer(ctx, email, strings.TrimSpace(reg.Name), strings.TrimSpace(reg.Phone), lang)
	if err != nil {
		return nil, nil, err
	}

	if err := s.store.JoinPole(ctx, pole.ID, p.ID); err != nil {
		return nil, nil, err
	}

	s.logAs(ctx, a.Person, logPole(store.LogRegistered, pole, p))

	return p, pole, nil
}

// putInPole puts people in a pole of the edition, whatever its access mode:
// the coordination's call.
func (s *Server) putInPole(ctx context.Context, a *access.Actor, e *store.Event, pole *store.Pole, people []*store.Person) error {
	if err := coordinates(a, e); err != nil {
		return err
	}

	if pole.EventID != e.ID {
		return errNotFound
	}

	for _, who := range people {
		if err := s.store.JoinPole(ctx, pole.ID, who.ID); err != nil {
			return err
		}

		s.logAs(ctx, a.Person, logPole(store.LogMemberAdded, pole, who))
	}

	return nil
}

// correctVolunteer corrects the name and phone of a volunteer of the
// edition: a typo, a number given by phone. The address stays the
// staff's, since changing it logs the person out; and the coordination of
// one edition does not edit the people of another.
func (s *Server) correctVolunteer(ctx context.Context, a *access.Actor, e *store.Event, who *store.Person, name, phone string) error {
	if err := coordinates(a, e); err != nil {
		return err
	}

	volunteers, err := s.store.ListEventVolunteers(ctx, e.ID)
	if err != nil {
		return err
	}

	if !slices.ContainsFunc(volunteers, func(v *store.EventVolunteer) bool { return v.Person.ID == who.ID }) {
		return errNotFound
	}

	name, phone = strings.TrimSpace(name), strings.TrimSpace(phone)
	if name == "" || len(name) > 200 || len(phone) > 40 {
		return errBadInput
	}

	return s.store.UpdateVolunteerProfile(ctx, who.ID, name, phone)
}
