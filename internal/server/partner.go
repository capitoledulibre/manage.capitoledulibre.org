package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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

func (s *Server) partnerRoutes(mux *router) {
	h := func(pattern string, fn handlerFunc) { mux.Handle(pattern, s.wrap(s.requireOrg(fn))) }

	// The way in, from e-mails and logins: the space of the default
	// edition. Every page of the space sits below /{event}/partner.
	h("GET /partners/{$}", s.partnerEntry)
	// Choosing an organization needs a logged-in person, not an organization
	// already chosen: one whose organization was taken away comes here too.
	mux.Handle("GET /choose", s.wrap(s.requirePerson(s.partnerChoose)))
	mux.Handle("POST /choose", s.wrap(s.requirePerson(s.partnerChooseSubmit)))

	const base = "/{event}/partner"

	h("GET "+base+"/{$}", s.partnerDashboard)
	h("POST "+base+"/notes", s.partnerNotes)
	h("GET "+base+"/documents/{kind}", s.partnerDocument)
	h("POST "+base+"/documents/signed-quote", s.partnerSignedQuote)
	h("GET "+base+"/event-documents/{kind}", s.partnerEventDocument)
	h("GET "+base+"/organization", s.partnerOrganization)
	h("POST "+base+"/organization", s.partnerOrganizationSubmit)
	h("GET "+base+"/logo", s.partnerLogo)
	h("POST "+base+"/logo", s.partnerLogoSubmit)
	h("GET "+base+"/attendees", s.partnerAttendees)
	h("POST "+base+"/attendees", s.partnerAttendeeAdd)
	h("POST "+base+"/attendees/{attendee}", s.partnerAttendeeUpdate)
	h("POST "+base+"/attendees/{attendee}/delete", s.partnerAttendeeDelete)
	h("GET "+base+"/jobs", s.partnerJobs)
	h("GET "+base+"/jobs/new", s.partnerJobForm)
	h("POST "+base+"/jobs/new", s.partnerJobSubmit)
	h("GET "+base+"/jobs/{job}", s.partnerJobForm)
	h("POST "+base+"/jobs/{job}", s.partnerJobSubmit)
	h("POST "+base+"/jobs/{job}/delete", s.partnerJobDelete)
	h("GET "+base+"/managers", s.partnerManagers)
	h("POST "+base+"/managers", s.partnerManagerInvite)
	h("POST "+base+"/managers/{manager}/delete", s.partnerManagerDelete)
	h("POST "+base+"/managers/{manager}/send-link", s.partnerManagerSendLink)
	h("POST "+base+"/profile", s.partnerProfile)
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
		rc(r).area = "partner"

		if rc(r).Org == nil {
			// Someone signed in is sent where their roles lead; anyone else
			// to the partners' login, which comes back here (an e-mail's
			// link to a document, say).
			to := "/partners/login"
			if r.Method == http.MethodGet {
				to += "?next=" + url.QueryEscape(r.URL.RequestURI())
			}

			if rc(r).Person != nil {
				to = "/"
			}

			http.Redirect(w, r, to, http.StatusSeeOther)

			return nil
		}

		// The organization of the session is the actor's to act for: the
		// rules below rest on it.
		a, err := s.actor(r)
		if err != nil {
			return err
		}

		if !a.ActsFor(rc(r).Org.ID) {
			return errForbidden
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

// currentPartnership resolves the edition of the path, or the most recent
// visible partnership outside one (the way in). Its Partnership is nil when
// the organization has none visible in that edition; its Event is then the
// edition of the path, for the links.
func (s *Server) currentPartnership(r *http.Request) (*partnerContext, error) {
	ctx := r.Context()
	org := rc(r).Org

	all, err := s.store.ListOrgPartnerships(ctx, org.ID)
	if err != nil {
		return nil, err
	}

	pc := &partnerContext{}
	want := r.PathValue("event")

	for _, p := range all {
		if !act(r).SeesPartnership(p) {
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

	if pc.Event == nil && want != "" {
		if pc.Event, err = s.store.GetEventBySlug(ctx, want); err != nil {
			return nil, err
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

// partnerBase is where a partner's space starts for an edition.
func partnerBase(e *store.Event) string { return "/" + e.Slug + "/partner" }

// partnerSpace is the partner's space of the request's edition.
func partnerSpace(r *http.Request) string { return "/" + r.PathValue("event") + "/partner" }

// partnerEntry leads into the partner's space: the edition asked for, else
// that of their latest visible partnership, else the current one.
func (s *Server) partnerEntry(w http.ResponseWriter, r *http.Request) error {
	// The edition switch of the dashboard names its choice.
	if slug := r.FormValue("event"); slug != "" {
		e, err := s.store.GetEventBySlug(r.Context(), slug)
		if err != nil {
			return err
		}

		http.Redirect(w, r, partnerBase(e)+"/", http.StatusSeeOther)

		return nil
	}

	pc, err := s.currentPartnership(r)
	if err != nil {
		return err
	}

	e := pc.Event
	if e == nil {
		if e, err = s.store.CurrentEvent(r.Context()); err != nil {
			return err
		}
	}

	http.Redirect(w, r, partnerBase(e)+"/", http.StatusSeeOther)

	return nil
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
		data["Progress"] = computeProgress(p, c.Org, pc.Event, attendees)
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
			out = append(out, eventDoc{Kind: kind, Link: partnerBase(e) + "/event-documents/" + kind})
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
	p.PONumber = formEdit(r, "po_number", p.PONumber)
	p.PartnerNotes = formEdit(r, "partner_notes", p.PartnerNotes)

	if err := s.store.UpdatePartnership(r.Context(), p); err != nil {
		return err
	}

	s.record(r, rc(r).Org, &p.ID, "partnership.notes", fmt.Sprintf("PO: %s\n%s", p.PONumber, p.PartnerNotes))
	s.flash(w, "flash.saved")
	http.Redirect(w, r, partnerSpace(r)+"/", http.StatusSeeOther)

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

	if !act(r).SeesPartnership(p) {
		return errNotFound
	}

	var file string

	switch r.PathValue("kind") {
	case "quote":
		file = p.QuoteFile
	case "invoice":
		file = p.InvoiceFile
	case "signed_quote":
		file = p.SignedQuoteFile
	}

	if file == "" {
		return errNotFound
	}

	s.files.Serve(w, r, file, fmt.Sprintf("%s-%s.pdf", r.PathValue("kind"), rc(r).Org.Slug))

	return nil
}

// partnerSignedQuote takes the quote back signed: the to-do asked for it
// without a way to send it. The staff hears of it and checks it before
// marking the quote signed.
func (s *Server) partnerSignedQuote(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	e, err := s.store.GetEventBySlug(ctx, r.PathValue("event"))
	if err != nil {
		return err
	}

	p, err := s.store.GetPartnershipByOrgEvent(ctx, rc(r).Org.ID, e.ID)
	if err != nil {
		return err
	}

	back := partnerBase(e) + "/"

	if !act(r).SeesPartnership(p) || p.QuoteFile == "" && p.QuoteSentAt == nil {
		return errNotFound
	}

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

	p.SignedQuoteFile = name

	if err := s.store.UpdatePartnership(ctx, p); err != nil {
		return err
	}

	s.record(r, rc(r).Org, &p.ID, "quote.signed_uploaded", e.Name)
	s.flash(w, "flash.signed_quote")
	http.Redirect(w, r, back, http.StatusSeeOther)

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
	http.Redirect(w, r, partnerSpace(r)+"/organization", http.StatusSeeOther)

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

	d := map[string]any{"Logos": logos}

	// The deadline of the edition shown on the dashboard, while the logo
	// is still awaited.
	if pc, err := s.currentPartnership(r); err == nil && pc.Partnership != nil && rc(r).Org.LogoFile == "" {
		d["Deadline"] = pc.Event.LogoDeadline
	}

	p := s.page(r, "logo.title")
	p.D = d

	return s.render(w, r, "partner/logo", p)
}

func (s *Server) partnerLogoSubmit(w http.ResponseWriter, r *http.Request) error {
	org := rc(r).Org

	if err := s.saveLogo(w, r, org); err != nil {
		var ue uploadError
		if errors.As(err, &ue) {
			s.flash(w, string(ue))
			http.Redirect(w, r, partnerSpace(r)+"/logo", http.StatusSeeOther)

			return nil
		}

		return err
	}

	s.flash(w, "flash.logo_saved")
	http.Redirect(w, r, partnerSpace(r)+"/logo", http.StatusSeeOther)

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

	data := map[string]any{"PC": pc, "Form": form}

	if pc.Partnership != nil {
		attendees, err := s.store.ListAttendees(r.Context(), pc.Partnership.ID)
		if err != nil {
			return err
		}

		data["Attendees"] = attendees

		// The one being changed (?edit=), its fields in the form.
		for _, a := range attendees {
			if a.UID == r.URL.Query().Get("edit") && form == nil {
				data["Editing"], data["Form"] = a, map[string]any{"A": a}
			}
		}

		if form != nil && form["Editing"] != nil {
			data["Editing"] = form["Editing"]
		}
		data["Locked"] = !act(r).ChangesAttendees(pc.Event, rc(r).Org.ID)
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

	if !act(r).ChangesAttendees(pc.Event, rc(r).Org.ID) {
		return errForbidden
	}

	a, form := readAttendee(r, pc.Partnership.ID)

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
	http.Redirect(w, r, partnerSpace(r)+"/attendees", http.StatusSeeOther)

	return nil
}

// readAttendee reads the attendee form, and the form to show again with an
// error when it is not valid.
func readAttendee(r *http.Request, partnershipID int64) (*store.Attendee, map[string]any) {
	a := &store.Attendee{
		PartnershipID: partnershipID,
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

	return a, form
}

// partnerAttendeeUpdate saves the changes to an attendee: a typo or a diet
// took deleting and adding them again. Once the list is closed, the diet
// alone can still change: an allergy learnt late matters to the caterer.
func (s *Server) partnerAttendeeUpdate(w http.ResponseWriter, r *http.Request) error {
	pc, err := s.currentPartnership(r)
	if err != nil {
		return err
	}

	if pc.Partnership == nil {
		return errNotFound
	}

	if !act(r).ChangesAttendees(pc.Event, rc(r).Org.ID) {
		uid := r.PathValue("attendee")
		if err := s.store.UpdateAttendeeDietary(r.Context(), pc.Partnership.ID, uid, strings.TrimSpace(r.FormValue("dietary"))); err != nil {
			return err
		}

		s.record(r, rc(r).Org, &pc.Partnership.ID, "attendee.updated", "")
		s.flash(w, "flash.saved")
		http.Redirect(w, r, partnerSpace(r)+"/attendees", http.StatusSeeOther)

		return nil
	}

	a, form := readAttendee(r, pc.Partnership.ID)
	a.UID = r.PathValue("attendee")

	if form["Error"] != nil {
		form["Editing"] = a

		return s.renderAttendees(w, r, form)
	}

	if err := s.store.UpdateAttendee(r.Context(), a); err != nil {
		return err
	}

	s.record(r, rc(r).Org, &pc.Partnership.ID, "attendee.updated", a.FirstName+" "+a.LastName)
	s.flash(w, "flash.saved")
	http.Redirect(w, r, partnerSpace(r)+"/attendees", http.StatusSeeOther)

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

	if !act(r).ChangesAttendees(pc.Event, rc(r).Org.ID) {
		return errForbidden
	}

	if err := s.store.DeleteAttendee(r.Context(), pc.Partnership.ID, r.PathValue("attendee")); err != nil {
		return err
	}

	s.record(r, rc(r).Org, &pc.Partnership.ID, "attendee.deleted", "")
	s.flash(w, "flash.deleted")
	http.Redirect(w, r, partnerSpace(r)+"/attendees", http.StatusSeeOther)

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
	http.Redirect(w, r, partnerSpace(r)+"/jobs", http.StatusSeeOther)

	return nil
}

func (s *Server) partnerJobDelete(w http.ResponseWriter, r *http.Request) error {
	if err := s.store.DeleteJobOffer(r.Context(), rc(r).Org.ID, r.PathValue("job")); err != nil {
		return err
	}

	s.record(r, rc(r).Org, nil, "job.deleted", "")
	s.flash(w, "flash.deleted")
	http.Redirect(w, r, partnerSpace(r)+"/jobs", http.StatusSeeOther)

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

	if err := s.sendInvite(r, m, c.Org, inviterName(c)); err != nil {
		return err
	}

	s.record(r, c.Org, nil, "manager.invited", m.Email)
	s.flash(w, "flash.invited")
	http.Redirect(w, r, partnerSpace(r)+"/managers", http.StatusSeeOther)

	return nil
}

// inviterName is who an invitation comes from: the manager sending it,
// else the organization.
func inviterName(c *reqCtx) string {
	if c.Manager != nil && !c.Impersonating {
		return firstNonEmpty(c.Manager.Name, c.Manager.Email)
	}

	return c.Org.Name
}

// partnerManagerSendLink sends a colleague a fresh invitation, for one who
// let the first expire: inviting them again only said they already were.
func (s *Server) partnerManagerSendLink(w http.ResponseWriter, r *http.Request) error {
	c := rc(r)

	managers, err := s.store.ListManagers(r.Context(), c.Org.ID)
	if err != nil {
		return err
	}

	for _, m := range managers {
		if m.UID != r.PathValue("manager") {
			continue
		}

		if err := s.sendInvite(r, m, c.Org, inviterName(c)); err != nil {
			return err
		}

		s.record(r, c.Org, nil, "manager.invited", m.Email)
		s.flash(w, "flash.invited")
		http.Redirect(w, r, partnerSpace(r)+"/managers", http.StatusSeeOther)

		return nil
	}

	return errNotFound
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
		http.Redirect(w, r, partnerSpace(r)+"/managers", http.StatusSeeOther)

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
		s.flash(w, "flash.manager_left", c.Org.Name)
		http.Redirect(w, r, "/", http.StatusSeeOther)

		return nil
	}

	s.flash(w, "flash.deleted")
	http.Redirect(w, r, partnerSpace(r)+"/managers", http.StatusSeeOther)

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
	http.Redirect(w, r, partnerSpace(r)+"/managers", http.StatusSeeOther)

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
