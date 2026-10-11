package server

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"manage/internal/i18n"
	"manage/internal/mail"
	"manage/internal/store"
)

// RunVolunteerReminders sends every volunteer, each day at
// VolunteerReminderHour (event time), the recap of their slots of the next
// day. A server started after that hour catches up at once; volunteer_reminders
// keeps anyone from getting the same recap twice.
func (s *Server) RunVolunteerReminders(ctx context.Context) {
	hour := s.cfg.VolunteerReminderHour

	for {
		now := time.Now().In(paris)

		if now.Hour() >= hour {
			n, err := s.sendVolunteerReminders(ctx, now.AddDate(0, 0, 1))
			if err != nil {
				slog.Error("volunteer reminders", "err", err)
			} else if n > 0 {
				slog.Info("volunteer reminders sent", "count", n)
			}
		}

		next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, paris)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
	}
}

// sendVolunteerReminders e-mails everyone with a slot on day, in an
// edition open to volunteers, who has not had the recap of that day yet. It
// returns how many were sent. A failed e-mail is tried again on the next
// run.
func (s *Server) sendVolunteerReminders(ctx context.Context, day time.Time) (int, error) {
	day = day.In(paris)
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, paris)
	end := start.AddDate(0, 0, 1)
	key := start.Format(time.DateOnly)

	ids, err := s.store.PeopleWithSlotsBetween(ctx, start, end)
	if err != nil {
		return 0, err
	}

	events := map[int64]*store.Event{}
	contacts := map[int64][]*store.Person{}
	sent := 0

	for _, id := range ids {
		done, err := s.store.VolunteerReminderSent(ctx, id, key)
		if err != nil {
			return sent, err
		}

		if done {
			continue
		}

		p, err := s.store.GetPerson(ctx, id)
		if err != nil {
			return sent, err
		}

		all, err := s.store.ListPersonSlotsBetween(ctx, id, start, end)
		if err != nil {
			return sent, err
		}

		// Only the slots of editions open to volunteers: a draft is still
		// being prepared.
		var slots []*store.Slot

		for _, sl := range all {
			e, ok := events[sl.EventID]
			if !ok {
				if e, err = s.store.GetEvent(ctx, sl.EventID); err != nil {
					return sent, err
				}

				events[sl.EventID] = e
			}

			if e.VisibleToVolunteers() {
				slots = append(slots, sl)
			}
		}

		if len(slots) == 0 {
			continue
		}

		for _, sl := range slots {
			if _, ok := contacts[sl.PoleID]; !ok {
				managers, err := s.store.ListPoleManagers(ctx, sl.PoleID)
				if err != nil {
					return sent, err
				}

				contacts[sl.PoleID] = named(managers)
			}
		}

		msg := mail.Message{
			To:      []string{p.Email},
			Subject: volSubject(p.Lang, "mail.vol.reminder.subject", dayName(p.Lang, start)),
			Body:    s.volMail(p, slots[0].EventSlug, s.reminderBody(p, start, slots, contacts)),
			ReplyTo: s.site.VolunteerContactEmail,
		}

		if err := s.mailer.Send(msg); err != nil {
			slog.Error("volunteer reminder", "person", p.UID, "err", err)

			continue
		}

		if err := s.store.MarkVolunteerReminderSent(ctx, id, key); err != nil {
			return sent, err
		}

		sent++
	}

	return sent, nil
}

// reminderBody lists the slots of the day, with whom to call for each pole:
// its leads who gave a name, their address and, when they share it, their
// phone.
func (s *Server) reminderBody(p *store.Person, day time.Time, slots []*store.Slot, contacts map[int64][]*store.Person) string {
	var b strings.Builder

	b.WriteString(i18n.T(p.Lang, "mail.vol.reminder.intro", dayName(p.Lang, day)))
	b.WriteString("\n\n")

	for _, sl := range slots {
		fmt.Fprintf(&b, "  • %s – %s  %s", clock(sl.StartsAt), clock(sl.EndsAt), sl.PoleName)

		if sl.LocationName != "" {
			b.WriteString(" / " + sl.LocationName)
		}

		if sl.Title != "" {
			b.WriteString(" — " + sl.Title)
		}

		fmt.Fprintf(&b, "\n    %s\n", s.slotURL(sl))

		var who []string

		for _, m := range contacts[sl.PoleID] {
			c := m.Name + ", " + m.Email
			if m.SharePhone && m.Phone != "" {
				c += ", " + formatPhone(m.Phone)
			}

			who = append(who, c)
		}

		if len(who) > 0 {
			b.WriteString("    " + i18n.T(p.Lang, "mail.vol.reminder.contact", strings.Join(who, " ; ")) + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
