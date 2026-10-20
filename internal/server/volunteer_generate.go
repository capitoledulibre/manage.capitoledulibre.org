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

// genHours are the hours of a day, as typed.
type genHours struct {
	Mode     string // "regular", "list", or "off" for a day without slots
	DayStart string // HH:MM
	DayEnd   string
	Length   int // minutes
	List     string
}

// genDayForm is a day of the form, with its own hours.
type genDayForm struct {
	Day   time.Time
	Field string // prefix of its fields, "d2026-11-14."
	Hours genHours
}

// genForm is the generator form, as typed.
type genForm struct {
	From, To string // YYYY-MM-DD
	Hours    genHours
	// PerDay gives each day its own hours, in Days, instead of Hours.
	PerDay       bool
	Days         []*genDayForm
	Locations    []string // UIDs of the existing places chosen
	NewLocations string   // one per line
	Capacity     int
	Title        string
	Tasks        string
	// The cells of the preview's timetable, by key: those shown (Seen),
	// those left ticked (Keep), and the volunteers typed in each (Caps).
	// A cell seen and not kept is not created; one not seen yet (new hours
	// or places since) is.
	Seen, Keep map[string]bool
	Caps       map[string]int
}

// genDay is a day of the preview.
type genDay struct {
	Day     time.Time
	Outside bool
	Off     bool
	Ranges  []genRange
	// Grid is the day as a timetable: a row per range, a cell per place
	// (one for the whole pole without places).
	Grid [][]genCell
	// Slots and Seats count what the day creates.
	Slots, Seats int
}

// genCell is a slot of the timetable: the volunteers it wants, or Exists
// when it is there already and will not be created again, or Skip when it
// was unticked. Key names it in the form: day, hours and place.
type genCell struct {
	Key      string
	Capacity int
	Exists   bool
	Skip     bool
}

// cellKey names a cell of the timetable in the form.
func cellKey(day time.Time, g genRange, place string) string {
	return fmt.Sprintf("%s.%d-%d.%s", day.Format(time.DateOnly), g.Start, g.End, place)
}

// genMsg is an error or a warning of the preview, about one day when the
// days have their own hours.
type genMsg struct {
	Key string // i18n key
	Day *time.Time
}

// genPlan is what a form would create.
type genPlan struct {
	Days      []*genDay
	Places    []string // names, existing and new
	NewPlaces []string
	Slots     int
	Seats     int
	Skipped   int
	Errors    []genMsg
	Warnings  []genMsg
}

// RangeCount counts the hour ranges of every day, each giving a slot per
// place.
func (p *genPlan) RangeCount() int {
	n := 0
	for _, d := range p.Days {
		n += len(d.Ranges)
	}

	return n
}

func (p *genPlan) fail(key string, day *time.Time) { p.Errors = appendMsg(p.Errors, genMsg{key, day}) }
func (p *genPlan) warn(key string, day *time.Time) {
	p.Warnings = appendMsg(p.Warnings, genMsg{key, day})
}

func appendMsg(list []genMsg, m genMsg) []genMsg {
	for _, o := range list {
		if o.Key == m.Key && (o.Day == nil) == (m.Day == nil) && (o.Day == nil || o.Day.Equal(*m.Day)) {
			return list
		}
	}

	return append(list, m)
}

var genLengths = []int{30, 60, 90, 120, 150, 180, 240, 360}

func (s *Server) generateRoutes(m func(string, handlerFunc)) {
	const base = "/{event}/poles/{pole}/manage/generate"

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

	f := &genForm{Hours: genHours{Mode: "regular", DayStart: "09:00", DayEnd: "18:00", Length: 120}, Capacity: 2, Tasks: v.Pole.DefaultTasks}

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

	f.fillDays(nil)

	return s.renderGenerate(w, r, v, f, locations, nil)
}

