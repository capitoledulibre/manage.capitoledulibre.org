package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

func TestDue(t *testing.T) {
	now := time.Now().In(paris)
	day := func(n int) time.Time { return time.Date(now.Year(), now.Month(), now.Day()+n, 0, 0, 0, 0, time.UTC) }

	for _, c := range []struct {
		in        time.Time
		text, red string
	}{
		{day(-1), "échéance passée", "text-red-700"},
		{day(0), "dernier jour aujourd'hui", "text-red-700"},
		{day(3), "dans 3 jours", "text-amber-800"},
		{day(30), "avant le", "text-gray-600"},
	} {
		got := due("fr", c.in)
		if !strings.Contains(got.Text, c.text) || !strings.Contains(got.Class, c.red) {
			t.Errorf("due(%s) = %+v", c.in.Format(time.DateOnly), got)
		}
	}
}

func TestPartnerTodosSayWhatIsMissing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	alice := e.loginAs("alice@acme.test")

	org, _ := e.st.GetOrganization(ctx, f.acme.ID)
	org.LegalName, org.AddressLine1, org.City, org.ZipCode = "Acme SAS", "1 rue X", "Toulouse", ""
	mustOK(t, e.st.UpdateOrganization(ctx, org))

	_, body := e.get(alice, "/partners/")
	mustContain(t, body, "Manque\u00a0: Code postal", ">Envoyer le logo<", `role="progressbar"`, `aria-current="page"`, "À faire\u00a0:")

	_, body = e.get(alice, "/"+f.event.Slug+"/partner/organization")
	mustContain(t, body, "pour la facture", `autocomplete="postal-code"`)
}

func TestPartnerManagersPage(t *testing.T) {
	e := newEnv(t)
	f := e.fixture(nil)
	alice := e.loginAs("alice@acme.test")

	// Alone, she is marked as herself and cannot remove the last manager.
	_, body := e.get(alice, "/"+f.event.Slug+"/partner/managers")
	mustContain(t, body, ">vous<")

	if strings.Contains(body, "/delete") {
		t.Error("the last manager can be removed")
	}

	// A colleague who never logged in can be sent a new link.
	e.post(alice, "/"+f.event.Slug+"/partner/managers", url.Values{"email": {"dave@acme.test"}, "lang": {"fr"}})

	_, body = e.get(alice, "/"+f.event.Slug+"/partner/managers")
	mustContain(t, body, "Renvoyer l&#39;invitation", "Vous retirer ?", "Retirer dave@acme.test ?")

	before := e.mailer.count("dave@acme.test")

	m, _ := e.st.ListManagers(context.Background(), f.acme.ID)
	for _, x := range m {
		if x.Email == "dave@acme.test" {
			e.post(alice, "/"+f.event.Slug+"/partner/managers/"+x.UID+"/send-link", nil)
		}
	}

	waitFor(t, func() bool { return e.mailer.count("dave@acme.test") == before+1 })
}

func TestPipelineFollowupsAndMarks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	admin := e.adminClient()
	pid := partnershipURL(f.acme, f.event)

	// A follow-up due shows at the top, in red.
	yesterday := time.Now().AddDate(0, 0, -1).Format(time.DateOnly)
	e.post(admin, pid, url.Values{"status": {"confirmed"}, "level": {f.level.UID}, "followup_at": {yesterday}, "quote_sent_at": {yesterday}})

	_, body := e.get(admin, "/"+f.event.Slug+"/partners/")
	mustContain(t, body, "À relancer (1)", "font-medium text-red-700", `data-filter=".pipeline-table"`)

	// The quote comes back signed: one click.
	_, body = e.get(admin, pid)
	mustContain(t, body, "Marquer signé aujourd")

	e.post(admin, pid+"/mark", url.Values{"what": {"quote_signed"}})

	if p, _ := e.st.GetPartnership(ctx, f.acmeP.ID); p.QuoteSignedAt == nil {
		t.Error("quote not marked signed")
	}

	if code, _ := e.post(admin, pid+"/mark", url.Values{"what": {"bogus"}}); code != 400 {
		t.Errorf("bad mark: %d", code)
	}

	_, body = e.get(admin, "/admin/organizations")
	mustContain(t, body, `data-filter="#orgs-table"`)
}

