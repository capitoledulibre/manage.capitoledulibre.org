package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"manage/internal/files"
	"manage/internal/store"
)

const (
	maxLogoSize = 10 << 20
	maxPDFSize  = 20 << 20
	// maxLogoVersions bounds the logo history kept per variant, and so the
	// disk space a partner can use: logos are the only files partners
	// upload.
	maxLogoVersions = 10
)

func (s *Server) partnerRoutes(mux *http.ServeMux) {
	h := func(pattern string, fn handlerFunc) { mux.Handle(pattern, s.wrap(s.requireOrg(fn))) }

	h("GET /partners/{$}", s.partnerDashboard)
	h("POST /partners/notes", s.partnerNotes)
	// Choosing an organization needs a logged-in person, not an organization
	// already chosen: one whose organization was taken away comes here too.
	mux.Handle("GET /partners/choose", s.wrap(s.requirePerson(s.partnerChoose)))
	mux.Handle("POST /partners/choose", s.wrap(s.requirePerson(s.partnerChooseSubmit)))
	h("GET /partners/documents/{event}/{kind}", s.partnerDocument)
	h("GET /partners/event-documents/{event}/{kind}", s.partnerEventDocument)
	h("GET /partners/organization", s.partnerOrganization)
	h("POST /partners/organization", s.partnerOrganizationSubmit)
	h("GET /partners/logo", s.partnerLogo)
	h("POST /partners/logo", s.partnerLogoSubmit)
	h("GET /partners/attendees", s.partnerAttendees)
	h("POST /partners/attendees", s.partnerAttendeeAdd)
	h("POST /partners/attendees/{attendee}/delete", s.partnerAttendeeDelete)
	h("GET /partners/jobs", s.partnerJobs)
	h("GET /partners/jobs/new", s.partnerJobForm)
	h("POST /partners/jobs/new", s.partnerJobSubmit)
	h("GET /partners/jobs/{job}", s.partnerJobForm)
	h("POST /partners/jobs/{job}", s.partnerJobSubmit)
	h("POST /partners/jobs/{job}/delete", s.partnerJobDelete)
	h("GET /partners/managers", s.partnerManagers)
	h("POST /partners/managers", s.partnerManagerInvite)
	h("POST /partners/managers/{manager}/delete", s.partnerManagerDelete)
	h("POST /partners/profile", s.partnerProfile)
}

func (s *Server) requirePerson(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if rc(r).Person == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)

			return nil
		}

		return next(w, r)
	}
}

func (s *Server) requireOrg(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if rc(r).Org == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)

			return nil
		}

		return next(w, r)
	}
}

// partnerContext is the edition currently displayed on the dashboard.
type partnerContext struct {
	Partnership *store.Partnership
	Event       *store.Event
	Level       *store.Level
	// Editions lists every edition visible to the partner, for the
	// edition selector.
	Editions []*store.Event
}

