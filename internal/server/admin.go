package server

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"manage/internal/files"
	"manage/internal/i18n"
	"manage/internal/store"
)

func (s *Server) adminRoutes(mux *router) {
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
	h("POST /admin/people/{person}/photo", s.adminPersonPhoto)
	h("POST /admin/people/{person}/photo/delete", s.adminPersonPhotoDelete)
	// The editions themselves: the global administration.
	h("GET /admin/editions", s.adminEvents)
	h("GET /admin/editions/new", s.adminEventForm)
	h("POST /admin/editions/new", s.adminEventSubmit)
	h("GET /admin/editions/{event}", s.adminEventForm)
	h("POST /admin/editions/{event}", s.adminEventSubmit)
	h("POST /admin/editions/{event}/documents", s.adminEventDocument)
	h("GET /admin/editions/{event}/levels", s.adminLevels)
	h("POST /admin/editions/{event}/levels", s.adminLevelSave)
	h("POST /admin/editions/{event}/levels/{level}/delete", s.adminLevelDelete)
	h("POST /admin/editions/{event}/perks", s.adminPerkSave)
	h("POST /admin/editions/{event}/perks/{perk}/delete", s.adminPerkDelete)
	h("POST /admin/editions/{event}/matrix", s.adminMatrixSave)
	h("POST /admin/editions/{event}/clone", s.adminLevelsClone)
	h("POST /admin/editions/{event}/purge", s.adminEventPurge)

	// An edition's partners, for the staff: the pipeline and each
	// partnership, below the edition's slug.
	h("GET /{event}/partners/{$}", s.adminPipeline)
	h("GET /{event}/partners/attendees.csv", s.adminAttendeesCSV)

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

	h("GET /{event}/partners/{org}", s.adminPartnership)
	h("POST /{event}/partners/{org}", s.adminPartnershipUpdate)
	h("POST /{event}/partners/{org}/documents", s.adminPartnershipDocument)
	h("POST /{event}/partners/{org}/mark", s.adminPartnershipMark)
	h("POST /{event}/partners/{org}/perks", s.adminPartnershipPerks)
	h("POST /{event}/partners/{org}/attendees", s.adminAttendeeAdd)
	h("POST /{event}/partners/{org}/attendees/{attendee}/delete", s.adminAttendeeDelete)
	h("POST /{event}/partners/{org}/posts", s.adminSocialPostSave)
	h("POST /{event}/partners/{org}/posts/{post}/delete", s.adminSocialPostDelete)
	h("POST /{event}/partners/{org}/delete", s.adminPartnershipDelete)

	h("GET /admin/profile", s.adminProfile)
	h("POST /admin/profile", s.adminProfileSave)
	h("POST /admin/profile/avatar", s.adminAvatarUpload)
	h("POST /admin/profile/avatar/delete", s.adminAvatarDelete)
	h("POST /admin/profile/tokens", s.adminTokenCreate)
	h("POST /admin/profile/tokens/{token}/delete", s.adminTokenDelete)
	h("POST /admin/stop-impersonate", s.adminStopImpersonate)
	h("GET /admin/files/{file}", s.adminFile)
}

