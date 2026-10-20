package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"manage/internal/access"
	"manage/internal/store"
)

// The partner's space, whichever way it is reached: a contact of the
// organization, or the staff seeing it as the partner does. As for the
// volunteers, each operation takes the actor and the organization they act
// for, checks the actor's rights itself (the session's organization is not
// taken on trust), does the change, writes the activity the staff follows,
// and tells whom it concerns.

// actsFor refuses an actor who may not act for the organization.
func actsFor(a *access.Actor, org *store.Organization) error {
	if !a.ActsFor(org.ID) {
		return errForbidden
	}

	return nil
}

// partnerSees refuses an actor who may not see a partnership of the
// organization: one of another organization, or one the staff has not shown
// to the partner yet, which does not exist for them.
func partnerSees(a *access.Actor, org *store.Organization, p *store.Partnership) error {
	if err := actsFor(a, org); err != nil {
		return err
	}

	if p.OrgID != org.ID || !a.SeesPartnership(p) {
		return errNotFound
	}

	return nil
}

// --- the partnership ----------------------------------------------------------

// savePartnerNotes saves the purchase order number and the notes the
// partner keeps on a partnership.
func (s *Server) savePartnerNotes(ctx context.Context, a *access.Actor, org *store.Organization, p *store.Partnership, poNumber, notes string) error {
	if err := partnerSees(a, org, p); err != nil {
		return err
	}

	p.PONumber, p.PartnerNotes = poNumber, notes

	if err := s.store.UpdatePartnership(ctx, p); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "partnership.notes", fmt.Sprintf("PO: %s\n%s", p.PONumber, p.PartnerNotes))

	return nil
}

// quoteAwaited refuses an actor who may not send back the quote of a
// partnership: one they do not see, or with no quote sent yet. The handler
// asks before taking the file in.
func quoteAwaited(a *access.Actor, org *store.Organization, p *store.Partnership) error {
	if err := partnerSees(a, org, p); err != nil {
		return err
	}

	if p.QuoteFile == "" && p.QuoteSentAt == nil {
		return errNotFound
	}

	return nil
}

// sendSignedQuote takes the quote of a partnership back signed, as file.
// The staff hears of it and checks it before marking the quote signed.
func (s *Server) sendSignedQuote(ctx context.Context, a *access.Actor, org *store.Organization, e *store.Event, p *store.Partnership, file string) error {
	if err := quoteAwaited(a, org, p); err != nil {
		return err
	}

	p.SignedQuoteFile = file

	if err := s.store.UpdatePartnership(ctx, p); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "quote.signed_uploaded", e.Name)

	return nil
}

// --- the organization ---------------------------------------------------------

// saveOrganization saves the details of an organization as the partner
// edited them: o is its edited copy.
func (s *Server) saveOrganization(ctx context.Context, a *access.Actor, o *store.Organization) error {
	if err := actsFor(a, o); err != nil {
		return err
	}

	if o.Name == "" {
		return refuse("organization.name_required")
	}

	if err := s.store.UpdateOrganization(ctx, o); err != nil {
		return err
	}

	s.recordAs(ctx, a, o, nil, "organization.updated", "")

	return nil
}

// uploadLogo adds a logo file to an organization, as its default or dark
// variant. Each variant keeps its last few versions only: logos are the
// only files partners upload, so the disk they may fill stays bounded.
func (s *Server) uploadLogo(ctx context.Context, a *access.Actor, org *store.Organization, variant, file string) error {
	if err := actsFor(a, org); err != nil {
		return err
	}

	if variant != store.LogoDark {
		variant = store.LogoDefault
	}

	if err := s.store.AddLogo(ctx, &store.Logo{OrgID: org.ID, Variant: variant, File: file, UploadedBy: activityActor(a)}); err != nil {
		return err
	}

	removed, err := s.store.PruneLogos(ctx, org.ID, variant, maxLogoVersions)
	if err != nil {
		slog.Error("prune logos", "org", org.Name, "err", err)
	}

	for _, f := range removed {
		if err := s.files.Remove(f); err != nil {
			slog.Error("remove old logo", "file", f, "err", err)
		}
	}

	s.recordAs(ctx, a, org, nil, "logo.uploaded", variant)

	return nil
}

// --- attendees ------------------------------------------------------------------

// cleanAttendee trims the fields of an attendee as typed, and makes anyone
// not a visitor someone on the booth.
func cleanAttendee(at *store.Attendee) {
	at.FirstName = strings.TrimSpace(at.FirstName)
	at.LastName = strings.TrimSpace(at.LastName)
	at.Email = strings.TrimSpace(at.Email)
	at.Dietary = strings.TrimSpace(at.Dietary)

	if at.Role != store.RoleVisitor {
		at.Role = store.RoleBooth
	}
}

