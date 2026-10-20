package server

import (
	"context"
	"strings"

	"manage/internal/access"
	"manage/internal/i18n"
	"manage/internal/store"
)

// The notices of an edition, of a pole or of a slot: posted on its page,
// and on request e-mailed to whom it concerns. The coordination writes to
// the edition, whoever runs a pole to the pole and its slots.

// noticeScope is where a notice goes: an edition, one of its poles, or one
// of that pole's slots.
type noticeScope struct {
	Event *store.Event
	Pole  *store.Pole
	Slot  *store.Slot
}

// may tells whether the actor writes in the scope.
func (sc noticeScope) may(a *access.Actor) bool {
	switch {
	case sc.Pole == nil:
		return a.Coordinates(sc.Event.ID)
	case sc.Slot != nil && sc.Slot.PoleID != sc.Pole.ID:
		return false
	}

	return a.ManagesPole(sc.Pole)
}

// postNotice posts a notice and, if notify, e-mails it to the volunteers of
// the scope: the edition's, the pole's members, the slot's people. A notice
// nobody comes back to read is a notice nobody got, so the author decides
// whether it is worth an e-mail; not for an edition volunteers cannot see
// (a draft, last year's). It returns how many were e-mailed.
func (s *Server) postNotice(ctx context.Context, a *access.Actor, sc noticeScope, body string, notify bool) (int, error) {
	// A notice is signed: its author is a person.
	if !sc.may(a) || a.Person == nil {
		return 0, errForbidden
	}

	body = strings.TrimSpace(body)
	if body == "" {
		return 0, refuse("vol.err.empty_message")
	}

	if notify && !sc.Event.VisibleToVolunteers() {
		return 0, errBadInput
	}

	m := &store.Message{EventID: sc.Event.ID, Body: body}
	if sc.Pole != nil {
		m.PoleID = sc.Pole.ID
	}

	if sc.Slot != nil {
		m.SlotID = sc.Slot.ID
	}

	if err := s.store.CreateMessage(ctx, m, a.Person.ID); err != nil {
		return 0, err
	}

	if !notify {
		return 0, nil
	}

	recipients, err := s.noticeRecipients(ctx, sc)
	if err != nil {
		return 0, err
	}

	e, pole, sl := sc.Event, sc.Pole, sc.Slot

	switch {
	case sl != nil:
		s.notifyMessage(recipients, sl.EventSlug, func(lang string) string {
			return i18n.T(lang, "mail.vol.scope_slot", slotWhen(lang, sl.StartsAt, sl.EndsAt))
		}, body, s.slotURL(sl))
	case pole != nil:
		posted := logPole(store.LogMessagePosted, pole, nil)
		posted.Count = len(recipients)
		s.logAs(ctx, a.Person, posted)

		s.notifyMessage(recipients, pole.EventSlug, func(lang string) string { return i18n.T(lang, "mail.vol.scope_pole", pole.Name) },
			body, s.poleURL(pole))
	default:
		s.logAs(ctx, a.Person, &store.LogEntry{Action: store.LogMessagePosted, EventID: e.ID, Count: len(recipients)})

		s.notifyMessage(recipients, e.Slug, func(lang string) string { return i18n.T(lang, "mail.vol.scope_event", e.Name) },
			body, s.cfg.BaseURL+volunteerHomeURL(e))
	}

	return len(recipients), nil
}

// noticeRecipients are the volunteers a notice of the scope is e-mailed to.
func (s *Server) noticeRecipients(ctx context.Context, sc noticeScope) ([]*store.Person, error) {
	if sc.Slot != nil {
		return s.store.ListSlotVolunteers(ctx, sc.Slot.ID)
	}

	var out []*store.Person

	if sc.Pole != nil {
		members, err := s.store.ListPoleMembers(ctx, sc.Pole.ID)
		if err != nil {
			return nil, err
		}

		for _, m := range members {
			out = append(out, m.Person)
		}

		return out, nil
	}

	volunteers, err := s.store.ListEventVolunteers(ctx, sc.Event.ID)
	if err != nil {
		return nil, err
	}

	for _, v := range volunteers {
		out = append(out, v.Person)
	}

	return out, nil
}

// deleteNotice deletes a notice of the scope: one of the edition (any, for
// the coordination), or one of the pole.
func (s *Server) deleteNotice(ctx context.Context, a *access.Actor, sc noticeScope, uid string) error {
	if !sc.may(a) {
		return errForbidden
	}

	m, err := s.store.GetMessageByUID(ctx, sc.Event.ID, uid)
	if err != nil {
		return err
	}

	if sc.Pole != nil && m.PoleID != sc.Pole.ID {
		return errNotFound
	}

	return s.store.DeleteMessage(ctx, m.ID)
}