func (s *Server) requireAdmin(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		rc(r).area = "admin"

		a, err := s.actor(r)
		if err != nil {
			return err
		}

		if !a.Staff {
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

	http.Redirect(w, r, editionURL(e), http.StatusSeeOther)

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

	to := editionAdminURL(e)

	res, err := s.purgeEdition(r.Context(), act(r), e)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, to, http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

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

	e.Name = r.FormValue("name")
	e.Year = formInt(r, "year")
	e.Slug = r.FormValue("slug")
	e.StartsOn = parseDate(r.FormValue("starts_on"))
	e.EndsOn = parseDate(r.FormValue("ends_on"))
	e.LogoDeadline = parseDate(r.FormValue("logo_deadline"))
	e.AttendeesDeadline = parseDate(r.FormValue("attendees_deadline"))
	e.IsCurrent = r.FormValue("is_current") == "on"
	e.PaymentInfo = r.FormValue("payment_info")
	e.PaymentMention = r.FormValue("payment_mention")

	// A new edition can start with the levels and perks of another one.
	cloned, err := s.saveEdition(r.Context(), act(r), e, r.FormValue("copy_from"))
	if rf, ok := refused(err); ok {
		p := s.page(r, "Édition")
		p.D = map[string]any{"E": e, "Error": i18n.T("fr", rf.Key, anys(rf.Args)...)}

		return s.render(w, r, "admin/event_form", p)
	}

	if err != nil {
		return err
	}

	if cloned {
		s.flash(w, "flash.cloned")
		http.Redirect(w, r, editionAdminURL(e)+"/levels", http.StatusSeeOther)

		return nil
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, editionAdminURL(e), http.StatusSeeOther)

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

	copied, err := s.cloneLevels(r.Context(), act(r), e, src)
	if err != nil {
		return err
	}

	if copied {
		s.flash(w, "flash.cloned")
	} else {
		s.flash(w, "flash.clone_nothing")
	}

	http.Redirect(w, r, editionAdminURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminEventDocument(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	back := editionAdminURL(e)

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

	if err := s.setEditionDocument(r.Context(), act(r), e, r.FormValue("kind"), name); err != nil {
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
	// Due is a follow-up planned for today or earlier: someone to chase.
	Due bool
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

	// The follow-ups due, gathered at the top, the oldest first.
	var due []*pipelineRow

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

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

		row := &pipelineRow{P: p, Org: org, Attendees: n, Progress: computeProgress(p, org, e, n)}
		row.Due = p.FollowupAt != nil && p.Status != store.StatusDeclined && !p.FollowupAt.After(today)

		if row.Due {
			due = append(due, row)
		}

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

	sort.SliceStable(due, func(i, j int) bool { return due[i].P.FollowupAt.Before(*due[j].P.FollowupAt) })

	p := s.page(r, e.Name)
	p.D = map[string]any{"E": e, "Levels": levels, "Columns": ordered, "Totals": totals, "Count": len(partnerships), "Mine": mine,
		"Due": due}

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

	l.Name = r.FormValue("name")
	l.Position = formInt(r, "position")
	l.Price = formInt(r, "price")
	l.MaxPartners = formInt(r, "max_partners")
	l.MaxAttendees = formInt(r, "max_attendees")
	l.TaxMode = r.FormValue("tax_mode")
	l.Color = r.FormValue("color")

	if err := s.saveLevel(r.Context(), act(r), e, l); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, editionAdminURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminLevelDelete(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	if err := s.deleteLevel(r.Context(), act(r), e, r.PathValue("level")); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, editionAdminURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminPerkSave(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	p := &store.Perk{
		EventID:  e.ID,
		NameFR:   r.FormValue("name_fr"),
		NameEN:   r.FormValue("name_en"),
		Position: formInt(r, "position"),
	}

	if uid := r.FormValue("perk"); uid != "" {
		existing, err := s.store.GetPerkByUID(r.Context(), e.ID, uid)
		if err != nil {
			return err
		}

		p.ID, p.UID = existing.ID, existing.UID
	}

	if err := s.savePerk(r.Context(), act(r), e, p); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, editionAdminURL(e)+"/levels", http.StatusSeeOther)

	return nil
}

func (s *Server) adminPerkDelete(w http.ResponseWriter, r *http.Request) error {
	e, err := s.eventFromPath(r)
	if err != nil {
		return err
	}

	if err := s.deletePerk(r.Context(), act(r), e, r.PathValue("perk")); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, editionAdminURL(e)+"/levels", http.StatusSeeOther)

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

	// Checkbox values are "<level uid>:<perk uid>".
	if err := s.saveLevelPerks(r.Context(), act(r), e, r.PostForm["lp"]); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, editionAdminURL(e)+"/levels", http.StatusSeeOther)

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

		latest, level, err := s.latestPartnership(ctx, o.ID)
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
	org, err := s.createOrganization(r.Context(), act(r), newOrganization{
		Name: r.FormValue("name"), Event: r.FormValue("event"), Status: r.FormValue("status"),
		ManagerEmail: r.FormValue("manager_email"), ManagerName: r.FormValue("manager_name"),
		Lang: r.FormValue("lang"), SendInvite: r.FormValue("send_invite") == "on",
	})
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
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

	if err := s.staffSaveOrganization(r.Context(), act(r), org); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

	return nil
}

func (s *Server) adminOrganizationDelete(w http.ResponseWriter, r *http.Request) error {
	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	err = s.deleteOrganization(r.Context(), act(r), org, r.FormValue("confirm"))
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

		return nil
	}

	if err != nil {
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

	m := &store.Manager{Email: r.FormValue("email"), Name: r.FormValue("name"), Lang: r.FormValue("lang")}

	err = s.addManager(r.Context(), act(r), org, m, r.FormValue("send_invite") == "on")
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

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
	org, m, err := s.managerFromPath(r)
	if err != nil {
		return err
	}

	back := orgURL(org)

	err = s.updateManager(r.Context(), act(r), org, m, managerEdit{Email: r.FormValue("email"), Name: r.FormValue("name"), Lang: r.FormValue("lang")})
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

func (s *Server) adminManagerDelete(w http.ResponseWriter, r *http.Request) error {
	org, m, err := s.managerFromPath(r)
	if err != nil {
		return err
	}

	if err := s.removeManager(r.Context(), act(r), org, m); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, orgURL(org), http.StatusSeeOther)

	return nil
}

func (s *Server) adminManagerSendLink(w http.ResponseWriter, r *http.Request) error {
	org, m, err := s.managerFromPath(r)
	if err != nil {
		return err
	}

	if err := s.sendManagerLink(r.Context(), act(r), org, m); err != nil {
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

	err = s.saveLogo(w, r, func(ctx context.Context, variant, file string) error {
		return s.staffUploadLogo(ctx, act(r), org, variant, file)
	})
	if err != nil {
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
	org, err := s.orgFromPath(r)
	if err != nil {
		return err
	}

	target, err := s.store.GetEventBySlug(r.Context(), r.FormValue("event"))
	if err != nil {
		return errBadInput
	}

	renewed, err := s.createPartnership(r.Context(), act(r), org, target)
	if rf, ok := refused(err); ok {
		s.flash(w, rf.Key, rf.Args...)
		http.Redirect(w, r, safeRedirect(r.FormValue("return"), orgURL(org)), http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	if renewed {
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

	if err := s.toggleJob(r.Context(), act(r), org, r.PathValue("job")); err != nil {
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
		target = "/" + slug + "/partner/"
	}

	http.Redirect(w, r, target, http.StatusSeeOther)

	return nil
}

// adminStopImpersonate leaves the partner area and goes back to the admin
// page the staff member came from.
// staffPartnersPath matches the staff's pages of an edition's partners.
var staffPartnersPath = regexp.MustCompile(`^/[^/]+/partners/`)

func (s *Server) adminStopImpersonate(w http.ResponseWriter, r *http.Request) error {
	c := rc(r)

	if err := s.store.SetImpersonation(r.Context(), c.adminToken, nil); err != nil {
		return err
	}

	back := "/admin/"
	if c.Impersonating {
		back = orgURL(c.Org)
	}

	// Back to a staff page only: the administration, or an edition's
	// partners.
	if ck, err := r.Cookie(returnCookie); err == nil && (strings.HasPrefix(ck.Value, "/admin/") || staffPartnersPath.MatchString(ck.Value)) {
		back = safeRedirect(ck.Value, back)
	}

	s.clearCookie(w, returnCookie)
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

// partnershipFromPath resolves /{event}/partners/{org}.
func (s *Server) partnershipFromPath(r *http.Request) (*store.Partnership, error) {
	_, _, p, err := s.partnershipOfPath(r)

	return p, err
}

// partnershipOfPath resolves /{event}/partners/{org}, with the organization
// and the edition, which the services take too.
func (s *Server) partnershipOfPath(r *http.Request) (*store.Organization, *store.Event, *store.Partnership, error) {
	org, err := s.orgFromPath(r)
	if err != nil {
		return nil, nil, nil, err
	}

	e, err := s.eventFromPath(r)
	if err != nil {
		return nil, nil, nil, err
	}

	p, err := s.store.GetPartnershipByOrgEvent(r.Context(), org.ID, e.ID)
	if err != nil {
		return nil, nil, nil, err
	}

	return org, e, p, nil
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
		"Progress": computeProgress(p, org, e, len(attendees)),
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
	org, _, p, err := s.partnershipOfPath(r)
	if err != nil {
		return err
	}

	err = s.savePartnership(r.Context(), act(r), org, p, partnershipEdit{
		Status:        r.FormValue("status"),
		Level:         r.FormValue("level"),
		PriceOverride: r.FormValue("price_override"),
		Owner:         r.FormValue("owner"),
		PONumber:      formEdit(r, "po_number", p.PONumber),
		QuoteSentAt:   parseDate(r.FormValue("quote_sent_at")),
		QuoteSignedAt: parseDate(r.FormValue("quote_signed_at")),
		InvoiceSentAt: parseDate(r.FormValue("invoice_sent_at")),
		InvoicePaidAt: parseDate(r.FormValue("invoice_paid_at")),
		FollowupAt:    parseDate(r.FormValue("followup_at")),
		PartnerNotes:  formEdit(r, "partner_notes", p.PartnerNotes),
		InternalNotes: r.FormValue("internal_notes"),
	})
	if err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, partnershipURLFromPath(r), http.StatusSeeOther)

	return nil
}

// adminPartnershipMark records, in one click, that the quote came back
// signed or the invoice was paid today: the commonest updates, which
// otherwise went through the whole follow-up form.
func (s *Server) adminPartnershipMark(w http.ResponseWriter, r *http.Request) error {
	org, _, p, err := s.partnershipOfPath(r)
	if err != nil {
		return err
	}

	if err := s.markPartnership(r.Context(), act(r), org, p, r.FormValue("what")); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, partnershipURLFromPath(r), http.StatusSeeOther)

	return nil
}

func (s *Server) adminPartnershipDocument(w http.ResponseWriter, r *http.Request) error {
	org, e, p, err := s.partnershipOfPath(r)
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

	err = s.setPartnershipDocument(r.Context(), act(r), org, e, p, r.FormValue("kind"), name, r.FormValue("notify") == "on")
	if err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}

func (s *Server) adminPartnershipPerks(w http.ResponseWriter, r *http.Request) error {
	org, _, p, err := s.partnershipOfPath(r)
	if err != nil {
		return err
	}

	if err := r.ParseForm(); err != nil {
		return errBadInput
	}

	if err := s.setDonePerks(r.Context(), act(r), org, p, r.PostForm["perk"]); err != nil {
		return err
	}

	s.flash(w, "flash.saved")
	http.Redirect(w, r, partnershipURLFromPath(r), http.StatusSeeOther)

	return nil
}

func (s *Server) adminAttendeeAdd(w http.ResponseWriter, r *http.Request) error {
	p, err := s.partnershipFromPath(r)
	if err != nil {
		return err
	}

	if err := s.staffAddAttendee(r.Context(), act(r), p, readAttendee(r)); err != nil {
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

	if err := s.staffDeleteAttendee(r.Context(), act(r), p, r.PathValue("attendee")); err != nil {
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
	org, e, p, err := s.partnershipOfPath(r)
	if err != nil {
		return err
	}

	post := &store.SocialPost{PartnershipID: p.ID}

	if uid := r.FormValue("post"); uid != "" {
		if post, err = s.store.GetSocialPost(r.Context(), p.ID, uid); err != nil {
			return err
		}
	}

	err = s.saveSocialPost(r.Context(), act(r), org, e, p, post, socialPostEdit{
		Network: r.FormValue("network"), PlannedAt: parseDate(r.FormValue("planned_at")),
		URL: r.FormValue("url"), Note: r.FormValue("note"),
	}, r.FormValue("notify") == "on")
	if err != nil {
		return err
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

	if err := s.deleteSocialPost(r.Context(), act(r), p, r.PathValue("post")); err != nil {
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

	if err := s.deletePartnership(r.Context(), act(r), p); err != nil {
		return err
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, "/admin/organizations/"+r.PathValue("org"), http.StatusSeeOther)

	return nil
}

const maxAvatarSize = 2 << 20

func (s *Server) adminProfile(w http.ResponseWriter, r *http.Request) error {
	return s.renderAdminProfile(w, r, "")
}

// renderAdminProfile shows the staff profile, with the API token just made,
// if any: the only time it is shown.
func (s *Server) renderAdminProfile(w http.ResponseWriter, r *http.Request, newToken string) error {
	api, err := s.apiTokensOf(r, rc(r).Admin.PersonID, "/admin/profile/tokens", newToken)
	if err != nil {
		return err
	}

	p := s.page(r, "Mon profil")
	p.D = map[string]any{"API": api}

	return s.render(w, r, "admin/profile", p)
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
