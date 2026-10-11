package server

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"manage/internal/store"
)

// The slot generator of a pole: days × hours × places in one form, a
// preview, then the slots. An event with eight rooms over two days is a
// hundred slots, which nobody wants to type one by one.

// maxGenerated bounds what one run creates, so a slip (a day range of a
// year, five-minute slots) cannot flood the planning.
const maxGenerated = 2000

// genRange is a time range of the day, in minutes since midnight, with the
// volunteers it needs.
type genRange struct {
	Start, End int
	Capacity   int
}

func (g genRange) Label() string {
	return fmt.Sprintf("%02d:%02d – %02d:%02d", g.Start/60, g.Start%60, g.End/60, g.End%60)
}

// genForm is the generator form, as typed.
type genForm struct {
	From, To     string // YYYY-MM-DD
	Mode         string // "regular" or "list"
	DayStart     string // HH:MM
	DayEnd       string
	Length       int // minutes
	List         string
	Locations    []string // UIDs of the existing places chosen
	NewLocations string   // one per line
	Capacity     int
	Title        string
	Tasks        string
}

// genDay is a day of the preview.
type genDay struct {
	Day     time.Time
	Outside bool
	Ranges  []genRange
}

// genPlan is what a form would create.
type genPlan struct {
	Days      []*genDay
	Ranges    []genRange
	Places    []string // names, existing and new
	NewPlaces []string
	Slots     int
	Seats     int
	Skipped   int
	Errors    []string // i18n keys
	Warnings  []string // i18n keys
}

var genLengths = []int{30, 60, 90, 120, 150, 180, 240, 360}

func (s *Server) generateRoutes(m func(string, handlerFunc)) {
	const base = "/volunteers/{event}/{pole}/manage/generate"

	m("GET "+base, s.generateForm)
	m("POST "+base, s.generateSubmit)
}

func (s *Server) generateForm(w http.ResponseWriter, r *http.Request) error {
	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	locations, err := s.store.ListLocations(r.Context(), v.Pole.ID)
	if err != nil {
		return err
	}

	f := &genForm{Mode: "regular", DayStart: "09:00", DayEnd: "18:00", Length: 120, Capacity: 2, Tasks: v.Pole.DefaultTasks}

	day := time.Now().In(paris)
	if v.Event.StartsOn != nil {
		day = *v.Event.StartsOn
	}

	f.From, f.To = day.Format(time.DateOnly), day.Format(time.DateOnly)
	if v.Event.EndsOn != nil {
		f.To = v.Event.EndsOn.Format(time.DateOnly)
	}

	for _, l := range locations {
		f.Locations = append(f.Locations, l.UID)
	}

	return s.renderGenerate(w, r, v, f, locations, nil)
}

func (s *Server) renderGenerate(w http.ResponseWriter, r *http.Request, v *poleView, f *genForm, locations []*store.Location, plan *genPlan) error {
	p := s.page(r, "vol.gen.title")
	p.D = map[string]any{"V": v, "F": f, "Locations": locations, "Plan": plan, "Lengths": genLengths}

	return s.render(w, r, "volunteer/generate", p)
}

func readGenForm(r *http.Request) *genForm {
	f := &genForm{
		From: r.FormValue("from"), To: r.FormValue("to"), Mode: r.FormValue("mode"),
		DayStart: r.FormValue("day_start"), DayEnd: r.FormValue("day_end"), Length: formInt(r, "length"),
		List: r.FormValue("list"), Locations: r.Form["location"], NewLocations: r.FormValue("new_locations"),
		Capacity: formInt(r, "capacity"), Title: strings.TrimSpace(r.FormValue("title")), Tasks: r.FormValue("tasks"),
	}

	if f.Mode != "list" {
		f.Mode = "regular"
	}

	return f
}

// parseClock reads "9:00", "09:00" or "9h30" as minutes since midnight;
// "24:00" ends a day.
func parseClock(v string) (int, bool) {
	v = strings.TrimSpace(strings.ReplaceAll(strings.ToLower(v), "h", ":"))
	if strings.HasSuffix(v, ":") {
		v += "00"
	}

	h, m, ok := strings.Cut(v, ":")
	if !ok {
		return 0, false
	}

	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)

	if err1 != nil || err2 != nil || hh < 0 || mm < 0 || mm > 59 || hh*60+mm > 24*60 {
		return 0, false
	}

	return hh*60 + mm, true
}