// checkAttendee refuses an attendee without a full name, or with an
// address that is none.
func checkAttendee(at *store.Attendee) error {
	switch {
	case at.FirstName == "" || at.LastName == "":
		return refuse("attendees.name_required")
	case at.Email != "" && !validEmail(store.NormalizeEmail(at.Email)):
		return refuse("login.invalid_email")
	}

	return nil
}

// changesAttendees refuses an actor who may not change the attendees of a
// partnership they see: a contact once the edition's list is closed.
func changesAttendees(a *access.Actor, org *store.Organization, e *store.Event, p *store.Partnership) error {
	if err := partnerSees(a, org, p); err != nil {
		return err
	}

	if p.EventID != e.ID {
		return errNotFound
	}

	if !a.ChangesAttendees(e, org.ID) {
		return errForbidden
	}

	return nil
}

// addAttendee adds someone the organization sends to the edition, within
// the number its level allows.
func (s *Server) addAttendee(ctx context.Context, a *access.Actor, org *store.Organization, e *store.Event, p *store.Partnership, at *store.Attendee) error {
	if err := changesAttendees(a, org, e, p); err != nil {
		return err
	}

	at.PartnershipID = p.ID
	cleanAttendee(at)

	if p.LevelID != nil {
		l, err := s.store.GetLevel(ctx, *p.LevelID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}

		if l != nil && l.MaxAttendees > 0 {
			n, err := s.store.CountAttendees(ctx, p.ID)
			if err != nil {
				return err
			}

			if n >= l.MaxAttendees {
				return refuse("attendees.full")
			}
		}
	}

	if err := checkAttendee(at); err != nil {
		return err
	}

	if err := s.store.AddAttendee(ctx, at); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "attendee.added", at.FirstName+" "+at.LastName)

	return nil
}

// updateAttendee saves the changes to an attendee, at.UID: a typo or a diet
// took deleting and adding them again. Once the list is closed, the diet
// alone still changes for a contact: an allergy learnt late matters to the
// caterer.
func (s *Server) updateAttendee(ctx context.Context, a *access.Actor, org *store.Organization, e *store.Event, p *store.Partnership, at *store.Attendee) error {
	if err := partnerSees(a, org, p); err != nil {
		return err
	}

	if p.EventID != e.ID {
		return errNotFound
	}

	at.PartnershipID = p.ID
	cleanAttendee(at)

	if !a.ChangesAttendees(e, org.ID) {
		if err := s.store.UpdateAttendeeDietary(ctx, p.ID, at.UID, at.Dietary); err != nil {
			return err
		}

		s.recordAs(ctx, a, org, &p.ID, "attendee.updated", "")

		return nil
	}

	if err := checkAttendee(at); err != nil {
		return err
	}

	if err := s.store.UpdateAttendee(ctx, at); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "attendee.updated", at.FirstName+" "+at.LastName)

	return nil
}

// deleteAttendee takes someone off the attendees of a partnership.
func (s *Server) deleteAttendee(ctx context.Context, a *access.Actor, org *store.Organization, e *store.Event, p *store.Partnership, uid string) error {
	if err := changesAttendees(a, org, e, p); err != nil {
		return err
	}

	if err := s.store.DeleteAttendee(ctx, p.ID, uid); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "attendee.deleted", "")

	return nil
}

// --- job offers -------------------------------------------------------------------

// saveJob saves a job offer of the organization, new or loaded: it needs a
// title, and a link or a description to say what it is.
func (s *Server) saveJob(ctx context.Context, a *access.Actor, org *store.Organization, j *store.JobOffer) error {
	if err := actsFor(a, org); err != nil {
		return err
	}

	if j.OrgID != org.ID {
		return errNotFound
	}

	j.Title = strings.TrimSpace(j.Title)
	j.Location = strings.TrimSpace(j.Location)
	j.Contract = strings.TrimSpace(j.Contract)
	j.URL = normalizeURL(strings.TrimSpace(j.URL))
	j.Description = strings.TrimSpace(j.Description)

	switch {
	case j.Title == "":
		return refuse("jobs.title_required")
	case j.URL == "" && j.Description == "":
		return refuse("jobs.url_or_description")
	}

	if err := s.store.SaveJobOffer(ctx, j); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, nil, "job.saved", j.Title)

	return nil
}

// deleteJob deletes a job offer of the organization.
func (s *Server) deleteJob(ctx context.Context, a *access.Actor, org *store.Organization, uid string) error {
	if err := actsFor(a, org); err != nil {
		return err
	}

	if err := s.store.DeleteJobOffer(ctx, org.ID, uid); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, nil, "job.deleted", "")

	return nil
}

