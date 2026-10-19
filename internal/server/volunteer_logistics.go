package server

import (
	"encoding/csv"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"manage/internal/i18n"
	"manage/internal/store"
)

// The logistics of an edition's volunteers: the t-shirt sizes and the meals
// it provides, which each volunteer answers for, and which the coordination
// counts. And the reminders the coordination sends: to the volunteers on no
// slot yet, and to those on a slot who have not answered.

// maxDietary bounds the dietary needs a volunteer types.
const maxDietary = 300

// volunteerDetailsSave records a volunteer's answers for an edition.
func (s *Server) volunteerDetailsSave(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	e, err := s.volunteerEvent(r)
	if err != nil {
		return err
	}

	members, err := s.store.MemberPoleIDs(ctx, e.ID, person.ID)
	if err != nil {
		return err
	}

	if len(members) == 0 || !e.AsksVolunteers() {
		return errForbidden
	}

	if !e.DetailsOpen(time.Now()) {
		return errBadInput
	}

	d := &store.VolunteerDetails{EventID: e.ID, PersonID: person.ID, Dietary: strings.TrimSpace(r.FormValue("dietary")),
		AskedSizes: e.TshirtSizeList(), AskedMeals: e.MealList()}

	if size := r.FormValue("tshirt"); size == "" || slices.Contains(e.TshirtSizeList(), size) {
		d.Tshirt = size
	} else {
		return errBadInput
	}

	for _, m := range r.Form["meal"] {
		if !slices.Contains(e.MealList(), m) {
			return errBadInput
		}

		d.Meals = append(d.Meals, m)
	}

	if len([]rune(d.Dietary)) > maxDietary {
		return errBadInput
	}

	if err := s.store.SaveVolunteerDetails(ctx, d); err != nil {
		return err
	}

	s.flash(w, "flash.vol.details_saved")
	http.Redirect(w, r, "/volunteers/"+e.Slug, http.StatusSeeOther)

	return nil
}

// logisticsLine is a volunteer of the edition with their answers.
type logisticsLine struct {
	Volunteer *store.EventVolunteer
	Details   *store.VolunteerDetails
}

// logistics is what the edition's logistics tab shows: for the volunteers
// on at least one slot and those running the edition, the people the
// t-shirts and meals are for.
type logistics struct {
	// Open is true while volunteers can still answer.
	Open       bool
	Lines      []*logisticsLine
	Unanswered []*logisticsLine
	// Outdated answered before the choices on offer changed.
	Outdated []*logisticsLine
	Sizes    []countOf
	Meals    []countOf
	Dietary  []*logisticsLine
	Idle     int
}

// Pending is whom to remind: those who have not answered, and those whose
// answer is to be checked again.
func (l *logistics) Pending() []*logisticsLine {
	return append(slices.Clip(l.Unanswered), l.Outdated...)
}

// countOf counts a size or a meal. Gone is for one no longer on offer that
// some answers still name: kept in sight rather than silently dropped.
type countOf struct {
	Name  string
	Count int
	Gone  bool
}

// countChoices lists the counts of the choices on offer, in their order,
// then those of the choices gone.
func countChoices(offer []string, n map[string]int) []countOf {
	out := make([]countOf, 0, len(offer))

	for _, v := range offer {
		out = append(out, countOf{Name: v, Count: n[v]})
	}

	var gone []string

	for v := range n {
		if !slices.Contains(offer, v) {
			gone = append(gone, v)
		}
	}

	slices.Sort(gone)

	for _, v := range gone {
		out = append(out, countOf{Name: v, Count: n[v], Gone: true})
	}

	return out
}

