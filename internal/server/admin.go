package server

import (
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"manage/internal/files"
	"manage/internal/store"
)

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.Handle("GET /admin/login", s.wrap(s.adminLogin))
	mux.Handle("POST /admin/login", s.wrap(s.adminDevLogin))
	mux.Handle("GET /admin/callback", s.wrap(s.adminCallback))
	mux.Handle("POST /admin/logout", s.wrap(s.adminLogout))

	h := func(pattern string, fn handlerFunc) { mux.Handle(pattern, s.wrap(s.requireAdmin(fn))) }

	// URLs never contain integer ids: editions and organizations are
	// addressed by slug, a partnership by organization and edition, other
	// rows by their random UUID.
	h("GET /admin/{$}", s.adminHome)
	h("GET /admin/volunteers", s.adminVolunteers)
	h("GET /admin/people", s.adminPeople)
	h("GET /admin/people/{person}", s.adminPerson)
	h("POST /admin/people/{person}", s.adminPersonSave)
	h("POST /admin/people/{person}/send-link", s.adminPersonSendLink)
	h("POST /admin/people/{person}/delete", s.adminPersonDelete)
	h("GET /admin/events", s.adminEvents)
	h("GET /admin/events/new", s.adminEventForm)
	h("POST /admin/events/new", s.adminEventSubmit)
	h("GET /admin/events/{event}", s.adminPipeline)
	h("GET /admin/events/{event}/edit", s.adminEventForm)
	h("POST /admin/events/{event}/edit", s.adminEventSubmit)
	h("POST /admin/events/{event}/documents", s.adminEventDocument)
	h("GET /admin/events/{event}/levels", s.adminLevels)
	h("POST /admin/events/{event}/levels", s.adminLevelSave)
	h("POST /admin/events/{event}/levels/{level}/delete", s.adminLevelDelete)
	h("POST /admin/events/{event}/perks", s.adminPerkSave)
	h("POST /admin/events/{event}/perks/{perk}/delete", s.adminPerkDelete)
	h("POST /admin/events/{event}/matrix", s.adminMatrixSave)
	h("POST /admin/events/{event}/clone", s.adminLevelsClone)
	h("GET /admin/events/{event}/attendees.csv", s.adminAttendeesCSV)
	h("POST /admin/events/{event}/purge", s.adminEventPurge)

	h("GET /admin/organizations", s.adminOrganizations)
	h("POST /admin/organizations", s.adminOrganizationCreate)
	h("GET /admin/organizations/{org}", s.adminOrganization)
	h("POST /admin/organizations/{org}", s.adminOrganizationUpdate)
	h("POST /admin/organizations/{org}/delete", s.adminOrganizationDelete)
	h("POST /admin/organizations/{org}/managers", s.adminManagerAdd)
	h("POST /admin/organizations/{org}/managers/{manager}", s.adminManagerUpdate)
	h("POST /admin/organizations/{org}/managers/{manager}/delete", s.adminManagerDelete)
	h("POST /admin/organizations/{org}/managers/{manager}/send-link", s.adminManagerSendLink)
	h("POST /admin/organizations/{org}/logo", s.adminLogoUpload)
	h("POST /admin/organizations/{org}/partnerships", s.adminPartnershipCreate)
	h("POST /admin/organizations/{org}/jobs/{job}/toggle", s.adminJobToggle)
	h("POST /admin/organizations/{org}/view-as", s.adminViewAs)

	h("GET /admin/organizations/{org}/editions/{event}", s.adminPartnership)
	h("POST /admin/organizations/{org}/editions/{event}", s.adminPartnershipUpdate)
	h("POST /admin/organizations/{org}/editions/{event}/documents", s.adminPartnershipDocument)
	h("POST /admin/organizations/{org}/editions/{event}/perks", s.adminPartnershipPerks)
	h("POST /admin/organizations/{org}/editions/{event}/attendees", s.adminAttendeeAdd)
	h("POST /admin/organizations/{org}/editions/{event}/attendees/{attendee}/delete", s.adminAttendeeDelete)
	h("POST /admin/organizations/{org}/editions/{event}/posts", s.adminSocialPostSave)
	h("POST /admin/organizations/{org}/editions/{event}/posts/{post}/delete", s.adminSocialPostDelete)
	h("POST /admin/organizations/{org}/editions/{event}/delete", s.adminPartnershipDelete)

	h("GET /admin/profile", s.adminProfile)
	h("POST /admin/profile", s.adminProfileSave)
	h("POST /admin/profile/avatar", s.adminAvatarUpload)
	h("POST /admin/profile/avatar/delete", s.adminAvatarDelete)
	h("POST /admin/stop-impersonate", s.adminStopImpersonate)
	h("GET /admin/files/{file}", s.adminFile)
}

func (s *Server) requireAdmin(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if rc(r).Admin == nil {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)

			return nil
		}

		return next(w, r)
	}
}

func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) error {
	if c := rc(r); c.adminToken != "" {
		if err := s.store.DeleteSession(r.Context(), c.adminToken); err != nil {
			return err
		}
	}

	s.clearCookie(w, adminCookie)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)

	return nil
}

func (s *Server) adminHome(w http.ResponseWriter, r *http.Request) error {
	e, err := s.store.CurrentEvent(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		// A fresh install: the steps, rather than a bare creation form.
		return s.render(w, r, "admin/welcome", s.page(r, "Bienvenue"))
	}

	if err != nil {
		return err
	}

	http.Redirect(w, r, eventURL(e), http.StatusSeeOther)

	return nil
}

