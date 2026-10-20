package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"manage/internal/access"
	"manage/internal/store"
)

// The sign-ups of the volunteers, whichever way they come: a page, a
// form, an API. Each operation takes the actor and what they act upon,
// checks the actor's rights itself (a caller is not trusted to have), does
// the change, tells whom it concerns by e-mail, writes the history, and
// returns what happened. Nothing here knows of HTTP: the handlers read the
// form, call, and say the outcome.

// refusal is an operation declined for a reason the actor can act on: its
// i18n key says why, with Args.
type refusal struct {
	Key  string
	Args []string
}

func (r *refusal) Error() string { return r.Key }

// refuse declines an operation for the reason of key.
func refuse(key string, args ...string) error { return &refusal{Key: key, Args: args} }

// refused tells whether err is a refusal, and which.
func refused(err error) (*refusal, bool) {
	var r *refusal
	if errors.As(err, &r) {
		return r, true
	}

	return nil, false
}

// poleViewOf is a pole as the actor sees it, or errNotFound when they may
// not see it.
func (s *Server) poleViewOf(ctx context.Context, a *access.Actor, e *store.Event, p *store.Pole) (*poleView, error) {
	v := &poleView{Event: e, Pole: p, Locked: e.SignupsLocked(p.FreezeAt, time.Now()), Manage: a.ManagesPole(p), Fiche: ficheBase(a, e)}

	if a.Person != nil {
		var err error
		if v.Member, err = s.store.IsPoleMember(ctx, p.ID, a.Person.ID); err != nil {
			return nil, err
		}
	}

	if !a.SeesPole(e, p, v.Member) {
		return nil, errNotFound
	}

	return v, nil
}

// --- a volunteer's own sign-ups ---------------------------------------------

// signupOutcome is what a sign-up did.
type signupOutcome struct {
	// Overlaps are the actor's other slots at that time: the sign-up waits
	// for them to confirm, and nothing was done.
	Overlaps []*store.Slot
	// Joined tells that signing up joined the pole on the way.
	Joined bool
	// Slot is the slot after the sign-up, as the actor sees it.
	Slot *store.Slot
}

// signUp puts the actor on a slot of a pole they may see. Anyone may join
// a public pole, so a sign-up joins it on the way; other poles take a key
// or a manager. Unless force, a slot overlapping one of theirs asks first.
func (s *Server) signUp(ctx context.Context, a *access.Actor, v *poleView, sl *store.Slot, force bool) (*signupOutcome, error) {
	person := a.Person
	if person == nil || !a.SeesPole(v.Event, v.Pole, v.Member) {
		return nil, errNotFound
	}

	autoJoin := !v.Member && v.Pole.IsOpen()

	switch {
	case !v.Member && !autoJoin:
		return nil, refuse("vol.err.invitation_only")
	case v.Locked:
		return nil, refuse("vol.err.locked")
	case !sl.EndsAt.After(time.Now()):
		return nil, refuse("vol.err.slot_over")
	}

	if !force {
		overlaps, err := s.store.OverlappingSlots(ctx, person.ID, v.Pole.ID, sl.ID, sl.StartsAt, sl.EndsAt)
		if err != nil {
			return nil, err
		}

		if len(overlaps) > 0 {
			return &signupOutcome{Overlaps: overlaps, Slot: sl}, nil
		}
	}

	// Joining comes after the overlap check, so that cancelling at that
	// point leaves the volunteer where they were.
	if autoJoin {
		if err := s.store.JoinPole(ctx, v.Pole.ID, person.ID); err != nil {
			return nil, err
		}

		v.Member = true
	}

	ok, err := s.store.Signup(ctx, sl.ID, person.ID)
	if err != nil {
		return nil, err
	}

	if autoJoin {
		s.logAs(ctx, person, logPole(store.LogJoined, v.Pole, person))
	}

	if sl, err = s.store.GetSlot(ctx, sl.ID, person.ID); err != nil {
		return nil, err
	}

	if !ok {
		return &signupOutcome{Joined: autoJoin, Slot: sl}, refuse("vol.err.full")
	}

	s.logAs(ctx, person, logSlot(store.LogSignup, v.Pole, sl, person))

	return &signupOutcome{Joined: autoJoin, Slot: sl}, nil
}

// unsignUp takes the actor off a slot, until the pole's sign-ups freeze.
// Those waiting hear of the free place, and the pole's managers of a
// withdrawal close to the start.
func (s *Server) unsignUp(ctx context.Context, a *access.Actor, v *poleView, sl *store.Slot) (*store.Slot, error) {
	person := a.Person
	if person == nil || !a.SeesPole(v.Event, v.Pole, v.Member) {
		return nil, errNotFound
	}

	if v.Locked {
		return sl, refuse("vol.err.locked_unsignup")
	}

	if err := s.store.Unsignup(ctx, sl.ID, person.ID); err != nil {
		return nil, err
	}

	sl, err := s.store.GetSlot(ctx, sl.ID, person.ID)
	if err != nil {
		return nil, err
	}

	s.logAs(ctx, person, logSlot(store.LogUnsignup, v.Pole, sl, person))
	s.announceFreePlace(ctx, v.Pole, sl.ID)

	if isLate(sl) {
		s.notifyLateWithdrawal(ctx, person, v.Pole, []*store.Slot{sl})
	}

	return sl, nil
}

