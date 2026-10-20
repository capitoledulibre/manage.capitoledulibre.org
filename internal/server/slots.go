package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"manage/internal/access"
	"manage/internal/store"
)

// The slots of a pole and their checklists, for whoever runs the pole:
// creating them (by hand, in series, copied from a day, generated), changing,
// deleting them. As for the sign-ups, each operation checks the actor's
// rights itself, does the change, tells the volunteers it concerns and
// writes the history.

// outsideKey refuses a slot outside the dates of its edition, a frequent
// slip of the day, until the actor confirms.
const outsideKey = "vol.slot_form.outside"

// slotDraft is a slot as typed: a location by UID ("" for none), times
// zero when unreadable.
type slotDraft struct {
	Title            string
	Capacity         int
	Location         string
	StartsAt, EndsAt time.Time
}

// runs checks that the actor runs the pole of v, and that sl is one of its
// slots.
func runs(a *access.Actor, v *poleView, sl *store.Slot) error {
	if !a.ManagesPole(v.Pole) || sl != nil && sl.PoleID != v.Pole.ID {
		return errForbidden
	}

	return nil
}

// fillSlot sets the fields of sl from a draft, and refuses an invalid one.
func (s *Server) fillSlot(ctx context.Context, v *poleView, d slotDraft, sl *store.Slot) error {
	sl.Title, sl.Capacity, sl.StartsAt, sl.EndsAt = strings.TrimSpace(d.Title), d.Capacity, d.StartsAt, d.EndsAt
	sl.LocationID, sl.LocationName = 0, ""

	if d.Location != "" {
		l, err := s.store.GetLocationByUID(ctx, v.Pole.ID, d.Location)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}

		if l != nil {
			sl.LocationID, sl.LocationName = l.ID, l.Name
		}
	}

	switch {
	case sl.StartsAt.IsZero() || !sl.EndsAt.After(sl.StartsAt):
		return refuse("vol.err.slot_times")
	case sl.Capacity < 1 || sl.Capacity > 500:
		return refuse("vol.err.slot_capacity")
	}

	return nil
}

// slotSeries repeats a new slot: consecutive slots of the same length up to
// Until (zero for one), at every location of the pole if AllLocations, each
// with the tasks.
type slotSeries struct {
	Until        time.Time
	AllLocations bool
	Tasks        []string
}

// createSlots creates a slot, or a series of them, in a pole the actor runs.
// It returns how many, and the slot as typed (for a form to show again on a
// refusal).
func (s *Server) createSlots(ctx context.Context, a *access.Actor, v *poleView, d slotDraft, series slotSeries, confirmOutside bool) (int, *store.Slot, error) {
	if err := runs(a, v, nil); err != nil {
		return 0, nil, err
	}

	sl := &store.Slot{PoleID: v.Pole.ID}
	if err := s.fillSlot(ctx, v, d, sl); err != nil {
		return 0, sl, err
	}

	if outsideEdition(v.Event, sl) && !confirmOutside {
		return 0, sl, refuse(outsideKey)
	}

	length := sl.EndsAt.Sub(sl.StartsAt)
	starts := []time.Time{sl.StartsAt}

	if !series.Until.IsZero() {
		for t := sl.StartsAt.Add(length); !t.Add(length).After(series.Until) && len(starts) < 200; t = t.Add(length) {
			starts = append(starts, t)
		}
	}

	locIDs := []int64{sl.LocationID}

	if series.AllLocations {
		locations, err := s.store.ListLocations(ctx, v.Pole.ID)
		if err != nil {
			return 0, nil, err
		}

		if len(locations) > 0 {
			locIDs = locIDs[:0]
			for _, l := range locations {
				locIDs = append(locIDs, l.ID)
			}
		}
	}

	created := 0

	for _, locID := range locIDs {
		for _, start := range starts {
			n := &store.Slot{PoleID: v.Pole.ID, LocationID: locID, Title: sl.Title, Capacity: sl.Capacity, StartsAt: start, EndsAt: start.Add(length)}
			if err := s.store.CreateSlot(ctx, n); err != nil {
				return 0, nil, err
			}

			if err := s.addTaskLabels(ctx, n.ID, series.Tasks); err != nil {
				return 0, nil, err
			}

			created++
		}
	}

	s.logSlotsCreated(ctx, a.Person, v.Pole, created)

	return created, sl, nil
}