func (s *Server) renderGenerate(w http.ResponseWriter, r *http.Request, v *poleView, f *genForm, locations []*store.Location, plan *genPlan) error {
	p := s.page(r, "vol.gen.title")
	p.D = map[string]any{"V": v, "F": f, "Locations": locations, "Plan": plan, "Lengths": genLengths}

	return s.render(w, r, "volunteer/generate", p)
}

func readGenForm(r *http.Request) *genForm {
	f := &genForm{
		From: r.FormValue("from"), To: r.FormValue("to"), Hours: readGenHours(r, "", false),
		PerDay: r.FormValue("per_day") != "", Locations: r.Form["location"], NewLocations: r.FormValue("new_locations"),
		Capacity: formInt(r, "capacity"), Title: strings.TrimSpace(r.FormValue("title")), Tasks: r.FormValue("tasks"),
		Seen: map[string]bool{}, Keep: map[string]bool{}, Caps: map[string]int{},
	}

	for _, k := range r.Form["seen"] {
		f.Seen[k] = true

		if n := formInt(r, "cap."+k); n >= 1 && n <= 500 {
			f.Caps[k] = n
		}
	}

	for _, k := range r.Form["keep"] {
		f.Keep[k] = true
	}

	f.fillDays(r)

	return f
}

// readGenHours reads the hours whose fields start with prefix. Only a day
// of its own can be off: the common hours always give slots.
func readGenHours(r *http.Request, prefix string, offAllowed bool) genHours {
	h := genHours{
		Mode: r.FormValue(prefix + "mode"), DayStart: r.FormValue(prefix + "day_start"),
		DayEnd: r.FormValue(prefix + "day_end"), Length: formInt(r, prefix+"length"), List: r.FormValue(prefix + "list"),
	}

	if h.Mode != "list" && (h.Mode != "off" || !offAllowed) {
		h.Mode = "regular"
	}

	return h
}

// genDates reads the days of a form; ok is false when they make no sense.
func (f *genForm) genDates() (from, to time.Time, ok bool) {
	from, err1 := time.ParseInLocation(time.DateOnly, f.From, paris)
	to, err2 := time.ParseInLocation(time.DateOnly, f.To, paris)

	return from, to, err1 == nil && err2 == nil && !to.Before(from) && to.Sub(from) <= 31*24*time.Hour
}

// fillDays lists the days of the form with their hours: those typed for
// the day when there are some, else the common ones. A day added by a
// change of dates thus starts from the common hours. r is nil for a blank
// form.
func (f *genForm) fillDays(r *http.Request) {
	from, to, ok := f.genDates()
	if !ok {
		return
	}

	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		day := &genDayForm{Day: d, Field: "d" + d.Format(time.DateOnly) + ".", Hours: f.Hours}

		if r != nil && r.Form.Has(day.Field+"mode") {
			day.Hours = readGenHours(r, day.Field, true)
		}

		f.Days = append(f.Days, day)
	}
}

