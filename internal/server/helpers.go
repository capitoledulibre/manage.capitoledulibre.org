package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"manage/internal/i18n"
	"manage/internal/mail"
	"manage/internal/store"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func formInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))

	return n
}

// formEdit returns a text field as edited in the form, or current when the
// form left it as it was shown, which its "<name>_was" twin carries: a form
// shared by staff and partner, saved later, must not undo what the other
// changed in the meantime.
func formEdit(r *http.Request, name, current string) string {
	v := cleanText(r.FormValue(name))
	if was, ok := r.Form[name+"_was"]; ok && v == cleanText(was[0]) {
		return current
	}

	return v
}

// cleanText trims a text field, with the line ends a browser sends as CRLF.
func cleanText(v string) string { return strings.TrimSpace(strings.ReplaceAll(v, "\r\n", "\n")) }

// parseDate parses an HTML date input value.
func parseDate(v string) *time.Time {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(v))
	if err != nil {
		return nil
	}

	return &t
}

// actionLabels are the staff-facing descriptions of activity log entries.
var actionLabels = map[string]string{
	"organization.updated": "Informations de l'organisation modifiées",
	"logo.uploaded":        "Nouveau logo",
	"attendee.added":       "Participant ajouté",
	"attendee.deleted":     "Participant supprimé",
	"attendee.updated":     "Participant modifié",
	"job.saved":            "Offre d'emploi enregistrée",
	"job.deleted":          "Offre d'emploi supprimée",
	"manager.invited":      "Manager invité",
	"manager.removed":      "Manager retiré",
	"manager.updated":      "Manager modifié",
	"partnership.notes":    "Notes / bon de commande modifiés",
	"partnership.updated":  "Partenariat modifié",
	"partnership.created":  "Partenariat créé",
	"partnership.renewed":  "Partenariat reconduit",
	"profile.updated":      "Profil modifié",
	"document.uploaded":    "Document déposé",
	"perks.updated":        "Contreparties mises à jour",
	"reminder.sent":        "Relance envoyée",
	"social.saved":         "Post réseaux sociaux enregistré",
}

// networkName returns the display name of a social network; "other" is
// translated in templates.
func networkName(n string) string {
	switch n {
	case "linkedin":
		return "LinkedIn"
	case "mastodon":
		return "Mastodon"
	case "bluesky":
		return "Bluesky"
	case "x":
		return "X"
	}

	return n
}

func actionLabel(action string) string {
	if l, ok := actionLabels[action]; ok {
		return l
	}

	return action
}

// record logs an activity entry and, when a partner made the change, notifies
// the staff by email.
func (s *Server) record(r *http.Request, org *store.Organization, partnershipID *int64, action, details string) {
	c := rc(r)

	err := s.store.LogActivity(r.Context(), &store.Activity{
		OrgID:         org.ID,
		PartnershipID: partnershipID,
		Actor:         c.Actor(),
		Action:        action,
		Details:       details,
	})
	if err != nil {
		slog.Error("log activity", "err", err)
	}

	if strings.HasPrefix(c.Actor(), "staff:") || len(s.cfg.StaffNotifyEmails) == 0 {
		return
	}

	msg := mail.Message{
		To:      s.cfg.StaffNotifyEmails,
		Subject: fmt.Sprintf("[Partenaires] %s : %s", org.Name, actionLabel(action)),
		Body: fmt.Sprintf("%s\n\nPar : %s\n%s\n\n%s/admin/organizations/%d\n",
			actionLabel(action), c.Actor(), details, s.cfg.BaseURL, org.ID),
	}

	s.background(func() {
		if err := s.mailer.Send(msg); err != nil {
			slog.Error("notify staff", "err", err)
		}
	})
}

// notifyManagers emails every manager of an organization in their language.
// Args are passed to the localized body after the link, to path, the page
// the e-mail is about: logged out, the login comes back to it. Answers go
// to the partnership's staff contact, else to the site's address.
func (s *Server) notifyManagers(r *http.Request, org *store.Organization, p *store.Partnership, path, subjectKey, bodyKey string, args ...any) error {
	managers, err := s.store.ListManagers(r.Context(), org.ID)
	if err != nil {
		return err
	}

	replyTo := s.site.ContactEmail

	if p.OwnerAdminID != nil {
		if owner, err := s.store.GetAdmin(r.Context(), *p.OwnerAdminID); err == nil {
			replyTo = owner.Email
		}
	}

	for _, m := range managers {
		msg := mail.Message{
			To:      []string{m.Email},
			Subject: i18n.T(m.Lang, subjectKey, org.Name),
			Body:    i18n.T(m.Lang, bodyKey, append([]any{s.cfg.BaseURL + path}, args...)...),
			ReplyTo: replyTo,
		}

		s.background(func() {
			if err := s.mailer.Send(msg); err != nil {
				slog.Error("notify manager", "email", msg.To, "err", err)
			}
		})
	}

	return nil
}