var dashSpaces = regexp.MustCompile(`\s*-\s*`)

// parseRanges reads the list mode: one range per line, "09:00-12:00",
// optionally followed by the volunteers it needs, "12:00-14:00 4".
func parseRanges(list string, capacity int) ([]genRange, bool) {
	var out []genRange

	for line := range strings.SplitSeq(list, "\n") {
		// "12:00 – 14:00" and "12:00-14:00" alike.
		line = dashSpaces.ReplaceAllString(strings.NewReplacer("–", "-", "—", "-").Replace(line), "-")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		from, to, ok := strings.Cut(fields[0], "-")
		if !ok {
			return nil, false
		}

		start, ok1 := parseClock(from)
		end, ok2 := parseClock(to)

		if !ok1 || !ok2 || end <= start {
			return nil, false
		}

		c := capacity

		if len(fields) > 1 {
			n, err := strconv.Atoi(fields[1])
			if err != nil || n < 1 || n > 500 {
				return nil, false
			}

			c = n
		}

		out = append(out, genRange{Start: start, End: end, Capacity: c})
	}

	return out, true
}

// plan works out what a form creates, checking it on the way.
func (s *Server) plan(r *http.Request, v *poleView, f *genForm, locations []*store.Location) (*genPlan, error) {
	p := &genPlan{}

	from, err1 := time.ParseInLocation(time.DateOnly, f.From, paris)
	to, err2 := time.ParseInLocation(time.DateOnly, f.To, paris)

	switch {
	case err1 != nil || err2 != nil || to.Before(from):
		p.Errors = append(p.Errors, "vol.gen.err_days")
	case to.Sub(from) > 31*24*time.Hour:
		p.Errors = append(p.Errors, "vol.gen.err_days_long")
	}

	if f.Capacity < 1 || f.Capacity > 500 {
		p.Errors = append(p.Errors, "vol.err.slot_capacity")
		f.Capacity = 1
	}

	if f.Mode == "list" {
		ranges, ok := parseRanges(f.List, f.Capacity)
		if !ok || len(ranges) == 0 {
			p.Errors = append(p.Errors, "vol.gen.err_list")
		}

		p.Ranges = ranges
	} else {
		start, ok1 := parseClock(f.DayStart)
		end, ok2 := parseClock(f.DayEnd)

		if !ok1 || !ok2 || end <= start || !slices.Contains(genLengths, f.Length) {
			p.Errors = append(p.Errors, "vol.gen.err_regular")
		} else {
			for t := start; t+f.Length <= end; t += f.Length {
				p.Ranges = append(p.Ranges, genRange{Start: t, End: t + f.Length, Capacity: f.Capacity})
			}

			if len(p.Ranges) == 0 {
				p.Errors = append(p.Errors, "vol.gen.err_regular")
			} else if p.Ranges[len(p.Ranges)-1].End < end {
				p.Warnings = append(p.Warnings, "vol.gen.warn_remainder")
			}
		}
	}

	// Ranges that overlap are allowed (two shifts covering a rush) but worth
	// a word.
	sorted := slices.Clone(p.Ranges)
	slices.SortFunc(sorted, func(a, b genRange) int { return a.Start - b.Start })

	for i := 1; i < len(sorted); i++ {
		if sorted[i].Start < sorted[i-1].End {
			p.Warnings = append(p.Warnings, "vol.gen.warn_overlap")

			break
		}
	}

	// Places: the existing ones chosen, then the new ones.
	chosen := map[string]bool{}
	for _, uid := range f.Locations {
		chosen[uid] = true
	}

	existing := map[string]bool{}

	for _, l := range locations {
		existing[strings.ToLower(l.Name)] = true

		if chosen[l.UID] {
			p.Places = append(p.Places, l.Name)
		}
	}

	for line := range strings.SplitSeq(f.NewLocations, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || existing[strings.ToLower(name)] {
			continue
		}

		existing[strings.ToLower(name)] = true
		p.NewPlaces = append(p.NewPlaces, name)
		p.Places = append(p.Places, name)
	}

	if len(p.Errors) > 0 {
		return p, nil
	}

	places := max(len(p.Places), 1)

	// What already exists is not created twice: the generator can run again
	// after a change without doubling the planning.
	have, err := s.existingSlots(r, v)
	if err != nil {
		return nil, err
	}

	byName := map[string]int64{}
	for _, l := range locations {
		byName[l.Name] = l.ID
	}

	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		day := &genDay{Day: d, Ranges: p.Ranges, Outside: outsideDay(v.Event, d)}
		if day.Outside {
			p.Warnings = appendOnce(p.Warnings, "vol.gen.warn_outside")
		}

		p.Days = append(p.Days, day)

		for _, g := range p.Ranges {
			for i := range places {
				locID := int64(0)
				if len(p.Places) > 0 {
					locID = byName[p.Places[i]] // 0 for a new place, which cannot clash yet
				}

				start, end := genTimes(d, g)
				if locID != 0 || len(p.Places) == 0 {
					if have[slotKey(locID, start, end)] {
						p.Skipped++

						continue
					}
				}

				p.Slots++
				p.Seats += g.Capacity
			}
		}
	}

	if p.Slots > maxGenerated {
		p.Errors = append(p.Errors, "vol.gen.err_too_many")
	}

	return p, nil
}

