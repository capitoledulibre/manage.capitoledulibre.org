package server

import (
	"context"
	"strings"
	"testing"
	"time"
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
	mustContain(t, body, "Manque : Code postal")

	_, body = e.get(alice, "/partners/organization")
	mustContain(t, body, "pour la facture", `autocomplete="postal-code"`)
}
