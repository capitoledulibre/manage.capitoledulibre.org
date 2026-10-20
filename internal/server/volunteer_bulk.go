package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"manage/internal/i18n"
	"manage/internal/store"
)

// The slots of a pole as a timetable, to act on several at once: a day per
// table, the hours down and the places across, as the generator's preview
// shows them before they exist.

// bulkGrid is the slots of a pole laid out by day, hours and place.
type bulkGrid struct {
	// Columns are the places used, then "whole pole" when some slots have
	// none (an empty name).
	Columns []string
	Days    []*bulkDay
	// People are who is on each slot, by slot ID, when the planning shows
	// them; Fiche leads their names to their pages, for the coordination,
	// and Manage names those who gave none, for whoever runs the pole.
	People map[int64][]*store.Person
	Fiche  string
	Manage bool
}

type bulkDay struct {
	Day  time.Time
	Rows []*bulkRow
}

// bulkRow is an hour range of a day: a cell per column, each holding the
// slots at that time and place (usually one).
type bulkRow struct {
	Start, End time.Time
	Cells      [][]*store.Slot
}

// newBulkGrid lays out slots, sorted by start time, against the places of
// the pole in their order.
func newBulkGrid(slots []*store.Slot, locations []*store.Location) *bulkGrid {
	used := map[int64]bool{}
	for _, sl := range slots {
		used[sl.LocationID] = true
	}

	g := &bulkGrid{}
	col := map[int64]int{}

	for _, l := range locations {
		if used[l.ID] {
			col[l.ID] = len(g.Columns)
			g.Columns = append(g.Columns, l.Name)
		}
	}

	if used[0] {
		col[0] = len(g.Columns)
		g.Columns = append(g.Columns, "")
	}

	var day *bulkDay

	for _, sl := range slots {
		if day == nil || dayKey(day.Day) != dayKey(sl.StartsAt) {
			day = &bulkDay{Day: sl.StartsAt}
			g.Days = append(g.Days, day)
		}

		// Rows follow the start times; a slot of the same start and end
		// joins the row of its range, wherever it comes in the list.
		var row *bulkRow

		for _, x := range day.Rows {
			if x.Start.Equal(sl.StartsAt) && x.End.Equal(sl.EndsAt) {
				row = x
			}
		}

		if row == nil {
			row = &bulkRow{Start: sl.StartsAt, End: sl.EndsAt, Cells: make([][]*store.Slot, len(g.Columns))}
			day.Rows = append(day.Rows, row)
		}

		i := col[sl.LocationID]
		row.Cells[i] = append(row.Cells[i], sl)
	}

	return g
}

func (s *Server) manageSlotsBulk(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	slots, err := s.store.ListSlots(ctx, v.Pole.ID, 0)
	if err != nil {
		return err
	}

	locations, err := s.store.ListLocations(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	p := s.page(r, "vol.bulk.title")
	p.TitleText = i18n.T(p.Lang, "vol.bulk.title") + " · " + v.Pole.Name
	p.D = map[string]any{"V": v, "Grid": newBulkGrid(slots, locations)}

	return s.render(w, r, "volunteer/bulk", p)
}

// manageSlotsBulkDelete deletes the slots ticked. Each person on them, or
// waiting for them, gets one e-mail listing the slots they lose.
func (s *Server) manageSlotsBulkDelete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	if err := r.ParseForm(); err != nil {
		return errBadInput
	}

	var (
		lost    = map[int64][]*store.Slot{}
		people  []*store.Person
		deleted int
		told    = map[int64]bool{}
	)

	// Every slot ticked is checked before any goes: a bad one stops all.
	var slots []*store.Slot

	for _, uid := range r.Form["slot"] {
		sl, err := s.store.GetSlotByUID(ctx, v.Event.ID, uid, 0)
		if err != nil {
			return err
		}

		if sl.PoleID != v.Pole.ID {
			return errNotFound
		}

		slots = append(slots, sl)
	}

	for _, sl := range slots {
		// Read who is on it before the cascade removes the sign-ups.
		volunteers, err := s.store.ListSlotVolunteers(ctx, sl.ID)
		if err != nil {
			return err
		}

		waiting, err := s.store.ListWaitlist(ctx, sl.ID)
		if err != nil {
			return err
		}

		if err := s.store.DeleteSlot(ctx, sl.ID); err != nil {
			return err
		}

		entry := logSlot(store.LogSlotDeleted, v.Pole, sl, nil)
		entry.Count = len(volunteers)
		s.logVol(r, entry)

		for _, p := range append(volunteers, waiting...) {
			if !told[p.ID] {
				told[p.ID] = true
				people = append(people, p)
			}

			lost[p.ID] = append(lost[p.ID], sl)
		}

		deleted++
	}

	pole := v.Pole
	s.notifyPeople(people, func(lang string) string { return volSubject(lang, "mail.vol.slots_deleted.subject", pole.Name) },
		func(p *store.Person) string {
			var lines []string
			for _, sl := range lost[p.ID] {
				lines = append(lines, "  "+slotLine(p.Lang, pole, sl))
			}

			return s.volMail(p, pole.EventSlug, plural(p.Lang, "mail.vol.slots_deleted.body", len(lines), strings.Join(lines, "\n")))
		})

	s.flashCount(w, "flash.vol.slots_deleted", deleted, strconv.Itoa(len(people)))
	http.Redirect(w, r, pole.ManageURL()+"/slots/bulk", http.StatusSeeOther)

	return nil
}
