package server

import (
	"context"

	"manage/internal/access"
	"manage/internal/store"
)

// What the volunteer side shows, as the actor may see it: the poles and how
// well they are staffed, the people of a pole, the logistics, a
// volunteer's page. The planning is in volunteer_planning.go (planning).
// The pages lay these out; an API returns them as they are.

// poleCoverage is how well each pole of an edition the actor may list is
// staffed (access.ListsPole), with the poles the actor is in.
func (s *Server) poleCoverage(ctx context.Context, a *access.Actor, e *store.Event) ([]*store.PoleCoverage, map[int64]bool, error) {
	if !a.SeesEvent(e) {
		return nil, nil, errNotFound
	}

	all, err := s.store.ListPoleCoverage(ctx, e.ID)
	if err != nil {
		return nil, nil, err
	}

	members := map[int64]bool{}

	if a.Person != nil {
		if members, err = s.store.MemberPoleIDs(ctx, e.ID, a.Person.ID); err != nil {
			return nil, nil, err
		}
	}

	var out []*store.PoleCoverage

	for _, c := range all {
		if a.ListsPole(c.Pole, members[c.Pole.ID]) {
			out = append(out, c)
		}
	}

	return out, members, nil
}

// poleMembers are the members of a pole the actor runs, with how many slots
// each has in it.
func (s *Server) poleMembers(ctx context.Context, a *access.Actor, v *poleView) ([]*store.PoleMember, error) {
	if err := runs(a, v, nil); err != nil {
		return nil, err
	}

	return s.store.ListPoleMembers(ctx, v.Pole.ID)
}

// logisticsOf is what the volunteers of an edition answered (t-shirts,
// meals) and who has yet to: for its coordination.
func (s *Server) logisticsOf(ctx context.Context, a *access.Actor, e *store.Event) (*logistics, error) {
	if err := coordinates(a, e); err != nil {
		return nil, err
	}

	return s.logistics(ctx, e)
}

// volunteerFile is one volunteer of an edition, as its coordination needs
// them: their poles, slots and waiting list, their logistics answers (when
// the edition asks) and what happened to them.
type volunteerFile struct {
	Volunteer *store.EventVolunteer
	Slots     []*store.Slot
	Waiting   []*store.Slot
	History   []*store.LogEntry
	Details   *store.VolunteerDetails
}

// volunteerFileOf is a volunteer's page for the coordination of the
// edition; errNotFound for someone who is not one of its volunteers.
func (s *Server) volunteerFileOf(ctx context.Context, a *access.Actor, e *store.Event, who *store.Person) (*volunteerFile, error) {
	if err := coordinates(a, e); err != nil {
		return nil, err
	}

	volunteers, err := s.store.ListEventVolunteers(ctx, e.ID)
	if err != nil {
		return nil, err
	}

	f := &volunteerFile{}

	for _, x := range volunteers {
		if x.Person.ID == who.ID {
			f.Volunteer = x
		}
	}

	if f.Volunteer == nil {
		return nil, errNotFound
	}

	if f.Slots, err = s.store.ListPersonSlots(ctx, e.ID, who.ID); err != nil {
		return nil, err
	}

	if f.Waiting, err = s.store.ListPersonWaitlist(ctx, e.ID, who.ID); err != nil {
		return nil, err
	}

	if f.History, err = s.store.ListPersonEventLog(ctx, e.ID, who.ID); err != nil {
		return nil, err
	}

	if e.AsksVolunteers() {
		if f.Details, err = s.store.GetVolunteerDetails(ctx, e.ID, who.ID); err != nil {
			return nil, err
		}
	}

	return f, nil
}