// updateSlot changes a slot of a pole the actor runs. The volunteers on it
// are told when it moves (times or place), those waiting when it gains
// places; it returns how many were told, and the slot as typed. A slot
// moved outside the edition's dates asks for a confirmation first.
func (s *Server) updateSlot(ctx context.Context, a *access.Actor, v *poleView, sl *store.Slot, d slotDraft, confirmOutside bool) (int, *store.Slot, error) {
	if err := runs(a, v, sl); err != nil {
		return 0, nil, err
	}

	before := *sl
	typed := *sl

	if err := s.fillSlot(ctx, v, d, &typed); err != nil {
		return 0, &typed, err
	}

	if outsideEdition(v.Event, &typed) && !outsideEdition(v.Event, &before) && !confirmOutside {
		return 0, &typed, refuse(outsideKey)
	}

	if err := s.store.UpdateSlot(ctx, &typed); err != nil {
		return 0, nil, err
	}

	after, err := s.store.GetSlot(ctx, sl.ID, 0)
	if err != nil {
		return 0, nil, err
	}

	moved := !before.StartsAt.Equal(after.StartsAt) || !before.EndsAt.Equal(after.EndsAt) || before.LocationID != after.LocationID
	notified := 0

	if moved {
		s.logAs(ctx, a.Person, logSlot(store.LogSlotMoved, v.Pole, after, nil))

		volunteers, err := s.store.ListSlotVolunteers(ctx, sl.ID)
		if err != nil {
			return 0, nil, err
		}

		s.notifySlotChanged(volunteers, v.Pole, &before, after)
		notified = len(volunteers)
	} else {
		s.logAs(ctx, a.Person, logSlot(store.LogSlotUpdated, v.Pole, after, nil))
	}

	// More places on a slot that was full: the waiting list wants to know.
	if before.IsFull() && !after.IsFull() {
		s.announceFreePlace(ctx, v.Pole, sl.ID)
	}

	return notified, after, nil
}

// slotsDeleted is what deleting slots did.
type slotsDeleted struct {
	// Slots deleted, the sign-ups they had, and the people told (on them
	// or waiting for them).
	Slots, Signups, Told int
}

