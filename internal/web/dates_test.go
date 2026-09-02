package web

import (
	"testing"
	"time"
)

func TestDateRange(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Paris")
	cases := []struct{ from, to, want string }{
		{"2026-11-13", "2026-11-15", "du 13 au 15 novembre 2026"},
		{"2026-11-14", "2026-11-14", "le samedi 14 novembre 2026"},
		{"2026-11-30", "2026-12-01", "du 30 novembre 2026 au 1 décembre 2026"},
		{"bad", "2026-12-01", "bad → 2026-12-01"},
	}
	for _, c := range cases {
		if got := dateRange(c.from, c.to, loc); got != c.want {
			t.Errorf("dateRange(%s, %s) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}
