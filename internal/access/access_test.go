package access

import (
	"context"
	"testing"

	"manage/internal/store"
)

type roles struct {
	coordinated map[int64]bool
	led         map[int64]int64
}

func (r roles) CoordinatedEventIDs(context.Context, int64) (map[int64]bool, error) {
	return r.coordinated, nil
}

func (r roles) LedPoles(context.Context, int64) (map[int64]int64, error) { return r.led, nil }

func load(t *testing.T, r roles, staff bool) *Actor {
	t.Helper()

	a, err := Load(context.Background(), r, &store.Person{ID: 1}, staff)
	if err != nil {
		t.Fatal(err)
	}

	return a
}

func TestRules(t *testing.T) {
	open := &store.Event{ID: 1, VolunteerStatus: store.VolunteersOpen}
	draft := &store.Event{ID: 2, VolunteerStatus: store.VolunteersDraft}

	public := &store.Pole{ID: 10, EventID: 1, AccessMode: store.PolePublic}
	restricted := &store.Pole{ID: 11, EventID: 1, AccessMode: store.PoleRestricted}
	invitation := &store.Pole{ID: 12, EventID: 1, AccessMode: store.PoleInvitation}
	drafted := &store.Pole{ID: 20, EventID: 2, AccessMode: store.PolePublic}

	staff := load(t, roles{}, true)
	coordinator := load(t, roles{coordinated: map[int64]bool{1: true}}, false)
	lead := load(t, roles{led: map[int64]int64{11: 1, 20: 2}}, false)
	volunteer := load(t, roles{}, false)
	nobody := &Actor{}

	for _, c := range []struct {
		name string
		got  bool
		want bool
	}{
		{"staff coordinates every edition", staff.Coordinates(2), true},
		{"coordinator coordinates theirs", coordinator.Coordinates(1), true},
		{"coordinator not another", coordinator.Coordinates(2), false},
		{"coordinator manages every pole of theirs", coordinator.ManagesPole(restricted), true},
		{"coordinator leads none", coordinator.Leads(restricted.ID), false},
		{"lead manages their pole", lead.ManagesPole(restricted), true},
		{"lead not another", lead.ManagesPole(public), false},
		{"lead's poles of an edition", len(lead.LedPoles(1)) == 1 && lead.LedPoles(1)[11], true},

		{"everyone sees an open edition", volunteer.SeesEvent(open), true},
		{"a volunteer does not see a draft", volunteer.SeesEvent(draft), false},
		{"a lead sees the draft they prepare", lead.SeesEvent(draft), true},
		{"a coordinator of another edition does not", coordinator.SeesEvent(draft), false},

		{"a volunteer opens a public pole", volunteer.SeesPole(open, public, false), true},
		{"not a restricted one", volunteer.SeesPole(open, restricted, false), false},
		{"unless in it", volunteer.SeesPole(open, restricted, true), true},
		{"nor a pole of a draft", volunteer.SeesPole(draft, drafted, true), false},
		{"its lead does", lead.SeesPole(draft, drafted, false), true},

		{"the home lists an invitation pole", volunteer.ListsPole(invitation, false), true},
		{"not a restricted one", volunteer.ListsPole(restricted, false), false},
		{"the planning shows open poles", volunteer.PlansPole(public, false), true},
		{"not an invitation one", volunteer.PlansPole(invitation, false), false},
		{"unless in it", volunteer.PlansPole(invitation, true), true},

		{"the coordination sees the fiches", coordinator.SeesFiches(1), true},
		{"a lead does not", lead.SeesFiches(1), false},

		{"a lead adds members to an open pole", lead.CanAddMembers(drafted), true},
		{"not to a restricted one", lead.CanAddMembers(restricted), false},
		{"the coordination does", coordinator.CanAddMembers(restricted), true},
		{"a volunteer adds nobody", volunteer.CanAddMembers(public), false},
		{"a lead does not restrict", lead.CanRestrict(restricted), false},
		{"nor appoint managers", lead.CanAppointManagers(restricted), false},
		{"the coordination does", coordinator.CanAppointManagers(restricted), true},

		{"the staff names coordinators", staff.CanAppointCoordinators(), true},
		{"a coordinator does not", coordinator.CanAppointCoordinators(), false},

		{"the zero actor may do nothing", nobody.Prepares(1) || nobody.ManagesPole(public) || nobody.SeesEvent(draft), false},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %v", c.name, c.got)
		}
	}
}
