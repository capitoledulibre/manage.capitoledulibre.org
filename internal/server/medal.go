package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"manage/internal/store"
)

// Medal holds the colors and size used to draw a level medal (see the
// "medal" template). Shades are derived from a single base color so any
// color picked by the staff gives a metallic look.
type Medal struct {
	Size  int
	Base  string
	Light string
	Dark  string
	Deep  string
}

// medalPresets are suggested colors, also used as defaults for levels named
// after a metal.
var medalPresets = []struct{ Name, Color string }{
	{"Platine", "#d3e4f0"},
	{"Or", "#d4a42c"},
	{"Argent", "#aab2bb"},
	{"Bronze", "#c07a3a"},
}

// defaultMedalColor guesses a color from the level name, falling back to the
// brand color.
func defaultMedalColor(name string) string {
	n := strings.ToLower(name)

	switch {
	case strings.Contains(n, "platin"), strings.Contains(n, "diamant"), strings.Contains(n, "diamond"):
		return medalPresets[0].Color
	case n == "or" || strings.Contains(n, "gold") || strings.HasPrefix(n, "or "):
		return medalPresets[1].Color
	case strings.Contains(n, "argent"), strings.Contains(n, "silver"):
		return medalPresets[2].Color
	case strings.Contains(n, "bronze"):
		return medalPresets[3].Color
	}

	return "#ea580c"
}

func levelColor(l *store.Level) string {
	if l == nil {
		return "#9ca3af"
	}

	if l.Color != "" {
		return l.Color
	}

	return defaultMedalColor(l.Name)
}

// newMedal builds a medal of the given pixel size for a level (nil for an
// undefined level, drawn in gray).
func newMedal(l *store.Level, size int) Medal {
	base := levelColor(l)

	return Medal{
		Size:  size,
		Base:  base,
		Light: mixHex(base, "#ffffff", 0.45),
		Dark:  mixHex(base, "#000000", 0.25),
		Deep:  mixHex(base, "#000000", 0.45),
	}
}

// rankedMedal sizes the medal by the level rank among its edition's levels:
// the highest level gets the largest medal.
func rankedMedal(l *store.Level, levels []*store.Level) Medal {
	rank := 0

	for i, other := range levels {
		if l != nil && other.ID == l.ID {
			rank = i
		}
	}

	return newMedal(l, max(88-rank*12, 52))
}

// mixHex blends color a toward b by t (0..1).
func mixHex(a, b string, t float64) string {
	ar, ag, ab := parseHex(a)
	br, bg, bb := parseHex(b)
	mix := func(x, y int) int { return int(float64(x) + (float64(y)-float64(x))*t + 0.5) }

	return fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb))
}

func parseHex(c string) (r, g, b int) {
	v, err := strconv.ParseUint(strings.TrimPrefix(c, "#"), 16, 32)
	if err != nil || len(c) != 7 {
		return 156, 163, 175
	}

	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)
}

// Badge is a past or current confirmed partnership, displayed as
// "Gold sponsor 2025".
type Badge struct {
	Level string
	Year  int
	Medal Medal
}

// orgBadges lists the confirmed partnerships of an organization, most recent
// first.
func (s *Server) orgBadges(r *http.Request, orgID int64) ([]Badge, error) {
	ctx := r.Context()

	partnerships, err := s.store.ListOrgPartnerships(ctx, orgID)
	if err != nil {
		return nil, err
	}

	var out []Badge

	for _, p := range partnerships {
		if p.Status != store.StatusConfirmed {
			continue
		}

		e, err := s.store.GetEvent(ctx, p.EventID)
		if err != nil {
			return nil, err
		}

		var l *store.Level
		if p.LevelID != nil {
			if l, err = s.store.GetLevel(ctx, *p.LevelID); err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		}

		b := Badge{Year: e.Year, Medal: newMedal(l, 18)}
		if l != nil {
			b.Level = l.Name
		}

		out = append(out, b)
	}

	return out, nil
}
