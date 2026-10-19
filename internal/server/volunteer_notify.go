package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"manage/internal/i18n"
	"manage/internal/mail"
	"manage/internal/store"
)

// Notifications are a side effect of an action, never its point: a
// volunteer taken off a slot is taken off whether or not the SMTP server
// answers. They go out off the request goroutine and failures are logged.
// Each one is written in its recipient's language.

// notifyPeople sends one message per recipient; subject and body build it in
// the recipient's language.
func (s *Server) notifyPeople(people []*store.Person, subject func(lang string) string, body func(p *store.Person) string) {
	var msgs []mail.Message

	for _, p := range people {
		if strings.TrimSpace(p.Email) == "" {
			continue
		}

		msgs = append(msgs, mail.Message{To: []string{p.Email}, Subject: subject(p.Lang), Body: body(p), ReplyTo: s.site.VolunteerContactEmail})
	}

	if len(msgs) == 0 {
		return
	}

	s.background(func() {
		for _, m := range msgs {
			if err := s.mailer.Send(m); err != nil {
				slog.Error("volunteer notification", "to", m.To, "err", err)
			}
		}
	})
}

// volSubject prefixes a subject with the event name.
func volSubject(lang, key string, args ...any) string {
	return "[" + i18n.Site() + "] " + i18n.T(lang, key, args...)
}

// slotLine renders a slot as a line of an e-mail: "Accueil, samedi 14
// novembre de 14:00 à 18:00 (Hall) — Badges".
func slotLine(lang string, pole *store.Pole, sl *store.Slot) string {
	line := pole.Name + ", " + slotWhen(lang, sl.StartsAt, sl.EndsAt)
	if sl.LocationName != "" {
		line += " (" + sl.LocationName + ")"
	}

	if sl.Title != "" {
		line += " — " + sl.Title
	}

	return line
}

// volMail wraps a message body with the greeting and the signature.
func (s *Server) volMail(p *store.Person, eventSlug, body string) string {
	planning := s.cfg.BaseURL + "/volunteers/"
	if eventSlug != "" {
		planning += eventSlug + "/planning?view=mine"
	}

	return i18n.T(p.Lang, "mail.vol.greeting", p.Label()) + "\n\n" + body + "\n\n" +
		i18n.T(p.Lang, "mail.vol.signature", planning, s.site.VolunteerContactEmail)
}

// notifyPlaceFreed tells everyone waiting for a slot that a place opened.
// First come, first served rather than a reservation: an event needs the
// hole filled quickly, not a queue that stalls on one person.
func (s *Server) notifyPlaceFreed(waiting []*store.Person, pole *store.Pole, sl *store.Slot) {
	s.notifyPeople(waiting, func(lang string) string { return volSubject(lang, "mail.vol.freed.subject", pole.Name) },
		func(p *store.Person) string {
			return s.volMail(p, sl.EventSlug, i18n.T(p.Lang, "mail.vol.freed.body", slotLine(p.Lang, pole, sl), s.slotURL(sl)))
		})
}

// notifyLateWithdrawal tells the managers of a pole that a volunteer pulled
// out of slots starting soon; the slots carry their counts after the
// withdrawal. A pole without a manager falls back on the coordinators.
func (s *Server) notifyLateWithdrawal(r *http.Request, who *store.Person, pole *store.Pole, slots []*store.Slot) {
	if len(slots) == 0 {
		return
	}

	ctx := r.Context()

	recipients, err := s.store.ListPoleManagers(ctx, pole.ID)
	if err == nil && len(recipients) == 0 {
		recipients, err = s.store.ListEventCoordinators(ctx, pole.EventID)
	}

	if err != nil {
		slog.Error("late withdrawal recipients", "pole", pole.UID, "err", err)

		return
	}

	var others []*store.Person

	for _, p := range recipients {
		if p.ID != who.ID {
			others = append(others, p)
		}
	}

	name := who.Label()
	if who.Phone != "" {
		name += " (" + formatPhone(who.Phone) + ")"
	}

	s.notifyPeople(others, func(lang string) string { return volSubject(lang, "mail.vol.late.subject", pole.Name) },
		func(p *store.Person) string {
			var lines strings.Builder

			for _, sl := range slots {
				fmt.Fprintf(&lines, "  %s : %d/%d\n    %s%s\n", slotLine(p.Lang, pole, sl), sl.Filled, sl.Capacity, s.cfg.BaseURL, sl.ManageURL())
			}

			return s.volMail(p, pole.EventSlug, i18n.T(p.Lang, "mail.vol.late.body", name, lines.String()))
		})
}

