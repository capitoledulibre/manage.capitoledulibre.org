package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()

	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { s.Close() })

	return s
}

func newEvent(t *testing.T, s *Store, slug string, year int) *Event {
	t.Helper()

	e := &Event{Slug: slug, Name: slug, Year: year}
	if err := s.SaveEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}

	return e
}

func newPerson(t *testing.T, s *Store, email string) *Person {
	t.Helper()

	p, err := s.EnsureVolunteer(context.Background(), email, "", "", "fr")
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func TestSignupFlow(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	e := newEvent(t, s, "ec-2026", 2026)

	def, err := s.DefaultPole(ctx, e.ID)
	if err != nil || def.Name != DefaultPoleName || def.Slug != "general" {
		t.Fatalf("a new edition gets its default pole: %+v %v", def, err)
	}

	pole := &Pole{EventID: e.ID, Name: "Buvette"}
	if err := s.CreatePole(ctx, pole); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

	slot := &Slot{PoleID: pole.ID, StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 1}
	if err := s.CreateSlot(ctx, slot); err != nil {
		t.Fatal(err)
	}

	alice, bob := newPerson(t, s, "alice@example.org"), newPerson(t, s, "bob@example.org")

	if err := s.JoinPole(ctx, pole.ID, alice.ID); err != nil {
		t.Fatal(err)
	}

	if in, _ := s.IsPoleMember(ctx, def.ID, alice.ID); !in {
		t.Fatal("joining a pole joins the default pole")
	}

	if ok, err := s.Signup(ctx, slot.ID, alice.ID); err != nil || !ok {
		t.Fatalf("alice signup: ok=%v err=%v", ok, err)
	}

	if ok, _ := s.Signup(ctx, slot.ID, alice.ID); !ok {
		t.Fatal("signing up twice is not an error")
	}

	if ok, _ := s.Signup(ctx, slot.ID, bob.ID); ok {
		t.Fatal("bob is refused, the slot is full")
	}

	if err := s.JoinWaitlist(ctx, slot.ID, bob.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSlotByUID(ctx, e.ID, slot.UID, alice.ID)
	if err != nil || got.Filled != 1 || !got.Mine || !got.IsFull() || got.PoleSlug != "buvette" || got.EventSlug != "ec-2026" {
		t.Fatalf("unexpected slot: %+v %v", got, err)
	}

	if _, err := s.GetSlotByUID(ctx, e.ID+1, slot.UID, 0); err != ErrNotFound {
		t.Fatalf("a slot is only found in its edition, got %v", err)
	}

	overlaps, _ := s.OverlappingSlots(ctx, alice.ID, pole.ID, 0, start.Add(time.Hour), start.Add(3*time.Hour))
	if len(overlaps) != 1 {
		t.Fatalf("expected 1 overlap, got %d", len(overlaps))
	}

	overlaps, _ = s.OverlappingSlots(ctx, alice.ID, pole.ID, 0, start.Add(2*time.Hour), start.Add(3*time.Hour))
	if len(overlaps) != 0 {
		t.Fatalf("adjacent slots do not overlap, got %d", len(overlaps))
	}

	open, _ := s.ListOpenSlots(ctx, e.ID, bob.ID, start.Add(-time.Hour))
	if len(open) != 0 {
		t.Fatalf("a full slot is not open, got %d", len(open))
	}

	// A manager may go over the capacity; it takes bob off the waiting list.
	if err := s.ForceSignup(ctx, slot.ID, bob.ID); err != nil {
		t.Fatal(err)
	}

	if waiting, _ := s.OnWaitlist(ctx, slot.ID, bob.ID); waiting {
		t.Fatal("a signed up volunteer no longer waits")
	}

	// Over full, the slot still counts its capacity only.
	cov, err := s.ListPoleCoverage(ctx, e.ID)
	if err != nil || len(cov) != 2 || cov[0].Pole.ID != def.ID || cov[1].Filled != 1 || cov[1].Capacity != 1 || cov[1].Members != 1 {
		t.Fatalf("unexpected coverage: %+v %v", cov, err)
	}

	if err := s.LeavePole(ctx, pole.ID, alice.ID); err != nil {
		t.Fatal(err)
	}

	got, _ = s.GetSlot(ctx, slot.ID, alice.ID)
	if got.Filled != 1 || got.Mine {
		t.Fatalf("leaving the pole drops the sign-ups: %+v", got)
	}
}

func TestSignupsLocked(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)

	cases := []struct {
		name   string
		ed     Event
		pole   *time.Time
		locked bool
	}{
		{"open, no freeze", Event{VolunteerStatus: VolunteersOpen}, nil, false},
		{"open, edition freeze passed", Event{VolunteerStatus: VolunteersOpen, VolunteerFreezeAt: &past}, nil, true},
		{"open, edition freeze ahead", Event{VolunteerStatus: VolunteersOpen, VolunteerFreezeAt: &future}, nil, false},
		{"pole freeze overrides the edition's", Event{VolunteerStatus: VolunteersOpen, VolunteerFreezeAt: &future}, &past, true},
		{"pole freeze ahead overrides a passed one", Event{VolunteerStatus: VolunteersOpen, VolunteerFreezeAt: &past}, &future, false},
		{"frozen", Event{VolunteerStatus: VolunteersFrozen}, nil, true},
		{"draft", Event{VolunteerStatus: VolunteersDraft}, nil, true},
	}

	for _, c := range cases {
		if got := c.ed.SignupsLocked(c.pole, now); got != c.locked {
			t.Errorf("%s: got %v, want %v", c.name, got, c.locked)
		}
	}
}

func TestPoleSlugs(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	e := newEvent(t, s, "ec-2026", 2026)

	p1, p2, p3 := &Pole{EventID: e.ID, Name: "Buvette & Crêpes"}, &Pole{EventID: e.ID, Name: "Buvette & crêpes"}, &Pole{EventID: e.ID, Name: "Manage"}
	for _, p := range []*Pole{p1, p2, p3} {
		if err := s.CreatePole(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	if p1.Slug != "buvette-crepes" || p2.Slug != "buvette-crepes-2" || p3.Slug != "manage-pole" {
		t.Fatalf("pole slugs: %q %q %q", p1.Slug, p2.Slug, p3.Slug)
	}

	got, err := s.GetPoleBySlug(ctx, e.ID, "buvette-crepes-2")
	if err != nil || got.ID != p2.ID || got.URL() != "/ec-2026/poles/buvette-crepes-2" {
		t.Fatalf("GetPoleBySlug: %+v %v", got, err)
	}

	// Renaming keeps a pole's own slug free for itself.
	p1.Name = "Buvette"
	if err := s.UpdatePole(ctx, p1); err != nil || p1.Slug != "buvette-crepes" {
		t.Fatalf("UpdatePole: %q %v", p1.Slug, err)
	}

	if got := (&Pole{Name: "Salle Ada Lovelace"}).Initials(); got != "SA" {
		t.Errorf("Initials = %q", got)
	}
}

func TestCopyVolunteerSide(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	src, dst := newEvent(t, s, "ec-2025", 2025), newEvent(t, s, "ec-2026", 2026)
	if err := s.SetEventLogo(ctx, src.ID, "logo.png"); err != nil {
		t.Fatal(err)
	}

	// The sizes carry over; the meals the target already lists stay.
	src.TshirtSizes, src.Meals, dst.Meals = "S\nM", "Samedi midi", "Dimanche midi"
	for _, e := range []*Event{src, dst} {
		e.VolunteerStatus = VolunteersDraft
		if err := s.SaveVolunteerSettings(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	pole := &Pole{EventID: src.ID, Name: "Accueil", AccessMode: PolePrivate, ImageFile: "accueil.png"}
	if err := s.CreatePole(ctx, pole); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateLocation(ctx, pole.ID, "Hall"); err != nil {
		t.Fatal(err)
	}

	carol, dave := newPerson(t, s, "carol@example.org"), newPerson(t, s, "dave@example.org")
	if err := s.AddPoleManager(ctx, pole.ID, carol.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.AddEventCoordinator(ctx, src.ID, dave.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.CopyVolunteerSide(ctx, src.ID, dst.ID, nil); err != nil {
		t.Fatal(err)
	}

	copied, err := s.GetPoleBySlug(ctx, dst.ID, pole.Slug)
	if err != nil || copied.AccessKey == "" || copied.AccessKey == pole.AccessKey || copied.ImageFile != "accueil.png" {
		t.Fatalf("copied pole: %+v %v", copied, err)
	}

	if locs, _ := s.ListLocations(ctx, copied.ID); len(locs) != 1 || locs[0].UID == "" {
		t.Fatalf("locations are copied: %+v", locs)
	}

	if ok, _ := s.IsPoleManager(ctx, copied.ID, carol.ID); !ok {
		t.Fatal("managers are copied")
	}

	// Their roles, as the rights load them: each pole with its edition.
	if led, _ := s.LedPoles(ctx, carol.ID); led[copied.ID] != dst.ID || len(led) != 2 {
		t.Fatalf("led poles: %v", led)
	}

	def, _ := s.DefaultPole(ctx, dst.ID)
	if ok, _ := s.IsPoleMember(ctx, def.ID, carol.ID); !ok {
		t.Fatal("a copied manager joins the default pole")
	}

	if ok, _ := s.IsEventCoordinator(ctx, dst.ID, dave.ID); !ok {
		t.Fatal("coordinators carry over")
	}

	if ids, _ := s.CoordinatedEventIDs(ctx, dave.ID); !ids[dst.ID] {
		t.Fatalf("coordinated editions: %v", ids)
	}

	if ok, _ := s.IsPoleMember(ctx, def.ID, dave.ID); !ok {
		t.Fatal("a coordinator joins the default pole")
	}

	if poles, _ := s.ListPoles(ctx, dst.ID); len(poles) != 2 {
		t.Fatalf("the default pole is updated in place, got %d poles", len(poles))
	}

	got, _ := s.GetEvent(ctx, dst.ID)
	if got.LogoFile != "logo.png" {
		t.Fatalf("the logo carries over, got %q", got.LogoFile)
	}

	if got.TshirtSizes != "S\nM" || got.Meals != "Dimanche midi" {
		t.Fatalf("logistics settings: %q %q", got.TshirtSizes, got.Meals)
	}

	if used, _ := s.FileInUse(ctx, "accueil.png"); !used {
		t.Fatal("a shared image is in use")
	}

	// The previous poles of a volunteer are found by slug.
	prev, slugs, err := s.PreviousPoleSlugs(ctx, dst.ID, carol.ID)
	if err != nil || prev.ID != src.ID || len(slugs) != 1 || slugs[0] != pole.Slug {
		t.Fatalf("PreviousPoleSlugs: %+v %v %v", prev, slugs, err)
	}

	// Copying a pole within its edition marks the name.
	dup, err := s.CopyPole(ctx, pole.ID, src.ID)
	if err != nil || dup.Name != "Accueil (copie)" || dup.Slug != "accueil-copie" {
		t.Fatalf("CopyPole: %+v %v", dup, err)
	}
}

func TestVolunteerDetailsPrefill(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	y25, y26, y27 := newEvent(t, s, "ec-2025", 2025), newEvent(t, s, "ec-2026", 2026), newEvent(t, s, "ec-2027", 2027)
	p := newPerson(t, s, "nina@example.org")

	for _, a := range []struct {
		e    *Event
		size string
	}{{y25, "M"}, {y27, "L"}} {
		if err := s.SaveVolunteerDetails(ctx, &VolunteerDetails{EventID: a.e.ID, PersonID: p.ID, Tshirt: a.size}); err != nil {
			t.Fatal(err)
		}
	}

	// The size offered again is the one of the latest earlier edition,
	// never of a later one.
	d, err := s.GetVolunteerDetails(ctx, y26.ID, p.ID)
	if err != nil || d.Answered || d.Tshirt != "M" {
		t.Fatalf("prefill: %+v %v", d, err)
	}
}

func TestHours(t *testing.T) {
	for minutes, want := range map[int]string{360: "6\u00a0h", 270: "4\u00a0h\u00a030", 245: "4\u00a0h\u00a005", 5: "0\u00a0h\u00a005"} {
		if got := (&EventVolunteer{Minutes: minutes}).Hours(); got != want {
			t.Errorf("Hours(%d) = %q, want %q", minutes, got, want)
		}
	}
}

func TestTasksAndMessages(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	e := newEvent(t, s, "ec-2026", 2026)
	def, _ := s.DefaultPole(ctx, e.ID)
	erin := newPerson(t, s, "erin@example.org")

	start := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

	a, b := &Slot{PoleID: def.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Capacity: 2}, &Slot{PoleID: def.ID, StartsAt: start, EndsAt: start.Add(time.Hour), Capacity: 2}
	for _, sl := range []*Slot{a, b} {
		if err := s.CreateSlot(ctx, sl); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.CreateSlot(ctx, &Slot{PoleID: def.ID, StartsAt: start, EndsAt: start, Capacity: 1}); err != ErrBadInput {
		t.Fatalf("an empty slot is refused, got %v", err)
	}

	task, err := s.CreateTask(ctx, a.ID, "Open the doors")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ToggleTask(ctx, task.ID, erin.ID); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.GetTaskByUID(ctx, task.UID); !got.Done() || got.DoneBy != "erin@example.org" {
		t.Fatalf("ticked task: %+v", got)
	}

	if err := s.CopyTasks(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	if tasks, _ := s.ListTasks(ctx, b.ID); len(tasks) != 1 || tasks[0].Done() || tasks[0].UID == task.UID {
		t.Fatalf("copied tasks: %+v", tasks)
	}

	m := &Message{EventID: e.ID, Body: "Welcome"}
	if err := s.CreateMessage(ctx, m, erin.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateMessage(ctx, &Message{EventID: e.ID, PoleID: def.ID, Body: "Pole"}, 0); err != nil {
		t.Fatal(err)
	}

	msgs, _ := s.ListEventMessages(ctx, e.ID)
	if len(msgs) != 1 || msgs[0].Author != "erin@example.org" {
		t.Fatalf("edition messages: %+v", msgs)
	}

	if got, err := s.GetMessageByUID(ctx, e.ID, m.UID); err != nil || got.Body != "Welcome" {
		t.Fatalf("GetMessageByUID: %+v %v", got, err)
	}

	if msgs, _ := s.ListPoleMessages(ctx, def.ID); len(msgs) != 1 || msgs[0].Author != "" {
		t.Fatalf("pole messages: %+v", msgs)
	}
}

func TestVolunteerPerson(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	p, err := s.EnsureVolunteer(ctx, "Frank@Example.org", "Frank", "0601", "en")
	if err != nil || p.Email != "frank@example.org" || p.Name != "Frank" || p.Phone != "0601" || p.Lang != "en" {
		t.Fatalf("EnsureVolunteer: %+v %v", p, err)
	}

	// What the person entered is kept.
	again, _ := s.EnsureVolunteer(ctx, "frank@example.org", "F.", "0602", "fr")
	if again.ID != p.ID || again.Name != "Frank" || again.Phone != "0601" {
		t.Fatalf("EnsureVolunteer overwrote: %+v", again)
	}

	tok, err := s.CalendarToken(ctx, p.ID)
	if err != nil || tok == "" {
		t.Fatal(err)
	}

	if same, _ := s.CalendarToken(ctx, p.ID); same != tok {
		t.Fatal("the calendar token is stable")
	}

	if owner, err := s.PersonByCalendarToken(ctx, tok); err != nil || owner.ID != p.ID {
		t.Fatalf("PersonByCalendarToken: %v", err)
	}

	if _, err := s.ResetCalendarToken(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PersonByCalendarToken(ctx, tok); err != ErrNotFound {
		t.Fatalf("a reset token is void, got %v", err)
	}

	old, err := s.SetPersonAvatar(ctx, p.ID, "a.png", AvatarUpload)
	if err != nil || old != "" {
		t.Fatalf("SetPersonAvatar: %q %v", old, err)
	}

	if old, _ = s.SetPersonAvatar(ctx, p.ID, "", AvatarOptOut); old != "a.png" {
		t.Fatalf("SetPersonAvatar returns the replaced file, got %q", old)
	}

	if got, _ := s.GetPerson(ctx, p.ID); got.WantsGravatar() {
		t.Fatal("an opted-out person gets no Gravatar")
	}

	if n, _ := s.CountPeople(ctx, "fran"); n != 1 {
		t.Fatalf("CountPeople = %d", n)
	}

	if n, _ := s.CountPeople(ctx, "%"); n != 0 {
		t.Fatalf("LIKE wildcards are escaped, CountPeople = %d", n)
	}
}

func TestCopyVolunteerSideWithSlots(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	src, dst := newEvent(t, s, "ec-2025", 2025), newEvent(t, s, "ec-2026", 2026)

	pole := &Pole{EventID: src.ID, Name: "Accueil"}
	if err := s.CreatePole(ctx, pole); err != nil {
		t.Fatal(err)
	}

	hall, err := s.CreateLocation(ctx, pole.ID, "Hall")
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2025, 11, 15, 8, 0, 0, 0, time.UTC)
	sl := &Slot{PoleID: pole.ID, LocationID: hall.ID, Title: "Badges", StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 3}

	if err := s.CreateSlot(ctx, sl); err != nil {
		t.Fatal(err)
	}

	for _, label := range []string{"Ouvrir", "Fermer"} {
		if _, err := s.CreateTask(ctx, sl.ID, label); err != nil {
			t.Fatal(err)
		}
	}

	alice := newPerson(t, s, "alice@example.org")
	if err := s.ForceSignup(ctx, sl.ID, alice.ID); err != nil {
		t.Fatal(err)
	}

	week := func(t time.Time) time.Time { return t.AddDate(0, 0, 364) }
	if err := s.CopyVolunteerSide(ctx, src.ID, dst.ID, week); err != nil {
		t.Fatal(err)
	}

	copied, _ := s.GetPoleBySlug(ctx, dst.ID, pole.Slug)

	slots, err := s.ListSlots(ctx, copied.ID, 0)
	if err != nil || len(slots) != 1 {
		t.Fatalf("slots: %d %v", len(slots), err)
	}

	got := slots[0]
	if !got.StartsAt.Equal(week(start)) || got.EndsAt.Sub(got.StartsAt) != 2*time.Hour || got.Title != "Badges" ||
		got.Capacity != 3 || got.LocationName != "Hall" || got.LocationID == hall.ID || got.TaskCount != 2 || got.Filled != 0 {
		t.Fatalf("copied slot: %+v", got)
	}
}