// joinWaitlist records that the actor wants a full slot. As a sign-up
// does, waiting for a slot of a public pole joins it: it tells whether it
// did.
func (s *Server) joinWaitlist(ctx context.Context, a *access.Actor, v *poleView, sl *store.Slot) (bool, error) {
	person := a.Person
	if person == nil || !a.SeesPole(v.Event, v.Pole, v.Member) {
		return false, errNotFound
	}

	autoJoin := !v.Member && v.Pole.IsOpen()

	switch {
	case !v.Member && !autoJoin:
		return false, refuse("vol.err.join_first")
	case v.Locked:
		return false, refuse("vol.err.locked")
	}

	if autoJoin {
		if err := s.store.JoinPole(ctx, v.Pole.ID, person.ID); err != nil {
			return false, err
		}

		v.Member = true
		s.logAs(ctx, person, logPole(store.LogJoined, v.Pole, person))
	}

	if err := s.store.JoinWaitlist(ctx, sl.ID, person.ID); err != nil {
		return false, err
	}

	s.logAs(ctx, person, logSlot(store.LogWaitlist, v.Pole, sl, person))

	return autoJoin, nil
}

// leaveWaitlist takes the actor off the waiting list of a slot.
func (s *Server) leaveWaitlist(ctx context.Context, a *access.Actor, v *poleView, sl *store.Slot) error {
	person := a.Person
	if person == nil || !a.SeesPole(v.Event, v.Pole, v.Member) {
		return errNotFound
	}

	if err := s.store.LeaveWaitlist(ctx, sl.ID, person.ID); err != nil {
		return err
	}

	s.logAs(ctx, person, logSlot(store.LogWaitlistLeft, v.Pole, sl, person))

	return nil
}

// --- whoever runs the pole, for others ---------------------------------------

// placed is someone put on a slot by whoever runs the pole, with the other
// slots they are on at that time, if any: the lead decides, so it only
// warns.
type placed struct {
	Person *store.Person
	Also   string
}

// signUpOthers puts people on a slot of a pole the actor runs, even a full
// one: whoever runs the pole may overbook it. Someone not in the pole joins
// it, if the actor may bring people in. Each is told by e-mail.
func (s *Server) signUpOthers(ctx context.Context, a *access.Actor, v *poleView, sl *store.Slot, people []*store.Person) ([]placed, error) {
	if !a.ManagesPole(v.Pole) || sl.PoleID != v.Pole.ID {
		return nil, errForbidden
	}

	// Every right first: a refusal halfway would leave half of them on.
	var newcomers []*store.Person

	for _, who := range people {
		member, err := s.store.IsPoleMember(ctx, v.Pole.ID, who.ID)
		if err != nil {
			return nil, err
		}

		if !member {
			if !a.CanAddMembers(v.Pole) {
				return nil, errForbidden
			}

			newcomers = append(newcomers, who)
		}
	}

	for _, who := range newcomers {
		if err := s.store.JoinPole(ctx, v.Pole.ID, who.ID); err != nil {
			return nil, err
		}
	}

	out := make([]placed, 0, len(people))

	for _, who := range people {
		overlaps, err := s.store.OverlappingSlots(ctx, who.ID, v.Pole.ID, sl.ID, sl.StartsAt, sl.EndsAt)
		if err != nil {
			return nil, err
		}

		if err := s.store.ForceSignup(ctx, sl.ID, who.ID); err != nil {
			return nil, err
		}

		s.logAs(ctx, a.Person, logSlot(store.LogSignupAdded, v.Pole, sl, who))
		s.notifySignupAdded(who, v.Pole, sl)

		var also []string
		for _, o := range overlaps {
			also = append(also, o.PoleName+" ("+clock(o.StartsAt)+"–"+clock(o.EndsAt)+")")
		}

		out = append(out, placed{Person: who, Also: strings.Join(also, ", ")})
	}

	return out, nil
}

// takeOff takes someone off a slot of a pole the actor runs. They are told
// by e-mail, and those waiting of the free place.
func (s *Server) takeOff(ctx context.Context, a *access.Actor, v *poleView, sl *store.Slot, who *store.Person) error {
	if !a.ManagesPole(v.Pole) || sl.PoleID != v.Pole.ID {
		return errForbidden
	}

	on, err := s.store.IsSignedUp(ctx, sl.ID, who.ID)
	if err != nil {
		return err
	}

	if !on {
		return errNotFound
	}

	if err := s.store.Unsignup(ctx, sl.ID, who.ID); err != nil {
		return err
	}

	s.logAs(ctx, a.Person, logSlot(store.LogSignupRemoved, v.Pole, sl, who))
	s.notifySignupRemoved(who, v.Pole, sl)
	s.announceFreePlace(ctx, v.Pole, sl.ID)

	return nil
}