// ranges works out the slots of a day from its hours, with an error key
// when they make no sense and the warnings they deserve.
func (h genHours) ranges(capacity int) (out []genRange, errKey string, warnings []string) {
	switch h.Mode {
	case "off":
		return nil, "", nil
	case "list":
		ranges, ok := parseRanges(h.List, capacity)
		if !ok || len(ranges) == 0 {
			return nil, "vol.gen.err_list", nil
		}

		out = ranges
	default:
		start, ok1 := parseClock(h.DayStart)
		end, ok2 := parseClock(h.DayEnd)

		if !ok1 || !ok2 || end <= start || !slices.Contains(genLengths, h.Length) {
			return nil, "vol.gen.err_regular", nil
		}

		for t := start; t+h.Length <= end; t += h.Length {
			out = append(out, genRange{Start: t, End: t + h.Length, Capacity: capacity})
		}

		if len(out) == 0 {
			return nil, "vol.gen.err_regular", nil
		}

		if out[len(out)-1].End < end {
			warnings = append(warnings, "vol.gen.warn_remainder")
		}
	}

	// Ranges that overlap are allowed (two shifts covering a rush) but worth
	// a word.
	sorted := slices.Clone(out)
	slices.SortFunc(sorted, func(a, b genRange) int { return a.Start - b.Start })

	for i := 1; i < len(sorted); i++ {
		if sorted[i].Start < sorted[i-1].End {
			warnings = append(warnings, "vol.gen.warn_overlap")

			break
		}
	}

	return out, "", warnings
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

	if _, _, ok := f.genDates(); !ok {
		from, err1 := time.ParseInLocation(time.DateOnly, f.From, paris)
		to, err2 := time.ParseInLocation(time.DateOnly, f.To, paris)

		if err1 != nil || err2 != nil || to.Before(from) {
			p.fail("vol.gen.err_days", nil)
		} else {
			p.fail("vol.gen.err_days_long", nil)
		}
	}

	if f.Capacity < 1 || f.Capacity > 500 {
		p.fail("vol.err.slot_capacity", nil)
		f.Capacity = 1
	}

	for _, d := range f.Days {
		h, at := f.Hours, (*time.Time)(nil)
		if f.PerDay {
			h, at = d.Hours, &d.Day
		}

		ranges, errKey, warnings := h.ranges(f.Capacity)
		if errKey != "" {
			p.fail(errKey, at)
		}

		for _, w := range warnings {
			p.warn(w, at)
		}

		day := &genDay{Day: d.Day, Ranges: ranges, Off: h.Mode == "off", Outside: outsideDay(v.Event, d.Day)}
		if day.Outside && !day.Off {
			p.warn("vol.gen.warn_outside", nil)
		}

		p.Days = append(p.Days, day)
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

	// Each slot of the plan either exists already or is one more: once the
	// count passes what exists plus the bound, the plan is too big, and
	// going on (thousands of lines times thousands of places) would only
	// burn the CPU.
	limit := maxGenerated + len(have)
	tried := 0

days:
	for _, day := range p.Days {
		d := day.Day

		for _, g := range day.Ranges {
			row := make([]genCell, places)
			day.Grid = append(day.Grid, row)

			for i := range places {
				if tried++; tried > limit {
					p.Slots = max(p.Slots, maxGenerated+1)

					break days
				}

				locID, place := int64(0), ""
				if len(p.Places) > 0 {
					place = p.Places[i]
					locID = byName[place] // 0 for a new place, which cannot clash yet
				}

				key := cellKey(d, g, place)
				start, end := genTimes(d, g)

				if locID != 0 || len(p.Places) == 0 {
					if have[slotKey(locID, start, end)] {
						p.Skipped++
						row[i] = genCell{Key: key, Capacity: g.Capacity, Exists: true}

						continue
					}
				}

				cell := genCell{Key: key, Capacity: g.Capacity, Skip: f.Seen[key] && !f.Keep[key]}
				if n, ok := f.Caps[key]; ok {
					cell.Capacity = n
				}

				row[i] = cell

				if cell.Skip {
					continue
				}

				p.Slots++
				p.Seats += cell.Capacity
				day.Slots++
				day.Seats += cell.Capacity
			}
		}
	}

	if p.Slots > maxGenerated {
		p.fail("vol.gen.err_too_many", nil)
	}

	return p, nil
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

	// The timetable of the preview, as ticked and typed.
	for _, d := range plan.Days {
		for i, row := range d.Grid {
			g := d.Ranges[i]

			for j, cell := range row {
				if cell.Exists || cell.Skip {
					continue
				}

				locID := places[j]
				start, end := genTimes(d.Day, g)

				if have[slotKey(locID, start, end)] {
					continue
				}

				sl := &store.Slot{PoleID: v.Pole.ID, LocationID: locID, Title: f.Title, Capacity: cell.Capacity, StartsAt: start, EndsAt: end}
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
