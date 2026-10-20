package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"manage/internal/access"
	"manage/internal/store"
)

// partnerActors are the actors of the partner fixture: a contact of each
// organization, a volunteer, and the staff as a person (whose activity
// says "staff:").
type partnerActors struct {
	alice, bob, volunteer, staff *access.Actor
}

func (e *env) partnerActors(f *fixture) partnerActors {
	e.t.Helper()

	ctx := context.Background()

	load := func(p *store.Person, staff bool) *access.Actor {
		a, err := access.Load(ctx, e.st, p, staff)
		mustOK(e.t, err)

		return a
	}

	person := func(id int64) *store.Person {
		p, err := e.st.GetPerson(ctx, id)
		mustOK(e.t, err)

		return p
	}

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(e.t, err)

	admin, err := e.st.UpsertAdmin(ctx, "staff@conf.test", "Staff")
	mustOK(e.t, err)

	return partnerActors{
		alice:     load(person(f.alice.PersonID), false),
		bob:       load(person(f.bob.PersonID), false),
		volunteer: load(nina, false),
		staff:     load(person(admin.PersonID), true),
	}
}

// logged tells whether an organization's activity has an entry of action
// by who.
func (e *env) logged(org *store.Organization, action, who string) bool {
	e.t.Helper()

	acts, err := e.st.ListActivity(context.Background(), org.ID, 100)
	mustOK(e.t, err)

	for _, a := range acts {
		if a.Action == action && a.Actor == who {
			return true
		}
	}

	return false
}

// TestPartnerServiceRights drives the partner's space without HTTP: who
// may act for which organization, on which partnership.
func TestPartnerServiceRights(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	app := e.app
	who := e.partnerActors(f)

	// Acting for an organization takes being its contact, or the staff.
	for name, a := range map[string]*access.Actor{"another contact": who.bob, "a volunteer": who.volunteer} {
		if err := app.savePartnerNotes(ctx, a, f.acme, f.acmeP, "PO", "x"); !errors.Is(err, errForbidden) {
			t.Errorf("%s writes notes: %v", name, err)
		}

		o := *f.acme
		if err := app.saveOrganization(ctx, a, &o); !errors.Is(err, errForbidden) {
			t.Errorf("%s changes the organization: %v", name, err)
		}

		if err := app.addAttendee(ctx, a, f.acme, f.event, f.acmeP, &store.Attendee{FirstName: "X", LastName: "Y"}); !errors.Is(err, errForbidden) {
			t.Errorf("%s adds an attendee: %v", name, err)
		}

		if err := app.saveJob(ctx, a, f.acme, &store.JobOffer{OrgID: f.acme.ID, Title: "Dev", URL: "x.test"}); !errors.Is(err, errForbidden) {
			t.Errorf("%s posts a job: %v", name, err)
		}

		if err := app.inviteColleague(ctx, a, f.acme, &store.Manager{Email: "eve@evil.test"}); !errors.Is(err, errForbidden) {
			t.Errorf("%s invites a contact: %v", name, err)
		}

		if err := app.uploadLogo(ctx, a, f.acme, store.LogoDefault, "logo.png"); !errors.Is(err, errForbidden) {
			t.Errorf("%s uploads a logo: %v", name, err)
		}
	}

	// Nor does the partnership of an organization go with another's.
	if err := app.savePartnerNotes(ctx, who.alice, f.acme, f.prosP, "PO", "x"); !errors.Is(err, errNotFound) {
		t.Errorf("notes on another's partnership: %v", err)
	}

	// A partnership the staff prepares does not exist for its contacts.
	if err := app.savePartnerNotes(ctx, who.bob, f.prospect, f.prosP, "PO", "x"); !errors.Is(err, errNotFound) {
		t.Errorf("notes on a hidden partnership: %v", err)
	}

	if err := app.addAttendee(ctx, who.bob, f.prospect, f.event, f.prosP, &store.Attendee{FirstName: "X", LastName: "Y"}); !errors.Is(err, errNotFound) {
		t.Errorf("an attendee of a hidden partnership: %v", err)
	}

	mustOK(t, app.savePartnerNotes(ctx, who.staff, f.prospect, f.prosP, "PO-1", "prepared"))

	// No quote sent, none to send back.
	if err := app.sendSignedQuote(ctx, who.alice, f.acme, f.event, f.acmeP, "signed.pdf"); !errors.Is(err, errNotFound) {
		t.Errorf("a signed quote without a quote: %v", err)
	}

	// Someone's profile is their own: not another contact's, nor the
	// staff's seeing the space.
	if err := app.saveContactProfile(ctx, who.alice, f.prospect, f.bob, "Bobby", "en"); !errors.Is(err, errForbidden) {
		t.Errorf("a contact renames another's: %v", err)
	}

	if err := app.saveContactProfile(ctx, who.staff, f.acme, f.alice, "Al", "en"); !errors.Is(err, errForbidden) {
		t.Errorf("the staff renames a contact as the partner: %v", err)
	}

	// Choosing an organization takes being its contact.
	if err := app.chooseOrganization(ctx, who.bob, "token", f.alice); !errors.Is(err, errForbidden) {
		t.Errorf("switched to another's organization: %v", err)
	}
}

