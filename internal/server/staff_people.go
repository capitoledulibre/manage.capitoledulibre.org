package server

import (
	"context"
	"errors"
	"strings"

	"manage/internal/access"
	"manage/internal/i18n"
	"manage/internal/mail"
	"manage/internal/store"
)

// The people of the site, for the staff: correcting their details, sending
// them a way in, putting a face on them, removing them. As for the
// partners, each operation checks that the actor is of the staff.

// personEdit is a person's details as the staff corrects them.
type personEdit struct {
	Name  string
	Email string
	Phone string
	Lang  string
}

// savePerson corrects the details of a person, and tells whether their
// address changed: a new one logs them out, as for a partner contact, the
// old one may not be theirs any more. A staff member's address comes from
// the staff login and stays.
func (s *Server) savePerson(ctx context.Context, a *access.Actor, p *store.Person, ed personEdit) (bool, error) {
	if err := staffOnly(a); err != nil {
		return false, err
	}

	staff, err := s.store.IsStaff(ctx, p.ID)
	if err != nil {
		return false, err
	}

	oldEmail := p.Email

	p.Name = strings.TrimSpace(ed.Name)
	p.Phone = strings.TrimSpace(ed.Phone)
	p.Lang = ed.Lang

	if !staff {
		p.Email = store.NormalizeEmail(ed.Email)
	}

	if !validEmail(p.Email) || len(p.Name) > 200 || len(p.Phone) > 40 {
		return false, refuse("flash.invalid_email")
	}

	switch err := s.store.UpdatePerson(ctx, p); {
	case errors.Is(err, store.ErrDuplicate):
		return false, refuse("flash.person_email_taken", p.Email)
	case errors.Is(err, store.ErrBadInput):
		return false, errBadInput
	case err != nil:
		return false, err
	}

	if p.Email == oldEmail {
		return false, nil
	}

	return true, s.store.DeletePersonSessions(ctx, p.ID)
}

// sendPersonLink e-mails a person a login link, valid seven days, for
// someone who cannot find theirs or never asked for one.
func (s *Server) sendPersonLink(ctx context.Context, a *access.Actor, p *store.Person) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	token, err := s.store.CreateMagicToken(ctx, p.Email, inviteTokenTTL)
	if err != nil {
		return err
	}

	return s.mailer.Send(mail.Message{
		To:      []string{p.Email},
		Subject: i18n.T(p.Lang, "mail.link.subject"),
		Body:    i18n.T(p.Lang, "mail.link.body", s.cfg.BaseURL+"/auth/"+token),
	})
}

// deletePerson removes a person altogether, from every space; those
// waiting for the slots they leave hear of the free places. The staff are
// not removed here: their access comes from the staff login.
func (s *Server) deletePerson(ctx context.Context, a *access.Actor, p *store.Person) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	freed, err := s.store.DeletePerson(ctx, p.ID)
	if errors.Is(err, store.ErrBadInput) {
		return refuse("flash.person_staff")
	}

	if err != nil {
		return err
	}

	for _, id := range freed {
		if sl, err := s.store.GetSlot(ctx, id, 0); err == nil {
			if pole, err := s.store.GetPole(ctx, sl.PoleID); err == nil {
				s.announceFreePlace(ctx, pole, sl.ID)
			}
		}
	}

	s.dropFile(ctx, p.AvatarFile)

	return nil
}

// setPersonPhoto puts a picture file on a person, for one who sent it by
// e-mail or never comes to the site: it counts as their own upload, which
// they can remove. "" takes it off and, as when they do it themselves, no
// Gravatar comes back in its place.
func (s *Server) setPersonPhoto(ctx context.Context, a *access.Actor, p *store.Person, file string) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	source := store.AvatarUpload
	if file == "" {
		source = store.AvatarOptOut
	}

	old, err := s.store.SetPersonAvatar(ctx, p.ID, file, source)
	if err != nil {
		return err
	}

	s.dropFile(ctx, old)
	p.AvatarFile = file

	return nil
}
