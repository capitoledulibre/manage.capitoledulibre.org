package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"manage/internal/access"
	"manage/internal/i18n"
	"manage/internal/store"
)

// The MCP server: the volunteer side read by an AI assistant, for the staff
// and those who run an edition or a pole. It is served at /mcp over
// streamable HTTP, without sessions, and authenticated by a personal token
// (Authorization: Bearer), never by the site's cookies. Each request gets a
// server bound to the token's actor, whose tools go through the same
// services and rights (package access) as the pages: what a person sees
// there is what they read here, nothing more.
//
// The tools only read. Contact details go to whoever runs the pole, as on
// the pages; dietary needs are only counted, never quoted.

// apiTokenTTL is how long a personal token lasts.
const apiTokenTTL = 90 * 24 * time.Hour

// mcpCaller is who calls the API: the token's actor, and whether the
// token may change things.
type mcpCaller struct {
	actor  *access.Actor
	writes bool
}

type callerKey struct{}

func (s *Server) mcpRoutes(mux *router) {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		c, _ := r.Context().Value(callerKey{}).(*mcpCaller)
		if c == nil {
			c = &mcpCaller{actor: &access.Actor{}}
		}

		return s.mcpServer(c)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	mux.Handle("/mcp", cors(s.requireAPIToken(h)))
}

// requireAPIToken lets in a request with a valid personal token, as its
// actor; anything else gets a 401.
func (s *Server) requireAPIToken(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			s.unauthorized(w)

			return
		}

		t, err := s.store.APITokenFor(ctx, strings.TrimSpace(token))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				slog.Error("api token", "err", err)
			}

			s.unauthorized(w)

			return
		}

		person, err := s.store.GetPerson(ctx, t.PersonID)
		if err != nil {
			s.unauthorized(w)

			return
		}

		// A staff token works while its owner may still log in as staff:
		// on the list of addresses, and logging in as staff now and then.
		if t.Staff && !s.staffStillAllowed(ctx, person) {
			s.unauthorized(w)

			return
		}

		a, err := access.Load(ctx, s.store, person, t.Staff)
		if err != nil {
			slog.Error("api actor", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		h.ServeHTTP(w, r.WithContext(context.WithValue(ctx, callerKey{}, &mcpCaller{actor: a, writes: t.Writes})))
	})
}