// deleteSlots deletes slots of a pole the actor runs. Each person on them,
// or waiting for them, gets one e-mail listing the slots they lose. Every
// slot is checked before any goes: a bad one stops all.
func (s *Server) deleteSlots(ctx context.Context, a *access.Actor, v *poleView, slots []*store.Slot) (slotsDeleted, error) {
	var out slotsDeleted

	for _, sl := range slots {
		if err := runs(a, v, sl); err != nil {
			return out, err
		}
	}

	var (
		lost   = map[int64][]*store.Slot{}
		people []*store.Person
		told   = map[int64]bool{}
	)

	for _, sl := range slots {
		// Read who is on it before the cascade removes the sign-ups.
		volunteers, err := s.store.ListSlotVolunteers(ctx, sl.ID)
		if err != nil {
			return out, err
		}

		waiting, err := s.store.ListWaitlist(ctx, sl.ID)
		if err != nil {
			return out, err
		}

		if err := s.store.DeleteSlot(ctx, sl.ID); err != nil {
			return out, err
		}

		entry := logSlot(store.LogSlotDeleted, v.Pole, sl, nil)
		entry.Count = len(volunteers)
		s.logAs(ctx, a.Person, entry)

		for _, p := range append(volunteers, waiting...) {
			if !told[p.ID] {
				told[p.ID] = true
				people = append(people, p)
			}

			lost[p.ID] = append(lost[p.ID], sl)
		}

		out.Slots++
		out.Signups += len(volunteers)
	}

	out.Told = len(people)

	if len(slots) == 1 {
		s.notifySlotDeleted(people, v.Pole, slots[0])

		return out, nil
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

	return out, nil
}

// copyDay copies the slots of a day of a pole the actor runs to another
// day: same hours, places, titles, places wanted and tasks, nobody signed
// up. A slot already there (same place and times) is skipped: copying
// twice makes no doubles. It returns the slots created and skipped.
func (s *Server) copyDay(ctx context.Context, a *access.Actor, v *poleView, from, to time.Time) (int, int, error) {
	if err := runs(a, v, nil); err != nil {
		return 0, 0, err
	}

	if from.IsZero() || to.IsZero() || dayKey(from) == dayKey(to) {
		return 0, 0, refuse("vol.err.day_copy")
	}

	slots, err := s.store.ListSlots(ctx, v.Pole.ID, 0)
	if err != nil {
		return 0, 0, err
	}

	have, err := s.existingSlots(ctx, v)
	if err != nil {
		return 0, 0, err
	}

	created, skipped := 0, 0

	for _, sl := range slots {
		if dayKey(sl.StartsAt) != dayKey(from) {
			continue
		}

		// The clock time is kept across a change of daylight saving time.
		start := sl.StartsAt.In(paris)
		at := time.Date(to.Year(), to.Month(), to.Day(), start.Hour(), start.Minute(), 0, 0, paris)

		n := &store.Slot{PoleID: v.Pole.ID, LocationID: sl.LocationID, Title: sl.Title, Capacity: sl.Capacity,
			StartsAt: at, EndsAt: at.Add(sl.EndsAt.Sub(sl.StartsAt))}
		if have[slotKey(n.LocationID, n.StartsAt, n.EndsAt)] {
			skipped++

			continue
		}

		if err := s.store.CreateSlot(ctx, n); err != nil {
			return 0, 0, err
		}

		if err := s.store.CopyTasks(ctx, sl.ID, n.ID); err != nil {
			return 0, 0, err
		}

		created++
	}

	s.logSlotsCreated(ctx, a.Person, v.Pole, created)

	return created, skipped, nil
}

// plannedSlot is a slot the generator lays out: at a place by name ("" for
// the whole pole).
type plannedSlot struct {
	Place    string
	StartsAt time.Time
	EndsAt   time.Time
	Capacity int
	Title    string
}

// createPlanned creates the slots of the generator in a pole the actor
// runs: the new places first, then the slots, each with the tasks; one
// already there (same place and times) is skipped. It returns how many.
func (s *Server) createPlanned(ctx context.Context, a *access.Actor, v *poleView, newPlaces []string, planned []plannedSlot, tasks []string) (int, error) {
	if err := runs(a, v, nil); err != nil {
		return 0, err
	}

	locations, err := s.store.ListLocations(ctx, v.Pole.ID)
	if err != nil {
		return 0, err
	}

	byName := map[string]int64{"": 0}
	for _, l := range locations {
		byName[l.Name] = l.ID
	}

	for _, name := range newPlaces {
		if _, ok := byName[name]; ok {
			continue
		}

		l, err := s.store.CreateLocation(ctx, v.Pole.ID, name)
		if err != nil {
			return 0, err
		}

		byName[name] = l.ID
	}

	have, err := s.existingSlots(ctx, v)
	if err != nil {
		return 0, err
	}

	created := 0

	for _, p := range planned {
		locID, ok := byName[p.Place]
		if !ok {
			return 0, errBadInput
		}

		if have[slotKey(locID, p.StartsAt, p.EndsAt)] {
			continue
		}

		sl := &store.Slot{PoleID: v.Pole.ID, LocationID: locID, Title: p.Title, Capacity: p.Capacity, StartsAt: p.StartsAt, EndsAt: p.EndsAt}
		if err := s.store.CreateSlot(ctx, sl); err != nil {
			return 0, err
		}

		if err := s.addTaskLabels(ctx, sl.ID, tasks); err != nil {
			return 0, err
		}

		have[slotKey(locID, p.StartsAt, p.EndsAt)] = true
		created++
	}

	s.logSlotsCreated(ctx, a.Person, v.Pole, created)

	return created, nil
}

// --- the checklist of a slot ------------------------------------------------

// addTasks adds tasks to a slot of a pole the actor runs, one per label.
func (s *Server) addTasks(ctx context.Context, a *access.Actor, v *poleView, sl *store.Slot, labels []string) error {
	if err := runs(a, v, sl); err != nil {
		return err
	}

	return s.addTaskLabels(ctx, sl.ID, labels)
}

// addTaskLabels adds a task per non-blank label.
func (s *Server) addTaskLabels(ctx context.Context, slotID int64, labels []string) error {
	for _, label := range labels {
		if l := strings.TrimSpace(label); l != "" {
			if _, err := s.store.CreateTask(ctx, slotID, l); err != nil {
				return err
			}
		}
	}

	return nil
}

// deleteTask deletes a task of a slot of a pole the actor runs, and returns
// the slot.
func (s *Server) deleteTask(ctx context.Context, a *access.Actor, v *poleView, taskUID string) (*store.Slot, error) {
	task, err := s.store.GetTaskByUID(ctx, taskUID)
	if err != nil {
		return nil, err
	}

	sl, err := s.store.GetSlot(ctx, task.SlotID, 0)
	if err != nil {
		return nil, err
	}

	if sl.PoleID != v.Pole.ID {
		return nil, errNotFound
	}

	if err := runs(a, v, sl); err != nil {
		return nil, err
	}

	return sl, s.store.DeleteTask(ctx, task.ID)
}

// nonBlankLines splits a text into its non-blank lines, trimmed.
func nonBlankLines(text string) []string {
	var out []string

	for line := range strings.SplitSeq(text, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			out = append(out, l)
		}
	}

	return out
}
