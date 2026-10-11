package server

import (
	"log/slog"
	"net/http"

	"manage/internal/i18n"
	"manage/internal/store"
)

// logVol records an entry of the volunteer log, by the signed-in person
// unless the entry names its actor. Logging never fails the action it
// records.
func (s *Server) logVol(r *http.Request, e *store.LogEntry) {
	if e.ActorID == 0 && e.ActorName == "" {
		e.SetActor(rc(r).Person)
	}

	if err := s.store.Log(r.Context(), e); err != nil {
		slog.Error("volunteer log", "action", e.Action, "err", err)
	}
}

// logSlot is an entry about a slot of a pole.
func logSlot(action string, p *store.Pole, sl *store.Slot, subject *store.Person) *store.LogEntry {
	e := &store.LogEntry{Action: action}
	e.SetPole(p)
	e.SetSlot(sl)
	e.SetSubject(subject)

	return e
}

// logPole is an entry about a pole.
func logPole(action string, p *store.Pole, subject *store.Person) *store.LogEntry {
	e := &store.LogEntry{Action: action}
	e.SetPole(p)
	e.SetSubject(subject)

	return e
}

// logLine renders an entry in a language. Every action has its own text,
// which picks what it needs among: who acted, about whom, the pole, the
// slot and a count.
func logLine(lang string, e *store.LogEntry) string {
	who := func(name string) string {
		if name == "" {
			return i18n.T(lang, "vol.log.someone")
		}

		return name
	}

	slot := ""
	if e.SlotStarts != nil && e.SlotEnds != nil {
		slot = slotWhen(lang, *e.SlotStarts, *e.SlotEnds)
		if e.Place != "" {
			slot += " (" + e.Place + ")"
		}

		if e.Title != "" {
			slot += " — " + e.Title
		}
	}

	what := e.Title
	if e.Action == store.LogStatus {
		what = i18n.T(lang, "vol.status."+e.Title)
	}

	key := "vol.log." + e.Action
	if !i18n.Has(key) {
		return e.Action
	}

	return i18n.T(lang, key, who(e.ActorName), who(e.SubjectName), e.PoleName, slot, e.Count, what)
}