// unauthorized asks for a token, and tells where to get one (RFC 9728).
func (s *Server) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="manage", resource_metadata="`+s.resourceMetadataURL()+`"`)
	http.Error(w, "a valid personal token is required", http.StatusUnauthorized)
}

// readOnly marks a tool that changes nothing.
var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

// mcpServer is the server of a caller, with the tools they may call: the
// ones that change things only for a token allowed to.
func (s *Server) mcpServer(c *mcpCaller) *mcp.Server {
	a := c.actor

	srv := mcp.NewServer(&mcp.Implementation{Name: "manage", Title: s.site.Name, Version: "1"}, &mcp.ServerOptions{
		Instructions: "Volunteers of the editions of " + s.site.Name + ": poles, slots and who is on them, " +
			"as the person whose token this is may see them. Start with list_editions; editions and poles are named by their slug.",
	})

	mcp.AddTool(srv, &mcp.Tool{Name: "list_editions", Annotations: readOnly,
		Description: "The editions the person may see, with their dates, volunteer status and the person's roles."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, editionsOut, error) {
			out, err := s.mcpEditions(ctx, a)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "pole_coverage", Annotations: readOnly,
		Description: "How well each pole of an edition is staffed: members, slots, places wanted and filled, slots short of people."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in editionIn) (*mcp.CallToolResult, coverageOut, error) {
			out, err := s.mcpCoverage(ctx, a, in.Edition)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "planning", Annotations: readOnly,
		Description: "The slots of an edition day by day, with who is on each: all of them, the person's own, those still to fill, " +
			"or (for whoever runs a pole) those over with tasks left undone."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in planningIn) (*mcp.CallToolResult, planningOut, error) {
			out, err := s.mcpPlanning(ctx, a, in)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "pole_members", Annotations: readOnly,
		Description: "The members of a pole the person runs, with their contact details and how many slots each has in it."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in poleIn) (*mcp.CallToolResult, membersOut, error) {
			out, err := s.mcpMembers(ctx, a, in)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "volunteer", Annotations: readOnly,
		Description: "One volunteer of an edition, by e-mail address, for its coordination: poles, slots, waiting list, t-shirt and meals."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in volunteerIn) (*mcp.CallToolResult, volunteerOut, error) {
			out, err := s.mcpVolunteer(ctx, a, in)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "logistics", Annotations: readOnly,
		Description: "What the volunteers of an edition need, counted: t-shirts by size, meals, dietary needs, answers still missing. " +
			"For its coordination."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in editionIn) (*mcp.CallToolResult, logisticsOut, error) {
			out, err := s.mcpLogistics(ctx, a, in.Edition)

			return nil, out, toolErr(err)
		})

	if c.writes {
		s.mcpWriteTools(srv, a)
	}

	return srv
}

// toolErr turns an error into what the assistant is told: a reason it can
// act on, never an internal detail. A refusal says why, in English.
func toolErr(err error) error {
	var he *httpError

	rf, refusedErr := refused(err)

	switch {
	case err == nil:
		return nil
	case refusedErr && rf.Key == outsideKey:
		return errors.New("the slot falls outside the dates of the edition; call again with confirm_outside to create it anyway")
	case refusedErr:
		return errors.New(i18n.T("en", rf.Key, anys(rf.Args)...))
	case errors.Is(err, store.ErrNotFound), errors.As(err, &he) && he.status == http.StatusNotFound:
		return errors.New("not found, or not visible to this person")
	case errors.As(err, &he) && he.status == http.StatusForbidden:
		return errors.New("not allowed for this person")
	case errors.As(err, &he) && he.status == http.StatusBadRequest:
		return errors.New("invalid arguments")
	}

	slog.Error("mcp tool", "err", err)

	return errors.New("internal error")
}

// --- inputs and outputs -----------------------------------------------------------

type editionIn struct {
	Edition string `json:"edition" jsonschema:"the slug of the edition, from list_editions"`
}

type poleIn struct {
	Edition string `json:"edition" jsonschema:"the slug of the edition"`
	Pole    string `json:"pole" jsonschema:"the slug of the pole, from pole_coverage"`
}

type planningIn struct {
	Edition string `json:"edition" jsonschema:"the slug of the edition"`
	View    string `json:"view,omitempty" jsonschema:"all (default), mine, open (still to fill) or late (over, tasks undone)"`
	Pole    string `json:"pole,omitempty" jsonschema:"the slug of a pole, to read only its slots"`
	Day     string `json:"day,omitempty" jsonschema:"a day as YYYY-MM-DD, to read only its slots"`
}

type volunteerIn struct {
	Edition string `json:"edition" jsonschema:"the slug of the edition"`
	Email   string `json:"email" jsonschema:"the e-mail address of the volunteer"`
}

type edition struct {
	Slug            string   `json:"slug"`
	Name            string   `json:"name"`
	Year            int      `json:"year"`
	StartsOn        string   `json:"starts_on,omitempty"`
	EndsOn          string   `json:"ends_on,omitempty"`
	VolunteerStatus string   `json:"volunteer_status"`
	Roles           []string `json:"roles,omitempty" jsonschema:"staff, coordinator, or the poles the person runs"`
}

type editionsOut struct {
	Editions []edition `json:"editions"`
}

type poleStaffing struct {
	Slug       string   `json:"slug"`
	Name       string   `json:"name"`
	Access     string   `json:"access" jsonschema:"public, private (invitation link), invitation (added by its managers) or restricted"`
	Members    int      `json:"members"`
	Slots      int      `json:"slots"`
	Wanted     int      `json:"places_wanted"`
	Filled     int      `json:"places_filled"`
	ShortSlots int      `json:"slots_short" jsonschema:"slots still missing at least one person"`
	Managers   []string `json:"managers"`
	Member     bool     `json:"member" jsonschema:"whether the person is in this pole"`
}

type coverageOut struct {
	Poles []poleStaffing `json:"poles"`
}

type personRef struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

type slotOut struct {
	ID         string      `json:"id"`
	StartsAt   string      `json:"starts_at"`
	EndsAt     string      `json:"ends_at"`
	Place      string      `json:"place,omitempty"`
	Title      string      `json:"title,omitempty"`
	Capacity   int         `json:"capacity"`
	Filled     int         `json:"filled"`
	Mine       bool        `json:"mine,omitempty"`
	People     []personRef `json:"people"`
	TasksDone  int         `json:"tasks_done"`
	TasksTotal int         `json:"tasks_total"`
}

type planPoleOut struct {
	Pole  string    `json:"pole"`
	Name  string    `json:"name"`
	Slots []slotOut `json:"slots"`
}

type planDayOut struct {
	Day    string        `json:"day"`
	Wanted int           `json:"places_wanted"`
	Filled int           `json:"places_filled"`
	Poles  []planPoleOut `json:"poles"`
}

type planningOut struct {
	Days []planDayOut `json:"days"`
}

type memberOut struct {
	personRef
	Slots   int  `json:"slots"`
	Manager bool `json:"manager"`
}

type membersOut struct {
	Members []memberOut `json:"members"`
}

type volunteerSlot struct {
	Pole     string `json:"pole"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
	Place    string `json:"place,omitempty"`
	Title    string `json:"title,omitempty"`
}