// adminVolunteers opens the coordination of the current edition: the
// volunteer side of the staff administration lives with the coordinators'
// pages, which coordinators who are not staff use too.
func (s *Server) adminVolunteers(w http.ResponseWriter, r *http.Request) error {
	e, err := s.store.CurrentEvent(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	http.Redirect(w, r, "/volunteers/"+e.Slug+"/edition", http.StatusSeeOther)

	return nil
}

func (s *Server) adminEvents(w http.ResponseWriter, r *http.Request) error {
	events, err := s.store.ListEvents(r.Context())
	if err != nil {
		return err
	}

	p := s.page(r, "Éditions")
	p.D = events

	return s.render(w, r, "admin/events", p)
}

func (s *Server) eventFromPath(r *http.Request) (*store.Event, error) {
	return s.store.GetEventBySlug(r.Context(), r.PathValue("event"))
}

func (s *Server) adminEventForm(w http.ResponseWriter, r *http.Request) error {
	e := &store.Event{Year: time.Now().Year(), IsCurrent: true}

	if r.PathValue("event") != "" {
		var err error
		if e, err = s.eventFromPath(r); err != nil {
			return err
		}
	}

	others, err := s.otherEvents(r, e.ID)
	if err != nil {
		return err
	}

	p := s.page(r, "Édition")
	p.D = map[string]any{"E": e, "Others": others, "Ended": eventEnded(e)}

	return s.render(w, r, "admin/event_form", p)
}

// eventEnded tells whether an edition is over: its last day has passed.
func eventEnded(e *store.Event) bool {
	return e.EndsOn != nil && dayKey(time.Now()) > e.EndsOn.Format(time.DateOnly)
}

// adminEventPurge removes the personal data of a finished edition (see
// store.PurgeEvent), on the staff's request.
func (s *Server) adminEventPurge(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	to := "/admin/events/" + e.Slug + "/edit"

	if !eventEnded(e) {
		s.flash(w, "flash.event_not_over")
		http.Redirect(w, r, to, http.StatusSeeOther)

		return nil
	}

	end := e.EndsOn.In(time.UTC)
	ended := time.Date(end.Year(), end.Month(), end.Day()+1, 0, 0, 0, 0, paris)

	res, err := s.store.PurgeEvent(r.Context(), e.ID, ended)
	if err != nil {
		return err
	}

	for _, f := range res.Files {
		s.dropFile(r, f)
	}

	slog.Info("edition purged", "event", e.Slug, "attendees", res.Attendees, "signups", res.Signups, "people", res.People)
	s.flash(w, "flash.event_purged", strconv.FormatInt(res.Attendees, 10), strconv.FormatInt(res.Signups, 10), strconv.FormatInt(res.People, 10))
	http.Redirect(w, r, to, http.StatusSeeOther)

	return nil
}

func (s *Server) adminEventSubmit(w http.ResponseWriter, r *http.Request) error {
	e := &store.Event{}

	if r.PathValue("event") != "" {
		var err error
		if e, err = s.eventFromPath(r); err != nil {
			return err
		}
	}

	e.Name = strings.TrimSpace(r.FormValue("name"))
	e.Year = formInt(r, "year")
	e.Slug = store.Slugify(firstNonEmpty(r.FormValue("slug"), e.Name))
	e.StartsOn = parseDate(r.FormValue("starts_on"))
	e.EndsOn = parseDate(r.FormValue("ends_on"))
	e.LogoDeadline = parseDate(r.FormValue("logo_deadline"))
	e.AttendeesDeadline = parseDate(r.FormValue("attendees_deadline"))
	e.IsCurrent = r.FormValue("is_current") == "on"
	e.PaymentInfo = strings.TrimSpace(r.FormValue("payment_info"))
	e.PaymentMention = strings.TrimSpace(r.FormValue("payment_mention"))

	if e.Slug == "new" {
		e.Slug = "new-" + strconv.Itoa(e.Year)
	}

	if e.Name == "" || e.Year == 0 {
		p := s.page(r, "Édition")
		p.D = map[string]any{"E": e, "Error": "Le nom et l'année sont obligatoires."}

		return s.render(w, r, "admin/event_form", p)
	}

	isNew := e.ID == 0

	if err := s.store.SaveEvent(r.Context(), e); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			p := s.page(r, "Édition")
			p.D = map[string]any{"E": e, "Error": "Ce slug est déjà utilisé."}

			return s.render(w, r, "admin/event_form", p)
		}

		return err
	}

	// A new edition can start with the levels and perks of another one.
	if isNew && r.FormValue("copy_from") != "" {
		src, err := s.store.GetEventBySlug(r.Context(), r.FormValue("copy_from"))
		if err != nil {
			return errBadInput
		}

		if _, err := s.store.CloneLevelsAndPerks(r.Context(), src.ID, e.ID); err != nil {
			return err
		}

		s.flash(w, "flash.cloned")
		http.Redirect(w, r, eventURL(e)+"/levels", http.StatusSeeOther)

		return nil
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, eventURL(e)+"/edit", http.StatusSeeOther)

	return nil
}

// otherEvents lists the editions other than the given one, most recent
// first, as sources to copy levels from.
func (s *Server) otherEvents(r *http.Request, exceptID int64) ([]*store.Event, error) {
	events, err := s.store.ListEvents(r.Context())
	if err != nil {
		return nil, err
	}

	var out []*store.Event

	for _, e := range events {
		if e.ID != exceptID {
			out = append(out, e)
		}
	}

	return out, nil
}

