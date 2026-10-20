package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"manage/internal/access"
	"manage/internal/store"
)

// TestStaffServiceRights checks that the staff's services refuse anyone
// else, a contact of the organization acted upon included, whatever route
// they come by.
func TestStaffServiceRights(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	app := e.app
	who := e.partnerActors(f)

	nina := who.volunteer.Person

	ops := map[string]func(a *access.Actor) error{
		"save a person": func(a *access.Actor) error {
			_, err := app.savePerson(ctx, a, nina, personEdit{Name: "X", Email: nina.Email})
			return err
		},
		"send a login link": func(a *access.Actor) error { return app.sendPersonLink(ctx, a, nina) },
		"delete a person":   func(a *access.Actor) error { return app.deletePerson(ctx, a, nina) },
		"set a photo":       func(a *access.Actor) error { return app.setPersonPhoto(ctx, a, nina, "") },
		"save an edition": func(a *access.Actor) error {
			_, err := app.saveEdition(ctx, a, &store.Event{Name: "X", Year: 2030}, "")
			return err
		},
		"set a document":   func(a *access.Actor) error { return app.setEditionDocument(ctx, a, f.event, "guide_fr", "g.pdf") },
		"purge an edition": func(a *access.Actor) error { _, err := app.purgeEdition(ctx, a, f.event); return err },
		"save a level":     func(a *access.Actor) error { return app.saveLevel(ctx, a, f.event, f.level) },
		"delete a level":   func(a *access.Actor) error { return app.deleteLevel(ctx, a, f.event, f.level.UID) },
		"save a perk": func(a *access.Actor) error {
			return app.savePerk(ctx, a, f.event, &store.Perk{EventID: f.event.ID, NameFR: "X"})
		},
		"set the matrix": func(a *access.Actor) error { return app.saveLevelPerks(ctx, a, f.event, nil) },
		"create an org": func(a *access.Actor) error {
			_, err := app.createOrganization(ctx, a, newOrganization{Name: "X"})
			return err
		},
		"save an org":   func(a *access.Actor) error { o := *f.acme; return app.staffSaveOrganization(ctx, a, &o) },
		"delete an org": func(a *access.Actor) error { return app.deleteOrganization(ctx, a, f.acme, "Acme") },
		"add a contact": func(a *access.Actor) error {
			return app.addManager(ctx, a, f.acme, &store.Manager{Email: "x@acme.test"}, false)
		},
		"change a contact": func(a *access.Actor) error {
			return app.updateManager(ctx, a, f.acme, f.alice, managerEdit{Email: "x@acme.test"})
		},
		"remove a contact":    func(a *access.Actor) error { return app.removeManager(ctx, a, f.acme, f.alice) },
		"send a contact link": func(a *access.Actor) error { return app.sendManagerLink(ctx, a, f.acme, f.alice) },
		"upload a logo":       func(a *access.Actor) error { return app.staffUploadLogo(ctx, a, f.acme, "", "l.png") },
		"toggle a job":        func(a *access.Actor) error { return app.toggleJob(ctx, a, f.acme, "none") },
		"create a partnership": func(a *access.Actor) error {
			_, err := app.createPartnership(ctx, a, f.acme, f.event)
			return err
		},
		"save a partnership": func(a *access.Actor) error { return app.savePartnership(ctx, a, f.acme, f.acmeP, partnershipEdit{}) },
		"mark a partnership": func(a *access.Actor) error { return app.markPartnership(ctx, a, f.acme, f.acmeP, "invoice_paid") },
		"send a document": func(a *access.Actor) error {
			return app.setPartnershipDocument(ctx, a, f.acme, f.event, f.acmeP, "quote", "q.pdf", false)
		},
		"tick perks": func(a *access.Actor) error { return app.setDonePerks(ctx, a, f.acme, f.acmeP, nil) },
		"add an attendee": func(a *access.Actor) error {
			return app.staffAddAttendee(ctx, a, f.acmeP, &store.Attendee{FirstName: "X", LastName: "Y"})
		},
		"delete an attendee": func(a *access.Actor) error { return app.staffDeleteAttendee(ctx, a, f.acmeP, "none") },
		"save a post": func(a *access.Actor) error {
			return app.saveSocialPost(ctx, a, f.acme, f.event, f.acmeP, &store.SocialPost{PartnershipID: f.acmeP.ID}, socialPostEdit{Network: "linkedin"}, false)
		},
		"delete a post":        func(a *access.Actor) error { return app.deleteSocialPost(ctx, a, f.acmeP, "none") },
		"delete a partnership": func(a *access.Actor) error { return app.deletePartnership(ctx, a, f.acmeP) },
	}

	for op, do := range ops {
		for name, a := range map[string]*access.Actor{"the organization's contact": who.alice, "a volunteer": who.volunteer, "nobody": {}} {
			if err := do(a); !errors.Is(err, errForbidden) {
				t.Errorf("%s may %s: %v", name, op, err)
			}
		}
	}

	// Nothing moved.
	if p, err := e.st.GetPartnershipByOrgEvent(ctx, f.acme.ID, f.event.ID); err != nil || p.InvoicePaidAt != nil {
		t.Errorf("the partnership changed: %+v, %v", p, err)
	}
}

