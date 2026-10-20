package server

import (
	"context"
	"strings"
	"time"

	"manage/internal/access"
	"manage/internal/store"
)

// The settings of a pole, for whoever runs it: its description and access,
// its invitation key, its image, its places. As for the sign-ups, each
// operation checks the actor's rights itself.

// poleSettings are the settings of a pole as typed. Name is kept when
// blank, Slug when nil (and always for the default pole), AccessMode when
// empty or unknown.
type poleSettings struct {
	Name         string
	Slug         *string
	Description  string
	Icon         string
	DefaultTasks string
	FreezeAt     *time.Time
	AccessMode   string
}

// savePole changes the settings of a pole the actor runs. Only the
// coordination makes a pole restricted, or lifts it: it hides the pole
// from everyone else, and keeps its managers from bringing people in.
func (s *Server) savePole(ctx context.Context, a *access.Actor, v *poleView, set poleSettings) error {
	if err := runs(a, v, nil); err != nil {
		return err
	}

	p := *v.Pole

	if name := strings.TrimSpace(set.Name); name != "" {
		p.Name = name
	}

	if set.Slug != nil && !p.IsDefault {
		p.Slug = strings.TrimSpace(*set.Slug)
	}

	p.Description = strings.TrimSpace(set.Description)
	p.Icon = strings.TrimSpace(set.Icon)
	p.DefaultTasks = strings.TrimSpace(set.DefaultTasks)
	p.FreezeAt = set.FreezeAt

	if mode := set.AccessMode; mode != "" && !p.IsDefault && store.ValidPoleMode(mode) {
		restricting := mode == store.PoleRestricted || p.AccessMode == store.PoleRestricted
		if !restricting || a.CanRestrict(&p) {
			p.AccessMode = mode
		}
	}

	if err := s.store.UpdatePole(ctx, &p); err != nil {
		return err
	}

	*v.Pole = p

	return nil
}

// regenerateKey gives a pole the actor runs a new invitation key: the
// links sent so far stop working.
func (s *Server) regenerateKey(ctx context.Context, a *access.Actor, v *poleView) error {
	if err := runs(a, v, nil); err != nil {
		return err
	}

	key, err := s.store.RegenerateAccessKey(ctx, v.Pole.ID)
	if err != nil {
		return err
	}

	v.Pole.AccessKey = key

	return nil
}

// setPoleImage sets the image file of a pole the actor runs, "" to take it
// off, and drops the former one.
func (s *Server) setPoleImage(ctx context.Context, a *access.Actor, v *poleView, file string) error {
	if err := runs(a, v, nil); err != nil {
		return err
	}

	if err := s.store.SetPoleImage(ctx, v.Pole.ID, file); err != nil {
		return err
	}

	s.dropFile(ctx, v.Pole.ImageFile)
	v.Pole.ImageFile = file

	return nil
}

// addPlaces adds places to a pole the actor runs, one per non-blank name.
func (s *Server) addPlaces(ctx context.Context, a *access.Actor, v *poleView, names []string) error {
	if err := runs(a, v, nil); err != nil {
		return err
	}

	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			if _, err := s.store.CreateLocation(ctx, v.Pole.ID, name); err != nil {
				return err
			}
		}
	}

	return nil
}

// deletePlace deletes a place of a pole the actor runs.
func (s *Server) deletePlace(ctx context.Context, a *access.Actor, v *poleView, uid string) error {
	if err := runs(a, v, nil); err != nil {
		return err
	}

	l, err := s.store.GetLocationByUID(ctx, v.Pole.ID, uid)
	if err != nil {
		return err
	}

	return s.store.DeleteLocation(ctx, l.ID)
}
