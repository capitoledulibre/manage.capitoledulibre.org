package server

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"manage/internal/access"
	"manage/internal/store"
)

// The staff's work with the partners: the organizations, their contacts,
// their partnerships of each edition. As everywhere, each operation checks
// the actor's rights itself, the staff only, though the administration's
// routes already let no one else in.

// staffOnly refuses an actor who is not of the staff.
func staffOnly(a *access.Actor) error {
	if !a.Staff {
		return errForbidden
	}

	return nil
}

// staffPartnership refuses an actor who is not of the staff, and a
// partnership that is not of the organization.
func staffPartnership(a *access.Actor, org *store.Organization, p *store.Partnership) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	if p.OrgID != org.ID {
		return errNotFound
	}

	return nil
}

// --- organizations --------------------------------------------------------------

// newOrganization is an organization as the staff creates it: a name, and
// at once, if given, its partnership of an edition and its first contact.
type newOrganization struct {
	Name string
	// Event is the slug of the edition to attach it to, Status the
	// partnership's (a prospect when unknown).
	Event  string
	Status string
	// ManagerEmail, when given, makes a first contact, invited by e-mail if
	// SendInvite.
	ManagerEmail string
	ManagerName  string
	Lang         string
	SendInvite   bool
}

// createOrganization creates an organization. An invalid contact address
// is refused once the organization exists: it returns it with the refusal.
func (s *Server) createOrganization(ctx context.Context, a *access.Actor, n newOrganization) (*store.Organization, error) {
	if err := staffOnly(a); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(n.Name)
	if name == "" {
		return nil, errBadInput
	}

	org := &store.Organization{Name: name}
	if err := s.store.CreateOrganization(ctx, org); err != nil {
		return nil, err
	}

	if n.Event != "" {
		e, err := s.store.GetEventBySlug(ctx, n.Event)
		if err != nil {
			return nil, errBadInput
		}

		p := &store.Partnership{OrgID: org.ID, EventID: e.ID, Status: n.Status}
		if !store.ValidStatus(p.Status) {
			p.Status = store.StatusProspect
		}

		if err := s.store.CreatePartnership(ctx, p); err != nil {
			return nil, err
		}
	}

	if email := store.NormalizeEmail(n.ManagerEmail); email != "" {
		if !validEmail(email) {
			return org, refuse("flash.invalid_email")
		}

		m := &store.Manager{
			OrgID: org.ID, Email: email, Name: strings.TrimSpace(n.ManagerName),
			Lang: firstNonEmpty(n.Lang, "fr"), InvitedBy: activityActor(a),
		}
		if _, err := s.store.AddManager(ctx, m); err != nil {
			return nil, err
		}

		if n.SendInvite {
			if err := s.sendInvite(ctx, m, org, s.cfg.SiteName); err != nil {
				return nil, err
			}
		}
	}

	return org, nil
}

// staffSaveOrganization saves the details of an organization as the staff
// edited them: o is its edited copy.
func (s *Server) staffSaveOrganization(ctx context.Context, a *access.Actor, o *store.Organization) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	if o.Name == "" {
		return errBadInput
	}

	if err := s.store.UpdateOrganization(ctx, o); err != nil {
		return err
	}

	s.recordAs(ctx, a, o, nil, "organization.updated", "")

	return nil
}

// deleteOrganization deletes an organization and all it has, once its name
// is typed again as confirm.
func (s *Server) deleteOrganization(ctx context.Context, a *access.Actor, org *store.Organization, confirm string) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	if confirm != org.Name {
		return refuse("flash.confirm_mismatch")
	}

	return s.store.DeleteOrganization(ctx, org.ID)
}

// staffUploadLogo adds a logo file to an organization, as the partner
// does (see uploadLogo).
func (s *Server) staffUploadLogo(ctx context.Context, a *access.Actor, org *store.Organization, variant, file string) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	return s.uploadLogo(ctx, a, org, variant, file)
}

// toggleJob hides a job offer of an organization from the public page, or
// shows it again.
func (s *Server) toggleJob(ctx context.Context, a *access.Actor, org *store.Organization, uid string) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	j, err := s.store.GetJobOffer(ctx, org.ID, uid)
	if err != nil {
		return err
	}

	j.Hidden = !j.Hidden

	return s.store.SaveJobOffer(ctx, j)
}

