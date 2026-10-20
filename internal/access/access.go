// Package access says who may do what with the volunteers of an edition.
//
// An Actor is who acts, with the roles that decide their rights: the staff,
// the coordinators of an edition, the managers of a pole. Its rules are pure
// functions of those roles and of what is acted upon, the same whichever
// way the action comes (a page, a form, an API), so they are written and
// tested once, here.
//
// Membership of a pole is not a role: it changes as one browses (joining a
// pole, then seeing its slots), so the caller tells it to the rules that
// need it.
package access

import (
	"context"

	"manage/internal/store"
)

// Actor is a person and their roles. The zero value may do nothing.
type Actor struct {
	// Person is who acts; nil for the staff on the administration pages,
	// who act as no volunteer.
	Person *store.Person
	// Staff may do anything.
	Staff bool

	coordinated map[int64]bool  // edition IDs
	led         map[int64]int64 // pole ID → edition ID
}

// Roles is where an Actor's roles come from.
type Roles interface {
	CoordinatedEventIDs(ctx context.Context, personID int64) (map[int64]bool, error)
	LedPoles(ctx context.Context, personID int64) (map[int64]int64, error)
}

// Load gathers the roles of a person, once for all the rules of a request.
func Load(ctx context.Context, src Roles, person *store.Person, staff bool) (*Actor, error) {
	a := &Actor{Person: person, Staff: staff}

	// The staff may do anything: their roles change nothing.
	if person == nil || staff {
		return a, nil
	}

	var err error

	if a.coordinated, err = src.CoordinatedEventIDs(ctx, person.ID); err != nil {
		return nil, err
	}

	if a.led, err = src.LedPoles(ctx, person.ID); err != nil {
		return nil, err
	}

	return a, nil
}

// --- roles ------------------------------------------------------------------

// Coordinates tells whether the actor runs the volunteers of an edition:
// the staff, and its coordinators.
func (a *Actor) Coordinates(eventID int64) bool {
	return a.Staff || a.coordinated[eventID]
}

// Leads tells whether the actor was made a manager of the pole. Whoever
// coordinates its edition runs it too, without being one: see ManagesPole.
func (a *Actor) Leads(poleID int64) bool {
	_, ok := a.led[poleID]

	return ok
}

// LedPoles are the poles of an edition the actor was made a manager of.
func (a *Actor) LedPoles(eventID int64) map[int64]bool {
	out := map[int64]bool{}

	for pole, event := range a.led {
		if event == eventID {
			out[pole] = true
		}
	}

	return out
}

// ManagesPole tells whether the actor runs a pole: its managers, and
// whoever coordinates its edition.
func (a *Actor) ManagesPole(p *store.Pole) bool {
	return a.Coordinates(p.EventID) || a.Leads(p.ID)
}

// Prepares tells whether the actor has a hand in an edition before it
// opens: its coordination and the managers of its poles.
func (a *Actor) Prepares(eventID int64) bool {
	return a.Coordinates(eventID) || len(a.LedPoles(eventID)) > 0
}

// --- seeing -----------------------------------------------------------------

// SeesEvent tells whether the actor may see the volunteer side of an
// edition: everyone once it opens, before (a draft) or after (archived) the
// people preparing it.
func (a *Actor) SeesEvent(e *store.Event) bool {
	return e.VisibleToVolunteers() || a.Prepares(e.ID)
}

// SeesPole tells whether the actor may open a pole of an edition: whoever
// runs it; the others once the edition is open, a restricted pole only if
// they are in it.
func (a *Actor) SeesPole(e *store.Event, p *store.Pole, member bool) bool {
	if a.ManagesPole(p) {
		return true
	}

	return e.VisibleToVolunteers() && (p.AccessMode != store.PoleRestricted || member)
}

// ListsPole tells whether a pole shows among the poles of the edition's
// home page: all but the restricted ones, which only their people see.
func (a *Actor) ListsPole(p *store.Pole, member bool) bool {
	return p.AccessMode != store.PoleRestricted || member || a.ManagesPole(p)
}

// PlansPole tells whether the slots of a pole show in the actor's planning:
// the poles they are in or run, and the open ones they may join.
func (a *Actor) PlansPole(p *store.Pole, member bool) bool {
	return member || a.ManagesPole(p) || p.IsOpen()
}

// SeesFiches tells whether the actor may open the volunteers' pages of an
// edition, where their details are: its coordination.
func (a *Actor) SeesFiches(eventID int64) bool {
	return a.Coordinates(eventID)
}

// --- acting -----------------------------------------------------------------

// CanAddMembers tells whether the actor may bring new people into a pole:
// whoever runs it, but a restricted pole only by its coordination, as it
// hides from everyone else.
func (a *Actor) CanAddMembers(p *store.Pole) bool {
	return a.Coordinates(p.EventID) || a.Leads(p.ID) && p.ManagersCanAdd()
}

// CanRestrict tells whether the actor may make a pole restricted, hiding it
// from everyone but its people: the coordination only.
func (a *Actor) CanRestrict(p *store.Pole) bool {
	return a.Coordinates(p.EventID)
}

// CanAppointManagers tells whether the actor may name or remove the
// managers of a pole: the coordination only.
func (a *Actor) CanAppointManagers(p *store.Pole) bool {
	return a.Coordinates(p.EventID)
}

// CanAppointCoordinators tells whether the actor may name or remove the
// coordinators of an edition: the staff only, as it hands over its people.
func (a *Actor) CanAppointCoordinators() bool {
	return a.Staff
}
