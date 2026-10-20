package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"manage/internal/i18n"
	"manage/internal/store"
)

// logVol records an entry of the volunteer log, by the signed-in person
// unless the entry names its actor. Logging never fails the action it
// records.
func (s *Server) logVol(r *http.Request, e *store.LogEntry) {
	s.logAs(r.Context(), rc(r).Person, e)
}

// logAs writes an entry of the volunteer history, by actor unless the
// entry names one.
func (s *Server) logAs(ctx context.Context, actor *store.Person, e *store.LogEntry) {
	if e.ActorID == 0 && e.ActorName == "" {
		e.SetActor(actor)
	}

	if err := s.store.Log(ctx, e); err != nil {
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

		// A task entry names the task in Title, not the slot.
		if e.Title != "" && e.Action != store.LogTaskDone && e.Action != store.LogTaskUndone {
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

// logPage reads the part of the log asked for: its kind and page.
func logPage(r *http.Request) store.LogPage {
	p := store.LogPage{Kind: r.URL.Query().Get("kind")}
	if _, ok := store.LogKinds[p.Kind]; !ok {
		p.Kind = ""
	}

	p.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	p.Page = max(p.Page, 0)

	return p
}

// logView is what the history's filters and pager need: the page shown,
// whether older lines follow, and the URL of the tab (with its query).
type logView struct {
	Page store.LogPage
	More bool
	Base string
}

// Kinds lists the kinds offered, in order.
func (logView) Kinds() []string { return store.LogKindOrder }

// URL is the link to a kind (all when empty) and a page of the log.
func (l logView) URL(kind string, page int) string {
	u := l.Base
	if kind != "" {
		u += "&kind=" + kind
	}

	if page > 0 {
		u += "&page=" + strconv.Itoa(page)
	}

	return u
}
