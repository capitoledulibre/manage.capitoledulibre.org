package server

import (
	"context"

	"manage/internal/access"
	"manage/internal/store"
)

// The people of a pole, whichever way the change comes: a volunteer joining
// or leaving, whoever runs the pole adding or taking out members, the
// coordination naming its managers. As for the sign-ups, each operation
// checks the actor's rights itself, does the change, tells whom it concerns
// and writes the history.

// joinPole makes the actor a member of a pole: any open pole, a private one
// with its invitation key, while the edition takes volunteers (whoever runs
// the pole may join it before).
func (s *Server) joinPole(ctx context.Context, a *access.Actor, v *poleView, key string) error {
	person := a.Person
	if person == nil || !a.SeesPole(v.Event, v.Pole, v.Member) {
		return errNotFound
	}

	if v.Event.VolunteerStatus != store.VolunteersOpen && !a.ManagesPole(v.Pole) {
		return refuse("vol.err.closed")
	}

	switch {
	case v.Pole.IsOpen():
	case v.Pole.IsPrivate() && key == v.Pole.AccessKey:
	case v.Pole.IsPrivate():
		return refuse("vol.err.bad_invite", v.Pole.Name)
	default:
		return refuse("vol.err.invitation_only_pole", v.Pole.Name)
	}

	if err := s.store.JoinPole(ctx, v.Pole.ID, person.ID); err != nil {
		return err
	}

	v.Member = true
	s.logAs(ctx, person, logPole(store.LogJoined, v.Pole, person))

	return nil
}

// leavePole takes the actor out of a pole, and so off every slot they had
// in it: each is a withdrawal, for the waiting list and, close to the day,
// the managers. Not the default pole, which is everyone's, nor once the
// pole's sign-ups freeze.
func (s *Server) leavePole(ctx context.Context, a *access.Actor, v *poleView) error {
	person := a.Person
	if person == nil || !a.SeesPole(v.Event, v.Pole, v.Member) {
		return errNotFound
	}

	switch {
	case v.Pole.IsDefault:
		return refuse("vol.err.default_pole")
	case v.Locked:
		return refuse("vol.err.locked_unsignup")
	}

	mine, err := s.store.PersonSlotsInPole(ctx, v.Pole.ID, person.ID)
	if err != nil {
		return err
	}

	if err := s.store.LeavePole(ctx, v.Pole.ID, person.ID); err != nil {
		return err
	}

	v.Member = false

	left := logPole(store.LogLeft, v.Pole, person)
	left.Count = len(mine)
	s.logAs(ctx, person, left)

	var late []*store.Slot

	for _, sl := range mine {
		s.announceFreePlace(ctx, v.Pole, sl.ID)

		if after, err := s.store.GetSlot(ctx, sl.ID, 0); err == nil && isLate(after) {
			late = append(late, after)
		}
	}

	s.notifyLateWithdrawal(ctx, person, v.Pole, late)

	return nil
}

// newMember is someone to add to a pole by address, with a name for a
// first account.
type newMember struct {
	Email, Name string
}

// addMembers adds people to a pole the actor may bring people into. Those
// without an account get one, in lang: the language of whoever adds them,
// until they choose theirs. It returns them.
func (s *Server) addMembers(ctx context.Context, a *access.Actor, v *poleView, people []newMember, lang string) ([]*store.Person, error) {
	if !a.CanAddMembers(v.Pole) {
		return nil, errForbidden
	}

	if len(people) > maxAtOnce {
		return nil, errBadInput
	}

	var added []*store.Person

	seen := map[string]bool{}

	for _, m := range people {
		key := store.NormalizeEmail(m.Email)
		if seen[key] {
			continue
		}

		seen[key] = true

		p, err := s.store.EnsureVolunteer(ctx, m.Email, m.Name, "", lang)
		if err != nil {
			return nil, err
		}

		if err := s.store.JoinPole(ctx, v.Pole.ID, p.ID); err != nil {
			return nil, err
		}

		s.logAs(ctx, a.Person, logPole(store.LogMemberAdded, v.Pole, p))

		added = append(added, p)
	}

	return added, nil
}

// removeMember takes someone out of a pole the actor runs, and off its
// slots. They are told by e-mail, with what they lose, and those waiting of
// the freed places.
func (s *Server) removeMember(ctx context.Context, a *access.Actor, v *poleView, who *store.Person) error {
	if !a.ManagesPole(v.Pole) {
		return errForbidden
	}

	if v.Pole.IsDefault {
		return refuse("vol.err.default_pole")
	}

	// Only a member can be taken out: anyone else would get an e-mail about
	// a pole they are not in.
	member, err := s.store.IsPoleMember(ctx, v.Pole.ID, who.ID)
	if err != nil {
		return err
	}

	if !member {
		return errNotFound
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
	s.logAs(ctx, a.Person, removed)

	s.notifyPoleRemoved(who, v.Pole, len(freed))

	for _, sl := range freed {
		s.announceFreePlace(ctx, v.Pole, sl.ID)
	}

	return nil
}

// appointManager makes someone, by address, a manager of a pole: the
// coordination's call. They get an account if they have none, in lang.
func (s *Server) appointManager(ctx context.Context, a *access.Actor, v *poleView, email, lang string) (*store.Person, error) {
	if !a.CanAppointManagers(v.Pole) {
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

	if err := s.store.AddPoleManager(ctx, v.Pole.ID, p.ID); err != nil {
		return nil, err
	}

	s.logAs(ctx, a.Person, logPole(store.LogLeadAdded, v.Pole, p))

	return p, nil
}

// dismissManager takes someone off the managers of a pole; they stay a
// member. The coordination's call too.
func (s *Server) dismissManager(ctx context.Context, a *access.Actor, v *poleView, who *store.Person) error {
	if !a.CanAppointManagers(v.Pole) {
		return errForbidden
	}

	if err := s.store.RemovePoleManager(ctx, v.Pole.ID, who.ID); err != nil {
		return err
	}

	s.logAs(ctx, a.Person, logPole(store.LogLeadRemoved, v.Pole, who))

	return nil
}
