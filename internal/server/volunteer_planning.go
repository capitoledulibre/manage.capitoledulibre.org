package server

import (
	"context"
	"encoding/csv"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"manage/internal/access"
	"manage/internal/store"
)

// The planning of an edition: one page for every volunteer, whose content
// follows their role. A volunteer sees the slots of their poles and of the
// open ones, with who is signed up, to join friends; they sign up only
// themselves. A pole lead sees, on their poles, the phone numbers and the
// management links; the coordination sees every pole. Three views (all,
// mine, to fill), a filter by pole and day, and a print layout: the roster
// for the day, with boxes to tick and lines for the free places.

// planSlot is a slot of the planning, with its people and checklist. Card
// is what the screen shows of it, filled by the planning page.
type planSlot struct {
	Slot   *store.Slot
	People []*store.Person
	Tasks  []*store.SlotTask
	Free   int
	Card   *slotCardData
}

// planPole is the slots of a pole on a day. Manage tells whether the
// person looking runs the pole, and so sees the phone numbers.
type planPole struct {
	Pole   *store.Pole
	Manage bool
	Slots  []*planSlot
	// Grid is the day of the pole as a timetable, in the grid layout.
	Grid *bulkGrid
	// MultiRole tells whether the pole's slots, over the whole edition,
	// staff more than one role; Groups is its day grouped by time then,
	// in the list layout, as on the pole's page, when Layout says so.
	MultiRole bool
	Layout    string
	Groups    []*slotGroup
}

type planDay struct {
	Day      string
	Poles    []*planPole
	Capacity int
	Filled   int
}

// planQuery selects the slots of a planning.
type planQuery struct {
	Pole *store.Pole // a single pole, nil for every one the actor plans
	View string      // "mine", "open", "late" or "all"
	Day  string      // a single day, empty for all of them
	// Layout is "grid" for each pole's day as a timetable instead of
	// cards, "names" for that timetable with who is on each slot, empty
	// for the cards.
	Layout string
}

// planPoles are the poles of an edition whose slots the actor's planning
// shows (access.PlansPole): every one for the coordination; else those the
// actor is in or runs, and the open ones they may join. members are the
// poles the actor is in.
func (s *Server) planPoles(ctx context.Context, a *access.Actor, e *store.Event) (shown []*store.Pole, members map[int64]bool, err error) {
	poles, err := s.store.ListPoles(ctx, e.ID)
	if err != nil {
		return nil, nil, err
	}

	members = map[int64]bool{}

	if a.Person != nil {
		if members, err = s.store.MemberPoleIDs(ctx, e.ID, a.Person.ID); err != nil {
			return nil, nil, err
		}
	}

	for _, p := range poles {
		if a.PlansPole(p, members[p.ID]) {
			shown = append(shown, p)
		}
	}

	return shown, members, nil
}