// TestPartnerServiceAttendees drives the attendees: the level's limit, and
// the deadline, which binds the contacts but not the staff.
func TestPartnerServiceAttendees(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	app := e.app
	who := e.partnerActors(f)

	f.level.MaxAttendees = 1
	mustOK(t, e.st.SaveLevel(ctx, f.level))

	err := app.addAttendee(ctx, who.alice, f.acme, f.event, f.acmeP, &store.Attendee{FirstName: " Ann ", Email: "nope"})
	if rf, ok := refused(err); !ok || rf.Key != "attendees.name_required" {
		t.Errorf("an attendee without a name: %v", err)
	}

	ann := &store.Attendee{FirstName: " Ann ", LastName: "Lee", Role: "anything"}
	mustOK(t, app.addAttendee(ctx, who.alice, f.acme, f.event, f.acmeP, ann))

	list, err := e.st.ListAttendees(ctx, f.acmeP.ID)
	mustOK(t, err)

	if len(list) != 1 || list[0].FirstName != "Ann" || list[0].Role != store.RoleBooth {
		t.Fatalf("attendees: %+v", list)
	}

	if !e.logged(f.acme, "attendee.added", "alice@acme.test") {
		t.Error("no activity for the attendee added")
	}

	// The level takes one.
	err = app.addAttendee(ctx, who.alice, f.acme, f.event, f.acmeP, &store.Attendee{FirstName: "Bo", LastName: "Ng"})
	if rf, ok := refused(err); !ok || rf.Key != "attendees.full" {
		t.Errorf("past the level's limit: %v", err)
	}

	// Once the list is closed, a contact changes the diet only, and adds
	// or deletes nobody; the staff still does.
	past := time.Now().AddDate(0, 0, -3)
	f.event.AttendeesDeadline = &past

	mustOK(t, app.updateAttendee(ctx, who.alice, f.acme, f.event, f.acmeP, &store.Attendee{UID: list[0].UID, FirstName: "Changed", LastName: "Name", Dietary: "Vegan"}))

	list, err = e.st.ListAttendees(ctx, f.acmeP.ID)
	mustOK(t, err)

	if list[0].FirstName != "Ann" || list[0].Dietary != "Vegan" {
		t.Errorf("changed once closed: %+v", list[0])
	}

	if err := app.addAttendee(ctx, who.alice, f.acme, f.event, f.acmeP, &store.Attendee{FirstName: "Bo", LastName: "Ng"}); !errors.Is(err, errForbidden) {
		t.Errorf("added once closed: %v", err)
	}

	if err := app.deleteAttendee(ctx, who.alice, f.acme, f.event, f.acmeP, list[0].UID); !errors.Is(err, errForbidden) {
		t.Errorf("deleted once closed: %v", err)
	}

	// A volunteer changes not even the diet.
	if err := app.updateAttendee(ctx, who.volunteer, f.acme, f.event, f.acmeP, &store.Attendee{UID: list[0].UID, Dietary: "None"}); !errors.Is(err, errForbidden) {
		t.Errorf("a volunteer changes a diet: %v", err)
	}

	mustOK(t, app.updateAttendee(ctx, who.staff, f.acme, f.event, f.acmeP, &store.Attendee{UID: list[0].UID, FirstName: "Anna", LastName: "Lee"}))
	mustOK(t, app.deleteAttendee(ctx, who.staff, f.acme, f.event, f.acmeP, list[0].UID))

	if list, _ := e.st.ListAttendees(ctx, f.acmeP.ID); len(list) != 0 {
		t.Errorf("the staff did not delete: %+v", list)
	}

	if !e.logged(f.acme, "attendee.deleted", "staff:staff@conf.test") {
		t.Error("no activity for the staff's deletion")
	}
}

