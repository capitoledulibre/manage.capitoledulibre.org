package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"manage/internal/access"
	"manage/internal/store"
)

// The navigation of the whole site, the same for everyone: an edition
// switch, three sections (partners, volunteers, planning) each shown to
// those it concerns, the administration for the staff, and below them the
// tabs of the section being looked at. It replaces one menu per space.

// navLink is an entry of the navigation. Label is an i18n key, or the
// name of an edition.
type navLink struct {
	Label string
	URL   string
	On    bool
}

// siteNav is the navigation of a page.
type siteNav struct {
	// Event is the edition looked at: the one of the path, else the one
	// the person would land on.
	Event *store.Event
	// Editions are those the person can switch to, each leading to the
	// same section; empty when there is no choice.
	Editions []navLink
	Sections []navLink
	// Admin is the global administration, for the staff.
	Admin *navLink
	// Tabs are the pages of the current section, when it has several.
	Tabs []navLink
}

// roles is what the person is, for the navigation.
type roles struct {
	staff, contact, volunteer bool
	actor                     *access.Actor
}

func (s *Server) siteNav(r *http.Request) *siteNav {
	c := rc(r)
	if c.Person == nil && c.Admin == nil {
		return nil
	}

	ctx := r.Context()
	nav := &siteNav{}
	ro := roles{staff: c.Admin != nil, contact: c.Org != nil}

	var err error
	if ro.actor, err = s.actor(r); err != nil {
		slog.Error("navigation", "err", err)

		ro.actor = &access.Actor{}
	}

	if !ro.contact && c.Person != nil {
		managers, err := s.store.ManagersByEmail(ctx, c.Person.Email)
		if err != nil {
			slog.Error("navigation", "err", err)
		}

		ro.contact = len(managers) > 0
	}

	// A partner contact who never volunteered is shown their space only.
	ro.volunteer = ro.staff || c.Person != nil && !ro.contact

	if c.Person != nil && !ro.volunteer {
		ok, err := s.store.IsVolunteer(ctx, c.Person.ID)
		if err != nil {
			slog.Error("navigation", "err", err)
		}

		ro.volunteer = ok
	}

	nav.Event = s.navEvent(r, ro)
	section := pathSection(r.URL.Path, nav.Event)

	if ro.staff {
		nav.Admin = &navLink{Label: "nav.admin", URL: "/admin/editions", On: section == "admin"}
	}

	if nav.Event != nil {
		s.navSections(r, nav, ro, section)
		nav.Editions = s.navEditions(r, nav.Event, ro)
	}

	if section == "admin" {
		nav.Tabs = adminTabs(r.URL.Path)
	}

	return nav
}

// navEvent is the edition of the path, else the one the person lands on.
func (s *Server) navEvent(r *http.Request, ro roles) *store.Event {
	ctx := r.Context()

	var (
		e   *store.Event
		err error
	)

	switch slug := r.PathValue("event"); {
	case slug != "":
		e, err = s.store.GetEventBySlug(ctx, slug)
	case rc(r).Org != nil:
		var pc *partnerContext
		if pc, err = s.currentPartnership(r); err == nil {
			e = pc.Event
		}
	case ro.volunteer:
		e, err = s.currentVolunteerEvent(r)
	}

	if e == nil && err == nil && ro.staff {
		e, err = s.store.CurrentEvent(ctx)
	}

	if err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Error("navigation", "err", err)
	}

	return e
}

// pathSection tells the section of a path: "admin", "partners",
// "planning", "volunteers", or none (the profile, the logins).
func pathSection(path string, e *store.Event) string {
	switch {
	case strings.HasPrefix(path, "/admin"):
		return "admin"
	case path == "/choose":
		return "partners"
	case e == nil || !strings.HasPrefix(path, "/"+e.Slug+"/"):
		return ""
	}

	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"+e.Slug+"/"), "/")

	switch first {
	case "partners", "partner":
		return "partners"
	case "planning":
		return "planning"
	}

	return "volunteers"
}

