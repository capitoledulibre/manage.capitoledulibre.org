package server

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

func TestGroupSlots(t *testing.T) {
	pole := &store.Pole{Name: "Accueil", Slug: "accueil", EventSlug: "cdl"}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 21, h, m, 0, 0, time.UTC) }
	card := func(title string, from, to time.Time, capacity, filled int) *slotCardData {
		return &slotCardData{Pole: pole, Slot: &store.Slot{Title: title, StartsAt: from, EndsAt: to, Capacity: capacity, Filled: filled}}
	}

	// As the store sorts them: by start, then by place.
	cards := []*slotCardData{
		card("Buvette", at(9, 0), at(11, 0), 2, 1),
		card("Accueil", at(9, 0), at(12, 0), 3, 0),
		card("Accueil", at(9, 0), at(11, 0), 2, 2),
		card("Vestiaire", at(14, 0), at(16, 0), 1, 0),
		card("Buvette", at(14, 0), at(16, 0), 2, 0),
	}

	t.Run("time", func(t *testing.T) {
		groups := groupSlots(cards, layoutTime)
		want := []struct {
			from, to       time.Time
			cards, missing int
		}{{at(9, 0), at(11, 0), 2, 1}, {at(9, 0), at(12, 0), 1, 3}, {at(14, 0), at(16, 0), 2, 3}}

		if len(groups) != len(want) {
			t.Fatalf("%d groups, want %d", len(groups), len(want))
		}

		for i, w := range want {
			g := groups[i]
			if !g.StartsAt.Equal(w.from) || !g.EndsAt.Equal(w.to) || len(g.Cards) != w.cards || g.Missing != w.missing {
				t.Errorf("group %d: %s–%s, %d cards, %d missing; want %s–%s, %d, %d", i,
					g.StartsAt.Format("15:04"), g.EndsAt.Format("15:04"), len(g.Cards), g.Missing,
					w.from.Format("15:04"), w.to.Format("15:04"), w.cards, w.missing)
			}
		}
	})

	t.Run("role", func(t *testing.T) {
		groups := groupSlots(cards, layoutRole)
		want := []struct {
			role           string
			cards, missing int
		}{{"Buvette", 2, 3}, {"Accueil", 2, 3}, {"Vestiaire", 1, 1}}

		if len(groups) != len(want) {
			t.Fatalf("%d groups, want %d", len(groups), len(want))
		}

		for i, w := range want {
			if g := groups[i]; g.Role != w.role || len(g.Cards) != w.cards || g.Missing != w.missing {
				t.Errorf("group %d: %q, %d cards, %d missing; want %q, %d, %d", i, g.Role, len(g.Cards), g.Missing, w.role, w.cards, w.missing)
			}
		}

		// Within a role, the cards stay in the order they run.
		if c := groups[0].Cards; !c[0].Slot.StartsAt.Before(c[1].Slot.StartsAt) {
			t.Error("the Buvette cards are out of order")
		}
	})

	t.Run("untitled", func(t *testing.T) {
		groups := groupSlots([]*slotCardData{card("", at(9, 0), at(11, 0), 1, 0)}, layoutRole)
		if len(groups) != 1 || groups[0].Role != "Accueil" {
			t.Errorf("an untitled slot goes under the pole's name, got %+v", groups)
		}
	})

	t.Run("flat", func(t *testing.T) {
		if groups := groupSlots(cards, ""); len(groups) != 1 || len(groups[0].Cards) != len(cards) {
			t.Errorf("no layout should give one group of every card, got %d groups", len(groups))
		}
	})
}

func TestPoleSlotsURL(t *testing.T) {
	pole := &store.Pole{Slug: "accueil", EventSlug: "cdl"}

	for _, c := range []struct {
		location string
		open     bool
		layout   string
		want     string
	}{
		{"", false, "", "/cdl/poles/accueil"},
		{"", false, layoutRole, "/cdl/poles/accueil?layout=role"},
		{"abc", true, layoutTime, "/cdl/poles/accueil?layout=time&location=abc&open=1"},
	} {
		if got := poleSlotsURL(pole, c.location, c.open, c.layout); got != c.want {
			t.Errorf("poleSlotsURL(%q, %v, %q) = %q, want %q", c.location, c.open, c.layout, got, c.want)
		}
	}
}

func TestPoleLayouts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()

	nina := e.volunteer("nina@example.org", "Nina", "/volunteers/")
	ninaP, _ := e.st.PersonByEmail(ctx, "nina@example.org")
	mustOK(t, e.st.JoinPole(ctx, f.accueil.ID, ninaP.ID))

	page := f.accueil.URL()
	start := time.Now().Add(72 * time.Hour).Truncate(time.Hour)
	badges := &store.Slot{PoleID: f.accueil.ID, StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 2, Title: "Badges"}
	mustOK(t, e.st.CreateSlot(ctx, badges))

	slotURL := page + "/slots/" + badges.UID

	// A single role: the slots are not grouped, and no switch.
	_, body := e.get(nina, page)
	mustContain(t, body, `name="back" value="`+page+`"`)

	if strings.Contains(body, "Par poste") {
		t.Error("a single role offers the role layout")
	}

	mustOK(t, e.st.CreateSlot(ctx, &store.Slot{PoleID: f.accueil.ID, StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 1, Title: "Vestiaire"}))

	// Two roles: by time by default, the time in the heading.
	_, body = e.get(nina, page)
	mustContain(t, body, "Par créneau", "Par poste", `name="back" value="`+page+`?layout=time"`, "il manque 3 bénévoles")

	// By role, the role in the heading and the time on the card, linking
	// to the slot.
	_, body = e.get(nina, page+"?layout=role")
	mustContain(t, body, `name="back" value="`+page+`?layout=role"`, `<a href="`+slotURL+`" class="text-gray-900 hover:underline">`)

	// A card swapped in by htmx keeps the layout of the page.
	_, body = e.hx(nina, slotURL+"/signup", url.Values{"back": {page + "?layout=role"}})
	mustContain(t, body, `<a href="`+slotURL+`" class="text-gray-900 hover:underline">`)

	if strings.Contains(body, ">Badges</a>") {
		t.Error("the re-rendered card names the role its group already shows")
	}

	_, body = e.hx(nina, slotURL+"/unsignup", url.Values{"back": {page + "?layout=time"}})
	if strings.Contains(body, "w-28 shrink-0") {
		t.Error("the re-rendered card shows the time its group already shows")
	}
}