// adminLevelsClone copies the levels, perks and their associations of
// another edition into this one, keeping what already exists.
func (s *Server) adminLevelsClone(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	src, err := s.store.GetEventBySlug(r.Context(), r.FormValue("source"))
	if err != nil {
		return errBadInput
	}

	if src.ID == e.ID {
		return errBadInput
	}

	res, err := s.store.CloneLevelsAndPerks(r.Context(), src.ID, e.ID)
	if err != nil {
		return err
	}

	if res.Levels+res.Perks+res.Pairs == 0 {
		s.flash(w, "flash.clone_nothing")
	} else {
		s.flash(w, "flash.cloned")
	}

	http.Redirect(w, r, eventURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminEventDocument(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	back := eventURL(e) + "/edit"

	name, err := s.upload(w, r, "file", files.KindPDF, maxPDFSize)
	if err != nil {
		var ue uploadError
		if errors.As(err, &ue) {
			s.flash(w, string(ue))
			http.Redirect(w, r, back, http.StatusSeeOther)

			return nil
		}

		return err
	}

	switch r.FormValue("kind") {
	case "brochure_fr":
		e.BrochureFRFile = name
	case "brochure_en":
		e.BrochureENFile = name
	case "guide_fr":
		e.GuideFRFile = name
	case "guide_en":
		e.GuideENFile = name
	default:
		return errBadInput
	}

	if err := s.store.SaveEvent(r.Context(), e); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

type pipelineRow struct {
	P         *store.Partnership
	Owner     *store.Admin
	Org       *store.Organization
	Level     *store.Level
	Pricing   Pricing
	Progress  Progress
	Attendees int
}

type pipelineColumn struct {
	Status string
	Rows   []*pipelineRow
}

func (s *Server) adminPipeline(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	levels, err := s.store.ListLevels(ctx, e.ID)
	if err != nil {
		return err
	}

	levelByID := map[int64]*store.Level{}
	for _, l := range levels {
		levelByID[l.ID] = l
	}

	partnerships, err := s.store.ListEventPartnerships(ctx, e.ID)
	if err != nil {
		return err
	}

	admins, err := s.store.ListAdmins(ctx)
	if err != nil {
		return err
	}

	adminByID := map[int64]*store.Admin{}
	for _, a := range admins {
		adminByID[a.ID] = a
	}

	// "mine" restricts the pipeline to the partnerships followed by the
	// current staff member.
	mine := r.URL.Query().Get("mine") == "1"
	me := rc(r).Admin.ID

	columns := map[string]*pipelineColumn{}
	for _, st := range store.Statuses {
		columns[st] = &pipelineColumn{Status: st}
	}

	var totals struct{ Confirmed, Signed, Paid, Pipeline, Partners, Attendees int }

	for _, p := range partnerships {
		if mine && (p.OwnerAdminID == nil || *p.OwnerAdminID != me) {
			continue
		}

		org, err := s.store.GetOrganization(ctx, p.OrgID)
		if err != nil {
			return err
		}

		n, err := s.store.CountAttendees(ctx, p.ID)
		if err != nil {
			return err
		}

		row := &pipelineRow{P: p, Org: org, Attendees: n, Progress: computeProgress(p, org, e, n, "")}

		if p.LevelID != nil {
			row.Level = levelByID[*p.LevelID]
		}

		row.Pricing = pricing(p, row.Level)

		if p.OwnerAdminID != nil {
			row.Owner = adminByID[*p.OwnerAdminID]
		}
		price := row.Pricing.Amount

		switch p.Status {
		case store.StatusConfirmed:
			totals.Confirmed += price
			totals.Partners++
			totals.Attendees += n

			if p.QuoteSignedAt != nil {
				totals.Signed += price
			}

			if p.InvoicePaidAt != nil {
				totals.Paid += price
			}
		case store.StatusNegotiating, store.StatusContacted:
			totals.Pipeline += price
		}

		columns[p.Status].Rows = append(columns[p.Status].Rows, row)
	}

	var ordered []*pipelineColumn
	for _, st := range store.Statuses {
		col := columns[st]
		sortRows(col.Rows)
		ordered = append(ordered, col)
	}

	events, err := s.store.ListEvents(ctx)
	if err != nil {
		return err
	}

	p := s.page(r, e.Name)
	p.D = map[string]any{"E": e, "Events": events, "Levels": levels, "Columns": ordered, "Totals": totals, "Count": len(partnerships), "Mine": mine}

	return s.render(w, r, "admin/pipeline", p)
}

// sortRows orders pipeline rows by level (highest first) then name.
func sortRows(rows []*pipelineRow) {
	rank := func(r *pipelineRow) [2]int {
		if r.Level == nil {
			return [2]int{-1, 0}
		}

		return [2]int{r.Level.Position, r.Level.Price}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rank(rows[i]), rank(rows[j])
		if a != b {
			return a[0] > b[0] || a[0] == b[0] && a[1] > b[1]
		}

		return strings.ToLower(rows[i].Org.Name) < strings.ToLower(rows[j].Org.Name)
	})
}

func (s *Server) adminLevels(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	levels, err := s.store.ListLevels(ctx, e.ID)
	if err != nil {
		return err
	}

	perks, err := s.store.ListPerks(ctx, e.ID)
	if err != nil {
		return err
	}

	matrix, err := s.store.LevelPerkMatrix(ctx, e.ID)
	if err != nil {
		return err
	}

	others, err := s.otherEvents(r, e.ID)
	if err != nil {
		return err
	}

	p := s.page(r, "Niveaux et contreparties")
	p.D = map[string]any{"E": e, "Levels": levels, "Perks": perks, "Matrix": matrix, "Others": others}

	return s.render(w, r, "admin/levels", p)
}

func (s *Server) adminLevelSave(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	l := &store.Level{EventID: e.ID}

	if uid := r.FormValue("level"); uid != "" {
		if l, err = s.store.GetLevelByUID(r.Context(), e.ID, uid); err != nil {
			return err
		}
	}

	l.Name = strings.TrimSpace(r.FormValue("name"))
	l.Position = formInt(r, "position")
	l.Price = formInt(r, "price")
	l.MaxPartners = formInt(r, "max_partners")
	l.MaxAttendees = formInt(r, "max_attendees")
	l.TaxMode = r.FormValue("tax_mode")
	l.Color = strings.ToLower(strings.TrimSpace(r.FormValue("color")))

	if l.Name == "" {
		return errBadInput
	}

	if err := s.store.SaveLevel(r.Context(), l); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, eventURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminLevelDelete(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	l, err := s.store.GetLevelByUID(r.Context(), e.ID, r.PathValue("level"))
	if err != nil {
		return err
	}

	if err := s.store.DeleteLevel(r.Context(), l.ID); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, eventURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminPerkSave(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	p := &store.Perk{
		EventID:  e.ID,
		NameFR:   strings.TrimSpace(r.FormValue("name_fr")),
		NameEN:   strings.TrimSpace(r.FormValue("name_en")),
		Position: formInt(r, "position"),
	}

	if uid := r.FormValue("perk"); uid != "" {
		existing, err := s.store.GetPerkByUID(r.Context(), e.ID, uid)
		if err != nil {
			return err
		}

		p.ID, p.UID = existing.ID, existing.UID
	}

	if p.NameFR == "" {
		return errBadInput
	}

	if err := s.store.SavePerk(r.Context(), p); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, eventURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminPerkDelete(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	perk, err := s.store.GetPerkByUID(r.Context(), e.ID, r.PathValue("perk"))
	if err != nil {
		return err
	}

	if err := s.store.DeletePerk(r.Context(), perk.ID); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, eventURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminMatrixSave(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	if err := r.ParseForm(); err != nil {
		return errBadInput
	}

	// Checkbox values are "<level uid>:<perk uid>"; unknown uids are
	// ignored.
	levels, err := s.store.ListLevels(r.Context(), e.ID)
	if err != nil {
		return err
	}

	perks, err := s.store.ListPerks(r.Context(), e.ID)
	if err != nil {
		return err
	}

	levelIDs, perkIDs := map[string]int64{}, map[string]int64{}

	for _, l := range levels {
		levelIDs[l.UID] = l.ID
	}

	for _, p := range perks {
		perkIDs[p.UID] = p.ID
	}

	var pairs [][2]int64

	for _, v := range r.PostForm["lp"] {
		lu, pu, ok := strings.Cut(v, ":")
		if l, p := levelIDs[lu], perkIDs[pu]; ok && l != 0 && p != 0 {
			pairs = append(pairs, [2]int64{l, p})
		}
	}

	if err := s.store.SetLevelPerkMatrix(r.Context(), e.ID, pairs); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, eventURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminAttendeesCSV(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	levels, err := s.store.ListLevels(ctx, e.ID)
	if err != nil {
		return err
	}

	levelName := map[int64]string{}
	for _, l := range levels {
		levelName[l.ID] = l.Name
	}

	partnerships, err := s.store.ListEventPartnerships(ctx, e.ID)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "participants-"+e.Slug+".csv"))

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Organisation", "Niveau", "Prénom", "Nom", "Email", "Rôle", "Régime alimentaire"})

	for _, p := range partnerships {
		if p.Status != store.StatusConfirmed {
			continue
		}

		org, err := s.store.GetOrganization(ctx, p.OrgID)
		if err != nil {
			return err
		}

		attendees, err := s.store.ListAttendees(ctx, p.ID)
		if err != nil {
			return err
		}

		level := ""
		if p.LevelID != nil {
			level = levelName[*p.LevelID]
		}

		for _, a := range attendees {
			role := "Stand"
			if a.Role == store.RoleVisitor {
				role = "Visiteur"
			}

			// Partners type these cells: a spreadsheet must not run them.
			_ = cw.Write([]string{csvSafe(org.Name), csvSafe(level), csvSafe(a.FirstName), csvSafe(a.LastName), csvSafe(a.Email), role, csvSafe(a.Dietary)})
		}
	}

	cw.Flush()

	return cw.Error()
}

func (s *Server) adminOrganizations(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	orgs, err := s.store.ListOrganizations(ctx)
	if err != nil {
		return err
	}

	events, err := s.store.ListEvents(ctx)
	if err != nil {
		return err
	}

	eventByID := map[int64]*store.Event{}
	for _, e := range events {
		eventByID[e.ID] = e
	}

	// Each row shows the most recent edition and opens the partner file on
	// it.
	type row struct {
		Org    *store.Organization
		URL    string
		Latest *store.Partnership
		Event  *store.Event
		Level  *store.Level
	}

	var rows []row

	for _, o := range orgs {
		rw := row{Org: o, URL: orgURL(o)}

		latest, level, err := s.latestPartnership(r, o.ID)
		if err != nil {
			return err
		}

		if latest != nil {
			rw.Latest, rw.Level, rw.Event = latest, level, eventByID[latest.EventID]
			rw.URL = partnershipURL(o, rw.Event)
		}

		rows = append(rows, rw)
	}

	p := s.page(r, "Partenaires")
	p.D = map[string]any{"Rows": rows, "Events": events}

	return s.render(w, r, "admin/organizations", p)
}

func (s *Server) adminOrganizationCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return errBadInput
	}

	org := &store.Organization{Name: name}
	if err := s.store.CreateOrganization(ctx, org); err != nil {
		return err
	}

	if slug := r.FormValue("event"); slug != "" {
		e, err := s.store.GetEventBySlug(ctx, slug)
		if err != nil {
			return errBadInput
		}

		p := &store.Partnership{OrgID: org.ID, EventID: e.ID, Status: r.FormValue("status")}
		if !store.ValidStatus(p.Status) {
			p.Status = store.StatusProspect
		}

		if err := s.store.CreatePartnership(ctx, p); err != nil {
			return err
		}
	}

	if email := store.NormalizeEmail(r.FormValue("manager_email")); email != "" {
		if !validEmail(email) {
			s.flash(w, "flash.invalid_email")
			http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

			return nil
		}

		m := &store.Manager{
			OrgID: org.ID, Email: email, Name: strings.TrimSpace(r.FormValue("manager_name")),
			Lang: firstNonEmpty(r.FormValue("lang"), "fr"), InvitedBy: rc(r).Actor(),
		}
		if _, err := s.store.AddManager(ctx, m); err != nil {
			return err
		}

		if r.FormValue("send_invite") == "on" {
			if err := s.sendInvite(r, m, org, s.cfg.SiteName); err != nil {
				return err
			}
		}
	}

	target, err := s.ficheURL(r, org)
	if err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, target, http.StatusSeeOther)

	return nil
}

func (s *Server) orgFromPath(r *http.Request) (*store.Organization, error) {
	return s.store.GetOrganizationBySlug(r.Context(), r.PathValue("org"))
}

func (s *Server) adminOrganization(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	managers, err := s.store.ListManagers(ctx, org.ID)
	if err != nil {
		return err
	}

	logos, err := s.store.ListLogos(ctx, org.ID)
	if err != nil {
		return err
	}

	jobs, err := s.store.ListJobOffers(ctx, org.ID)
	if err != nil {
		return err
	}

	activity, err := s.store.ListActivity(ctx, org.ID, 50)
	if err != nil {
		return err
	}

	header, err := s.ficheHeaderFor(r, org, ficheOrgTab)
	if err != nil {
		return err
	}

	p := s.page(r, org.Name)
	p.D = map[string]any{
		"O": org, "Managers": managers,
		"Logos": logos, "Jobs": jobs, "Activity": activity, "ActionLabel": actionLabel, "Header": header,
	}

	return s.render(w, r, "admin/organization", p)
}

func (s *Server) adminOrganizationUpdate(w http.ResponseWriter, r *http.Request) error {
	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	readOrgForm(r, org)

	if org.Name == "" {
		return errBadInput
	}

	if err := s.store.UpdateOrganization(r.Context(), org); err != nil {
		return err
	}

	s.record(r, org, nil, "organization.updated", "")
	s.flash(w, "flash.saved")
	http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

	return nil
}

func (s *Server) adminOrganizationDelete(w http.ResponseWriter, r *http.Request) error {
	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	if r.FormValue("confirm") != org.Name {
		s.flash(w, "flash.confirm_mismatch")
		http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

		return nil
	}

	if err := s.store.DeleteOrganization(r.Context(), org.ID); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, "/admin/organizations", http.StatusSeeOther)

	return nil
}

func (s *Server) adminManagerAdd(w http.ResponseWriter, r *http.Request) error {
	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	back := orgURL(org)

	m := &store.Manager{
		OrgID:     org.ID,
		Email:     store.NormalizeEmail(r.FormValue("email")),
		Name:      strings.TrimSpace(r.FormValue("name")),
		Lang:      r.FormValue("lang"),
		InvitedBy: rc(r).Actor(),
	}

	if !validLang(m.Lang) {
		m.Lang = "fr"
	}

	if !validEmail(m.Email) {
		s.flash(w, "flash.invalid_email")
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	if _, err := s.store.AddManager(r.Context(), m); err != nil {
		return err
	}

	if r.FormValue("send_invite") == "on" {
		if err := s.sendInvite(r, m, org, s.cfg.SiteName); err != nil {
			return err
		}
	}

	s.record(r, org, nil, "manager.invited", m.Email)
	s.flash(w, "flash.saved")
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

func (s *Server) managerFromPath(r *http.Request) (*store.Organization, *store.Manager, error) {
	org, err := s.orgFromPath(r)
	if err != nil {
		return nil, nil, err
	}

	m, err := s.store.GetManagerByUID(r.Context(), r.PathValue("manager"))
	if err != nil {
		return nil, nil, err
	}

	if m.OrgID != org.ID {
		return nil, nil, errNotFound
	}

	return org, m, nil
}

// adminManagerUpdate edits a manager's name, email and language. Changing
// the email revokes the manager's sessions, since the account now belongs to
// another address.
func (s *Server) adminManagerUpdate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	org, m, err := s.managerFromPath(r)
	if err != nil {
		return err
	}

	back := orgURL(org)
	oldEmail := m.Email

	m.Email = store.NormalizeEmail(r.FormValue("email"))
	m.Name = strings.TrimSpace(r.FormValue("name"))

	if lang := r.FormValue("lang"); validLang(lang) {
		m.Lang = lang
	}

	if !validEmail(m.Email) {
		s.flash(w, "flash.invalid_email")
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	err = s.store.UpdateManagerIdentity(ctx, m)
	if errors.Is(err, store.ErrDuplicate) {
		s.flash(w, "managers.already")
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	details := m.Email
	if m.Email != oldEmail {
		if err := s.store.DetachManagerSessions(ctx, m.ID); err != nil {
			return err
		}

		details = oldEmail + " → " + m.Email
	}

	s.record(r, org, nil, "manager.updated", details)
	s.flash(w, "flash.saved")
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

func (s *Server) adminManagerDelete(w http.ResponseWriter, r *http.Request) error {
	org, m, err := s.managerFromPath(r)
	if err != nil {
		return err
	}

	if err := s.store.DetachManagerSessions(r.Context(), m.ID); err != nil {
		return err
	}

	if err := s.store.DeleteManager(r.Context(), org.ID, m.ID); err != nil {
		return err
	}

	s.record(r, org, nil, "manager.removed", m.Email)
	s.flash(w, "flash.deleted")
	http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

	return nil
}

func (s *Server) adminManagerSendLink(w http.ResponseWriter, r *http.Request) error {
	org, m, err := s.managerFromPath(r)
	if err != nil {
		return err
	}

	if err := s.sendInvite(r, m, org, s.cfg.SiteName); err != nil {
		return err
	}

	s.flash(w, "flash.invited")
	http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

	return nil
}

func (s *Server) adminLogoUpload(w http.ResponseWriter, r *http.Request) error {
	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	back := orgURL(org)

	if err := s.saveLogo(w, r, org); err != nil {
		var ue uploadError
		if errors.As(err, &ue) {
			s.flash(w, string(ue))
			http.Redirect(w, r, back, http.StatusSeeOther)

			return nil
		}

		return err
	}

	s.flash(w, "flash.logo_saved")
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

// adminPartnershipCreate attaches the organization to another edition. When
// it already has a partnership, the new one is a renewal: status
// "contacted", same level (matched by name) and same staff owner. Otherwise
// it starts as a prospect.
func (s *Server) adminPartnershipCreate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	target, err := s.store.GetEventBySlug(ctx, r.FormValue("event"))
	if err != nil {
		return errBadInput
	}

	np := &store.Partnership{OrgID: org.ID, EventID: target.ID, Status: store.StatusProspect}
	action := "partnership.created"

	latest, level, err := s.latestPartnership(r, org.ID)
	if err != nil {
		return err
	}

	if latest != nil {
		np.Status, np.OwnerAdminID, action = store.StatusContacted, latest.OwnerAdminID, "partnership.renewed"

		if level != nil {
			levels, err := s.store.ListLevels(ctx, target.ID)
			if err != nil {
				return err
			}

			for _, l := range levels {
				if strings.EqualFold(l.Name, level.Name) {
					np.LevelID = &l.ID
				}
			}
		}
	}

	err = s.store.CreatePartnership(ctx, np)
	if errors.Is(err, store.ErrDuplicate) {
		s.flash(w, "flash.already_renewed")
		http.Redirect(w, r, safeRedirect(r.FormValue("return"), orgURL(org)), http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	s.record(r, org, &np.ID, action, target.Name)

	if latest != nil {
		s.flash(w, "flash.renewed")
	}

	http.Redirect(w, r, partnershipURL(org, target), http.StatusSeeOther)

	return nil
}

func (s *Server) adminJobToggle(w http.ResponseWriter, r *http.Request) error {
	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	j, err := s.store.GetJobOffer(r.Context(), org.ID, r.PathValue("job"))
	if err != nil {
		return err
	}

	j.Hidden = !j.Hidden

	if err := s.store.SaveJobOffer(r.Context(), j); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

	return nil
}

// adminViewAs opens the partner area as the organization, on the edition
// given by "event" when set. "return" is the admin page to come back to.
func (s *Server) adminViewAs(w http.ResponseWriter, r *http.Request) error {
	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	if err := s.store.SetImpersonation(r.Context(), rc(r).adminToken, &org.ID); err != nil {
		return err
	}

	s.setCookie(w, returnCookie, safeRedirect(r.FormValue("return"), orgURL(org)), adminSessionTTL)

	target := "/partners/"
	if slug := r.FormValue("event"); slug != "" {
		target += "?event=" + url.QueryEscape(slug)
	}

	http.Redirect(w, r, target, http.StatusSeeOther)

	return nil
}

// adminStopImpersonate leaves the partner area and goes back to the admin
// page the staff member came from.
func (s *Server) adminStopImpersonate(w http.ResponseWriter, r *http.Request) error {
	c := rc(r)

	if err := s.store.SetImpersonation(r.Context(), c.adminToken, nil); err != nil {
		return err
	}

	back := "/admin/"
	if c.Impersonating {
		back = orgURL(c.Org)
	}

	if ck, err := r.Cookie(returnCookie); err == nil && strings.HasPrefix(ck.Value, "/admin/") {
		back = safeRedirect(ck.Value, back)
	}

	s.clearCookie(w, returnCookie)
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

// partnershipFromPath resolves /admin/organizations/{org}/editions/{event}.
func (s *Server) partnershipFromPath(r *http.Request) (*store.Partnership, error) {
	org, err := s.orgFromPath(r)
	if err != nil {
		return nil, err
	}

	e, err := s.eventFromPath(r)
	if err != nil {
		return nil, err
	}

	return s.store.GetPartnershipByOrgEvent(r.Context(), org.ID, e.ID)
}

func (s *Server) adminPartnership(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	org, err := s.store.GetOrganization(ctx, p.OrgID)
	if err != nil {
		return err
	}

	e, err := s.store.GetEvent(ctx, p.EventID)
	if err != nil {
		return err
	}

	levels, err := s.store.ListLevels(ctx, e.ID)
	if err != nil {
		return err
	}

	attendees, err := s.store.ListAttendees(ctx, p.ID)
	if err != nil {
		return err
	}

	posts, err := s.store.ListSocialPosts(ctx, p.ID)
	if err != nil {
		return err
	}

	data := map[string]any{
		"P": p, "O": org, "E": e, "URL": partnershipURL(org, e), "Levels": levels, "Attendees": attendees, "Posts": posts,
		"Progress": computeProgress(p, org, e, len(attendees), ""),
	}

	if p.LevelID != nil {
		perks, err := s.store.ListLevelPerks(ctx, *p.LevelID)
		if err != nil {
			return err
		}

		done, err := s.store.DonePerks(ctx, p.ID)
		if err != nil {
			return err
		}

		data["Perks"], data["DonePerks"] = perks, done

		for _, l := range levels {
			if l.ID == *p.LevelID {
				data["Level"] = l
			}
		}
	}

	level, _ := data["Level"].(*store.Level)
	data["Pricing"] = pricing(p, level)
	header, err := s.ficheHeaderFor(r, org, p.ID)
	if err != nil {
		return err
	}

	medal := rankedMedal(level, levels)
	header.Medal = &medal
	data["Header"] = header

	if data["Admins"], err = s.store.ListAdmins(ctx); err != nil {
		return err
	}

	page := s.page(r, org.Name+" — "+e.Name)
	page.D = data

	return s.render(w, r, "admin/partnership", page)
}

func (s *Server) adminPartnershipUpdate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	if st := r.FormValue("status"); store.ValidStatus(st) {
		p.Status = st
	}

	p.LevelID = nil

	if r.FormValue("level") != "" {
		l, err := s.store.GetLevelByUID(ctx, p.EventID, r.FormValue("level"))
		if err != nil {
			return errBadInput
		}

		p.LevelID = &l.ID
	}

	p.PriceOverride = nil

	if v := strings.TrimSpace(r.FormValue("price_override")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return errBadInput
		}

		p.PriceOverride = &n
	}

	p.OwnerAdminID = nil

	if uid := r.FormValue("owner"); uid != "" {
		owner, err := s.store.GetAdminByUID(ctx, uid)
		if err != nil {
			return errBadInput
		}

		p.OwnerAdminID = &owner.ID
	}

	p.PONumber = strings.TrimSpace(r.FormValue("po_number"))
	p.QuoteSentAt = parseDate(r.FormValue("quote_sent_at"))
	p.QuoteSignedAt = parseDate(r.FormValue("quote_signed_at"))
	p.InvoiceSentAt = parseDate(r.FormValue("invoice_sent_at"))
	p.InvoicePaidAt = parseDate(r.FormValue("invoice_paid_at"))
	p.FollowupAt = parseDate(r.FormValue("followup_at"))
	p.PartnerNotes = strings.TrimSpace(r.FormValue("partner_notes"))
	p.InternalNotes = strings.TrimSpace(r.FormValue("internal_notes"))

	if err := s.store.UpdatePartnership(ctx, p); err != nil {
		return err
	}

	org, err := s.store.GetOrganization(ctx, p.OrgID)
	if err != nil {
		return err
	}

	s.record(r, org, &p.ID, "partnership.updated", "Statut : "+p.Status)
	s.flash(w, "flash.saved")
	http.Redirect(w, r, partnershipURLFromPath(r), http.StatusSeeOther)

	return nil
}

func (s *Server) adminPartnershipDocument(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	back := partnershipURLFromPath(r)

	name, err := s.upload(w, r, "file", files.KindPDF, maxPDFSize)
	if err != nil {
		var ue uploadError
		if errors.As(err, &ue) {
			s.flash(w, string(ue))
			http.Redirect(w, r, back, http.StatusSeeOther)

			return nil
		}

		return err
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	kind := r.FormValue("kind")

	switch kind {
	case "quote":
		p.QuoteFile = name
		if p.QuoteSentAt == nil {
			p.QuoteSentAt = &today
		}
	case "invoice":
		p.InvoiceFile = name
		if p.InvoiceSentAt == nil {
			p.InvoiceSentAt = &today
		}
	default:
		return errBadInput
	}

	if err := s.store.UpdatePartnership(ctx, p); err != nil {
		return err
	}

	org, err := s.store.GetOrganization(ctx, p.OrgID)
	if err != nil {
		return err
	}

	s.record(r, org, &p.ID, "document.uploaded", kind)

	if r.FormValue("notify") == "on" && p.VisibleToPartner() {
		if err := s.notifyManagers(r, org, "mail."+kind+".subject", "mail."+kind+".body"); err != nil {
			return err
		}
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

func (s *Server) adminPartnershipPerks(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	if err := r.ParseForm(); err != nil {
		return errBadInput
	}

	var ids []int64

	if p.LevelID != nil {
		allowed, err := s.store.ListLevelPerks(ctx, *p.LevelID)
		if err != nil {
			return err
		}

		checked := map[string]bool{}
		for _, v := range r.PostForm["perk"] {
			checked[v] = true
		}

		for _, perk := range allowed {
			if checked[perk.UID] {
				ids = append(ids, perk.ID)
			}
		}
	}

	if err := s.store.SetDonePerks(ctx, p.ID, ids); err != nil {
		return err
	}

	org, err := s.store.GetOrganization(ctx, p.OrgID)
	if err != nil {
		return err
	}

	s.record(r, org, &p.ID, "perks.updated", "")
	s.flash(w, "flash.saved")
	http.Redirect(w, r, partnershipURLFromPath(r), http.StatusSeeOther)

	return nil
}

func (s *Server) adminAttendeeAdd(w http.ResponseWriter, r *http.Request) error {
	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	a := &store.Attendee{
		PartnershipID: p.ID,
		FirstName:     strings.TrimSpace(r.FormValue("first_name")),
		LastName:      strings.TrimSpace(r.FormValue("last_name")),
		Email:         strings.TrimSpace(r.FormValue("email")),
		Role:          r.FormValue("role"),
		Dietary:       strings.TrimSpace(r.FormValue("dietary")),
	}

	if a.Role != store.RoleVisitor {
		a.Role = store.RoleBooth
	}

	if a.FirstName == "" || a.LastName == "" {
		return errBadInput
	}

	if err := s.store.AddAttendee(r.Context(), a); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, partnershipURLFromPath(r), http.StatusSeeOther)

	return nil
}

func (s *Server) adminAttendeeDelete(w http.ResponseWriter, r *http.Request) error {
	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	if err := s.store.DeleteAttendee(r.Context(), p.ID, r.PathValue("attendee")); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, partnershipURLFromPath(r), http.StatusSeeOther)

	return nil
}

// adminSocialPostSave creates or updates a planned social media post. When
// the post gets its URL and "notify" is checked, managers are emailed so they
// can share it.
func (s *Server) adminSocialPostSave(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	post := &store.SocialPost{PartnershipID: p.ID}

	if uid := r.FormValue("post"); uid != "" {
		if post, err = s.store.GetSocialPost(ctx, p.ID, uid); err != nil {
			return err
		}
	}

	wasPublished := post.Published()

	post.Network = r.FormValue("network")
	post.PlannedAt = parseDate(r.FormValue("planned_at"))
	post.URL = normalizeURL(strings.TrimSpace(r.FormValue("url")))
	post.Note = strings.TrimSpace(r.FormValue("note"))

	if !store.ValidNetwork(post.Network) {
		return errBadInput
	}

	if err := s.store.SaveSocialPost(ctx, post); err != nil {
		return err
	}

	org, err := s.store.GetOrganization(ctx, p.OrgID)
	if err != nil {
		return err
	}

	s.record(r, org, &p.ID, "social.saved", networkName(post.Network)+" "+post.URL)

	if post.Published() && !wasPublished && r.FormValue("notify") == "on" && p.VisibleToPartner() {
		if err := s.notifyManagers(r, org, "mail.post.subject", "mail.post.body", networkName(post.Network), post.URL); err != nil {
			return err
		}
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, partnershipURLFromPath(r), http.StatusSeeOther)

	return nil
}

func (s *Server) adminSocialPostDelete(w http.ResponseWriter, r *http.Request) error {
	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	if err := s.store.DeleteSocialPost(r.Context(), p.ID, r.PathValue("post")); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, partnershipURLFromPath(r), http.StatusSeeOther)

	return nil
}

// eventsWithoutPartnership lists the editions an organization is not yet
// attached to.
func (s *Server) eventsWithoutPartnership(r *http.Request, orgID int64) ([]*store.Event, error) {
	ctx := r.Context()

	events, err := s.store.ListEvents(ctx)
	if err != nil {
		return nil, err
	}

	existing, err := s.store.ListOrgPartnerships(ctx, orgID)
	if err != nil {
		return nil, err
	}

	taken := map[int64]bool{}
	for _, p := range existing {
		taken[p.EventID] = true
	}

	var out []*store.Event

	for _, e := range events {
		if !taken[e.ID] {
			out = append(out, e)
		}
	}

	return out, nil
}

func (s *Server) adminPartnershipDelete(w http.ResponseWriter, r *http.Request) error {
	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	if err := s.store.DeletePartnership(r.Context(), p.ID); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, "/admin/organizations/"+r.PathValue("org"), http.StatusSeeOther)

	return nil
}

const maxAvatarSize = 2 << 20

func (s *Server) adminProfile(w http.ResponseWriter, r *http.Request) error {
	return s.render(w, r, "admin/profile", s.page(r, "Mon profil"))
}

func (s *Server) adminProfileSave(w http.ResponseWriter, r *http.Request) error {
	a := rc(r).Admin
	a.Name = strings.TrimSpace(r.FormValue("name"))

	if err := s.store.UpdateAdminProfile(r.Context(), a); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, "/admin/profile", http.StatusSeeOther)

	return nil
}

func (s *Server) adminAvatarUpload(w http.ResponseWriter, r *http.Request) error {
	name, err := s.upload(w, r, "avatar", files.KindLogo, maxAvatarSize)
	if err != nil {
		var ue uploadError
		if errors.As(err, &ue) {
			s.flash(w, string(ue))
			http.Redirect(w, r, "/admin/profile", http.StatusSeeOther)

			return nil
		}

		return err
	}

	a := rc(r).Admin
	a.AvatarFile = name

	if err := s.store.UpdateAdminProfile(r.Context(), a); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, "/admin/profile", http.StatusSeeOther)

	return nil
}

func (s *Server) adminAvatarDelete(w http.ResponseWriter, r *http.Request) error {
	a := rc(r).Admin
	a.AvatarFile = ""

	if err := s.store.UpdateAdminProfile(r.Context(), a); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, "/admin/profile", http.StatusSeeOther)

	return nil
}

func (s *Server) adminFile(w http.ResponseWriter, r *http.Request) error {
	s.files.Serve(w, r, r.PathValue("file"), "")

	return nil
}
