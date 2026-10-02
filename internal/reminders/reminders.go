// Package reminders sends each volunteer a recap of their slots for the next day.
package reminders

import (
	"fmt"
	"log"
	"strings"
	"time"

	"benevoles/internal/db"
	"benevoles/internal/mail"
)

// Sender sends daily reminders.
type Sender struct {
	DB      *db.DB
	Mailer  *mail.Mailer
	Loc     *time.Location
	BaseURL string
	OrgName string
}

// SendForDay e-mails every volunteer who has a slot on the given local day and
// has not been reminded yet. It returns the number of e-mails sent.
func (s *Sender) SendForDay(day time.Time) (int, error) {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, s.Loc)
	dayEnd := dayStart.Add(24 * time.Hour)
	dayKey := dayStart.Format("2006-01-02")

	userIDs, err := s.DB.UsersWithSlotsBetween(dayStart, dayEnd)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, uid := range userIDs {
		if s.DB.ReminderSent(uid, dayKey) {
			continue
		}
		u, err := s.DB.GetUser(uid)
		if err != nil {
			continue
		}
		slots, err := s.DB.ListUserSlotsBetween(uid, dayStart, dayEnd)
		if err != nil || len(slots) == 0 {
			continue
		}
		body := s.body(u, dayStart, slots)
		subject := fmt.Sprintf("[%s] Vos créneaux de %s", s.OrgName, FormatDay(dayStart, s.Loc))
		if err := s.Mailer.Send(u.Email, subject, body); err != nil {
			log.Printf("reminder to %s failed: %v", u.Email, err)
			continue
		}
		s.DB.MarkReminderSent(uid, dayKey)
		sent++
	}
	return sent, nil
}

func (s *Sender) body(u db.User, day time.Time, slots []db.MySlot) string {
	var b strings.Builder
	contacts := map[int64]string{}
	fmt.Fprintf(&b, "Bonjour %s,\n\n", u.DisplayName())
	fmt.Fprintf(&b, "Voici vos créneaux de bénévolat pour %s :\n\n", FormatDay(day, s.Loc))
	for _, sl := range slots {
		fmt.Fprintf(&b, "  • %s – %s  %s", sl.StartsAt.In(s.Loc).Format("15:04"), sl.EndsAt.In(s.Loc).Format("15:04"), sl.PoleName)
		if sl.LocationName != "" {
			fmt.Fprintf(&b, " / %s", sl.LocationName)
		}
		if sl.Title != "" {
			fmt.Fprintf(&b, " — %s", sl.Title)
		}
		fmt.Fprintf(&b, "\n    %s/slots/%d\n", s.BaseURL, sl.ID)
		if c := s.contact(sl.PoleID, contacts); c != "" {
			fmt.Fprintf(&b, "    Contact : %s\n", c)
		}
	}
	fmt.Fprintf(&b, "\nVotre planning complet : %s/me\n\nMerci pour votre aide !\n", s.BaseURL)
	return b.String()
}

// contact names whom to reach for a pole on the day: its managers who gave a
// name, with their e-mail and, when they chose to share it, their phone.
// cache keeps one lookup per pole for the whole message.
func (s *Sender) contact(poleID int64, cache map[int64]string) string {
	if c, ok := cache[poleID]; ok {
		return c
	}
	managers, _ := s.DB.ListPoleManagers(poleID)
	var parts []string
	for _, m := range managers {
		if strings.TrimSpace(m.Name) == "" {
			continue
		}
		p := m.Name + ", " + m.Email
		if m.SharePhone && m.Phone != "" {
			p += ", " + m.Phone
		}
		parts = append(parts, p)
	}
	cache[poleID] = strings.Join(parts, " ; ")
	return cache[poleID]
}

// FormatDay renders a date in French, e.g. "samedi 15 novembre".
func FormatDay(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	days := []string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}
	months := []string{"", "janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
	return fmt.Sprintf("%s %d %s", days[t.Weekday()], t.Day(), months[t.Month()])
}

// RunDaily blocks and sends reminders for the next day once per day at hour (local).
func (s *Sender) RunDaily(hour int) {
	for {
		now := time.Now().In(s.Loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, s.Loc)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		time.Sleep(time.Until(next))
		n, err := s.SendForDay(time.Now().In(s.Loc).Add(24 * time.Hour))
		if err != nil {
			log.Printf("reminders: %v", err)
		} else if n > 0 {
			log.Printf("reminders: %d e-mail(s) sent", n)
		}
	}
}