// currentPartnership resolves the edition selected with the "event"
// parameter, defaulting to the most recent visible partnership. It returns a
// nil Partnership when the organization has none visible.
func (s *Server) currentPartnership(r *http.Request) (*partnerContext, error) {
	ctx := r.Context()
	org := rc(r).Org

	all, err := s.store.ListOrgPartnerships(ctx, org.ID)
	if err != nil {
		return nil, err
	}

	pc := &partnerContext{}
	want := r.FormValue("event")

	for _, p := range all {
		if !p.VisibleToPartner() && !rc(r).Impersonating {
			continue
		}

		e, err := s.store.GetEvent(ctx, p.EventID)
		if err != nil {
			return nil, err
		}

		pc.Editions = append(pc.Editions, e)

		if pc.Partnership == nil && (want == "" || want == e.Slug) {
			pc.Partnership, pc.Event = p, e
		}
	}

	if pc.Partnership != nil && pc.Partnership.LevelID != nil {
		pc.Level, err = s.store.GetLevel(ctx, *pc.Partnership.LevelID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}

	return pc, nil
}

// eventQuery returns the query string preserving the selected edition.
func eventQuery(pc *partnerContext) string {
	if pc == nil || pc.Event == nil || len(pc.Editions) < 2 {
		return ""
	}

	return "?event=" + pc.Event.Slug
}

func (s *Server) partnerDashboard(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c := rc(r)

	pc, err := s.currentPartnership(r)
	if err != nil {
		return err
	}

	data := map[string]any{"PC": pc}

	if c.Manager != nil {
		others, err := s.store.ManagersByEmail(ctx, c.Manager.Email)
		if err != nil {
			return err
		}

		data["CanSwitch"] = len(others) > 1
	}

	if p := pc.Partnership; p != nil {
		attendees, err := s.store.CountAttendees(ctx, p.ID)
		if err != nil {
			return err
		}

		posts, err := s.store.ListSocialPosts(ctx, p.ID)
		if err != nil {
			return err
		}

		data["Posts"] = posts
		data["Pricing"] = pricing(p, pc.Level)

		levels, err := s.store.ListLevels(ctx, pc.Event.ID)
		if err != nil {
			return err
		}

		data["Medal"] = rankedMedal(pc.Level, levels)

		if p.OwnerAdminID != nil {
			contact, err := s.store.GetAdmin(ctx, *p.OwnerAdminID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}

			data["Contact"] = contact
		}
		data["Progress"] = computeProgress(p, c.Org, pc.Event, attendees, eventQuery(pc))
		data["Attendees"] = attendees
		data["EventDocs"] = eventDocs(pc.Event, c.Lang)

		if pc.Level != nil {
			perks, err := s.store.ListLevelPerks(ctx, pc.Level.ID)
			if err != nil {
				return err
			}

			done, err := s.store.DonePerks(ctx, p.ID)
			if err != nil {
				return err
			}

			data["Perks"], data["DonePerks"] = perks, done
		}
	}

	badges, err := s.orgBadges(r, c.Org.ID)
	if err != nil {
		return err
	}

	data["Badges"] = badges

	p := s.page(r, "dashboard.title")
	p.D = data

	return s.render(w, r, "partner/dashboard", p)
}

type eventDoc struct {
	Kind string
	Link string
}

// eventDocs lists the brochure and sponsor guide available for an edition, in
// the requested language when possible.
func eventDocs(e *store.Event, lang string) []eventDoc {
	var out []eventDoc

	for _, kind := range []string{"brochure", "guide"} {
		if eventDocFile(e, kind, lang) != "" {
			out = append(out, eventDoc{Kind: kind, Link: "/partners/event-documents/" + e.Slug + "/" + kind})
		}
	}

	return out
}

func eventDocFile(e *store.Event, kind, lang string) string {
	var fr, en string

	switch kind {
	case "brochure":
		fr, en = e.BrochureFRFile, e.BrochureENFile
	case "guide":
		fr, en = e.GuideFRFile, e.GuideENFile
	default:
		return ""
	}

	if lang == "en" && en != "" || fr == "" {
		return en
	}

	return fr
}

func (s *Server) partnerNotes(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.currentPartnership(r)
	if err != nil {
		return err
	}

	if pc.Partnership == nil {
		return errNotFound
	}

	p := pc.Partnership
	p.PONumber = strings.TrimSpace(r.FormValue("po_number"))
	p.PartnerNotes = strings.TrimSpace(r.FormValue("partner_notes"))

	if err := s.store.UpdatePartnership(r.Context(), p); err != nil {
		return err
	}

	s.record(r, rc(r).Org, &p.ID, "partnership.notes", fmt.Sprintf("PO: %s\n%s", p.PONumber, p.PartnerNotes))
	s.flash(w, "flash.saved")
	http.Redirect(w, r, "/partners/"+eventQuery(pc), http.StatusSeeOther)

	return nil
}

func (s *Server) partnerChoose(w http.ResponseWriter, r *http.Request) error {
	c := rc(r)

	managers, err := s.store.ManagersByEmail(r.Context(), c.Person.Email)
	if err != nil {
		return err
	}

	type choice struct {
		ManagerUID string
		Org        *store.Organization
	}

	var choices []choice

	for _, m := range managers {
		org, err := s.store.GetOrganization(r.Context(), m.OrgID)
		if err != nil {
			return err
		}

		choices = append(choices, choice{ManagerUID: m.UID, Org: org})
	}

	p := s.page(r, "choose.title")
	p.D = choices

	return s.render(w, r, "partner/choose", p)
}

func (s *Server) partnerChooseSubmit(w http.ResponseWriter, r *http.Request) error {
	c := rc(r)

	target, err := s.store.GetManagerByUID(r.Context(), r.FormValue("manager"))
	if err != nil {
		return errBadInput
	}

	// Only switch between organizations the logged-in person manages.
	if target.PersonID != c.Person.ID {
		return errForbidden
	}

	if err := s.store.SwitchSessionManager(r.Context(), c.personToken, target.ID); err != nil {
		return err
	}

	http.Redirect(w, r, "/partners/", http.StatusSeeOther)

	return nil
}

func (s *Server) partnerDocument(w http.ResponseWriter, r *http.Request) error {
	e, err := s.store.GetEventBySlug(r.Context(), r.PathValue("event"))
	if err != nil {
		return err
	}

	// The partnership is looked up for the logged-in organization only.
	p, err := s.store.GetPartnershipByOrgEvent(r.Context(), rc(r).Org.ID, e.ID)
	if err != nil {
		return err
	}

	if !p.VisibleToPartner() && !rc(r).Impersonating {
		return errNotFound
	}

	var file string

	switch r.PathValue("kind") {
	case "quote":
		file = p.QuoteFile
	case "invoice":
		file = p.InvoiceFile
	}

	if file == "" {
		return errNotFound
	}

	s.files.Serve(w, r, file, fmt.Sprintf("%s-%s.pdf", r.PathValue("kind"), rc(r).Org.Slug))

	return nil
}

func (s *Server) partnerEventDocument(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.store.GetEventBySlug(ctx, r.PathValue("event"))
	if err != nil {
		return err
	}

	pc, err := s.currentPartnership(r)
	if err != nil {
		return err
	}

	allowed := false

	for _, ed := range pc.Editions {
		if ed.ID == e.ID {
			allowed = true
		}
	}

	if !allowed {
		return errNotFound
	}

	file := eventDocFile(e, r.PathValue("kind"), rc(r).Lang)
	if file == "" {
		return errNotFound
	}

	s.files.Serve(w, r, file, fmt.Sprintf("%s-%s.pdf", r.PathValue("kind"), e.Slug))

	return nil
}

func (s *Server) partnerOrganization(w http.ResponseWriter, r *http.Request) error {
	p := s.page(r, "organization.title")
	p.D = map[string]any{"O": rc(r).Org}

	return s.render(w, r, "partner/organization", p)
}

func (s *Server) partnerOrganizationSubmit(w http.ResponseWriter, r *http.Request) error {
	org := *rc(r).Org
	readOrgForm(r, &org)

	if org.Name == "" {
		p := s.page(r, "organization.title")
		p.D = map[string]any{"O": &org, "Error": "organization.name_required"}

		return s.render(w, r, "partner/organization", p)
	}

	if err := s.store.UpdateOrganization(r.Context(), &org); err != nil {
		return err
	}

	s.record(r, &org, nil, "organization.updated", "")
	s.flash(w, "flash.saved")
	http.Redirect(w, r, "/partners/organization", http.StatusSeeOther)

	return nil
}

// readOrgForm copies the organization form fields shared by the partner and
// admin forms.
func readOrgForm(r *http.Request, o *store.Organization) {
	f := func(name string) string { return strings.TrimSpace(r.FormValue(name)) }

	o.Name = f("name")
	o.LegalName = f("legal_name")
	o.AddressLine1 = f("address_line1")
	o.AddressLine2 = f("address_line2")
	o.ZipCode = f("zip_code")
	o.City = f("city")
	o.Country = f("country")
	o.SIRET = f("siret")
	o.VATNumber = f("vat_number")
	o.Website = normalizeURL(f("website"))
	o.DescriptionFR = f("description_fr")
	o.DescriptionEN = f("description_en")
	o.LinkedIn = normalizeURL(f("linkedin"))
	o.Mastodon = f("mastodon")
	o.Bluesky = f("bluesky")
	o.X = f("x")
}

func normalizeURL(u string) string {
	if u == "" || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return u
	}

	return "https://" + u
}