type volunteerOut struct {
	personRef
	Poles      []string        `json:"poles"`
	Slots      []volunteerSlot `json:"slots"`
	Waiting    []volunteerSlot `json:"waiting_for"`
	Tshirt     string          `json:"tshirt,omitempty"`
	Meals      []string        `json:"meals,omitempty"`
	HasDietary bool            `json:"has_dietary_needs" jsonschema:"whether they told of dietary needs; what they are stays on the site"`
	Answered   bool            `json:"answered_logistics"`
}

type countOut struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type logisticsOut struct {
	Open       bool       `json:"open" jsonschema:"whether the volunteers may still answer"`
	Tshirts    []countOut `json:"tshirts"`
	Meals      []countOut `json:"meals"`
	Dietary    int        `json:"dietary_needs" jsonschema:"how many told of dietary needs"`
	Answered   int        `json:"answered"`
	Pending    int        `json:"pending" jsonschema:"volunteers on a slot who have not answered, or whose answer is out of date"`
	IdleOnSite int        `json:"idle" jsonschema:"volunteers on no slot yet"`
}

// --- the tools ------------------------------------------------------------------

// clockIn writes a time in Paris, the event's.
func clockIn(t time.Time) string { return t.In(paris).Format("2006-01-02T15:04") }

func dayOf(t *time.Time) string {
	if t == nil {
		return ""
	}

	return t.Format(time.DateOnly)
}

// mcpEdition loads an edition the actor may see.
func (s *Server) mcpEdition(ctx context.Context, a *access.Actor, slug string) (*store.Event, error) {
	e, err := s.store.GetEventBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}

	if !a.SeesEvent(e) {
		return nil, errNotFound
	}

	return e, nil
}

func (s *Server) mcpEditions(ctx context.Context, a *access.Actor) (editionsOut, error) {
	out := editionsOut{Editions: []edition{}}

	all, err := s.store.ListEvents(ctx)
	if err != nil {
		return out, err
	}

	for _, e := range all {
		if !a.SeesEvent(e) {
			continue
		}

		ed := edition{Slug: e.Slug, Name: e.Name, Year: e.Year, StartsOn: dayOf(e.StartsOn), EndsOn: dayOf(e.EndsOn), VolunteerStatus: e.VolunteerStatus}

		switch {
		case a.Staff:
			ed.Roles = append(ed.Roles, "staff")
		case a.Coordinates(e.ID):
			ed.Roles = append(ed.Roles, "coordinator")
		}

		if led := a.LedPoles(e.ID); len(led) > 0 {
			poles, err := s.store.ListPoles(ctx, e.ID)
			if err != nil {
				return out, err
			}

			for _, p := range poles {
				if led[p.ID] {
					ed.Roles = append(ed.Roles, "manager of "+p.Slug)
				}
			}
		}

		out.Editions = append(out.Editions, ed)
	}

	return out, nil
}

func (s *Server) mcpCoverage(ctx context.Context, a *access.Actor, slug string) (coverageOut, error) {
	out := coverageOut{Poles: []poleStaffing{}}

	e, err := s.mcpEdition(ctx, a, slug)
	if err != nil {
		return out, err
	}

	coverage, members, err := s.poleCoverage(ctx, a, e)
	if err != nil {
		return out, err
	}

	for _, c := range coverage {
		ps := poleStaffing{Slug: c.Pole.Slug, Name: c.Pole.Name, Access: c.Pole.AccessMode, Members: c.Members, Slots: c.Slots,
			Wanted: c.Capacity, Filled: c.Filled, ShortSlots: c.Short, Managers: []string{}, Member: members[c.Pole.ID]}

		for _, m := range c.Managers {
			ps.Managers = append(ps.Managers, m.Label())
		}

		out.Poles = append(out.Poles, ps)
	}

	return out, nil
}

// personFor is how the actor sees someone on a slot: their contact details
// for whoever runs the pole, their name only for anyone else (and no
// address in place of a missing name).
func personFor(p *store.Person, manage bool) personRef {
	if manage {
		return personRef{Name: p.Label(), Email: p.Email, Phone: p.Phone}
	}

	return personRef{Name: p.Name}
}

func slotFor(ps *planSlot, manage bool) slotOut {
	sl := ps.Slot
	out := slotOut{ID: sl.UID, StartsAt: clockIn(sl.StartsAt), EndsAt: clockIn(sl.EndsAt), Place: sl.LocationName, Title: sl.Title,
		Capacity: sl.Capacity, Filled: sl.Filled, Mine: sl.Mine, People: []personRef{}, TasksDone: sl.TaskDone, TasksTotal: sl.TaskCount}

	for _, p := range ps.People {
		out.People = append(out.People, personFor(p, manage))
	}

	return out
}

