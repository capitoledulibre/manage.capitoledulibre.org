package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"manage/internal/store"
)

func TestTasksFromThePlanning(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	planning := "/volunteers/" + f.event.Slug + "/planning?day=all"

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)
	mustOK(t, e.st.JoinPole(ctx, f.buvette.ID, nina.ID))
	mustOK(t, e.st.ForceSignup(ctx, f.morning.ID, nina.ID))

	ninaC := e.loginAs("nina@example.org")

	// On her card, the checklist folded with its state, tickable.
	_, body := e.get(ninaC, planning)
	mustContain(t, body, `<details id="tasks-`+f.morning.UID+`"`, "Tâches · 0/1", `name="view" value="card"`)

	toggle := "/volunteers/" + f.event.Slug + "/" + f.buvette.Slug + "/tasks/" + f.taskUID + "/toggle"

	_, body = e.hx(ninaC, toggle, url.Values{"view": {"card"}, "back": {planning}})
	mustContain(t, body, `<details id="tasks-`+f.morning.UID+`" class="group/tasks mt-2" open>`, "Tâches · 1/1", ">faites<", "fait par Nina à")

	// Someone else sees the list, but cannot tick it.
	omar := e.volunteer("omar@example.org", "Omar", "/volunteers/")

	_, body = e.get(omar, planning)
	mustContain(t, body, "Tâches · 1/1", `disabled checked aria-label="Allumer la machine"`)

	if code, _ := e.hx(omar, toggle, url.Values{"view": {"card"}}); code != http.StatusForbidden {
		t.Errorf("someone not on the slot ticks a task: %d", code)
	}

	// The history says who did it, and who took it back.
	lead := e.loginAs(f.leadEmail)
	e.hx(lead, toggle, url.Values{"view": {"card"}})

	_, body = e.get(lead, f.buvette.URL()+"?tab=history")
	mustContain(t, body, "Nina a fait « Allumer la machine » sur Buvette", "Lou Lead a décoché « Allumer la machine »")

	// A slot over with a task undone is late, for whoever runs the pole.
	start := time.Now().Add(-3 * time.Hour)
	done := &store.Slot{PoleID: f.buvette.ID, StartsAt: start, EndsAt: start.Add(2 * time.Hour), Capacity: 1, Title: "Rangement"}
	mustOK(t, e.st.CreateSlot(ctx, done))
	_, err = e.st.CreateTask(ctx, done.ID, "Fermer la caisse")
	mustOK(t, err)

	done, _ = e.st.GetSlot(ctx, done.ID, 0)

	_, body = e.get(lead, "/volunteers/"+f.event.Slug+"/planning?view=late&day=all")
	mustContain(t, body, `aria-current="page">Tâches en retard`, "Rangement", ">en retard<")

	if strings.Contains(body, "Café") {
		t.Error("a slot not over is late")
	}

	// Nobody signs up for a slot that is over.
	_, body = e.get(ninaC, "/volunteers/"+f.event.Slug+"/planning?view=all&day=all")
	mustContain(t, body, ">Terminé<")

	_, body = e.hx(ninaC, done.URL()+"/signup", url.Values{"back": {planning}})
	mustContain(t, body, "Ce créneau est terminé.")

	// A volunteer has no such view.
	_, body = e.get(ninaC, "/volunteers/"+f.event.Slug+"/planning?view=late&day=all")
	if strings.Contains(body, "Tâches en retard") {
		t.Error("a volunteer gets the late tasks view")
	}
}