// TestStaffServicePartners drives the staff's work with the partners.
func TestStaffServicePartners(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	app := e.app
	staff := e.partnerActors(f).staff

	// An organization with its partnership and first contact, invited.
	org, err := app.createOrganization(ctx, staff, newOrganization{
		Name: " Globex ", Event: f.event.Slug, Status: "bogus", ManagerEmail: "Hank@Globex.test", SendInvite: true,
	})
	mustOK(t, err)

	if org.Name != "Globex" {
		t.Errorf("organization: %+v", org)
	}

	p, err := e.st.GetPartnershipByOrgEvent(ctx, org.ID, f.event.ID)
	mustOK(t, err)

	if p.Status != store.StatusProspect {
		t.Errorf("status: %q", p.Status)
	}

	e.mailed("hank@globex.test", "Globex", "/auth/")

	// A bad address is refused once the organization exists.
	org2, err := app.createOrganization(ctx, staff, newOrganization{Name: "Initech", ManagerEmail: "nope"})
	if rf, ok := refused(err); !ok || rf.Key != "flash.invalid_email" || org2 == nil || org2.ID == 0 {
		t.Errorf("a bad first contact: %v, %+v", err, org2)
	}

	// Contacts.
	if rf, ok := refused(app.addManager(ctx, staff, f.acme, &store.Manager{Email: "nope"}, false)); !ok || rf.Key != "flash.invalid_email" {
		t.Error("a contact without an address")
	}

	dave := &store.Manager{Email: "dave@acme.test", Lang: "xx"}
	mustOK(t, app.addManager(ctx, staff, f.acme, dave, true))
	e.mailed("dave@acme.test", "/auth/")

	if dave.Lang != "fr" || dave.InvitedBy != "staff:staff@conf.test" {
		t.Errorf("contact: %+v", dave)
	}

	if !e.logged(f.acme, "manager.invited", "staff:staff@conf.test") {
		t.Error("no activity for the contact added")
	}

	err = app.updateManager(ctx, staff, f.acme, dave, managerEdit{Email: "alice@acme.test"})
	if rf, ok := refused(err); !ok || rf.Key != "managers.already" {
		t.Errorf("a contact made another: %v", err)
	}

	if err := app.removeManager(ctx, staff, f.prospect, f.alice); !errors.Is(err, errNotFound) {
		t.Errorf("removed through another organization: %v", err)
	}

	// The follow-up of a partnership.
	mustOK(t, app.savePartnership(ctx, staff, f.prospect, f.prosP, partnershipEdit{Status: store.StatusNegotiating, Level: f.level.UID, PriceOverride: "1500"}))

	got, err := e.st.GetPartnershipByOrgEvent(ctx, f.prospect.ID, f.event.ID)
	mustOK(t, err)

	if got.Status != store.StatusNegotiating || got.LevelID == nil || *got.LevelID != f.level.ID || got.PriceOverride == nil || *got.PriceOverride != 1500 {
		t.Errorf("follow-up: %+v", got)
	}

	if !e.logged(f.prospect, "partnership.updated", "staff:staff@conf.test") {
		t.Error("no activity for the follow-up")
	}

	if err := app.savePartnership(ctx, staff, f.prospect, f.prosP, partnershipEdit{PriceOverride: "-1"}); !errors.Is(err, errBadInput) {
		t.Errorf("a negative price: %v", err)
	}

	if err := app.savePartnership(ctx, staff, f.acme, f.prosP, partnershipEdit{}); !errors.Is(err, errNotFound) {
		t.Errorf("a partnership through another organization: %v", err)
	}

	if err := app.markPartnership(ctx, staff, f.acme, f.acmeP, "nonsense"); !errors.Is(err, errBadInput) {
		t.Errorf("marked nonsense: %v", err)
	}

	mustOK(t, app.markPartnership(ctx, staff, f.acme, f.acmeP, "quote_signed"))

	if f.acmeP.QuoteSignedAt == nil {
		t.Error("quote not marked signed")
	}

	// A quote, its contacts told.
	mustOK(t, app.setPartnershipDocument(ctx, staff, f.acme, f.event, f.acmeP, "quote", "q.pdf", true))
	e.mailed("alice@acme.test", "devis", "/ec-2026/partner/documents/quote")

	if f.acmeP.QuoteFile != "q.pdf" || f.acmeP.QuoteSentAt == nil {
		t.Errorf("quote: %+v", f.acmeP)
	}

	// A post, once published, told too.
	post := &store.SocialPost{PartnershipID: f.acmeP.ID}
	mustOK(t, app.saveSocialPost(ctx, staff, f.acme, f.event, f.acmeP, post, socialPostEdit{Network: "mastodon", URL: "toot.test/1"}, true))
	e.mailed("alice@acme.test", "Mastodon", "https://toot.test/1")

	if err := app.saveSocialPost(ctx, staff, f.acme, f.event, f.acmeP, post, socialPostEdit{Network: "myspace"}, false); !errors.Is(err, errBadInput) {
		t.Errorf("an unknown network: %v", err)
	}

	// The staff adds attendees after the deadline too.
	past := time.Now().AddDate(0, 0, -3)
	f.event.AttendeesDeadline = &past

	mustOK(t, app.staffAddAttendee(ctx, staff, f.acmeP, &store.Attendee{FirstName: "Late", LastName: "Comer"}))

	if list, _ := e.st.ListAttendees(ctx, f.acmeP.ID); len(list) != 1 {
		t.Errorf("attendees: %+v", list)
	}

	// A renewal keeps the owner and the level by name; twice, it is refused.
	next := &store.Event{Slug: "ec-2027", Name: "EC 2027", Year: 2027}
	mustOK(t, e.st.SaveEvent(ctx, next))

	or := &store.Level{EventID: next.ID, Name: "or"}
	mustOK(t, e.st.SaveLevel(ctx, or))

	renewed, err := app.createPartnership(ctx, staff, f.acme, next)
	mustOK(t, err)

	np, err := e.st.GetPartnershipByOrgEvent(ctx, f.acme.ID, next.ID)
	mustOK(t, err)

	if !renewed || np.Status != store.StatusContacted || np.LevelID == nil || *np.LevelID != or.ID {
		t.Errorf("renewal: %v %+v", renewed, np)
	}

	if !e.logged(f.acme, "partnership.renewed", "staff:staff@conf.test") {
		t.Error("no activity for the renewal")
	}

	if _, err := app.createPartnership(ctx, staff, f.acme, next); err == nil {
		t.Error("renewed twice")
	} else if rf, ok := refused(err); !ok || rf.Key != "flash.already_renewed" {
		t.Errorf("renewed twice: %v", err)
	}

	// Deleting an organization takes its name.
	if rf, ok := refused(app.deleteOrganization(ctx, staff, org, "Globe")); !ok || rf.Key != "flash.confirm_mismatch" {
		t.Error("deleted without its name")
	}

	mustOK(t, app.deleteOrganization(ctx, staff, org, "Globex"))

	if _, err := e.st.GetOrganization(ctx, org.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("organization still there: %v", err)
	}
}

