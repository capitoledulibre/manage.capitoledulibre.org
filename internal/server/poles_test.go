package server

import (
	"context"
	"errors"
	"testing"

	"manage/internal/access"
	"manage/internal/store"
)

// TestPoleService drives the settings of a pole without HTTP.
func TestPoleService(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.volFixture()
	app := e.app

	lead, err := access.Load(ctx, e.st, f.lead, false)
	mustOK(t, err)

	nina, err := e.st.EnsureVolunteer(ctx, "nina@example.org", "Nina", "", "fr")
	mustOK(t, err)

	volunteer, err := access.Load(ctx, e.st, nina, false)
	mustOK(t, err)

	staff, err := access.Load(ctx, e.st, nil, true)
	mustOK(t, err)

	v, err := app.poleViewOf(ctx, lead, f.event, f.buvette)
	mustOK(t, err)

	// Whoever runs the pole, and nobody else.
	if err := app.savePole(ctx, volunteer, v, poleSettings{Description: "Nope"}); !errors.Is(err, errForbidden) {
		t.Errorf("a volunteer changes a pole: %v", err)
	}

	// A lead does not hide a pole; the rest of the settings go through.
	mustOK(t, app.savePole(ctx, lead, v, poleSettings{Name: "Bar", Description: "Bières", AccessMode: store.PoleRestricted}))

	got, err := e.st.GetPole(ctx, f.buvette.ID)
	mustOK(t, err)

	if got.Name != "Bar" || got.Description != "Bières" || got.AccessMode == store.PoleRestricted {
		t.Errorf("saved by the lead: %+v", got)
	}

	mustOK(t, app.savePole(ctx, staff, v, poleSettings{AccessMode: store.PoleRestricted}))

	if got, _ := e.st.GetPole(ctx, f.buvette.ID); got.AccessMode != store.PoleRestricted || got.Name != "Bar" {
		t.Errorf("restricted by the coordination: %+v", got)
	}

	// A new key: the links sent so far stop working.
	before := v.Pole.AccessKey
	mustOK(t, app.regenerateKey(ctx, lead, v))

	if v.Pole.AccessKey == before || v.Pole.AccessKey == "" {
		t.Error("the key did not change")
	}

	// Places.
	mustOK(t, app.addPlaces(ctx, lead, v, []string{"Comptoir", " ", "Terrasse"}))

	places, err := e.st.ListLocations(ctx, f.buvette.ID)
	mustOK(t, err)

	if len(places) != 2 {
		t.Fatalf("places: %d", len(places))
	}

	if err := app.deletePlace(ctx, volunteer, v, places[0].UID); !errors.Is(err, errForbidden) {
		t.Errorf("a volunteer deletes a place: %v", err)
	}

	mustOK(t, app.deletePlace(ctx, lead, v, places[0].UID))
}