// notifySlotDeleted warns the volunteers of a slot that it no longer
// exists.
func (s *Server) notifySlotDeleted(volunteers []*store.Person, pole *store.Pole, sl *store.Slot) {
	s.notifyPeople(volunteers, func(lang string) string { return volSubject(lang, "mail.vol.slot_deleted.subject", pole.Name) },
		func(p *store.Person) string {
			return s.volMail(p, pole.EventSlug, i18n.T(p.Lang, "mail.vol.slot_deleted.body", slotLine(p.Lang, pole, sl)))
		})
}

// notifySignupAdded tells a volunteer that a manager signed them up for a
// slot: nobody should learn of a shift from the roster on the day.
func (s *Server) notifySignupAdded(who *store.Person, pole *store.Pole, sl *store.Slot) {
	s.notifyPeople([]*store.Person{who}, func(lang string) string { return volSubject(lang, "mail.vol.signup_added.subject", pole.Name) },
		func(p *store.Person) string {
			return s.volMail(p, pole.EventSlug, i18n.T(p.Lang, "mail.vol.signup_added.body", slotLine(p.Lang, pole, sl), s.slotURL(sl)))
		})
}

// notifySignupRemoved warns a volunteer that a manager took them off a slot.
func (s *Server) notifySignupRemoved(who *store.Person, pole *store.Pole, sl *store.Slot) {
	s.notifyPeople([]*store.Person{who}, func(lang string) string { return volSubject(lang, "mail.vol.signup_removed.subject", pole.Name) },
		func(p *store.Person) string {
			return s.volMail(p, pole.EventSlug, i18n.T(p.Lang, "mail.vol.signup_removed.body", slotLine(p.Lang, pole, sl), pole.Name, s.poleURL(pole)))
		})
}

// notifyPoleRemoved warns a volunteer that they were taken out of a pole,
// which cancelled their slots in it.
func (s *Server) notifyPoleRemoved(who *store.Person, pole *store.Pole, slotCount int) {
	s.notifyPeople([]*store.Person{who}, func(lang string) string { return volSubject(lang, "mail.vol.pole_removed.subject", pole.Name) },
		func(p *store.Person) string {
			body := i18n.T(p.Lang, "mail.vol.pole_removed.body", pole.Name)
			if slotCount > 0 {
				body += "\n\n" + plural(p.Lang, "mail.vol.pole_removed.slots", slotCount)
			}

			return s.volMail(p, pole.EventSlug, body)
		})
}

// notifySlotChanged tells the volunteers of a slot that its time or place
// moved. The calendar feed follows on its own, but most volunteers never
// subscribe and would turn up at the old time.
func (s *Server) notifySlotChanged(volunteers []*store.Person, pole *store.Pole, before, after *store.Slot) {
	s.notifyPeople(volunteers, func(lang string) string { return volSubject(lang, "mail.vol.slot_changed.subject", pole.Name) },
		func(p *store.Person) string {
			return s.volMail(p, pole.EventSlug, i18n.T(p.Lang, "mail.vol.slot_changed.body",
				slotLine(p.Lang, pole, before), slotLine(p.Lang, pole, after), s.slotURL(after)))
		})
}

// notifyMessage forwards a newly posted notice to the people it concerns.
// scope names them ("le pôle Accueil"), in each language.
func (s *Server) notifyMessage(recipients []*store.Person, eventSlug string, scope func(lang string) string, body, link string) {
	s.notifyPeople(recipients, func(lang string) string { return volSubject(lang, "mail.vol.message.subject", scope(lang)) },
		func(p *store.Person) string {
			return s.volMail(p, eventSlug, i18n.T(p.Lang, "mail.vol.message.body", scope(p.Lang), indent(body), link))
		})
}

// indent offsets a quoted message so it stands out from the wrapper text.
func indent(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}

	return strings.Join(lines, "\n")
}
