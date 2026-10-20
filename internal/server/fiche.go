package server

import (
	"errors"
	"fmt"
	"net/http"

	"manage/internal/store"
)

// The partner file ("fiche") gathers the organization page and its
// partnership pages under a single header: breadcrumb, badges, one tab per
// edition plus an "Organisation" tab, and the shared actions.

// ficheTab is one tab of the partner file.
type ficheTab struct {
	Label  string
	URL    string
	Status string
	Active bool
}

type ficheHeader struct {
	Org    *store.Organization
	Badges []Badge
	Tabs   []ficheTab
	// Event is the edition of the active tab, nil on the organization tab.
	Event *store.Event
	// Medal is shown next to the name on an edition tab.
	Medal *Medal
	// AddEvents are the editions the organization can still be attached to.
	AddEvents []*store.Event
	// Return is the current page, where the staff comes back after viewing
	// the partner area.
	Return string
}

// ficheOrgTab is the key of the organization tab.
const ficheOrgTab = 0

// ficheHeaderFor builds the header of an organization's file. active is the
// id of the displayed partnership, or ficheOrgTab.
func (s *Server) ficheHeaderFor(r *http.Request, org *store.Organization, active int64) (*ficheHeader, error) {
	ctx := r.Context()

	partnerships, err := s.store.ListOrgPartnerships(ctx, org.ID)
	if err != nil {
		return nil, err
	}

	h := &ficheHeader{Org: org, Return: r.URL.RequestURI()}

	for _, p := range partnerships {
		e, err := s.store.GetEvent(ctx, p.EventID)
		if err != nil {
			return nil, err
		}

		tab := ficheTab{
			Label:  fmt.Sprint(e.Year),
			URL:    partnershipURL(org, e),
			Status: p.Status,
			Active: p.ID == active,
		}

		if tab.Active {
			h.Event = e
		}

		h.Tabs = append(h.Tabs, tab)
	}

	h.Tabs = append(h.Tabs, ficheTab{
		Label:  "Organisation",
		URL:    orgURL(org),
		Active: active == ficheOrgTab,
	})

	if h.Badges, err = s.orgBadges(r, org.ID); err != nil {
		return nil, err
	}

	if h.AddEvents, err = s.eventsWithoutPartnership(r, org.ID); err != nil {
		return nil, err
	}

	return h, nil
}

// ficheURL is the entry point of an organization's file: its most recent
// edition, or the organization tab when it has none.
func (s *Server) ficheURL(r *http.Request, org *store.Organization) (string, error) {
	partnerships, err := s.store.ListOrgPartnerships(r.Context(), org.ID)
	if err != nil {
		return "", err
	}

	if len(partnerships) > 0 {
		e, err := s.store.GetEvent(r.Context(), partnerships[0].EventID)
		if err != nil {
			return "", err
		}

		return partnershipURL(org, e), nil
	}

	return orgURL(org), nil
}

// Staff URLs, built from slugs: an edition's pages sit below its slug, the
// global administration below /admin.

// eventURL is the pipeline of an edition's partners.
func eventURL(e *store.Event) string { return "/" + e.Slug + "/partners/" }

// editionAdminURL is the settings of an edition, in the administration.
func editionAdminURL(e *store.Event) string { return "/admin/editions/" + e.Slug }

func orgURL(o *store.Organization) string { return "/admin/organizations/" + o.Slug }

func partnershipURL(o *store.Organization, e *store.Event) string {
	return "/" + e.Slug + "/partners/" + o.Slug
}

// partnershipURLFromPath is the partnership page of the current request.
func partnershipURLFromPath(r *http.Request) string {
	return "/" + r.PathValue("event") + "/partners/" + r.PathValue("org")
}

// latestPartnership returns the most recent partnership of an organization
// and its level, used to prefill a new edition.
func (s *Server) latestPartnership(r *http.Request, orgID int64) (*store.Partnership, *store.Level, error) {
	ctx := r.Context()

	partnerships, err := s.store.ListOrgPartnerships(ctx, orgID)
	if err != nil || len(partnerships) == 0 {
		return nil, nil, err
	}

	p := partnerships[0]
	if p.LevelID == nil {
		return p, nil, nil
	}

	l, err := s.store.GetLevel(ctx, *p.LevelID)
	if errors.Is(err, store.ErrNotFound) {
		return p, nil, nil
	}

	return p, l, err
}
