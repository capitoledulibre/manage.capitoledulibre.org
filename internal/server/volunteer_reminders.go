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
	// The leads and referents of each pole, read once for everyone, and
	// the referents' pictures, made once.
	managers := map[int64][]*store.Person{}
	referents := map[int64]map[int64][]*store.Person{}
	avatars := map[int64]mailAvatar{}
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

		contacts := map[int64][]slotContact{}

		for _, sl := range slots {
			if _, ok := managers[sl.PoleID]; !ok {
				if managers[sl.PoleID], err = s.store.ListPoleManagers(ctx, sl.PoleID); err != nil {
					return sent, err
				}

				if referents[sl.PoleID], err = s.store.RoleReferents(ctx, sl.PoleID); err != nil {
					return sent, err
				}
			}

			contacts[sl.ID] = contactsFor(sl, managers[sl.PoleID], referents[sl.PoleID])
		}

		msg := mail.Message{
			To:      []string{p.Email},
			Subject: volSubject(p.Lang, "mail.vol.reminder.subject", dayName(p.Lang, start)),
			Body:    s.volMail(p, slots[0].EventSlug, s.reminderBody(p, start, slots, contacts)),
			ReplyTo: s.site.VolunteerContactEmail,
			Lang:    p.Lang,
		}

		// In the HTML, each referent shows with their picture.
		decor := map[string]string{}
		seen := map[int64]bool{}

		for _, sl := range slots {
			for _, c := range contacts[sl.ID] {
				if c.Role != roleReferent {
					continue
				}

				a, ok := avatars[c.ID]
				if !ok {
					a.html, a.image = s.mailAvatar(c.Person)
					avatars[c.ID] = a
				}

				decor[referentText(p.Lang, c)] = a.html

				if a.image != nil && !seen[c.ID] {
					seen[c.ID] = true
					msg.Inline = append(msg.Inline, *a.image)
				}
			}
		}

		if len(decor) > 0 {
			msg.HTML = mail.HTMLWith(msg.Subject, msg.Body, p.Lang, mailBrand(s), decor)
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

// mailAvatar is a referent's picture in a reminder: its HTML, and the
// image sent with the message (nil for initials).
type mailAvatar struct {
	html  string
	image *mail.Inline
}

// referentText is a referent among the contacts of a reminder: their name
// and why, nothing to reach them by; the HTML adds their picture.
func referentText(lang string, c slotContact) string {
	return c.Name + " (" + i18n.T(lang, c.Role) + ")"
}

// reminderBody lists the slots of the day, with whom to call for each, by
// slot (contactsFor): their name and why; for the leads, their address
// and, when they share it, their phone. The referents are named only: the
// volunteers meet them on the spot.
func (s *Server) reminderBody(p *store.Person, day time.Time, slots []*store.Slot, contacts map[int64][]slotContact) string {
	var b strings.Builder

	b.WriteString(i18n.T(p.Lang, "mail.vol.reminder.intro", dayName(p.Lang, day)))
	b.WriteString("\n\n")

	for _, sl := range slots {
		fmt.Fprintf(&b, "  • %s – %s  %s", clock(sl.StartsAt), clock(sl.EndsAt), sl.PoleName)

		if label := sl.Label(); label != "" {
			b.WriteString(" – " + label)
		}

		fmt.Fprintf(&b, "\n    %s\n", s.slotURL(sl))

		var who []string

		for _, m := range contacts[sl.ID] {
			c := referentText(p.Lang, m)
			if m.Role == roleReferent {
				who = append(who, c)

				continue
			}

			c += ", " + m.Email
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