// navSections fills the sections an edition offers the person, and the
// tabs of the one looked at.
func (s *Server) navSections(r *http.Request, nav *siteNav, ro roles, section string) {
	c := rc(r)
	e := nav.Event
	base := "/" + e.Slug

	if ro.staff || ro.contact {
		// The staff's partners; a contact's (or the staff seeing as one)
		// their own space.
		url := base + "/partners/"
		if !ro.staff || c.Impersonating {
			url = partnerBase(e) + "/"
		}

		nav.Sections = append(nav.Sections, navLink{Label: "nav.partners", URL: url, On: section == "partners"})

		if section == "partners" && (!ro.staff || c.Impersonating) {
			for _, page := range []string{"", "organization", "logo", "attendees", "jobs", "managers"} {
				url := partnerBase(e) + "/" + page
				key := "nav." + page
				if page == "" {
					key = "nav.dashboard"
				}

				nav.Tabs = append(nav.Tabs, navLink{Label: key, URL: url, On: r.URL.Path == url})
			}
		}
	}

	if !ro.volunteer {
		return
	}

	coordinator := ro.actor.Coordinates(e.ID)

	// An edition whose volunteering is not open is for those preparing it.
	if !ro.actor.SeesEvent(e) {
		return
	}

	nav.Sections = append(nav.Sections,
		navLink{Label: "nav.volunteers", URL: base + "/", On: section == "volunteers"},
		navLink{Label: "nav.planning", URL: base + "/planning", On: section == "planning"})

	if section == "volunteers" && coordinator {
		coordination := editionURL(e)
		inCoordination := r.URL.Path == coordination || strings.HasPrefix(r.URL.Path, coordination+"/")
		nav.Tabs = []navLink{
			{Label: "nav.vol_home", URL: base + "/", On: !inCoordination},
			{Label: "nav.coordination", URL: coordination, On: inCoordination},
		}
	}
}

// navEditions lists the editions the person can switch to, each leading to
// the section looked at; none when there is no choice.
func (s *Server) navEditions(r *http.Request, current *store.Event, ro roles) []navLink {
	ctx := r.Context()
	c := rc(r)

	all, err := s.store.ListEvents(ctx)
	if err != nil {
		slog.Error("navigation", "err", err)

		return nil
	}

	partnered := map[int64]bool{}

	if c.Org != nil {
		if pc, err := s.currentPartnership(r); err == nil {
			for _, e := range pc.Editions {
				partnered[e.ID] = true
			}
		}
	}

	var out []navLink

	for _, e := range all {
		volunteering := ro.staff || ro.volunteer && e.VisibleToVolunteers() || ro.actor.Prepares(e.ID)

		if !volunteering && !partnered[e.ID] {
			continue
		}

		// Each leads to the same section, or to the one the person has there.
		url := switchURL(r.URL.Path, current, e)
		partnerPage := strings.Contains(url, "/partner")

		switch {
		case !volunteering && !partnerPage:
			url = partnerBase(e) + "/"
		case (!ro.staff || c.Impersonating) && !partnered[e.ID] && partnerPage:
			url = volunteerHomeURL(e)
		}

		out = append(out, navLink{Label: e.Name, URL: url, On: e.ID == current.ID})
	}

	if len(out) < 2 {
		return nil
	}

	return out
}

// switchURL is the page of another edition matching path: the root of the
// same section, its pages (a pole, a partnership) being the edition's own.
func switchURL(path string, from, to *store.Event) string {
	base := "/" + to.Slug + "/"

	rest, ok := strings.CutPrefix(path, "/"+from.Slug+"/")
	if !ok {
		return base
	}

	switch first, _, _ := strings.Cut(rest, "/"); first {
	case "partners", "partner":
		return base + first + "/"
	case "planning", "volunteers":
		return base + first
	}

	return base
}

// adminTabs are the pages of the global administration.
func adminTabs(path string) []navLink {
	tabs := []navLink{
		{Label: "nav.admin_editions", URL: "/admin/editions"},
		{Label: "nav.admin_organizations", URL: "/admin/organizations"},
		{Label: "nav.admin_people", URL: "/admin/people"},
		{Label: "nav.admin_profile", URL: "/admin/profile"},
	}

	for i := range tabs {
		tabs[i].On = strings.HasPrefix(path, tabs[i].URL)
	}

	return tabs
}
