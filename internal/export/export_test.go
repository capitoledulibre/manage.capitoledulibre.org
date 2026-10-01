package export

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"partners/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden file")

// TestGolden pins the JSON format consumed by the website build. Run with
// -update after an intentional change and review the diff.
func TestGolden(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	e := &store.Event{Slug: "ec-2026", Name: "Example Conf 2026", Year: 2026, IsCurrent: true}
	must(t, st.SaveEvent(ctx, e))

	gold := &store.Level{EventID: e.ID, Name: "Or", Position: 2, Price: 2000}
	bronze := &store.Level{EventID: e.ID, Name: "Bronze", Position: 0, Price: 500}
	must(t, st.SaveLevel(ctx, gold))
	must(t, st.SaveLevel(ctx, bronze))

	add := func(name, status string, level *store.Level, edit func(*store.Organization)) *store.Organization {
		o := &store.Organization{Name: name}
		must(t, st.CreateOrganization(ctx, o))

		if edit != nil {
			edit(o)
			must(t, st.UpdateOrganization(ctx, o))
		}

		p := &store.Partnership{OrgID: o.ID, EventID: e.ID, Status: status}
		if level != nil {
			p.LevelID = &level.ID
		}

		must(t, st.CreatePartnership(ctx, p))

		return o
	}

	acme := add("Acme", store.StatusConfirmed, gold, func(o *store.Organization) {
		o.Website, o.DescriptionFR, o.DescriptionEN = "https://acme.example", "Logiciels libres", "Free software"
		o.LinkedIn, o.Mastodon, o.X = "https://www.linkedin.com/company/acme", "@acme@floss.social", "@acme"
	})
	must(t, st.AddLogo(ctx, &store.Logo{OrgID: acme.ID, Variant: store.LogoDefault, File: "acme.svg", UploadedBy: "test"}))
	must(t, st.AddLogo(ctx, &store.Logo{OrgID: acme.ID, Variant: store.LogoDark, File: "acme-dark.svg", UploadedBy: "test"}))
	must(t, st.SaveJobOffer(ctx, &store.JobOffer{OrgID: acme.ID, Title: "Dev Go", URL: "https://acme.example/jobs"}))

	add("Zeta", store.StatusConfirmed, bronze, func(o *store.Organization) { o.DescriptionFR = "Seulement en français" })
	add("Beta", store.StatusConfirmed, bronze, nil)
	add("NoLevel", store.StatusConfirmed, nil, nil)
	add("Prospect", store.StatusNegotiating, gold, nil)

	doc, err := Build(ctx, st, "https://partenaires.example", e)
	if err != nil {
		t.Fatal(err)
	}

	doc.GeneratedAt = time.Time{}

	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	got = append(got, '\n')
	golden := filepath.Join("testdata", "export.golden.json")

	if *update {
		must(t, os.WriteFile(golden, got, 0o644))
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/export -update)", err)
	}

	if string(got) != string(want) {
		t.Errorf("export changed; run with -update if intended.\ngot:\n%s", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}
