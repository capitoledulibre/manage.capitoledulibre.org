package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"manage/internal/store"
)

// The people of the site, for the staff: one row per e-mail address, with
// what the person is (partner contact, staff, volunteer of the current
// edition). People are kept from one edition to the next, so the list is
// searched and paged on the server.

const peoplePerPage = 50

// personRow is a person with their roles.
type personRow struct {
	Person *store.Person
	Roles  *store.Roles
}

func (s *Server) adminPeople(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := max(formInt(r, "page"), 1)

	total, err := s.store.CountPeople(ctx, q)
	if err != nil {
		return err
	}

	offset := (page - 1) * peoplePerPage
	if offset >= total {
		page, offset = 1, 0
	}

	people, err := s.store.SearchPeople(ctx, q, peoplePerPage, offset)
	if err != nil {
		return err
	}

	var event *store.Event

	if e, err := s.store.CurrentEvent(ctx); err == nil {
		event = e
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	eventID := int64(0)
	if event != nil {
		eventID = event.ID
	}

	rows := make([]*personRow, 0, len(people))

	for _, p := range people {
		roles, err := s.store.PersonRoles(ctx, p.ID, eventID)
		if err != nil {
			return err
		}

		rows = append(rows, &personRow{Person: p, Roles: roles})
	}

	// Row actions come back to the list the staff member was looking at.
	back := "/admin/people"
	if q != "" || page > 1 {
		back += "?" + url.Values{"q": {q}, "page": {strconv.Itoa(page)}}.Encode()
	}

	p := s.page(r, "Personnes")
	p.D = map[string]any{
		"Rows": rows, "Q": q, "Page": page, "Total": total, "Back": back, "Event": event,
		"From": offset + 1, "To": offset + len(rows), "Prev": page - 1, "Next": page + 1,
		"HasPrev": page > 1, "HasNext": offset+len(rows) < total,
		"Me": rc(r).Admin.PersonID,
	}

	return s.render(w, r, "admin/people", p)
}

// adminPersonDelete removes a person altogether, from every space. The
// staff are not removed here: their access comes from the staff login.
func (s *Server) adminPersonDelete(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()

	p, err := s.pathPerson(r)
	if err != nil {
		return err
	}

	back := safeRedirect(r.FormValue("back"), "/admin/people")
	if !strings.HasPrefix(back, "/admin/people") {
		back = "/admin/people"
	}

	freed, err := s.store.DeletePerson(ctx, p.ID)
	if errors.Is(err, store.ErrBadInput) {
		s.flash(w, "flash.person_staff")
		http.Redirect(w, r, back, http.StatusSeeOther)

		return nil
	}

	if err != nil {
		return err
	}

	for _, id := range freed {
		if sl, err := s.store.GetSlot(ctx, id, 0); err == nil {
			if pole, err := s.store.GetPole(ctx, sl.PoleID); err == nil {
				s.announceFreePlace(r, pole, sl.ID)
			}
		}
	}

	s.dropFile(r, p.AvatarFile)
	s.flash(w, "flash.person_deleted", p.Label())
	http.Redirect(w, r, back, http.StatusSeeOther)

	return nil
}