func (s *Server) logistics(r *http.Request, e *store.Event) (*logistics, error) {
	ctx := r.Context()

	volunteers, err := s.store.ListEventVolunteers(ctx, e.ID)
	if err != nil {
		return nil, err
	}

	details, err := s.store.ListVolunteerDetails(ctx, e.ID)
	if err != nil {
		return nil, err
	}

	l := &logistics{Open: e.DetailsOpen(time.Now())}
	sizes := map[string]int{}
	meals := map[string]int{}

	for _, v := range volunteers {
		// Those running the edition eat and wear the t-shirt too.
		if v.SlotCount == 0 && !v.Staff {
			l.Idle++

			continue
		}

		line := &logisticsLine{Volunteer: v, Details: details[v.Person.ID]}
		l.Lines = append(l.Lines, line)

		if line.Details == nil {
			l.Unanswered = append(l.Unanswered, line)

			continue
		}

		if line.Details.Outdated(e) {
			l.Outdated = append(l.Outdated, line)
		}

		if line.Details.Tshirt != "" {
			sizes[line.Details.Tshirt]++
		}

		for _, m := range line.Details.Meals {
			meals[m]++
		}

		if line.Details.Dietary != "" {
			l.Dietary = append(l.Dietary, line)
		}
	}

	l.Sizes, l.Meals = countChoices(e.TshirtSizeList(), sizes), countChoices(e.MealList(), meals)

	return l, nil
}

// coordinationLogisticsCSV writes a line per volunteer counted, with what
// they answered, for the caterer and the t-shirt order. The meals gone from
// the offer that some still took get a column too, and "to_check" marks
// the answers given before the choices changed.
func (s *Server) coordinationLogisticsCSV(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	l, err := s.logistics(r, e)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="logistics-`+e.Slug+`.csv"`)

	cw := csv.NewWriter(w)

	head := []string{"name", "email", "slots", "minutes", "tshirt"}
	for _, m := range l.Meals {
		head = append(head, csvSafe(m.Name))
	}

	_ = cw.Write(append(head, "dietary", "to_check"))

	for _, line := range l.Lines {
		v, d := line.Volunteer, line.Details
		if d == nil {
			d = &store.VolunteerDetails{}
		}

		row := []string{csvSafe(v.Person.Name), csvSafe(v.Person.Email), strconv.Itoa(v.SlotCount), strconv.Itoa(v.Minutes), csvSafe(d.Tshirt)}

		for _, m := range l.Meals {
			row = append(row, flag(d.Takes(m.Name)))
		}

		_ = cw.Write(append(row, csvSafe(d.Dietary), flag(line.Details != nil && line.Details.Outdated(e))))
	}

	cw.Flush()

	return cw.Error()
}

// coordinationRemind writes to the volunteers yet to pick a slot ("idle"), or
// to those on a slot who have not answered the logistics ("details").
func (s *Server) coordinationRemind(w http.ResponseWriter, r *http.Request) error {
	e, err := s.coordinationEvent(r)
	if err != nil {
		return err
	}

	audience := r.FormValue("audience")

	var people []*store.Person

	switch audience {
	case "idle":
		if e.VolunteerStatus != store.VolunteersOpen {
			return errBadInput
		}

		volunteers, err := s.store.ListEventVolunteers(r.Context(), e.ID)
		if err != nil {
			return err
		}

		for _, v := range volunteers {
			if v.Idle() {
				people = append(people, v.Person)
			}
		}
	case "details":
		if !e.DetailsOpen(time.Now()) {
			return errBadInput
		}

		l, err := s.logistics(r, e)
		if err != nil {
			return err
		}

		for _, line := range l.Pending() {
			people = append(people, line.Volunteer.Person)
		}
	default:
		return errBadInput
	}

	planning := s.cfg.BaseURL + planningURL(e) + "?view=open"
	home := s.cfg.BaseURL + "/volunteers/" + e.Slug

	s.notifyPeople(people, func(lang string) string { return volSubject(lang, "mail.vol.remind_"+audience+".subject") },
		func(p *store.Person) string {
			link := home
			if audience == "idle" {
				link = planning
			}

			return s.volMail(p, e.Slug, i18n.T(p.Lang, "mail.vol.remind_"+audience+".body", e.Name, link))
		})

	s.logVol(r, &store.LogEntry{Action: store.LogReminded, EventID: e.ID, Title: audience, Count: len(people)})
	s.flashCount(w, "flash.vol.reminded", len(people))

	if audience == "idle" {
		http.Redirect(w, r, editionURL(e)+"/volunteers", http.StatusSeeOther)

		return nil
	}

	return s.redirectCoordination(w, r, e, "logistics")
}

// flag is a yes as a CSV cell: "1", or empty.
func flag(b bool) string {
	if b {
		return "1"
	}

	return ""
}