func (s *Server) mcpPlanning(ctx context.Context, a *access.Actor, in planningIn) (planningOut, error) {
	out := planningOut{Days: []planDayOut{}}

	e, err := s.mcpEdition(ctx, a, in.Edition)
	if err != nil {
		return out, err
	}

	q := planQuery{View: in.View, Day: in.Day}

	switch q.View {
	case "":
		q.View = "all"
	case "all", "mine", "open", "late":
	default:
		return out, errBadInput
	}

	if in.Pole != "" {
		if q.Pole, err = s.store.GetPoleBySlug(ctx, e.ID, in.Pole); err != nil {
			return out, err
		}
	}

	days, _, err := s.planning(ctx, a, e, q)
	if err != nil {
		return out, err
	}

	for _, d := range days {
		day := planDayOut{Day: d.Day, Wanted: d.Capacity, Filled: d.Filled, Poles: []planPoleOut{}}

		for _, pp := range d.Poles {
			manage := a.ManagesPole(pp.Pole)
			po := planPoleOut{Pole: pp.Pole.Slug, Name: pp.Pole.Name, Slots: []slotOut{}}

			for _, ps := range pp.Slots {
				po.Slots = append(po.Slots, slotFor(ps, manage))
			}

			day.Poles = append(day.Poles, po)
		}

		out.Days = append(out.Days, day)
	}

	return out, nil
}

func (s *Server) mcpMembers(ctx context.Context, a *access.Actor, in poleIn) (membersOut, error) {
	out := membersOut{Members: []memberOut{}}

	e, err := s.mcpEdition(ctx, a, in.Edition)
	if err != nil {
		return out, err
	}

	p, err := s.store.GetPoleBySlug(ctx, e.ID, in.Pole)
	if err != nil {
		return out, err
	}

	v, err := s.poleViewOf(ctx, a, e, p)
	if err != nil {
		return out, err
	}

	members, err := s.poleMembers(ctx, a, v)
	if err != nil {
		return out, err
	}

	for _, m := range members {
		out.Members = append(out.Members, memberOut{personRef: personFor(m.Person, true), Slots: m.SlotCount, Manager: m.IsManager})
	}

	return out, nil
}

func volunteerSlots(slots []*store.Slot) []volunteerSlot {
	out := []volunteerSlot{}

	for _, sl := range slots {
		out = append(out, volunteerSlot{Pole: sl.PoleName, StartsAt: clockIn(sl.StartsAt), EndsAt: clockIn(sl.EndsAt), Place: sl.LocationName, Title: sl.Title})
	}

	return out
}

func (s *Server) mcpVolunteer(ctx context.Context, a *access.Actor, in volunteerIn) (volunteerOut, error) {
	var out volunteerOut

	e, err := s.mcpEdition(ctx, a, in.Edition)
	if err != nil {
		return out, err
	}

	// The coordination first: whether someone has an account is not for
	// anyone else to learn.
	if err := coordinates(a, e); err != nil {
		return out, err
	}

	who, err := s.store.PersonByEmail(ctx, store.NormalizeEmail(in.Email))
	if err != nil {
		return out, err
	}

	f, err := s.volunteerFileOf(ctx, a, e, who)
	if err != nil {
		return out, err
	}

	out = volunteerOut{personRef: personFor(who, true), Poles: append([]string{}, f.Volunteer.Poles...),
		Slots: volunteerSlots(f.Slots), Waiting: volunteerSlots(f.Waiting)}

	if d := f.Details; d != nil {
		out.Tshirt, out.Meals, out.HasDietary, out.Answered = d.Tshirt, d.Meals, strings.TrimSpace(d.Dietary) != "", d.Answered
	}

	return out, nil
}

func counts(in []countOf) []countOut {
	out := []countOut{}

	for _, c := range in {
		out = append(out, countOut{Name: c.Name, Count: c.Count})
	}

	return out
}

func (s *Server) mcpLogistics(ctx context.Context, a *access.Actor, slug string) (logisticsOut, error) {
	var out logisticsOut

	e, err := s.mcpEdition(ctx, a, slug)
	if err != nil {
		return out, err
	}

	l, err := s.logisticsOf(ctx, a, e)
	if err != nil {
		return out, err
	}

	answered := 0

	for _, line := range l.Lines {
		if line.Details != nil && line.Details.Answered {
			answered++
		}
	}

	return logisticsOut{Open: l.Open, Tshirts: counts(l.Sizes), Meals: counts(l.Meals), Dietary: len(l.Dietary), Answered: answered,
		Pending: len(l.Pending()), IdleOnSite: l.Idle}, nil
}