// --- contacts ---------------------------------------------------------------------

// inviterName is who an invitation from the partner's space comes from: the
// contact sending it, else (the staff, seeing the space as the partner)
// the organization.
func (s *Server) inviterName(ctx context.Context, a *access.Actor, org *store.Organization) (string, error) {
	if a.Staff || a.Person == nil {
		return org.Name, nil
	}

	managers, err := s.store.ListManagers(ctx, org.ID)
	if err != nil {
		return "", err
	}

	for _, m := range managers {
		if m.PersonID == a.Person.ID {
			return firstNonEmpty(m.Name, m.Email), nil
		}
	}

	return org.Name, nil
}

// inviteColleague makes someone a contact of the organization and e-mails
// them a link to come in.
func (s *Server) inviteColleague(ctx context.Context, a *access.Actor, org *store.Organization, m *store.Manager) error {
	if err := actsFor(a, org); err != nil {
		return err
	}

	m.OrgID = org.ID
	m.Email = store.NormalizeEmail(m.Email)
	m.Name = strings.TrimSpace(m.Name)
	m.InvitedBy = activityActor(a)

	if !validEmail(m.Email) {
		return refuse("login.invalid_email")
	}

	inviter, err := s.inviterName(ctx, a, org)
	if err != nil {
		return err
	}

	created, err := s.store.AddManager(ctx, m)
	if err != nil {
		return err
	}

	if !created {
		return refuse("managers.already")
	}

	if err := s.sendInvite(ctx, m, org, inviter); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, nil, "manager.invited", m.Email)

	return nil
}

// colleague is the contact uid of the organization, or errNotFound.
func (s *Server) colleague(ctx context.Context, org *store.Organization, uid string) (*store.Manager, []*store.Manager, error) {
	managers, err := s.store.ListManagers(ctx, org.ID)
	if err != nil {
		return nil, nil, err
	}

	for _, m := range managers {
		if m.UID == uid {
			return m, managers, nil
		}
	}

	return nil, nil, errNotFound
}

// resendInvitation sends a contact of the organization a fresh invitation,
// for one who let the first expire: inviting them again only said they
// already were.
func (s *Server) resendInvitation(ctx context.Context, a *access.Actor, org *store.Organization, uid string) error {
	if err := actsFor(a, org); err != nil {
		return err
	}

	m, _, err := s.colleague(ctx, org, uid)
	if err != nil {
		return err
	}

	inviter, err := s.inviterName(ctx, a, org)
	if err != nil {
		return err
	}

	if err := s.sendInvite(ctx, m, org, inviter); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, nil, "manager.invited", m.Email)

	return nil
}

// removeColleague takes a contact off the organization, and out of the
// sessions they opened for it; it returns whom. The last one stays, or
// nobody could log in any more.
func (s *Server) removeColleague(ctx context.Context, a *access.Actor, org *store.Organization, uid string) (*store.Manager, error) {
	if err := actsFor(a, org); err != nil {
		return nil, err
	}

	target, managers, err := s.colleague(ctx, org, uid)
	if err != nil {
		return nil, err
	}

	if len(managers) <= 1 {
		return nil, refuse("flash.last_manager")
	}

	if err := s.store.DetachManagerSessions(ctx, target.ID); err != nil {
		return nil, err
	}

	if err := s.store.DeleteManager(ctx, org.ID, target.ID); err != nil {
		return nil, err
	}

	s.recordAs(ctx, a, org, nil, "manager.removed", target.Email)

	return target, nil
}

// saveContactProfile changes the name and, when valid, the language of a
// contact: their own only, which the staff seeing the space is not.
func (s *Server) saveContactProfile(ctx context.Context, a *access.Actor, org *store.Organization, m *store.Manager, name, lang string) error {
	if err := actsFor(a, org); err != nil {
		return err
	}

	if a.Person == nil || m.PersonID != a.Person.ID || m.OrgID != org.ID {
		return errForbidden
	}

	m.Name = strings.TrimSpace(name)

	if validLang(lang) {
		m.Lang = lang
	}

	if err := s.store.UpdateManager(ctx, m); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, nil, "profile.updated", m.Email)

	return nil
}

// chooseOrganization moves a session of the actor's to another
// organization they are a contact of, as m.
func (s *Server) chooseOrganization(ctx context.Context, a *access.Actor, sessionToken string, m *store.Manager) error {
	if a.Person == nil || m.PersonID != a.Person.ID {
		return errForbidden
	}

	return s.store.SwitchSessionManager(ctx, sessionToken, m.ID)
}
