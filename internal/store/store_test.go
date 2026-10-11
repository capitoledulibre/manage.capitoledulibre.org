package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrationsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")

	for i := 0; i < 2; i++ {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}

		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
			t.Fatal(err)
		}

		files, _ := fs.Glob(migrationsFS, "migrations/*.sql")
		if n != len(files) {
			t.Errorf("open #%d: %d migrations applied, want %d", i+1, n, len(files))
		}

		s.Close()
	}
}

func TestSocialPostsOrderAndScope(t *testing.T) {
	ctx := context.Background()

	s, err := Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	e := &Event{Slug: "e", Name: "E", Year: 2026}
	_ = s.SaveEvent(ctx, e)

	org := &Organization{Name: "Org"}
	_ = s.CreateOrganization(ctx, org)

	p1 := &Partnership{OrgID: org.ID, EventID: e.ID}
	_ = s.CreatePartnership(ctx, p1)

	d1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

	for _, p := range []*SocialPost{
		{PartnershipID: p1.ID, Network: "x"},
		{PartnershipID: p1.ID, Network: "mastodon", PlannedAt: &d2},
		{PartnershipID: p1.ID, Network: "linkedin", PlannedAt: &d1, URL: "https://l.example/1"},
	} {
		if err := s.SaveSocialPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	posts, err := s.ListSocialPosts(ctx, p1.ID)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, p := range posts {
		got = append(got, p.Network)
	}

	if len(got) != 3 || got[0] != "linkedin" || got[1] != "mastodon" || got[2] != "x" {
		t.Errorf("order: %v", got)
	}

	if !posts[0].Published() || posts[1].Published() {
		t.Error("published flag")
	}

	// A post cannot be read or deleted through another partnership.
	if _, err := s.GetSocialPost(ctx, p1.ID+1, posts[0].UID); err != ErrNotFound {
		t.Errorf("cross-partnership read: %v", err)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Écoles & Co": "ecoles-co", "  ": "org", "Acme Logiciels": "acme-logiciels"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCloneLevelsAndPerks(t *testing.T) {
	ctx := context.Background()

	s, err := Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	src := &Event{Slug: "a", Name: "A", Year: 2025}
	dst := &Event{Slug: "b", Name: "B", Year: 2026}

	for _, e := range []*Event{src, dst} {
		if err := s.SaveEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	gold := &Level{EventID: src.ID, Name: "Or", Position: 2, Price: 1000, MaxAttendees: 3, TaxMode: TaxNone, Color: "#d4a42c"}
	bronze := &Level{EventID: src.ID, Name: "Bronze", Position: 0, Price: 250}
	logo := &Perk{EventID: src.ID, NameFR: "Logo", NameEN: "Logo", Position: 1}
	stand := &Perk{EventID: src.ID, NameFR: "Stand", Position: 2}

	for _, l := range []*Level{gold, bronze} {
		if err := s.SaveLevel(ctx, l); err != nil {
			t.Fatal(err)
		}
	}

	for _, p := range []*Perk{logo, stand} {
		if err := s.SavePerk(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.SetLevelPerkMatrix(ctx, src.ID, [][2]int64{{gold.ID, logo.ID}, {gold.ID, stand.ID}, {bronze.ID, logo.ID}}); err != nil {
		t.Fatal(err)
	}

	// The target already has an "or" level with its own price: it is kept.
	own := &Level{EventID: dst.ID, Name: "or", Position: 2, Price: 1500}
	if err := s.SaveLevel(ctx, own); err != nil {
		t.Fatal(err)
	}

	res, err := s.CloneLevelsAndPerks(ctx, src.ID, dst.ID)
	if err != nil {
		t.Fatal(err)
	}

	if res.Levels != 1 || res.Perks != 2 || res.Pairs != 3 {
		t.Errorf("clone: %+v", res)
	}

	levels, _ := s.ListLevels(ctx, dst.ID)
	if len(levels) != 2 || levels[0].Price != 1500 || levels[1].Name != "Bronze" {
		t.Errorf("levels: %+v %+v", levels[0], levels[len(levels)-1])
	}

	matrix, _ := s.LevelPerkMatrix(ctx, dst.ID)
	if len(matrix) != 3 {
		t.Errorf("matrix: %d pairs", len(matrix))
	}

	// Copying again adds nothing; the source is untouched.
	res, err = s.CloneLevelsAndPerks(ctx, src.ID, dst.ID)
	if err != nil || res.Levels+res.Perks+res.Pairs != 0 {
		t.Errorf("second clone: %+v %v", res, err)
	}

	if l, _ := s.GetLevel(ctx, gold.ID); l.TaxMode != TaxNone || l.Color != "#d4a42c" || l.MaxAttendees != 3 {
		t.Errorf("source changed: %+v", l)
	}

	if _, err := s.CloneLevelsAndPerks(ctx, src.ID, src.ID); err == nil {
		t.Error("cloning an edition into itself must fail")
	}
}

func TestPublicIDs(t *testing.T) {
	ctx := context.Background()

	s, err := Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	seen := map[string]bool{}

	for i := 0; i < 100; i++ {
		u := NewUUID()
		if len(u) != 36 || u[14] != '4' || !strings.ContainsRune("89ab", rune(u[19])) || seen[u] {
			t.Fatalf("bad or duplicate UUID %q", u)
		}

		seen[u] = true
	}

	e := &Event{Slug: "e", Name: "E", Year: 2026}
	_ = s.SaveEvent(ctx, e)

	l := &Level{EventID: e.ID, Name: "Or"}
	if err := s.SaveLevel(ctx, l); err != nil || l.UID == "" {
		t.Fatalf("level uid: %q %v", l.UID, err)
	}

	got, err := s.GetLevelByUID(ctx, e.ID, l.UID)
	if err != nil || got.ID != l.ID {
		t.Errorf("GetLevelByUID: %v", err)
	}

	// Lookups are scoped to their parent.
	if _, err := s.GetLevelByUID(ctx, e.ID+1, l.UID); err != ErrNotFound {
		t.Errorf("level found through another edition: %v", err)
	}
}

// TestVolunteerSettings: the volunteer side of an edition has its own
// writes, which the partner form (SaveEvent) cannot overwrite.
func TestVolunteerSettings(t *testing.T) {
	ctx := context.Background()

	s, err := Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	e := &Event{Slug: "ec-2026", Name: "EC 2026", Year: 2026}
	if err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}

	got, _ := s.GetEvent(ctx, e.ID)
	if got.VolunteerStatus != VolunteersDraft || got.VisibleToVolunteers() {
		t.Fatalf("a new edition should be a hidden draft: %q", got.VolunteerStatus)
	}

	freeze := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	got.VolunteerStatus, got.VolunteerFreezeAt = VolunteersOpen, &freeze

	if err := s.SaveVolunteerSettings(ctx, got); err != nil {
		t.Fatal(err)
	}

	if err := s.SetEventLogo(ctx, e.ID, "logo.svg"); err != nil {
		t.Fatal(err)
	}

	// The partner form saves a struct that never loaded the volunteer side.
	e.Name = "Example Conf 2026"
	if err := s.SaveEvent(ctx, e); err != nil {
		t.Fatal(err)
	}

	got, _ = s.GetEvent(ctx, e.ID)
	if got.Name != "Example Conf 2026" || got.VolunteerStatus != VolunteersOpen || !got.VisibleToVolunteers() ||
		got.VolunteerFreezeAt == nil || !got.VolunteerFreezeAt.Equal(freeze) || got.LogoFile != "logo.svg" {
		t.Fatalf("the volunteer side was lost: %+v", got)
	}

	got.VolunteerStatus = "published"
	if err := s.SaveVolunteerSettings(ctx, got); err != ErrBadInput {
		t.Fatalf("an unknown status should be refused, got %v", err)
	}
}

// TestBackupTo checks the snapshot is a usable database: the point is that a
// copy taken during the event can be restored.
func TestBackupTo(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	s, err := Open(ctx, filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.EnsureVolunteer(ctx, "vol@example.test", "", "", "fr"); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "backup.db")
	if err := s.BackupTo(ctx, dest); err != nil {
		t.Fatal(err)
	}

	// Writing after the snapshot does not change what was captured.
	if _, err := s.EnsureVolunteer(ctx, "late@example.test", "", "", "fr"); err != nil {
		t.Fatal(err)
	}

	b, err := Open(ctx, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if _, err := b.PersonByEmail(ctx, "vol@example.test"); err != nil {
		t.Fatalf("backup lacks the person: %v", err)
	}

	if _, err := b.PersonByEmail(ctx, "late@example.test"); err != ErrNotFound {
		t.Fatalf("backup has a later write: %v", err)
	}

	if err := s.BackupTo(ctx, dest); err == nil {
		t.Fatal("an existing backup is overwritten")
	}
}