// TestStaffServiceEditions drives the editions, their levels and perks.
func TestStaffServiceEditions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	app := e.app
	staff := e.partnerActors(f).staff

	if rf, ok := refused(func() error { _, err := app.saveEdition(ctx, staff, &store.Event{Name: " "}, ""); return err }()); !ok || rf.Key == "" {
		t.Error("an edition without a name")
	}

	// A slug of the site's own takes the year; the levels come along.
	ne := &store.Event{Name: "Admin", Year: 2031}

	cloned, err := app.saveEdition(ctx, staff, ne, f.event.Slug)
	mustOK(t, err)

	if !cloned || ne.Slug != "admin-2031" {
		t.Errorf("edition: %v %q", cloned, ne.Slug)
	}

	if levels, _ := e.st.ListLevels(ctx, ne.ID); len(levels) != 1 || levels[0].Name != "Or" {
		t.Errorf("levels copied: %+v", levels)
	}

	// Copying again finds everything there.
	if copied, err := app.cloneLevels(ctx, staff, ne, f.event); err != nil || copied {
		t.Errorf("copied again: %v %v", copied, err)
	}

	if _, err := app.cloneLevels(ctx, staff, ne, ne); !errors.Is(err, errBadInput) {
		t.Errorf("copied onto itself: %v", err)
	}

	if err := app.setEditionDocument(ctx, staff, f.event, "poster", "p.pdf"); !errors.Is(err, errBadInput) {
		t.Errorf("an unknown document: %v", err)
	}

	// Levels, perks, and which goes with which.
	if err := app.saveLevel(ctx, staff, f.event, &store.Level{EventID: f.event.ID, Name: " "}); !errors.Is(err, errBadInput) {
		t.Errorf("a level without a name: %v", err)
	}

	if err := app.saveLevel(ctx, staff, ne, f.level); !errors.Is(err, errNotFound) {
		t.Errorf("a level through another edition: %v", err)
	}

	silver := &store.Level{EventID: f.event.ID, Name: " Argent ", Color: " #AABBCC "}
	mustOK(t, app.saveLevel(ctx, staff, f.event, silver))

	if silver.Name != "Argent" || silver.Color != "#aabbcc" {
		t.Errorf("level: %+v", silver)
	}

	booth := &store.Perk{EventID: f.event.ID, NameFR: "Stand"}
	mustOK(t, app.savePerk(ctx, staff, f.event, booth))
	mustOK(t, app.saveLevelPerks(ctx, staff, f.event, []string{f.level.UID + ":" + booth.UID, "bogus:" + booth.UID}))

	perks, err := e.st.ListLevelPerks(ctx, f.level.ID)
	mustOK(t, err)

	if len(perks) != 1 || perks[0].ID != booth.ID {
		t.Errorf("perks of the level: %+v", perks)
	}

	// Ticked perks are those of the level only.
	mustOK(t, app.setDonePerks(ctx, staff, f.acme, f.acmeP, []string{booth.UID, "bogus"}))

	if done, _ := e.st.DonePerks(ctx, f.acmeP.ID); len(done) != 1 {
		t.Errorf("done perks: %v", done)
	}

	mustOK(t, app.deletePerk(ctx, staff, f.event, booth.UID))
	mustOK(t, app.deleteLevel(ctx, staff, f.event, silver.UID))

	// An edition not over keeps its data.
	if _, err := app.purgeEdition(ctx, staff, f.event); err == nil {
		t.Error("purged an edition not over")
	} else if rf, ok := refused(err); !ok || rf.Key != "flash.event_not_over" {
		t.Errorf("purged an edition not over: %v", err)
	}
}

