package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

func TestSlotGenerator(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	d1 := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	f.event.StartsOn, f.event.EndsOn = &d1, &d2
	mustOK(t, e.st.SaveEvent(ctx, f.event))

	// A pole with no slot yet.
	salles := &store.Pole{EventID: f.event.ID, Name: "Salles"}
	mustOK(t, e.st.CreatePole(ctx, salles))
	mustOK(t, e.st.AddPoleManager(ctx, salles.ID, f.lead.ID))
	salles, _ = e.st.GetPole(ctx, salles.ID)
	gen := salles.ManageURL() + "/generate"

	lead := e.loginAs(f.leadEmail)
	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")

	if code, _ := e.get(nina, gen); code != http.StatusForbidden {
		t.Fatalf("a volunteer opens the generator: %d", code)
	}

	// Prefilled with the dates of the edition.
	_, body := e.get(lead, gen)
	mustContain(t, body, `value="2026-11-14"`, `value="2026-11-15"`, "Voir l&#39;aperçu")

	_, body = e.get(lead, salles.ManageURL())
	mustContain(t, body, `href="`+gen+`"`)

	// Eight rooms, two days, 9:00 to 18:00 in three-hour slots.
	form := url.Values{
		"from": {"2026-11-14"}, "to": {"2026-11-15"}, "mode": {"regular"},
		"day_start": {"09:00"}, "day_end": {"18:00"}, "length": {"180"},
		"new_locations": {"Salle 1\nSalle 2\nSalle 3\nSalle 4\nSalle 5\nSalle 6\nSalle 7\nSalle 8\nsalle 8"},
		"capacity":      {"2"}, "tasks": {"Accueillir l'orateur\nVérifier le micro"},
		"action": {"preview"},
	}

	_, body = e.post(lead, gen, form)
	mustContain(t, body, "48 créneaux, 96 places à pourvoir", "Créer les 48 créneaux", "Salle 8", "09:00 – 12:00 · 2", "samedi 14 novembre")

	if slots, _ := e.st.ListSlots(ctx, salles.ID, 0); len(slots) != 0 {
		t.Fatal("the preview created slots")
	}

	form.Set("action", "create")

	_, body = e.post(lead, gen, form)
	mustContain(t, body, "48 créneaux créés.")

	slots, _ := e.st.ListSlots(ctx, salles.ID, 0)
	locations, _ := e.st.ListLocations(ctx, salles.ID)

	if len(slots) != 48 || len(locations) != 8 || slots[0].TaskCount != 2 || slots[0].Capacity != 2 {
		t.Fatalf("generated: %d slots, %d places, first %+v", len(slots), len(locations), slots[0])
	}

	if clock(slots[0].StartsAt) != "09:00" || dayKey(slots[0].StartsAt) != "2026-11-14" {
		t.Fatalf("first slot at %s", slots[0].StartsAt)
	}

	// Running it again creates nothing twice.
	form.Set("action", "preview")
	form.Del("new_locations")

	for _, l := range locations {
		form.Add("location", l.UID)
	}

	_, body = e.post(lead, gen, form)
	mustContain(t, body, "48 créneaux existent déjà")

	if strings.Contains(body, "Créer les") {
		t.Error("a create button with nothing to create")
	}

	// Exact hours with their own volunteers, on one room and one day.
	list := url.Values{
		"from": {"2026-11-14"}, "to": {"2026-11-14"}, "mode": {"list"},
		"list":     {"08:00-09:00\n12:00-14:00 4"},
		"location": {locations[0].UID}, "capacity": {"1"}, "action": {"create"},
	}

	_, body = e.post(lead, gen, list)
	mustContain(t, body, "2 créneaux créés.")

	var rush *store.Slot

	slots, _ = e.st.ListSlots(ctx, salles.ID, 0)
	for _, sl := range slots {
		if clock(sl.StartsAt) == "12:00" && clock(sl.EndsAt) == "14:00" && sl.LocationID == locations[0].ID {
			rush = sl
		}
	}

	if rush == nil || rush.Capacity != 4 || clock(rush.EndsAt) != "14:00" {
		t.Fatalf("rush slot: %+v", rush)
	}

	// Mistakes are explained, nothing created.
	list.Set("list", "midi à deux")

	_, body = e.post(lead, gen, list)
	mustContain(t, body, "Une ligne n&#39;est pas comprise")

	// Warnings: an end of day off the slot length, days off the edition.
	_, body = e.post(lead, gen, url.Values{
		"from": {"2026-11-16"}, "to": {"2026-11-16"}, "mode": {"regular"},
		"day_start": {"09:00"}, "day_end": {"18:30"}, "length": {"120"}, "capacity": {"1"}, "action": {"preview"},
	})
	mustContain(t, body, "le dernier bout de journée reste sans créneau", "en dehors des dates de l&#39;édition", "4 créneaux, 4 places")

	// No integer id on the page.
	numeric := regexp.MustCompile(`(?:href|action)="[^"]*/[0-9]+(?:[/"?#])|value="[0-9]+" name="location"|name="location" value="[0-9]+"`)

	_, body = e.get(lead, gen)
	for _, m := range numeric.FindAllString(body, -1) {
		t.Errorf("generator exposes an integer id: %s", m)
	}
}

func TestParseRanges(t *testing.T) {
	got, ok := parseRanges("9h-12h\n12:00 – 14:00 4\n\n14:00-18:00", 2)
	if !ok || len(got) != 3 || got[0].Start != 9*60 || got[1].Capacity != 4 || got[2].End != 18*60 {
		t.Fatalf("parseRanges: %+v %v", got, ok)
	}

	for _, bad := range []string{"12:00-11:00", "25:00-26:00", "9-12", "09:00-12:00 0", "09:00-12:00 x"} {
		if _, ok := parseRanges(bad, 1); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