// --- contacts ---------------------------------------------------------------------

// addManager makes someone a contact of an organization, e-mailed a link
// to come in if sendInvite. An unknown language is French.
func (s *Server) addManager(ctx context.Context, a *access.Actor, org *store.Organization, m *store.Manager, sendInvite bool) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	m.OrgID = org.ID
	m.Email = store.NormalizeEmail(m.Email)
	m.Name = strings.TrimSpace(m.Name)
	m.InvitedBy = activityActor(a)

	if !validLang(m.Lang) {
		m.Lang = "fr"
	}

	if !validEmail(m.Email) {
		return refuse("flash.invalid_email")
	}

	if _, err := s.store.AddManager(ctx, m); err != nil {
		return err
	}

	if sendInvite {
		if err := s.sendInvite(ctx, m, org, s.cfg.SiteName); err != nil {
			return err
		}
	}

	s.recordAs(ctx, a, org, nil, "manager.invited", m.Email)

	return nil
}

// managerEdit is a contact's identity as the staff corrects it; Lang is
// kept when unknown.
type managerEdit struct {
	Email string
	Name  string
	Lang  string
}

// updateManager corrects the name, address and language of a contact of
// an organization. A new address logs the contact out: the account now
// belongs to another address.
func (s *Server) updateManager(ctx context.Context, a *access.Actor, org *store.Organization, m *store.Manager, ed managerEdit) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	if m.OrgID != org.ID {
		return errNotFound
	}

	oldEmail := m.Email

	m.Email = store.NormalizeEmail(ed.Email)
	m.Name = strings.TrimSpace(ed.Name)

	if validLang(ed.Lang) {
		m.Lang = ed.Lang
	}

	if !validEmail(m.Email) {
		return refuse("flash.invalid_email")
	}

	err := s.store.UpdateManagerIdentity(ctx, m)
	if errors.Is(err, store.ErrDuplicate) {
		return refuse("managers.already")
	}

	if err != nil {
		return err
	}

	details := m.Email
	if m.Email != oldEmail {
		if err := s.store.DetachManagerSessions(ctx, m.ID); err != nil {
			return err
		}

		details = oldEmail + " → " + m.Email
	}

	s.recordAs(ctx, a, org, nil, "manager.updated", details)

	return nil
}

// removeManager takes a contact off an organization, and out of the
// sessions they opened for it. The staff may remove the last one.
func (s *Server) removeManager(ctx context.Context, a *access.Actor, org *store.Organization, m *store.Manager) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	if m.OrgID != org.ID {
		return errNotFound
	}

	if err := s.store.DetachManagerSessions(ctx, m.ID); err != nil {
		return err
	}

	if err := s.store.DeleteManager(ctx, org.ID, m.ID); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, nil, "manager.removed", m.Email)

	return nil
}

// sendManagerLink e-mails a contact of an organization a fresh invitation.
func (s *Server) sendManagerLink(ctx context.Context, a *access.Actor, org *store.Organization, m *store.Manager) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	if m.OrgID != org.ID {
		return errNotFound
	}

	return s.sendInvite(ctx, m, org, s.cfg.SiteName)
}

// --- partnerships -------------------------------------------------------------------

// createPartnership attaches an organization to an edition. When it already
// has a partnership, the new one is a renewal: status "contacted", the same
// level (matched by name) and the same staff owner; otherwise it starts as
// a prospect. It tells whether it was a renewal.
func (s *Server) createPartnership(ctx context.Context, a *access.Actor, org *store.Organization, target *store.Event) (bool, error) {
	if err := staffOnly(a); err != nil {
		return false, err
	}

	np := &store.Partnership{OrgID: org.ID, EventID: target.ID, Status: store.StatusProspect}
	action := "partnership.created"

	latest, level, err := s.latestPartnership(ctx, org.ID)
	if err != nil {
		return false, err
	}

	if latest != nil {
		np.Status, np.OwnerAdminID, action = store.StatusContacted, latest.OwnerAdminID, "partnership.renewed"

		if level != nil {
			levels, err := s.store.ListLevels(ctx, target.ID)
			if err != nil {
				return false, err
			}

			for _, l := range levels {
				if strings.EqualFold(l.Name, level.Name) {
					np.LevelID = &l.ID
				}
			}
		}
	}

	err = s.store.CreatePartnership(ctx, np)
	if errors.Is(err, store.ErrDuplicate) {
		return false, refuse("flash.already_renewed")
	}

	if err != nil {
		return false, err
	}

	s.recordAs(ctx, a, org, &np.ID, action, target.Name)

	return latest != nil, nil
}