// TestPartnerServiceSpace drives the rest of the space: the organization,
// its logo, jobs and contacts.
func TestPartnerServiceSpace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	app := e.app
	who := e.partnerActors(f)

	// Notes.
	mustOK(t, app.savePartnerNotes(ctx, who.alice, f.acme, f.acmeP, "PO-42", "Hello"))

	if p, _ := e.st.GetPartnershipByOrgEvent(ctx, f.acme.ID, f.event.ID); p.PONumber != "PO-42" || p.PartnerNotes != "Hello" {
		t.Errorf("notes: %+v", p)
	}

	if !e.logged(f.acme, "partnership.notes", "alice@acme.test") {
		t.Error("no activity for the notes")
	}

	// The organization needs a name.
	o := *f.acme
	o.Name = ""

	if rf, ok := refused(app.saveOrganization(ctx, who.alice, &o)); !ok || rf.Key != "organization.name_required" {
		t.Error("an organization without a name")
	}

	o.Name, o.City = "Acme", "Toulouse"
	mustOK(t, app.saveOrganization(ctx, who.alice, &o))

	if got, _ := e.st.GetOrganization(ctx, f.acme.ID); got.City != "Toulouse" {
		t.Errorf("organization: %+v", got)
	}

	// A logo is the contact's upload.
	mustOK(t, app.uploadLogo(ctx, who.alice, f.acme, "whatever", "logo.png"))

	logos, err := e.st.ListLogos(ctx, f.acme.ID)
	mustOK(t, err)

	if len(logos) != 1 || logos[0].Variant != store.LogoDefault || logos[0].UploadedBy != "alice@acme.test" {
		t.Errorf("logos: %+v", logos)
	}

	// A job needs a title, then a link or a description.
	j := &store.JobOffer{OrgID: f.acme.ID, Title: "Dev"}
	if rf, ok := refused(app.saveJob(ctx, who.alice, f.acme, j)); !ok || rf.Key != "jobs.url_or_description" {
		t.Error("a job saying nothing")
	}

	j.URL = "jobs.acme.test"
	mustOK(t, app.saveJob(ctx, who.alice, f.acme, j))

	if j.URL != "https://jobs.acme.test" {
		t.Errorf("job link: %q", j.URL)
	}

	if err := app.saveJob(ctx, who.bob, f.prospect, j); !errors.Is(err, errNotFound) {
		t.Errorf("saved another's job: %v", err)
	}

	if err := app.deleteJob(ctx, who.bob, f.acme, j.UID); !errors.Is(err, errForbidden) {
		t.Errorf("another's contact deletes a job: %v", err)
	}

	mustOK(t, app.deleteJob(ctx, who.alice, f.acme, j.UID))

	// A colleague is invited by name, once.
	carol := &store.Manager{Email: " Carol@Acme.test ", Lang: "en"}
	mustOK(t, app.inviteColleague(ctx, who.alice, f.acme, carol))
	e.mailed("carol@acme.test", "alice@acme.test invites you", "Acme")

	if !e.logged(f.acme, "manager.invited", "alice@acme.test") {
		t.Error("no activity for the invitation")
	}

	if rf, ok := refused(app.inviteColleague(ctx, who.alice, f.acme, &store.Manager{Email: "carol@acme.test"})); !ok || rf.Key != "managers.already" {
		t.Error("invited twice")
	}

	if err := app.resendInvitation(ctx, who.alice, f.acme, f.bob.UID); !errors.Is(err, errNotFound) {
		t.Errorf("a link to another's contact: %v", err)
	}

	// Seen by the staff, an invitation comes from the organization.
	mustOK(t, app.resendInvitation(ctx, who.staff, f.acme, carol.UID))
	e.mailed("carol@acme.test", "Acme invites you")

	// The last contact stays.
	gone, err := app.removeColleague(ctx, who.alice, f.acme, carol.UID)
	mustOK(t, err)

	if gone.ID != carol.ID {
		t.Errorf("removed %+v", gone)
	}

	if _, err := app.removeColleague(ctx, who.alice, f.acme, f.alice.UID); err == nil {
		t.Error("removed the last contact")
	} else if rf, ok := refused(err); !ok || rf.Key != "flash.last_manager" {
		t.Errorf("the last contact: %v", err)
	}

	// One's own profile.
	mustOK(t, app.saveContactProfile(ctx, who.alice, f.acme, f.alice, " Alice ", "en"))

	if m, _ := e.st.GetManager(ctx, f.alice.ID); m.Name != "Alice" || m.Lang != "en" {
		t.Errorf("profile: %+v", m)
	}
}