func (s *Server) partnerLogo(w http.ResponseWriter, r *http.Request) error {
	logos, err := s.store.ListLogos(r.Context(), rc(r).Org.ID)
	if err != nil {
		return err
	}

	p := s.page(r, "logo.title")
	p.D = map[string]any{"Logos": logos}

	return s.render(w, r, "partner/logo", p)
}

func (s *Server) partnerLogoSubmit(w http.ResponseWriter, r *http.Request) error {
	org := rc(r).Org

	if err := s.saveLogo(w, r, org); err != nil {
		var ue uploadError
		if errors.As(err, &ue) {
			s.flash(w, string(ue))
			http.Redirect(w, r, "/partners/logo", http.StatusSeeOther)

			return nil
		}

		return err
	}

	s.flash(w, "flash.logo_saved")
	http.Redirect(w, r, "/partners/logo", http.StatusSeeOther)

	return nil
}

// uploadError is a user-facing upload problem; its value is an i18n key.
type uploadError string

func (e uploadError) Error() string { return string(e) }

// upload reads a single multipart file field and stores it.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, field string, kind files.Kind, maxSize int64) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSize+1<<20)

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return "", uploadError("flash.upload_too_large")
	}

	f, hdr, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return "", uploadError("flash.upload_missing")
	}

	if err != nil {
		return "", err
	}
	defer f.Close()

	name, err := s.files.Save(f, hdr.Filename, kind, maxSize)

	switch {
	case errors.Is(err, files.ErrBadType):
		if kind == files.KindLogo {
			return "", uploadError("flash.upload_bad_logo")
		}

		return "", uploadError("flash.upload_bad_pdf")
	case errors.Is(err, files.ErrTooLarge):
		return "", uploadError("flash.upload_too_large")
	}

	return name, err
}