// TestStaffServicePeople drives the people's details for the staff.
func TestStaffServicePeople(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	app := e.app
	who := e.partnerActors(f)
	staff := who.staff

	nina := who.volunteer.Person

	if rf, ok := refused(func() error {
		_, err := app.savePerson(ctx, staff, nina, personEdit{Name: "Nina", Email: "nope"})
		return err
	}()); !ok || rf.Key != "flash.invalid_email" {
		t.Error("a person without an address")
	}

	_, err := app.savePerson(ctx, staff, nina, personEdit{Name: "Nina", Email: "alice@acme.test", Lang: "fr"})
	if rf, ok := refused(err); !ok || rf.Key != "flash.person_email_taken" || rf.Args[0] != "alice@acme.test" {
		t.Errorf("another's address: %v", err)
	}

	moved, err := app.savePerson(ctx, staff, nina, personEdit{Name: " Nina S. ", Email: "Nina@Other.test", Lang: "en"})
	mustOK(t, err)

	if got, _ := e.st.GetPerson(ctx, nina.ID); !moved || got.Email != "nina@other.test" || got.Name != "Nina S." {
		t.Errorf("person: %v %+v", moved, got)
	}

	// A staff member's address stays.
	moved, err = app.savePerson(ctx, staff, staff.Person, personEdit{Name: "Boss", Email: "else@conf.test", Lang: "fr"})
	mustOK(t, err)

	if moved || staff.Person.Email != "staff@conf.test" {
		t.Errorf("staff address: %v %q", moved, staff.Person.Email)
	}

	mustOK(t, app.sendPersonLink(ctx, staff, nina))
	e.mailed("nina@other.test", "/auth/")

	mustOK(t, app.setPersonPhoto(ctx, staff, nina, ""))

	if rf, ok := refused(app.deletePerson(ctx, staff, staff.Person)); !ok || rf.Key != "flash.person_staff" {
		t.Error("deleted a staff member")
	}

	mustOK(t, app.deletePerson(ctx, staff, nina))

	if _, err := e.st.GetPerson(ctx, nina.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("person still there: %v", err)
	}
}