func appendOnce(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}

	return append(list, v)
}

// outsideDay tells whether a day falls outside the dates of an edition.
func outsideDay(e *store.Event, d time.Time) bool {
	if e.StartsOn == nil || e.EndsOn == nil {
		return false
	}

	k := d.Format(time.DateOnly)

	return k < e.StartsOn.Format(time.DateOnly) || k > e.EndsOn.Format(time.DateOnly)
}

// genTimes places a range on a day, in the event's time zone.
func genTimes(d time.Time, g genRange) (time.Time, time.Time) {
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, g.Start, 0, 0, paris)
	end := time.Date(d.Year(), d.Month(), d.Day(), 0, g.End, 0, 0, paris)

	return start, end
}

func slotKey(locationID int64, start, end time.Time) string {
	return fmt.Sprintf("%d|%d|%d", locationID, start.Unix(), end.Unix())
}

func (s *Server) existingSlots(r *http.Request, v *poleView) (map[string]bool, error) {
	slots, err := s.store.ListSlots(r.Context(), v.Pole.ID, 0)
	if err != nil {
		return nil, err
	}

	have := map[string]bool{}
	for _, sl := range slots {
		have[slotKey(sl.LocationID, sl.StartsAt, sl.EndsAt)] = true
	}

	return have, nil
}

func (s *Server) generateSubmit(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	v, err := s.managedPole(r)
	if err != nil {
		return err
	}

	locations, err := s.store.ListLocations(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	f := readGenForm(r)

	plan, err := s.plan(r, v, f, locations)
	if err != nil {
		return err
	}

	if r.FormValue("action") != "create" || len(plan.Errors) > 0 || plan.Slots == 0 {
		return s.renderGenerate(w, r, v, f, locations, plan)
	}

	// The new places first, then the slots.
	byName := map[string]int64{}
	for _, l := range locations {
		byName[l.Name] = l.ID
	}

	for _, name := range plan.NewPlaces {
		l, err := s.store.CreateLocation(ctx, v.Pole.ID, name)
		if err != nil {
			return err
		}

		byName[name] = l.ID
	}

	have, err := s.existingSlots(r, v)
	if err != nil {
		return err
	}

	var labels []string

	for line := range strings.SplitSeq(f.Tasks, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			labels = append(labels, l)
		}
	}

	places := []int64{0}
	if len(plan.Places) > 0 {
		places = places[:0]
		for _, name := range plan.Places {
			places = append(places, byName[name])
		}
	}

	created := 0

	for _, d := range plan.Days {
		for _, g := range plan.Ranges {
			for _, locID := range places {
				start, end := genTimes(d.Day, g)
				if have[slotKey(locID, start, end)] {
					continue
				}

				sl := &store.Slot{PoleID: v.Pole.ID, LocationID: locID, Title: f.Title, Capacity: g.Capacity, StartsAt: start, EndsAt: end}
				if err := s.store.CreateSlot(ctx, sl); err != nil {
					return err
				}

				for _, label := range labels {
					if _, err := s.store.CreateTask(ctx, sl.ID, label); err != nil {
						return err
					}
				}

				created++
			}
		}
	}

	s.logSlotsCreated(r, v.Pole, created)
	s.flashCount(w, "flash.vol.slots_created", created)

	return s.redirectTab(w, r, v.Pole, "")
}