func (s *Server) saveLogo(w http.ResponseWriter, r *http.Request, org *store.Organization) error {
	name, err := s.upload(w, r, "logo", files.KindLogo, maxLogoSize)
	if err != nil {
		return err
	}

	variant := store.LogoDefault
	if r.FormValue("variant") == store.LogoDark {
		variant = store.LogoDark
	}

	if err := s.store.AddLogo(r.Context(), &store.Logo{OrgID: org.ID, Variant: variant, File: name, UploadedBy: rc(r).Actor()}); err != nil {
		return err
	}

	removed, err := s.store.PruneLogos(r.Context(), org.ID, variant, maxLogoVersions)
	if err != nil {
		slog.Error("prune logos", "org", org.Name, "err", err)
	}

	for _, f := range removed {
		if err := s.files.Remove(f); err != nil {
			slog.Error("remove old logo", "file", f, "err", err)
		}
	}

	s.record(r, org, nil, "logo.uploaded", variant)

	return nil
}

func (s *Server) partnerAttendees(w http.ResponseWriter, r *http.Request) error {
	return s.renderAttendees(w, r, nil)
}

func (s *Server) renderAttendees(w http.ResponseWriter, r *http.Request, form map[string]any) error {
	pc, err := s.currentPartnership(r)
	if err != nil {
		return err
	}

	data := map[string]any{"PC": pc, "Form": form, "Query": eventQuery(pc)}

	if pc.Partnership != nil {
		attendees, err := s.store.ListAttendees(r.Context(), pc.Partnership.ID)
		if err != nil {
			return err
		}

		data["Attendees"] = attendees
		data["Locked"] = pc.Event.AttendeesLocked() && !rc(r).Impersonating
		data["Full"] = pc.Level != nil && pc.Level.MaxAttendees > 0 && len(attendees) >= pc.Level.MaxAttendees
	}

	p := s.page(r, "attendees.title")
	p.D = data

	return s.render(w, r, "partner/attendees", p)
}