// partnershipEdit is the follow-up form of a partnership: Level and Owner
// are the UIDs of a level of its edition and of a staff member, empty for
// none; PriceOverride a number of euros, empty for the level's price.
type partnershipEdit struct {
	Status        string
	Level         string
	PriceOverride string
	Owner         string
	PONumber      string
	QuoteSentAt   *time.Time
	QuoteSignedAt *time.Time
	InvoiceSentAt *time.Time
	InvoicePaidAt *time.Time
	FollowupAt    *time.Time
	PartnerNotes  string
	InternalNotes string
}

// savePartnership saves the follow-up of a partnership. A status it does
// not know leaves the status as it was.
func (s *Server) savePartnership(ctx context.Context, a *access.Actor, org *store.Organization, p *store.Partnership, ed partnershipEdit) error {
	if err := staffPartnership(a, org, p); err != nil {
		return err
	}

	if store.ValidStatus(ed.Status) {
		p.Status = ed.Status
	}

	p.LevelID = nil

	if ed.Level != "" {
		l, err := s.store.GetLevelByUID(ctx, p.EventID, ed.Level)
		if err != nil {
			return errBadInput
		}

		p.LevelID = &l.ID
	}

	p.PriceOverride = nil

	if v := strings.TrimSpace(ed.PriceOverride); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return errBadInput
		}

		p.PriceOverride = &n
	}

	p.OwnerAdminID = nil

	if ed.Owner != "" {
		owner, err := s.store.GetAdminByUID(ctx, ed.Owner)
		if err != nil {
			return errBadInput
		}

		p.OwnerAdminID = &owner.ID
	}

	p.PONumber = ed.PONumber
	p.QuoteSentAt = ed.QuoteSentAt
	p.QuoteSignedAt = ed.QuoteSignedAt
	p.InvoiceSentAt = ed.InvoiceSentAt
	p.InvoicePaidAt = ed.InvoicePaidAt
	p.FollowupAt = ed.FollowupAt
	p.PartnerNotes = ed.PartnerNotes
	p.InternalNotes = strings.TrimSpace(ed.InternalNotes)

	if err := s.store.UpdatePartnership(ctx, p); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "partnership.updated", "Statut : "+p.Status)

	return nil
}

// markPartnership records, in one click, that the quote of a partnership
// came back signed ("quote_signed") or its invoice was paid
// ("invoice_paid") today: the commonest updates, which otherwise went
// through the whole follow-up form. A date already set stays.
func (s *Server) markPartnership(ctx context.Context, a *access.Actor, org *store.Organization, p *store.Partnership, what string) error {
	if err := staffPartnership(a, org, p); err != nil {
		return err
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)

	switch what {
	case "quote_signed":
		if p.QuoteSignedAt == nil {
			p.QuoteSignedAt = &today
		}
	case "invoice_paid":
		if p.InvoicePaidAt == nil {
			p.InvoicePaidAt = &today
		}
	default:
		return errBadInput
	}

	if err := s.store.UpdatePartnership(ctx, p); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "partnership.updated", what)

	return nil
}