func TestPartnerEditsAttendee(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	alice := e.loginAs("alice@acme.test")

	e.post(alice, "/"+f.event.Slug+"/partner/attendees", url.Values{"first_name": {"Bob"}, "last_name": {"Martn"}, "role": {"booth"}})

	list, _ := e.st.ListAttendees(ctx, f.acmeP.ID)
	if len(list) != 1 {
		t.Fatalf("attendees: %d", len(list))
	}

	bob := list[0]

	// The level allows one: editing is still possible, prefilled.
	_, body := e.get(alice, "/"+f.event.Slug+"/partner/attendees")
	mustContain(t, body, "edit="+bob.UID)

	_, body = e.get(alice, "/"+f.event.Slug+"/partner/attendees?edit="+bob.UID)
	mustContain(t, body, "Modifier Bob", `action="/ec-2026/partner/attendees/`+bob.UID+`"`, `value="Martn"`)

	_, body = e.post(alice, "/"+f.event.Slug+"/partner/attendees/"+bob.UID, url.Values{"first_name": {"Bob"}, "last_name": {""}})
	mustContain(t, body, "Modifier Bob")

	e.post(alice, "/"+f.event.Slug+"/partner/attendees/"+bob.UID, url.Values{"first_name": {"Bob"}, "last_name": {"Martin"}, "role": {"visitor"}, "dietary": {"végétarien"}})

	list, _ = e.st.ListAttendees(ctx, f.acmeP.ID)
	if got := list[0]; got.LastName != "Martin" || got.Role != "visitor" || got.Dietary != "végétarien" {
		t.Fatalf("not updated: %+v", got)
	}

	// Someone else's attendee is out of reach.
	if code, _ := e.post(alice, "/"+f.event.Slug+"/partner/attendees/not-a-uid", url.Values{"first_name": {"X"}, "last_name": {"Y"}}); code != 404 {
		t.Errorf("unknown attendee: %d", code)
	}
}

func TestPartnerSendsSignedQuote(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.fixture(nil)
	admin := e.adminClient()
	alice := e.loginAs("alice@acme.test")
	signed := "/" + f.event.Slug + "/partner/documents/signed-quote"
	pdf := []byte("%PDF-1.4\n%signed\n")

	// No quote sent yet: nothing to send back.
	if code, _ := e.upload(alice, signed, nil, "file", "devis.pdf", pdf); code != 404 {
		t.Errorf("before the quote: %d", code)
	}

	e.post(admin, partnershipURL(f.acme, f.event), url.Values{"status": {"confirmed"}, "level": {f.level.UID}, "quote_sent_at": {"2026-09-10"}})

	_, body := e.get(alice, "/partners/")
	mustContain(t, body, "Envoyer le devis signé", "Déposez-le signé")

	_, body = e.upload(alice, signed, nil, "file", "devis.pdf", pdf)
	mustContain(t, body, "le devis signé est bien reçu", "Voir le devis signé envoyé", "en cours de vérification")

	if strings.Contains(body, "Nous retourner le devis signé") {
		t.Error("the to-do still asks for the signed quote")
	}

	// It waits for the staff to check it.
	p, _ := e.st.GetPartnership(ctx, f.acmeP.ID)
	if p.SignedQuoteFile == "" || p.QuoteSignedAt != nil {
		t.Fatalf("signed quote: %+v", p)
	}

	if code, got := e.get(alice, "/"+f.event.Slug+"/partner/documents/signed_quote"); code != 200 || !strings.Contains(got, "%signed") {
		t.Errorf("download: %d", code)
	}

	_, body = e.get(admin, partnershipURL(f.acme, f.event))
	mustContain(t, body, "à vérifier", p.SignedQuoteFile, "Valider la signature")

	_, body = e.get(admin, "/"+f.event.Slug+"/partners/")
	mustContain(t, body, "Signé, à valider")

	e.post(admin, partnershipURL(f.acme, f.event)+"/mark", url.Values{"what": {"quote_signed"}})

	if p, _ = e.st.GetPartnership(ctx, f.acmeP.ID); p.QuoteSignedAt == nil {
		t.Error("the staff's check does not mark it signed")
	}
}

func TestDietaryAfterLock(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	past := time.Now().AddDate(0, 0, -3)
	f := e.fixture(&past)
	alice := e.loginAs("alice@acme.test")

	mustOK(t, e.st.AddAttendee(ctx, &store.Attendee{PartnershipID: f.acmeP.ID, FirstName: "Bob", LastName: "Martin", Role: store.RoleBooth}))
	list, _ := e.st.ListAttendees(ctx, f.acmeP.ID)
	bob := list[0]

	// Closed: no adding, no deleting, but the diet still changes.
	_, body := e.get(alice, "/"+f.event.Slug+"/partner/attendees")
	mustContain(t, body, "Modifier le régime")

	if strings.Contains(body, "/delete") {
		t.Error("an attendee can be deleted after the lock")
	}

	_, body = e.get(alice, "/"+f.event.Slug+"/partner/attendees?edit="+bob.UID)
	mustContain(t, body, "La liste est close", `name="dietary"`)

	if strings.Contains(body, `name="last_name"`) {
		t.Error("the name can be changed after the lock")
	}

	// Only the diet is taken, whatever else is sent.
	e.post(alice, "/"+f.event.Slug+"/partner/attendees/"+bob.UID, url.Values{"dietary": {"sans gluten"}, "last_name": {"Autre"}})

	list, _ = e.st.ListAttendees(ctx, f.acmeP.ID)
	if got := list[0]; got.Dietary != "sans gluten" || got.LastName != "Martin" {
		t.Fatalf("after the lock: %+v", got)
	}

	if code, _ := e.post(alice, "/"+f.event.Slug+"/partner/attendees", url.Values{"first_name": {"X"}, "last_name": {"Y"}}); code != http.StatusForbidden {
		t.Errorf("adding after the lock: %d", code)
	}
}