// planning gathers the slots of an edition the actor plans, day by day and
// pole by pole: those of the poles they may see (planPoles), and in the
// "late" view, of the poles they run. days lists the days the other
// filters leave, for the day filter.
func (s *Server) planning(ctx context.Context, a *access.Actor, e *store.Event, q planQuery) (out []*planDay, days []string, err error) {
	if !a.SeesEvent(e) {
		return nil, nil, errNotFound
	}

	poles, _, err := s.planPoles(ctx, a, e)
	if err != nil {
		return nil, nil, err
	}

	visible := map[int64]bool{}

	for _, p := range poles {
		// Late tasks are for whoever runs the pole to chase.
		if q.View != "late" || a.ManagesPole(p) {
			visible[p.ID] = true
		}
	}

	poleID := int64(0)
	if q.Pole != nil {
		if !visible[q.Pole.ID] {
			return nil, nil, errNotFound
		}

		poleID = q.Pole.ID
	}

	var personID int64
	if a.Person != nil {
		personID = a.Person.ID
	}

	slots, err := s.store.ListEventSlots(ctx, e.ID, personID)
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

	// Grouping a pole's slots goes by all of them, as on its page, not by
	// those the filters leave.
	byPole := map[int64][]*store.Slot{}
	for _, sl := range slots {
		byPole[sl.PoleID] = append(byPole[sl.PoleID], sl)
	}

	now := time.Now()

	for _, sl := range slots {
		switch {
		case !visible[sl.PoleID],
			poleID != 0 && sl.PoleID != poleID,
			q.View == "mine" && !sl.Mine,
			q.View == "open" && (sl.IsFull() || !sl.StartsAt.After(now)),
			q.View == "late" && (sl.TaskDone >= sl.TaskCount || !sl.EndsAt.Before(now)):
			continue
		}

		key := dayKey(sl.StartsAt)
		if len(days) == 0 || days[len(days)-1] != key {
			days = append(days, key)
		}

		if q.Day != "" && key != q.Day {
			continue
		}

		if len(out) == 0 || out[len(out)-1].Day != key {
			out = append(out, &planDay{Day: key})
		}

		d := out[len(out)-1]

		var pp *planPole

		for _, x := range d.Poles {
			if x.Pole.ID == sl.PoleID {
				pp = x
			}
		}

		if pp == nil {
			pp = &planPole{Pole: byID[sl.PoleID], MultiRole: multiRole(byPole[sl.PoleID], byID[sl.PoleID])}
			d.Poles = append(d.Poles, pp)
		}

		pp.Slots = append(pp.Slots, &planSlot{Slot: sl, People: people[sl.ID], Tasks: tasks[sl.ID], Free: max(sl.Capacity-sl.Filled, 0)})
		d.Capacity += sl.Capacity
		d.Filled += min(sl.Filled, sl.Capacity)
	}

	for _, d := range out {
		slices.SortStableFunc(d.Poles, func(a, b *planPole) int { return order[a.Pole.ID] - order[b.Pole.ID] })
	}

	return out, days, nil
}

// planningURL is the planning of an edition.
func planningURL(e *store.Event) string { return "/" + e.Slug + "/planning" }

var planViews = []string{"all", "mine", "open"}

// leadViews are the views of whoever runs a pole: the tasks left undone on
// the slots that are over, on the poles they run.
var leadViews = []string{"late"}

