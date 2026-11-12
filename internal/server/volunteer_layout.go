package server

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"manage/internal/store"
)

// The layouts of a pole's slots: grouped by the time they run, or by the
// role they staff. The planning only groups by time, within each pole.
const (
	layoutTime = "time"
	layoutRole = "role"
)

// layoutCookie remembers the layout last chosen on a pole's page, for the
// next visit of any pole.
const layoutCookie = "manage_pole_layout"

// slotDay is a day of a pole's slots, its cards in groups: one group with
// no heading when the slots are not grouped.
type slotDay struct {
	Day    string
	Cards  []*slotCardData
	Groups []*slotGroup
}

// slotGroup is the slots of a day that run at the same time, or that staff
// the same role. Missing is the people they still need and Mine whether
// the person is on one of them; ID names the element showing both, which
// a card re-rendered by htmx updates too.
type slotGroup struct {
	ID       string
	Role     string
	StartsAt time.Time
	EndsAt   time.Time
	Missing  int
	Mine     bool
	Cards    []*slotCardData
}

// slotRole is what a slot staffs, as its card names it: its role (its
// location), or else its title, or else the pole.
func slotRole(sl *store.Slot, pole *store.Pole) string {
	switch {
	case sl.LocationName != "":
		return sl.LocationName
	case sl.Title != "":
		return sl.Title
	default:
		return pole.Name
	}
}

// groupKey is what the slots of a group share: the exact same start and
// end (two slots that only overlap are two groups), or the role on the day.
func groupKey(sl *store.Slot, pole *store.Pole, layout string) string {
	if layout == layoutTime {
		return sl.StartsAt.UTC().Format(time.RFC3339) + "/" + sl.EndsAt.UTC().Format(time.RFC3339)
	}

	return dayKey(sl.StartsAt) + "/" + slotRole(sl, pole)
}

// groupID is the id of a group's element, the same on every rendering.
func groupID(pole *store.Pole, key string) string {
	h := fnv.New64a()
	h.Write([]byte(pole.UID + "\x00" + key))

	return fmt.Sprintf("grp-%x", h.Sum64())
}

// multiRole tells whether the slots staff more than one role: with a
// single one, grouping would only add headings.
func multiRole(slots []*store.Slot, pole *store.Pole) bool {
	for _, sl := range slots {
		if slotRole(sl, pole) != slotRole(slots[0], pole) {
			return true
		}
	}

	return false
}

// groupSlots groups the cards of a day, in the order of the time they run.
// Roles come in the order they first run.
func groupSlots(cards []*slotCardData, layout string) []*slotGroup {
	if layout == "" {
		return []*slotGroup{{Cards: cards}}
	}

	var groups []*slotGroup

	index := map[string]*slotGroup{}

	for _, c := range cards {
		key := groupKey(c.Slot, c.Pole, layout)

		g := index[key]
		if g == nil {
			g = &slotGroup{ID: groupID(c.Pole, key), Role: slotRole(c.Slot, c.Pole), StartsAt: c.Slot.StartsAt, EndsAt: c.Slot.EndsAt}
			index[key] = g
			groups = append(groups, g)
		}

		g.Missing += c.Slot.Missing()
		g.Mine = g.Mine || c.Slot.Mine
		g.Cards = append(g.Cards, c)
	}

	if layout == layoutTime {
		slices.SortStableFunc(groups, func(a, b *slotGroup) int {
			if c := a.StartsAt.Compare(b.StartsAt); c != 0 {
				return c
			}

			return a.EndsAt.Compare(b.EndsAt)
		})
	}

	return groups
}

// chosenLayout is the layout of a pole's page: the one in the URL, which
// is remembered, else the one remembered, else by time.
func (s *Server) chosenLayout(w http.ResponseWriter, r *http.Request) string {
	if l := r.URL.Query().Get("layout"); l == layoutTime || l == layoutRole {
		s.setCookie(w, layoutCookie, l, 365*24*time.Hour)

		return l
	}

	if c, err := r.Cookie(layoutCookie); err == nil && c.Value == layoutRole {
		return layoutRole
	}

	return layoutTime
}

// cardLayout is the layout of the page a card goes back to, which the card
// re-rendered by htmx follows: that of the pole's page, carried in back,
// or by time on the planning's list, except for the person's own slots.
// Only a pole with more than one role is grouped, on either page.
func cardLayout(back string, e *store.Event, pole *store.Pole) string {
	page, query, _ := strings.Cut(back, "?")

	q, err := url.ParseQuery(query)
	if err != nil {
		return ""
	}

	switch {
	case page == pole.URL():
		if l := q.Get("layout"); l == layoutTime || l == layoutRole {
			return l
		}
	case page == planningURL(e):
		if q.Get("layout") == "" && q.Get("view") != "mine" {
			return layoutTime
		}
	}

	return ""
}

// poleSlotsURL is the slots of a pole, filtered and laid out.
func poleSlotsURL(pole *store.Pole, location string, onlyOpen bool, layout string) string {
	q := url.Values{}

	if location != "" {
		q.Set("location", location)
	}

	if onlyOpen {
		q.Set("open", "1")
	}

	if layout != "" {
		q.Set("layout", layout)
	}

	if len(q) == 0 {
		return pole.URL()
	}

	return pole.URL() + "?" + q.Encode()
}
