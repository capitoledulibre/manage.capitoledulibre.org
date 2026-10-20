package server

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"manage/internal/access"
	"manage/internal/store"
)

// The editions, for the staff: their settings and documents, the levels
// and perks partners choose from, and the purge of a finished one. As for
// the partners, each operation checks that the actor is of the staff.

// saveEdition creates or changes an edition, e as edited; its slug comes
// from its name when blank. A new one may start with the levels and perks
// of another, copyFrom (a slug): it tells whether they were copied.
//
// The staff pages are in French only: the refusals carry the text itself.
func (s *Server) saveEdition(ctx context.Context, a *access.Actor, e *store.Event, copyFrom string) (bool, error) {
	if err := staffOnly(a); err != nil {
		return false, err
	}

	e.Name = strings.TrimSpace(e.Name)
	e.Slug = store.Slugify(firstNonEmpty(e.Slug, e.Name))
	e.PaymentInfo = strings.TrimSpace(e.PaymentInfo)
	e.PaymentMention = strings.TrimSpace(e.PaymentMention)

	// An edition's pages sit below its slug: not a word of the site's own.
	if store.ReservedSlug(e.Slug) {
		e.Slug += "-" + strconv.Itoa(e.Year)
	}

	if e.Name == "" || e.Year == 0 {
		return false, refuse("staff.err.edition_required")
	}

	isNew := e.ID == 0

	if err := s.store.SaveEvent(ctx, e); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return false, refuse("staff.err.slug_taken")
		}

		return false, err
	}

	if !isNew || copyFrom == "" {
		return false, nil
	}

	src, err := s.store.GetEventBySlug(ctx, copyFrom)
	if err != nil {
		return false, errBadInput
	}

	if _, err := s.store.CloneLevelsAndPerks(ctx, src.ID, e.ID); err != nil {
		return false, err
	}

	return true, nil
}

// editionDocuments are the kinds of documents of an edition: the handler
// checks one before taking the file in, so that a wrong kind stores none.
var editionDocuments = []string{"brochure_fr", "brochure_en", "guide_fr", "guide_en"}

// setEditionDocument sets a document of an edition partners download:
// kind is the brochure or the sponsor guide, and its language
// ("brochure_fr", "guide_en"…).
func (s *Server) setEditionDocument(ctx context.Context, a *access.Actor, e *store.Event, kind, file string) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	switch kind {
	case "brochure_fr":
		e.BrochureFRFile = file
	case "brochure_en":
		e.BrochureENFile = file
	case "guide_fr":
		e.GuideFRFile = file
	case "guide_en":
		e.GuideENFile = file
	default:
		return errBadInput
	}

	return s.store.SaveEvent(ctx, e)
}

// purgeEdition removes the personal data of a finished edition (see
// store.PurgeEvent): not before its last day has passed.
func (s *Server) purgeEdition(ctx context.Context, a *access.Actor, e *store.Event) (*store.PurgeResult, error) {
	if err := staffOnly(a); err != nil {
		return nil, err
	}

	if !eventEnded(e) {
		return nil, refuse("flash.event_not_over")
	}

	end := e.EndsOn.In(time.UTC)
	ended := time.Date(end.Year(), end.Month(), end.Day()+1, 0, 0, 0, 0, paris)

	res, err := s.store.PurgeEvent(ctx, e.ID, ended)
	if err != nil {
		return nil, err
	}

	for _, f := range res.Files {
		s.dropFile(ctx, f)
	}

	slog.Info("edition purged", "event", e.Slug, "attendees", res.Attendees, "signups", res.Signups, "people", res.People)

	return res, nil
}

// --- levels and perks -----------------------------------------------------------

// cloneLevels copies the levels, perks and their associations of another
// edition, src, into e, keeping what already exists. It tells whether
// anything was copied.
func (s *Server) cloneLevels(ctx context.Context, a *access.Actor, e, src *store.Event) (bool, error) {
	if err := staffOnly(a); err != nil {
		return false, err
	}

	if src.ID == e.ID {
		return false, errBadInput
	}

	res, err := s.store.CloneLevelsAndPerks(ctx, src.ID, e.ID)
	if err != nil {
		return false, err
	}

	return res.Levels+res.Perks+res.Pairs > 0, nil
}

// saveLevel saves a level of an edition, new or loaded: it needs a name.
func (s *Server) saveLevel(ctx context.Context, a *access.Actor, e *store.Event, l *store.Level) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	if l.EventID != e.ID {
		return errNotFound
	}

	l.Name = strings.TrimSpace(l.Name)
	l.Color = strings.ToLower(strings.TrimSpace(l.Color))

	if l.Name == "" {
		return errBadInput
	}

	return s.store.SaveLevel(ctx, l)
}

// deleteLevel deletes a level of an edition.
func (s *Server) deleteLevel(ctx context.Context, a *access.Actor, e *store.Event, uid string) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	l, err := s.store.GetLevelByUID(ctx, e.ID, uid)
	if err != nil {
		return err
	}

	return s.store.DeleteLevel(ctx, l.ID)
}

// savePerk saves a perk of an edition, new or loaded: it needs a French
// name.
func (s *Server) savePerk(ctx context.Context, a *access.Actor, e *store.Event, p *store.Perk) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	if p.EventID != e.ID {
		return errNotFound
	}

	p.NameFR = strings.TrimSpace(p.NameFR)
	p.NameEN = strings.TrimSpace(p.NameEN)

	if p.NameFR == "" {
		return errBadInput
	}

	return s.store.SavePerk(ctx, p)
}

// deletePerk deletes a perk of an edition.
func (s *Server) deletePerk(ctx context.Context, a *access.Actor, e *store.Event, uid string) error {
	if err := staffOnly(a); err != nil {
		return err
	}

	p, err := s.store.GetPerkByUID(ctx, e.ID, uid)
	if err != nil {
		return err
	}

	return s.store.DeletePerk(ctx, p.ID)
}

// saveLevelPerks sets which perks each level of an edition includes, as
// "<level uid>:<perk uid>" pairs; unknown UIDs are ignored.
func (s *Server) saveLevelPerks(ctx context.Context, a *access.Actor, e *store.Event, pairs []string) error {
	if err := staffOnly(a); err != nil {
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

	levelIDs, perkIDs := map[string]int64{}, map[string]int64{}

	for _, l := range levels {
		levelIDs[l.UID] = l.ID
	}

	for _, p := range perks {
		perkIDs[p.UID] = p.ID
	}

	var ids [][2]int64

	for _, v := range pairs {
		lu, pu, ok := strings.Cut(v, ":")
		if l, p := levelIDs[lu], perkIDs[pu]; ok && l != 0 && p != 0 {
			ids = append(ids, [2]int64{l, p})
		}
	}

	return s.store.SetLevelPerkMatrix(ctx, e.ID, ids)
}
