package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"manage/internal/i18n"
	"manage/internal/mail"
	"manage/internal/store"
)

// reminderWindows are the number of days before a deadline at which a
// reminder is sent, smallest first.
var reminderWindows = []int{3, 14}

// RunReminders periodically emails managers about missing items before the
// logo and attendee deadlines, and cleans up expired sessions.
func (s *Server) RunReminders(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.ReminderInterval)
	defer ticker.Stop()

	for {
		if err := s.store.Cleanup(ctx); err != nil {
			slog.Error("cleanup", "err", err)
		}

		if err := s.sendReminders(ctx, time.Now()); err != nil {
			slog.Error("reminders", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type reminderItem struct {
	kind     string
	deadline time.Time
}

func (s *Server) sendReminders(ctx context.Context, at time.Time) error {
	e, err := s.store.CurrentEvent(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	partnerships, err := s.store.ListEventPartnerships(ctx, e.ID)
	if err != nil {
		return err
	}

	today := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)

	for _, p := range partnerships {
		if p.Status != store.StatusConfirmed {
			continue
		}

		org, err := s.store.GetOrganization(ctx, p.OrgID)
		if err != nil {
			return err
		}

		attendees, err := s.store.CountAttendees(ctx, p.ID)
		if err != nil {
			return err
		}

		var (
			due []reminderItem
			// keys are the reminder windows to mark as sent once the
			// email went out.
			keys []string
		)

		checks := []struct {
			kind     string
			missing  bool
			deadline *time.Time
		}{
			{"logo", org.LogoFile == "", e.LogoDeadline},
			{"attendees", attendees == 0, e.AttendeesDeadline},
		}

		for _, c := range checks {
			if !c.missing || c.deadline == nil || c.deadline.Before(today) {
				continue
			}

			days := int(c.deadline.Sub(today).Hours() / 24)

			for _, window := range reminderWindows {
				if days > window {
					continue
				}

				sent, err := s.store.ReminderSent(ctx, p.ID, c.kind+"-"+itoa(int64(window)))
				if err != nil {
					return err
				}

				if !sent {
					due = append(due, reminderItem{kind: c.kind, deadline: *c.deadline})

					// Mark the larger windows too, so a deadline set late
					// does not trigger two emails in a row.
					for _, w := range reminderWindows {
						if w >= window {
							keys = append(keys, c.kind+"-"+itoa(int64(w)))
						}
					}
				}

				break
			}
		}

		if len(due) == 0 {
			continue
		}

		// Only mark the reminders once delivered, so a mail outage is
		// retried on the next run.
		if err := s.emailReminder(ctx, org, p, due); err != nil {
			slog.Error("send reminder", "org", org.Name, "err", err)

			continue
		}

		for _, k := range keys {
			if err := s.store.MarkReminderSent(ctx, p.ID, k); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Server) emailReminder(ctx context.Context, org *store.Organization, p *store.Partnership, items []reminderItem) error {
	managers, err := s.store.ListManagers(ctx, org.ID)
	if err != nil {
		return err
	}

	var (
		kinds     []string
		delivered int
		lastErr   error
	)

	for _, m := range managers {
		var lines []string

		for _, it := range items {
			date := it.deadline.Format("02/01/2006")
			if m.Lang == "en" {
				date = it.deadline.Format("Jan 2, 2006")
			}

			lines = append(lines, "- "+i18n.T(m.Lang, "mail.reminder."+it.kind, date))
		}

		err := s.mailer.Send(mail.Message{
			To:      []string{m.Email},
			Subject: i18n.T(m.Lang, "mail.reminder.subject", org.Name),
			Body:    i18n.T(m.Lang, "mail.reminder.body", strings.Join(lines, "\n"), s.cfg.BaseURL+"/partners/"),
		})
		if err != nil {
			slog.Error("send reminder", "email", m.Email, "err", err)
			lastErr = err

			continue
		}

		delivered++
	}

	// An organization without managers has nobody to remind: consider it
	// done rather than retrying forever.
	if delivered == 0 && lastErr != nil {
		return lastErr
	}

	for _, it := range items {
		kinds = append(kinds, it.kind)
	}

	return s.store.LogActivity(ctx, &store.Activity{
		OrgID: org.ID, PartnershipID: &p.ID, Actor: "system", Action: "reminder.sent", Details: strings.Join(kinds, ", "),
	})
}