func (s *Server) planningPage(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	person := rc(r).Person

	e, err := s.volunteerEvent(r)
	if err != nil {
		return err
	}

	a := act(r)
	coordinator := a.Coordinates(e.ID)
	managed := a.LedPoles(e.ID)

	shown, members, err := s.planPoles(ctx, a, e)
	if err != nil {
		return err
	}

	query := r.URL.Query()

	views := planViews
	if coordinator || len(managed) > 0 {
		views = append(slices.Clone(planViews), leadViews...)
	}

	q := planQuery{View: query.Get("view"), Layout: query.Get("layout")}
	if q.Layout != "grid" && q.Layout != "names" {
		q.Layout = ""
	}

	if !slices.Contains(views, q.View) {
		q.View = "all"

		// A volunteer with slots comes for theirs, on the day above all;
		// whoever runs a pole comes for everyone's.
		if query.Get("view") == "" && !coordinator && len(managed) == 0 {
			mine, err := s.store.ListPersonSlots(ctx, e.ID, person.ID)
			if err != nil {
				return err
			}

			if len(mine) > 0 {
				q.View = "mine"
			}
		}
	}

	for _, p := range shown {
		if p.Slug == query.Get("pole") && (q.View != "late" || a.ManagesPole(p)) {
			q.Pole = p
		}
	}

	// The day: the one asked for, else today during the edition, the page
	// a volunteer opens in a corridor.
	day := query.Get("day")

	days, all, err := s.planning(ctx, a, e, q)
	if err != nil {
		return err
	}

	switch {
	case day == "all":
		day = ""
	case slices.Contains(all, day):
	case slices.Contains(all, dayKey(time.Now())) && day == "":
		day = dayKey(time.Now())
	default:
		day = ""
	}

	if day != "" {
		days = slices.DeleteFunc(days, func(d *planDay) bool { return d.Day != day })
	}

	waitlist, err := s.store.ListPersonWaitlist(ctx, e.ID, person.ID)
	if err != nil {
		return err
	}

	waiting := map[int64]bool{}
	for _, sl := range waitlist {
		waiting[sl.ID] = true
	}

	// Actions on a card come back to the planning as it was filtered.
	back := planningURL(e) + "?" + planURLQuery(q.View, q.Pole, dayParam(day, all), q.Layout)

	now := time.Now()
	lang := rc(r).Lang

	fiche := ficheBase(a, e)

	for _, d := range days {
		for _, pp := range d.Poles {
			pp.Manage = a.ManagesPole(pp.Pole)

			// In the list, a pole's slots group by time, as on its page;
			// one's own slots are few enough to stay a plain list.
			layout := ""
			if pp.MultiRole && q.Layout == "" && q.View != "mine" {
				layout = layoutTime
			}

			cards := make([]*slotCardData, 0, len(pp.Slots))

			for _, ps := range pp.Slots {
				ps.Card = &slotCardData{
					Lang: lang, Slot: ps.Slot, Pole: pp.Pole, Member: members[pp.Pole.ID],
					Locked: e.SignupsLocked(pp.Pole.FreezeAt, now), Waiting: waiting[ps.Slot.ID], Back: back,
					ShowPeople: true, People: ps.People, Manage: pp.Manage,
					Tasks: ps.Tasks, CanTick: ps.Slot.Mine || pp.Manage, OpenTasks: q.View == "late", Fiche: fiche,
					NoTime: layout == layoutTime,
				}
				cards = append(cards, ps.Card)
			}

			pp.Layout, pp.Groups = layout, groupSlots(cards, layout)
		}
	}

	// The grid layouts: each pole's day as a timetable, hours down and
	// places across, with or without who is on each slot.
	if q.Layout != "" {
		locations := map[int64][]*store.Location{}

		for _, d := range days {
			for _, pp := range d.Poles {
				if _, ok := locations[pp.Pole.ID]; !ok {
					if locations[pp.Pole.ID], err = s.store.ListLocations(ctx, pp.Pole.ID); err != nil {
						return err
					}
				}

				slots := make([]*store.Slot, 0, len(pp.Slots))
				people := map[int64][]*store.Person{}

				for _, ps := range pp.Slots {
					slots = append(slots, ps.Slot)
					people[ps.Slot.ID] = ps.People
				}

				pp.Grid = newBulkGrid(slots, locations[pp.Pole.ID])
				if q.Layout == "names" {
					pp.Grid.People, pp.Grid.Fiche, pp.Grid.Manage = people, fiche, pp.Manage
				}
			}
		}
	}

	// The filters, each link keeping the others.
	base := planningURL(e) + "?"
	dp := dayParam(day, all)
	layouts := []planLink{
		{Label: "vol.planning.layout_list", URL: base + planURLQuery(q.View, q.Pole, dp, ""), On: q.Layout == ""},
		{Label: "vol.planning.layout_grid", URL: base + planURLQuery(q.View, q.Pole, dp, "grid"), On: q.Layout != ""},
	}

	// On the grid, a switch shows who is on each slot, or hides them.
	var names *planLink

	switch q.Layout {
	case "grid":
		names = &planLink{Label: "vol.planning.names", URL: base + planURLQuery(q.View, q.Pole, dp, "names")}
	case "names":
		names = &planLink{Label: "vol.planning.names", URL: base + planURLQuery(q.View, q.Pole, dp, "grid"), On: true}
	}

	var viewLinks, poleLinks, dayLinks []planLink

	for _, v := range views {
		viewLinks = append(viewLinks, planLink{Label: "vol.planning.view_" + v, URL: base + planURLQuery(v, q.Pole, dp, q.Layout), On: v == q.View})
	}

	if len(shown) > 1 {
		poleLinks = append(poleLinks, planLink{Label: "vol.planning.all_poles", URL: base + planURLQuery(q.View, nil, dp, q.Layout), On: q.Pole == nil})

		for _, p := range shown {
			poleLinks = append(poleLinks, planLink{Name: p.Name, Pole: p, URL: base + planURLQuery(q.View, p, dp, q.Layout), On: q.Pole == p})
		}
	}

	if len(all) > 1 {
		dayLinks = append(dayLinks, planLink{Label: "vol.planning.all_days", URL: base + planURLQuery(q.View, q.Pole, dayParam("", all), q.Layout), On: day == ""})

		for _, d := range all {
			dayLinks = append(dayLinks, planLink{Day: d, URL: base + planURLQuery(q.View, q.Pole, d, q.Layout), On: d == day})
		}
	}

	// The CSV of what the person runs: the pole filtered on when they run
	// it, else the edition for the coordination.
	csvURL := ""

	switch {
	case q.Pole != nil && (coordinator || managed[q.Pole.ID]):
		csvURL = q.Pole.ManageURL() + "/signups.csv"
	case coordinator:
		csvURL = editionURL(e) + "/signups.csv"
	}

	data := map[string]any{
		"Event": e, "Days": days, "View": q.View, "Pole": q.Pole, "Today": dayKey(now), "Printed": now,
		"Views": viewLinks, "PoleLinks": poleLinks, "DayLinks": dayLinks, "CSV": csvURL, "Layouts": layouts, "Names": names, "Grid": q.Layout != "",
		"OpenURL": base + planURLQuery("open", q.Pole, dp, q.Layout),
	}

	// My slots: the waiting list and the calendar subscription go with
	// them. The subscription secret is created on the first visit of this
	// view, so a person who never opens it never gets one.
	if q.View == "mine" {
		upcoming, err := s.upcomingWaitlist(r, e.ID)
		if err != nil {
			return err
		}

		token, err := s.store.CalendarToken(ctx, person.ID)
		if err != nil {
			return err
		}

		data["Waiting"] = upcoming
		cal := s.cfg.BaseURL + "/calendar/" + token + ".ics"
		_, host, _ := strings.Cut(cal, "://")
		// webcal:// opens the calendar app's subscription in one tap on a
		// phone, where copying a link is a chore. html/template only lets
		// http(s) and mailto through: this one is ours.
		data["CalendarURL"], data["WebcalURL"] = cal, template.URL("webcal://"+host)
		data["OpenCalendar"] = query.Get("calendar") == "1"
	}

	p := s.page(r, "vol.planning.title")
	p.D = data

	return s.render(w, r, "volunteer/planning", p)
}

