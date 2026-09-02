package web

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"benevoles/internal/db"
)

func (s *Server) adminHome(w http.ResponseWriter, r *http.Request) {
	editions, err := s.DB.ListEditions()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	users, _ := s.DB.ListUsers()
	s.render(w, r, "admin", M{
		"Editions": editions, "UserCount": len(users), "OrgLogo": s.DB.GetSetting("org_logo", ""),
		"OrgName":         s.DB.GetSetting("org_name", "Bénévoles"),
		"DefaultPoleName": s.DB.GetSetting("default_pole_name", "Organisation"),
	})
}

func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	if v := strings.TrimSpace(r.FormValue("org_name")); v != "" {
		s.DB.SetSetting("org_name", v)
	}
	if v := strings.TrimSpace(r.FormValue("default_pole_name")); v != "" {
		s.DB.SetSetting("default_pole_name", v)
	}
	s.flash(w, "Paramètres enregistrés.")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminLogo(w http.ResponseWriter, r *http.Request) {
	name, err := s.Uploads.Save(r, "logo")
	if err != nil {
		s.flash(w, "Image refusée : PNG, JPEG, GIF ou WebP, 2 Mo maximum.")
	} else if name != "" {
		s.Uploads.Remove(s.DB.GetSetting("org_logo", ""))
		s.DB.SetSetting("org_logo", name)
		s.flash(w, "Logo mis à jour.")
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminLogoDelete(w http.ResponseWriter, r *http.Request) {
	s.Uploads.Remove(s.DB.GetSetting("org_logo", ""))
	s.DB.SetSetting("org_logo", "")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// planningRow is one line of the global planning grid: a pole, or a pole location.
type planningRow struct {
	Pole     db.Pole
	Location string
	Slots    []db.MySlot
	Capacity int
	Filled   int
}

type planningDay struct {
	Day      string
	Rows     []planningRow
	Capacity int
	Filled   int
}

func (s *Server) adminPlanning(w http.ResponseWriter, r *http.Request) {
	ed, err := s.DB.GetEdition(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	slots, err := s.DB.ListEditionSlots(ed.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	poles, _ := s.DB.ListPoles(ed.ID)
	poleByID := map[int64]db.Pole{}
	for _, p := range poles {
		poleByID[p.ID] = p
	}
	onlyShort := r.URL.Query().Get("short") == "1"
	var days []planningDay
	for _, sl := range slots {
		if onlyShort && sl.IsFull() {
			continue
		}
		key := sl.Day(s.Cfg.Timezone)
		if len(days) == 0 || days[len(days)-1].Day != key {
			days = append(days, planningDay{Day: key})
		}
		d := &days[len(days)-1]
		var row *planningRow
		for i := range d.Rows {
			if d.Rows[i].Pole.ID == sl.PoleID && d.Rows[i].Location == sl.LocationName {
				row = &d.Rows[i]
				break
			}
		}
		if row == nil {
			d.Rows = append(d.Rows, planningRow{Pole: poleByID[sl.PoleID], Location: sl.LocationName})
			row = &d.Rows[len(d.Rows)-1]
		}
		row.Slots = append(row.Slots, sl)
		row.Capacity += sl.Capacity
		row.Filled += sl.Filled
		d.Capacity += sl.Capacity
		d.Filled += sl.Filled
	}
	// Keep the pole order of the edition inside each day.
	order := map[int64]int{}
	for i, p := range poles {
		order[p.ID] = i
	}
	for i := range days {
		sort.SliceStable(days[i].Rows, func(a, b int) bool {
			ra, rb := days[i].Rows[a], days[i].Rows[b]
			if ra.Pole.ID != rb.Pole.ID {
				return order[ra.Pole.ID] < order[rb.Pole.ID]
			}
			return ra.Location < rb.Location
		})
	}
	s.render(w, r, "admin_planning", M{"Edition": ed, "Days": days, "OnlyShort": onlyShort})
}

func (s *Server) adminEditionDuplicate(w http.ResponseWriter, r *http.Request) {
	src, err := s.DB.GetEdition(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	startsOn, endsOn := r.FormValue("starts_on"), r.FormValue("ends_on")
	if name == "" || startsOn == "" || endsOn == "" || endsOn < startsOn {
		s.flash(w, "Indiquez un nom et des dates valides pour la nouvelle édition.")
		http.Redirect(w, r, fmt.Sprintf("/admin/editions/%d?tab=settings", src.ID), http.StatusSeeOther)
		return
	}
	id, err := s.DB.DuplicateEdition(src.ID, name, startsOn, endsOn, s.DB.GetSetting("default_pole_name", "Organisation"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Édition créée en brouillon avec les pôles, emplacements et responsables de « "+src.Name+" ». Les créneaux sont à recréer.")
	http.Redirect(w, r, fmt.Sprintf("/admin/editions/%d", id), http.StatusSeeOther)
}

func (s *Server) adminPoleCopy(w http.ResponseWriter, r *http.Request) {
	pole, err := s.DB.GetPole(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	target := formInt64(r, "edition_id")
	if target == 0 {
		target = pole.EditionID
	}
	if _, err := s.DB.GetEdition(target); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.DB.CopyPole(pole.ID, target)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Pôle « "+pole.Name+" » copié : réglages, emplacements et responsables. Les créneaux et bénévoles ne sont pas repris.")
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=settings", id), http.StatusSeeOther)
}

// --- editions -----------------------------------------------------------

func (s *Server) adminEditionNew(w http.ResponseWriter, r *http.Request) {
	year := time.Now().Year()
	s.render(w, r, "edition_form", M{"Edition": db.Edition{Name: fmt.Sprintf("%s %d", s.DB.GetSetting("org_name", ""), year), Status: db.StatusDraft}, "New": true})
}

func (s *Server) adminEditionCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	startsOn, endsOn := r.FormValue("starts_on"), r.FormValue("ends_on")
	if name == "" || startsOn == "" || endsOn == "" || endsOn < startsOn {
		s.render(w, r, "edition_form", M{"Edition": db.Edition{Name: name, StartsOn: startsOn, EndsOn: endsOn}, "New": true, "Error": "Nom et dates sont obligatoires, la fin ne peut pas précéder le début."})
		return
	}
	id, err := s.DB.CreateEdition(name, startsOn, endsOn, s.formTimePtr(r, "freeze_at"), s.DB.GetSetting("default_pole_name", "Organisation"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Édition créée. Ajoutez maintenant les pôles et leurs responsables.")
	http.Redirect(w, r, fmt.Sprintf("/admin/editions/%d", id), http.StatusSeeOther)
}

func (s *Server) adminEdition(w http.ResponseWriter, r *http.Request) {
	ed, err := s.DB.GetEdition(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	coverage, err := s.DB.ListPoleCoverage(ed.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	stats, _ := s.DB.GetEditionStats(ed.ID)
	msgs, _ := s.DB.ListEditionMessages(ed.ID)
	editions, _ := s.DB.ListEditions()
	s.render(w, r, "admin_edition", M{
		"Edition": ed, "Coverage": coverage, "Stats": stats, "Messages": msgs, "Modes": db.Modes, "Editions": editions,
		"NextYear": time.Now().Year() + 1,
		"Statuses": []string{db.StatusDraft, db.StatusOpen, db.StatusFrozen, db.StatusArchived},
		"Tab":      r.URL.Query().Get("tab"),
	})
}

func (s *Server) adminEditionUpdate(w http.ResponseWriter, r *http.Request) {
	ed, err := s.DB.GetEdition(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if name := strings.TrimSpace(r.FormValue("name")); name != "" {
		ed.Name = name
	}
	if st := r.FormValue("status"); st != "" {
		ed.Status = st
	}
	if v := r.FormValue("starts_on"); v != "" {
		ed.StartsOn = v
	}
	if v := r.FormValue("ends_on"); v != "" {
		ed.EndsOn = v
	}
	ed.FreezeAt = s.formTimePtr(r, "freeze_at")
	if err := s.DB.UpdateEdition(ed); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Édition enregistrée.")
	http.Redirect(w, r, fmt.Sprintf("/admin/editions/%d?tab=settings", ed.ID), http.StatusSeeOther)
}

func (s *Server) adminEditionDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.DeleteEdition(pathID(r, "id")); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Édition supprimée.")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) adminEditionMessage(w http.ResponseWriter, r *http.Request) {
	ed, err := s.DB.GetEdition(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if body := strings.TrimSpace(r.FormValue("body")); body != "" {
		if _, err := s.DB.CreateMessage(ed.ID, 0, 0, currentUser(r).ID, body); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/editions/%d?tab=messages", ed.ID), http.StatusSeeOther)
}

// --- poles --------------------------------------------------------------

func (s *Server) adminPoleCreate(w http.ResponseWriter, r *http.Request) {
	ed, err := s.DB.GetEdition(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, fmt.Sprintf("/admin/editions/%d", ed.ID), http.StatusSeeOther)
		return
	}
	poles, _ := s.DB.ListPoles(ed.ID)
	id, err := s.DB.CreatePole(db.Pole{EditionID: ed.ID, Name: name, AccessMode: r.FormValue("access_mode"), Icon: strings.TrimSpace(r.FormValue("icon")), Position: len(poles)})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if email := strings.ToLower(strings.TrimSpace(r.FormValue("manager_email"))); strings.Contains(email, "@") {
		if u, err := s.DB.GetOrCreateUser(email); err == nil {
			s.DB.AddPoleManager(id, u.ID)
		}
	}
	s.flash(w, "Pôle « "+name+" » créé.")
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=settings", id), http.StatusSeeOther)
}

func (s *Server) adminPoleDelete(w http.ResponseWriter, r *http.Request) {
	pole, err := s.DB.GetPole(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if pole.IsDefault {
		s.flash(w, "Le pôle par défaut ne peut pas être supprimé.")
	} else if err := s.DB.DeletePole(pole.ID); err != nil {
		s.serverError(w, r, err)
		return
	} else {
		s.flash(w, "Pôle supprimé.")
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/editions/%d", pole.EditionID), http.StatusSeeOther)
}

func (s *Server) adminManagerAdd(w http.ResponseWriter, r *http.Request) {
	pole, err := s.DB.GetPole(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	if !strings.Contains(email, "@") {
		s.flash(w, "Adresse e-mail invalide.")
	} else {
		u, err := s.DB.GetOrCreateUser(email)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if err := s.DB.AddPoleManager(pole.ID, u.ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.flash(w, u.DisplayName()+" est maintenant responsable du pôle.")
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=members", pole.ID), http.StatusSeeOther)
}

func (s *Server) adminManagerRemove(w http.ResponseWriter, r *http.Request) {
	pole, err := s.DB.GetPole(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.DB.RemovePoleManager(pole.ID, pathID(r, "uid")); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/manage/poles/%d?tab=members", pole.ID), http.StatusSeeOther)
}

// --- volunteers ---------------------------------------------------------

func (s *Server) adminVolunteers(w http.ResponseWriter, r *http.Request) {
	ed, err := s.DB.GetEdition(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	vols, err := s.DB.ListEditionVolunteers(ed.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, "admin_volunteers", M{"Edition": ed, "Volunteers": vols})
}

func (s *Server) adminVolunteersCSV(w http.ResponseWriter, r *http.Request) {
	ed, err := s.DB.GetEdition(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	vols, err := s.DB.ListEditionVolunteers(ed.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="benevoles.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"name", "email", "phone", "poles", "slots"})
	for _, v := range vols {
		cw.Write([]string{v.User.Name, v.User.Email, v.User.Phone, strings.Join(v.Poles, ", "), fmt.Sprint(v.SlotCount)})
	}
	cw.Flush()
}

// --- users --------------------------------------------------------------

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.DB.ListUsers()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data := M{"Users": users}
	if ed, err := s.currentEdition(currentUser(r)); err == nil {
		data["Edition"] = ed
		data["UserPoles"], _ = s.DB.UserPoles(ed.ID)
	}
	s.render(w, r, "admin_users", data)
}

func (s *Server) adminUserToggleAdmin(w http.ResponseWriter, r *http.Request) {
	u, err := s.DB.GetUser(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if u.IsAdmin {
		if n, _ := s.DB.CountAdmins(); n <= 1 {
			s.flash(w, "Impossible de retirer le dernier administrateur.")
			http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
			return
		}
	}
	if err := s.DB.SetAdmin(u.ID, !u.IsAdmin); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *Server) adminUserDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.DB.GetUser(pathID(r, "id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if u.ID == currentUser(r).ID {
		s.flash(w, "Vous ne pouvez pas supprimer votre propre compte depuis cette page.")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	if err := s.DB.DeleteUser(u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Compte supprimé.")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}
