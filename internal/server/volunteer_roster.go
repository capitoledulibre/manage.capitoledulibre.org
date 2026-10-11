package server

import (
	"encoding/csv"
	"net/http"
	"slices"
	"strconv"
	"time"

	"manage/internal/store"
)

// The printed rosters: who is where and when, with phone numbers and a box
// to tick on the day, for a pole (its leads) or the whole edition (the
// coordination). A page styled for paper, which the browser saves as a PDF:
// no PDF library to carry. The same data goes out as a CSV of sign-ups, one
// line per volunteer and slot.

// rosterSlot is a slot of a roster, with its people and checklist.
type rosterSlot struct {
	Slot   *store.Slot
	People []*store.Person
	Tasks  []*store.SlotTask
	Free   int
}

// rosterPole is the slots of a pole on a day.
type rosterPole struct {
	Pole  *store.Pole
	Slots []*rosterSlot
}

type rosterDay struct {
	Day      string
	Poles    []*rosterPole
	Capacity int
	Filled   int
}

// roster gathers the slots of an edition, or of one pole when pole is not
// nil, day by day and pole by pole. day keeps a single day when not empty;
// days lists them all, for the filter.
func (s *Server) roster(r *http.Request, e *store.Event, pole *store.Pole, day string) (out []*rosterDay, days []string, err error) {
	ctx := r.Context()

	poleID := int64(0)
	if pole != nil {
		poleID = pole.ID
	}

	var slots []*store.Slot

	if pole != nil {
		slots, err = s.store.ListSlots(ctx, pole.ID, 0)
	} else {
		slots, err = s.store.ListEventSlots(ctx, e.ID)
	}

	if err != nil {
		return nil, nil, err
	}

	poles, err := s.store.ListPoles(ctx, e.ID)
	if err != nil {
		return nil, nil, err
	}

	people, err := s.store.ListEventSignups(ctx, e.ID, poleID)
	if err != nil {
		return nil, nil, err
	}

	tasks, err := s.store.ListEventTasks(ctx, e.ID, poleID)
	if err != nil {
		return nil, nil, err
	}

	byID := map[int64]*store.Pole{}
	order := map[int64]int{}

	for i, p := range poles {
		byID[p.ID], order[p.ID] = p, i
	}

	for _, sl := range slots {
		key := dayKey(sl.StartsAt)
		if len(days) == 0 || days[len(days)-1] != key {
			days = append(days, key)
		}

		if day != "" && key != day {
			continue
		}

		if len(out) == 0 || out[len(out)-1].Day != key {
			out = append(out, &rosterDay{Day: key})
		}

		d := out[len(out)-1]

		var rp *rosterPole

		for _, x := range d.Poles {
			if x.Pole.ID == sl.PoleID {
				rp = x
			}
		}

		if rp == nil {
			rp = &rosterPole{Pole: byID[sl.PoleID]}
			d.Poles = append(d.Poles, rp)
		}

		rp.Slots = append(rp.Slots, &rosterSlot{Slot: sl, People: people[sl.ID], Tasks: tasks[sl.ID], Free: max(sl.Capacity-sl.Filled, 0)})
		d.Capacity += sl.Capacity
		d.Filled += min(sl.Filled, sl.Capacity)
	}

	for _, d := range out {
		slices.SortStableFunc(d.Poles, func(a, b *rosterPole) int { return order[a.Pole.ID] - order[b.Pole.ID] })
	}

	return out, days, nil
}

// rosterDayParam reads the day filter, empty for every day.
func rosterDayParam(r *http.Request) string {
	day := r.URL.Query().Get("day")
	if _, err := time.Parse(time.DateOnly, day); err != nil {
		return ""
	}

	return day
}

func (s *Server) coordinationRoster(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	poles, err := s.store.ListPoles(r.Context(), e.ID)
	if err != nil {
		return err
	}

	// One pole of the edition, or all of them.
	var pole *store.Pole

	if slug := r.URL.Query().Get("pole"); slug != "" {
		for _, p := range poles {
			if p.Slug == slug {
				pole = p
			}
		}
	}

	return s.renderRoster(w, r, e, pole, poles, coordinationURL(e))
}

func (s *Server) manageRoster(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	return s.renderRoster(w, r, v.Event, v.Pole, nil, v.Pole.ManageURL())
}

// renderRoster shows a roster. poles is the pole filter of the
// coordination, nil on the roster of a pole; base is the page the roster
// belongs to, where it goes back to and its CSV hangs from.
func (s *Server) renderRoster(w http.ResponseWriter, r *http.Request, e *store.Event, pole *store.Pole, poles []*store.Pole, base string) error {
	day := rosterDayParam(r)

	days, all, err := s.roster(r, e, pole, day)
	if err != nil {
		return err
	}

	p := s.page(r, "vol.roster.title")
	if pole != nil {
		p.TitleText = pole.Name
	}

	p.D = map[string]any{
		"Event": e, "Pole": pole, "Poles": poles, "Days": days, "AllDays": all, "Day": day,
		"Base": base, "Global": poles != nil, "Printed": time.Now(),
	}

	return s.render(w, r, "volunteer/roster", p)
}

func (s *Server) coordinationSignupsCSV(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	return s.signupsCSV(w, r, e, nil, "signups-"+e.Slug)
}

func (s *Server) manageSignupsCSV(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	return s.signupsCSV(w, r, v.Event, v.Pole, "signups-"+v.Event.Slug+"-"+v.Pole.Slug)
}

// signupsCSV writes one line per volunteer and slot, and one per slot left
// without anybody, so that a spreadsheet shows the gaps too.
func (s *Server) signupsCSV(w http.ResponseWriter, r *http.Request, e *store.Event, pole *store.Pole, name string) error {
	days, _, err := s.roster(r, e, pole, "")
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"day", "start", "end", "pole", "place", "title", "capacity", "name", "email", "phone"})

	for _, d := range days {
		for _, rp := range d.Poles {
			for _, rs := range rp.Slots {
				sl := rs.Slot
				line := []string{d.Day, clock(sl.StartsAt), clock(sl.EndsAt), csvSafe(rp.Pole.Name), csvSafe(sl.LocationName),
					csvSafe(sl.Title), strconv.Itoa(sl.Capacity)}

				if len(rs.People) == 0 {
					_ = cw.Write(append(line, "", "", ""))
				}

				for _, p := range rs.People {
					_ = cw.Write(append(slices.Clip(line), csvSafe(p.Name), p.Email, csvSafe(p.Phone)))
				}
			}
		}
	}

	cw.Flush()

	return cw.Error()
}