func (s *Server) partnerAttendeeAdd(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	pc, err := s.currentPartnership(r)
	if err != nil {
		return err
	}

	if pc.Partnership == nil {
		return errNotFound
	}

	if pc.Event.AttendeesLocked() && !rc(r).Impersonating {
		return errForbidden
	}

	a := &store.Attendee{
		PartnershipID: pc.Partnership.ID,
		FirstName:     strings.TrimSpace(r.FormValue("first_name")),
		LastName:      strings.TrimSpace(r.FormValue("last_name")),
		Email:         strings.TrimSpace(r.FormValue("email")),
		Role:          r.FormValue("role"),
		Dietary:       strings.TrimSpace(r.FormValue("dietary")),
	}

	if a.Role != store.RoleVisitor {
		a.Role = store.RoleBooth
	}

	form := map[string]any{"A": a}

	switch {
	case a.FirstName == "" || a.LastName == "":
		form["Error"] = "attendees.name_required"
	case a.Email != "" && !validEmail(store.NormalizeEmail(a.Email)):
		form["Error"] = "login.invalid_email"
	}

	if pc.Level != nil && pc.Level.MaxAttendees > 0 {
		n, err := s.store.CountAttendees(ctx, pc.Partnership.ID)
		if err != nil {
			return err
		}

		if n >= pc.Level.MaxAttendees {
			form["Error"] = "attendees.full"
		}
	}

	if form["Error"] != nil {
		return s.renderAttendees(w, r, form)
	}

	if err := s.store.AddAttendee(ctx, a); err != nil {
		return err
	}

	s.record(r, rc(r).Org, &pc.Partnership.ID, "attendee.added", a.FirstName+" "+a.LastName)
	s.flash(w, "flash.saved")
	http.Redirect(w, r, "/partners/attendees"+eventQuery(pc), http.StatusSeeOther)

	return nil
}

func (s *Server) partnerAttendeeDelete(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.currentPartnership(r)
	if err != nil {
		return err
	}

	if pc.Partnership == nil {
		return errNotFound
	}

	if pc.Event.AttendeesLocked() && !rc(r).Impersonating {
		return errForbidden
	}

	if err := s.store.DeleteAttendee(r.Context(), pc.Partnership.ID, r.PathValue("attendee")); err != nil {
		return err
	}

	s.record(r, rc(r).Org, &pc.Partnership.ID, "attendee.deleted", "")
	s.flash(w, "flash.deleted")
	http.Redirect(w, r, "/partners/attendees"+eventQuery(pc), http.StatusSeeOther)

	return nil
}

func (s *Server) partnerJobs(w http.ResponseWriter, r *http.Request) error {
	jobs, err := s.store.ListJobOffers(r.Context(), rc(r).Org.ID)
	if err != nil {
		return err
	}

	p := s.page(r, "jobs.manage_title")
	p.D = map[string]any{"Jobs": jobs}

	return s.render(w, r, "partner/jobs", p)
}

func (s *Server) partnerJobForm(w http.ResponseWriter, r *http.Request) error {
	j := &store.JobOffer{}

	if uid := r.PathValue("job"); uid != "" {
		var err error
		if j, err = s.store.GetJobOffer(r.Context(), rc(r).Org.ID, uid); err != nil {
			return err
		}
	}

	p := s.page(r, "jobs.manage_title")
	p.D = map[string]any{"J": j}

	return s.render(w, r, "partner/job_form", p)
}

func (s *Server) partnerJobSubmit(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	org := rc(r).Org
	j := &store.JobOffer{OrgID: org.ID}

	if uid := r.PathValue("job"); uid != "" {
		var err error
		if j, err = s.store.GetJobOffer(ctx, org.ID, uid); err != nil {
			return err
		}
	}

	j.Title = strings.TrimSpace(r.FormValue("title"))
	j.Location = strings.TrimSpace(r.FormValue("location"))
	j.Contract = strings.TrimSpace(r.FormValue("contract"))
	j.URL = normalizeURL(strings.TrimSpace(r.FormValue("url")))
	j.Description = strings.TrimSpace(r.FormValue("description"))
	j.ExpiresAt = parseDate(r.FormValue("expires_at"))

	var errKey string

	switch {
	case j.Title == "":
		errKey = "jobs.title_required"
	case j.URL == "" && j.Description == "":
		errKey = "jobs.url_or_description"
	}

	if errKey != "" {
		p := s.page(r, "jobs.manage_title")
		p.D = map[string]any{"J": j, "Error": errKey}

		return s.render(w, r, "partner/job_form", p)
	}

	if err := s.store.SaveJobOffer(ctx, j); err != nil {
		return err
	}

	s.record(r, org, nil, "job.saved", j.Title)
	s.flash(w, "flash.saved")
	http.Redirect(w, r, "/partners/jobs", http.StatusSeeOther)

	return nil
}

