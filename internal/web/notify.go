package web

import (
	"fmt"
	"log"
	"strings"
	"time"

	"benevoles/internal/db"
	"benevoles/internal/reminders"
)

// Notifications are a side effect of an action, never its point: a volunteer
// removed from a slot must be removed whether or not the SMTP server answers.
// So sending happens off the request goroutine and failures are only logged.
// The WaitGroup lets the tests - and a graceful shutdown - wait for the queue.

// notify sends one message per recipient, built by body. It returns
// immediately; use WaitMail to wait for delivery to have been attempted.
func (s *Server) notify(recipients []db.User, subject string, body func(db.User) string) {
	if len(recipients) == 0 {
		return
	}
	s.mailWG.Add(1)
	go func() {
		defer s.mailWG.Done()
		for _, u := range recipients {
			if strings.TrimSpace(u.Email) == "" {
				continue
			}
			if err := s.Mailer.Send(u.Email, subject, body(u)); err != nil {
				log.Printf("notification to %s failed: %v", u.Email, err)
			}
		}
	}()
}

// WaitMail blocks until every queued notification has been attempted.
func (s *Server) WaitMail() { s.mailWG.Wait() }

// org is the organisation name used in subjects and signatures.
func (s *Server) org() string { return s.DB.GetSetting("org_name", "Bénévoles") }

func (s *Server) subject(format string, args ...any) string {
	return fmt.Sprintf("[%s] %s", s.org(), fmt.Sprintf(format, args...))
}

// slotLine renders one slot as a readable line in an e-mail.
func (s *Server) slotLine(pole db.Pole, slot db.Slot) string {
	loc := s.Cfg.Timezone
	line := fmt.Sprintf("%s, %s de %s à %s", pole.Name,
		reminders.FormatDay(slot.StartsAt, loc),
		slot.StartsAt.In(loc).Format("15:04"), slot.EndsAt.In(loc).Format("15:04"))
	if slot.LocationName != "" {
		line += " (" + slot.LocationName + ")"
	}
	if slot.Title != "" {
		line += " — " + slot.Title
	}
	return line
}

func (s *Server) greeting(u db.User) string {
	return fmt.Sprintf("Bonjour %s,\n\n", u.DisplayName())
}

func (s *Server) signature() string {
	return fmt.Sprintf("\nVotre planning\u00a0: %s/me\n\nMerci pour votre aide\u00a0!\n%s\n", s.Cfg.BaseURL, s.org())
}

// --- cancellations -------------------------------------------------------

// notifySlotDeleted warns the volunteers of a slot that it no longer exists.
func (s *Server) notifySlotDeleted(volunteers []db.User, pole db.Pole, slot db.Slot) {
	line := s.slotLine(pole, slot)
	s.notify(volunteers, s.subject("Créneau annulé\u00a0: %s", pole.Name), func(u db.User) string {
		return s.greeting(u) +
			"Le créneau sur lequel vous étiez inscrit·e a été supprimé\u00a0:\n\n  " + line + "\n\n" +
			"Vous n'avez donc plus rien à faire à ce moment-là. N'hésitez pas à choisir un autre créneau.\n" +
			s.signature()
	})
}

// notifySignupRemoved warns one volunteer that a manager took them off a slot.
func (s *Server) notifySignupRemoved(u db.User, pole db.Pole, slot db.Slot) {
	line := s.slotLine(pole, slot)
	s.notify([]db.User{u}, s.subject("Inscription annulée\u00a0: %s", pole.Name), func(u db.User) string {
		return s.greeting(u) +
			"Un responsable a annulé votre inscription sur ce créneau\u00a0:\n\n  " + line + "\n\n" +
			"Vous restez membre du pôle «\u00a0" + pole.Name + "\u00a0» et pouvez vous inscrire sur un autre créneau.\n" +
			fmt.Sprintf("\nLe pôle\u00a0: %s%s\n", s.Cfg.BaseURL, pole.URL()) +
			s.signature()
	})
}

// notifyPoleRemoved warns one volunteer that they were taken out of a pole,
// which also cancelled the slots they had there.
func (s *Server) notifyPoleRemoved(u db.User, pole db.Pole, slotCount int) {
	s.notify([]db.User{u}, s.subject("Retrait du pôle %s", pole.Name), func(u db.User) string {
		b := s.greeting(u) + "Un responsable vous a retiré·e du pôle «\u00a0" + pole.Name + "\u00a0».\n"
		if slotCount > 0 {
			b += fmt.Sprintf("\nVos %d inscription(s) sur les créneaux de ce pôle ont été annulées.\n", slotCount)
		}
		b += "\nVous pouvez toujours rejoindre un autre pôle depuis l'accueil.\n" + s.signature()
		return b
	})
}