// planLink is a filter of the planning: Label is an i18n key, else Name
// or Day names it.
type planLink struct {
	Label string
	Name  string
	Pole  *store.Pole
	Day   string
	URL   string
	On    bool
}

// dayParam is the day filter of a link: "all" when every day is shown and
// today is one of them, so the link does not fall back to today.
func dayParam(day string, all []string) string {
	if day == "" && slices.Contains(all, dayKey(time.Now())) {
		return "all"
	}

	return day
}

// planURLQuery is the query of a planning link. The view always goes in:
// without one, the page picks a default (a volunteer's own slots), and the
// "everything" link would not lead to everything.
func planURLQuery(view string, pole *store.Pole, day, layout string) string {
	v := url.Values{}

	if view != "" {
		v.Set("view", view)
	}

	if layout != "" {
		v.Set("layout", layout)
	}

	if pole != nil {
		v.Set("pole", pole.Slug)
	}

	if day != "" {
		v.Set("day", day)
	}

	return v.Encode()
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
	days, _, err := s.planning(r.Context(), act(r), e, planQuery{Pole: pole})
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"day", "start", "end", "pole", "place", "title", "capacity", "name", "email", "phone"})

	for _, d := range days {
		for _, pp := range d.Poles {
			for _, ps := range pp.Slots {
				sl := ps.Slot
				line := []string{d.Day, clock(sl.StartsAt), clock(sl.EndsAt), csvSafe(pp.Pole.Name), csvSafe(sl.LocationName),
					csvSafe(sl.Title), strconv.Itoa(sl.Capacity)}

				if len(ps.People) == 0 {
					_ = cw.Write(append(line, "", "", ""))
				}

				for _, p := range ps.People {
					_ = cw.Write(append(slices.Clip(line), csvSafe(p.Name), csvSafe(p.Email), csvSafe(p.Phone)))
				}
			}
		}
	}

	cw.Flush()

	return cw.Error()
}