func (s *Server) partnerJobDelete(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.DeleteJobOffer(r.Context(), rc(r).Org.ID, r.PathValue("job")); err != nil {
		return err
	}

	s.record(r, rc(r).Org, nil, "job.deleted", "")
	s.flash(w, "flash.deleted")
	http.Redirect(w, r, "/partners/jobs", http.StatusSeeOther)

	return nil
}

func (s *Server) partnerManagers(w http.ResponseWriter, r *http.Request) error {
	return s.renderManagers(w, r, nil)
}

func (s *Server) renderManagers(w http.ResponseWriter, r *http.Request, form map[string]any) error {
	managers, err := s.store.ListManagers(r.Context(), rc(r).Org.ID)
	if err != nil {
		return err
	}

	p := s.page(r, "managers.title")
	p.D = map[string]any{"Managers": managers, "Form": form}

	return s.render(w, r, "partner/managers", p)
}

func (s *Server) partnerManagerInvite(w http.ResponseWriter, r *http.Request) error {
	c := rc(r)

	m := &store.Manager{
		OrgID:     c.Org.ID,
		Email:     store.NormalizeEmail(r.FormValue("email")),
		Name:      strings.TrimSpace(r.FormValue("name")),
		Lang:      r.FormValue("lang"),
		InvitedBy: c.Actor(),
	}

	if !validLang(m.Lang) {
		m.Lang = c.Lang
	}

	if !validEmail(m.Email) {
		return s.renderManagers(w, r, map[string]any{"M": m, "Error": "login.invalid_email"})
	}

	created, err := s.store.AddManager(r.Context(), m)
	if err != nil {
		return err
	}

	if !created {
		return s.renderManagers(w, r, map[string]any{"M": m, "Error": "managers.already"})
	}

	inviter := c.Org.Name
	if c.Manager != nil && !c.Impersonating {
		inviter = firstNonEmpty(c.Manager.Name, c.Manager.Email)
	}

	if err := s.sendInvite(r, m, c.Org, inviter); err != nil {
		return err
	}

	s.record(r, c.Org, nil, "manager.invited", m.Email)
	s.flash(w, "flash.invited")
	http.Redirect(w, r, "/partners/managers", http.StatusSeeOther)

	return nil
}

func (s *Server) partnerManagerDelete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c := rc(r)

	uid := r.PathValue("manager")

	managers, err := s.store.ListManagers(ctx, c.Org.ID)
	if err != nil {
		return err
	}

	var target *store.Manager

	for _, m := range managers {
		if m.UID == uid {
			target = m
		}
	}

	if target == nil {
		return errNotFound
	}

	// Keep at least one manager, otherwise nobody could log in any more.
	if len(managers) <= 1 {
		s.flash(w, "flash.last_manager")
		http.Redirect(w, r, "/partners/managers", http.StatusSeeOther)

		return nil
	}

	if err := s.store.DetachManagerSessions(ctx, target.ID); err != nil {
		return err
	}

	if err := s.store.DeleteManager(ctx, c.Org.ID, target.ID); err != nil {
		return err
	}

	s.record(r, c.Org, nil, "manager.removed", target.Email)

	// Removing oneself only takes the organization away: the person stays
	// logged in, for whatever else they come for.
	if c.Manager != nil && c.Manager.ID == target.ID && !c.Impersonating {
		http.Redirect(w, r, "/", http.StatusSeeOther)

		return nil
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, "/partners/managers", http.StatusSeeOther)

	return nil
}

// partnerProfile lets a manager edit their own name and language.
func (s *Server) partnerProfile(w http.ResponseWriter, r *http.Request) error {
	c := rc(r)
	if c.Manager == nil || c.Impersonating {
		return errForbidden
	}

	m := c.Manager
	m.Name = strings.TrimSpace(r.FormValue("name"))

	if lang := r.FormValue("lang"); validLang(lang) {
		m.Lang = lang
		s.setCookie(w, langCookie, lang, 365*24*time.Hour)
	}

	if err := s.store.UpdateManager(r.Context(), m); err != nil {
		return err
	}

	s.record(r, c.Org, nil, "profile.updated", m.Email)
	s.flash(w, "flash.saved")
	http.Redirect(w, r, "/partners/managers", http.StatusSeeOther)

	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}