// --- changes --------------------------------------------------------------

// notifySlotChanged tells the volunteers of a slot that its time or place
// moved. The calendar subscription follows on its own, but most volunteers
// coming once a year never set it up and would turn up at the old time.
func (s *Server) notifySlotChanged(volunteers []db.User, pole db.Pole, before, after db.Slot) {
	was, now := s.slotLine(pole, before), s.slotLine(pole, after)
	url := fmt.Sprintf("%s/slots/%d", s.Cfg.BaseURL, after.ID)
	s.notify(volunteers, s.subject("Créneau modifié\u00a0: %s", pole.Name), func(u db.User) string {
		return s.greeting(u) +
			"Un créneau sur lequel vous êtes inscrit·e a changé\u00a0:\n\n" +
			"  Avant      : " + was + "\n" +
			"  Désormais  : " + now + "\n\n" +
			"Vous restez inscrit·e. Si le nouvel horaire ne vous convient plus, vous pouvez vous désinscrire\u00a0: " + url + "\n" +
			s.signature()
	})
}

// lateWithdrawal is how close to its start a withdrawal is worth telling the
// managers about: earlier, the waiting list and the other volunteers have time
// to fill the hole; later, someone has to find a replacement.
const lateWithdrawal = 48 * time.Hour

// isLate tells whether a slot starts within lateWithdrawal from now.
func isLate(slot db.Slot) bool {
	d := time.Until(slot.StartsAt)
	return d > 0 && d < lateWithdrawal
}

// notifyLateWithdrawal tells the managers of a pole that a volunteer pulled
// out of slots starting soon. slots carry their counts after the withdrawal.
// A pole with no manager falls back on the coordinators of its event.
func (s *Server) notifyLateWithdrawal(u db.User, pole db.Pole, slots []db.Slot) {
	if len(slots) == 0 {
		return
	}
	recipients, _ := s.DB.ListPoleManagers(pole.ID)
	if len(recipients) == 0 {
		recipients, _ = s.DB.ListEventCoordinators(pole.EventID)
	}
	var others []db.User
	for _, r := range recipients {
		if r.ID != u.ID {
			others = append(others, r)
		}
	}
	var lines strings.Builder
	for _, sl := range slots {
		fmt.Fprintf(&lines, "  %s\u00a0: %d/%d inscrit·es\n    %s/manage/slots/%d\n", s.slotLine(pole, sl), sl.Filled, sl.Capacity, s.Cfg.BaseURL, sl.ID)
	}
	who := u.DisplayName()
	if u.Phone != "" {
		who += " (" + u.Phone + ")"
	}
	s.notify(others, s.subject("Désistement de dernière minute\u00a0: %s", pole.Name), func(m db.User) string {
		return s.greeting(m) +
			who + " vient de se désinscrire d'un créneau qui commence dans moins de 48 heures\u00a0:\n\n" +
			lines.String() + "\n" +
			"S'il y avait une liste d'attente, elle a été prévenue\u00a0; sinon, il faudra peut-être trouver quelqu'un d'autre.\n" +
			s.signature()
	})
}

// --- waiting list --------------------------------------------------------

// notifyPlaceFreed tells everyone waiting for a slot that a place opened. It is
// deliberately first-come-first-served rather than a reservation: an event
// needs the hole filled quickly, not a queue that can stall on one person.
func (s *Server) notifyPlaceFreed(waiting []db.User, pole db.Pole, slot db.Slot) {
	line := s.slotLine(pole, slot)
	url := fmt.Sprintf("%s/slots/%d", s.Cfg.BaseURL, slot.ID)
	s.notify(waiting, s.subject("Une place s'est libérée\u00a0: %s", pole.Name), func(u db.User) string {
		return s.greeting(u) +
			"Une place vient de se libérer sur un créneau que vous attendiez\u00a0:\n\n  " + line + "\n\n" +
			"Premier arrivé, premier servi\u00a0: " + url + "\n" +
			"\nSi vous ne le prenez pas, vous n'avez rien à faire\u00a0; nous vous préviendrons à la prochaine place libre.\n" +
			s.signature()
	})
}

// --- messages ------------------------------------------------------------

// notifyMessage forwards a newly published message to the people it concerns.
func (s *Server) notifyMessage(recipients []db.User, scope, body, link string) {
	s.notify(recipients, s.subject("Message\u00a0: %s", scope), func(u db.User) string {
		return s.greeting(u) +
			"Un message vient d'être publié pour " + scope + "\u00a0:\n\n" +
			indent(body) + "\n\n" +
			link + "\n" + s.signature()
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