// setPartnershipDocument sets the quote or the invoice (kind) of a
// partnership, sent today unless a date was set. With notify, the contacts
// hear of it, once the partnership is shown to them.
func (s *Server) setPartnershipDocument(ctx context.Context, a *access.Actor, org *store.Organization, e *store.Event, p *store.Partnership, kind, file string, notify bool) error {
	if err := staffPartnership(a, org, p); err != nil {
		return err
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)

	switch kind {
	case "quote":
		p.QuoteFile = file
		if p.QuoteSentAt == nil {
			p.QuoteSentAt = &today
		}
	case "invoice":
		p.InvoiceFile = file
		if p.InvoiceSentAt == nil {
			p.InvoiceSentAt = &today
		}
	default:
		return errBadInput
	}

	if err := s.store.UpdatePartnership(ctx, p); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "document.uploaded", kind)

	if notify && p.VisibleToPartner() {
		return s.notifyManagers(ctx, org, p, partnerBase(e)+"/documents/"+kind, "mail."+kind+".subject", "mail."+kind+".body")
	}

	return nil
}

// setDonePerks sets the perks of a partnership already delivered, by UID:
// those of its level only, the others are ignored.
func (s *Server) setDonePerks(ctx context.Context, a *access.Actor, org *store.Organization, p *store.Partnership, uids []string) error {
	if err := staffPartnership(a, org, p); err != nil {
		return err
	}

	var ids []int64

	if p.LevelID != nil {
		allowed, err := s.store.ListLevelPerks(ctx, *p.LevelID)
		if err != nil {
			return err
		}

		checked := map[string]bool{}
		for _, v := range uids {
			checked[v] = true
		}

		for _, perk := range allowed {
			if checked[perk.UID] {
				ids = append(ids, perk.ID)
			}
		}
	}

	if err := s.store.SetDonePerks(ctx, p.ID, ids); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "perks.updated", "")

	return nil
}

// staffAddAttendee adds an attendee to a partnership, whatever its level
// allows and after the deadline: the staff settles the exceptions.
func (s *Server) staffAddAttendee(ctx context.Context, a *access.Actor, p *store.Partnership, at *store.Attendee) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	at.PartnershipID = p.ID
	cleanAttendee(at)

	if at.FirstName == "" || at.LastName == "" {
		return errBadInput
	}

	return s.store.AddAttendee(ctx, at)
}

// staffDeleteAttendee takes an attendee off a partnership.
func (s *Server) staffDeleteAttendee(ctx context.Context, a *access.Actor, p *store.Partnership, uid string) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	return s.store.DeleteAttendee(ctx, p.ID, uid)
}

// socialPostEdit is a social network post as the staff plans it.
type socialPostEdit struct {
	Network   string
	PlannedAt *time.Time
	URL       string
	Note      string
}

// saveSocialPost saves a post about a partner, new (an empty post of the
// partnership) or loaded. When it gets its link and notify is set, the
// contacts are e-mailed so they can share it, once the partnership is
// shown to them.
func (s *Server) saveSocialPost(ctx context.Context, a *access.Actor, org *store.Organization, e *store.Event, p *store.Partnership, post *store.SocialPost, ed socialPostEdit, notify bool) error {
	if err := staffPartnership(a, org, p); err != nil {
		return err
	}

	if post.PartnershipID != p.ID {
		return errNotFound
	}

	wasPublished := post.Published()

	post.Network = ed.Network
	post.PlannedAt = ed.PlannedAt
	post.URL = normalizeURL(strings.TrimSpace(ed.URL))
	post.Note = strings.TrimSpace(ed.Note)

	if !store.ValidNetwork(post.Network) {
		return errBadInput
	}

	if err := s.store.SaveSocialPost(ctx, post); err != nil {
		return err
	}

	s.recordAs(ctx, a, org, &p.ID, "social.saved", networkName(post.Network)+" "+post.URL)

	if post.Published() && !wasPublished && notify && p.VisibleToPartner() {
		return s.notifyManagers(ctx, org, p, partnerBase(e)+"/", "mail.post.subject", "mail.post.body", networkName(post.Network), post.URL)
	}

	return nil
}

// deleteSocialPost deletes a post of a partnership.
func (s *Server) deleteSocialPost(ctx context.Context, a *access.Actor, p *store.Partnership, uid string) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	return s.store.DeleteSocialPost(ctx, p.ID, uid)
}

// deletePartnership deletes a partnership and all it has.
func (s *Server) deletePartnership(ctx context.Context, a *access.Actor, p *store.Partnership) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	return s.store.DeletePartnership(ctx, p.ID)
}
