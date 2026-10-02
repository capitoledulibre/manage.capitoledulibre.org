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

func TestDeadline(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Paris")
	at := func(v string) *time.Time {
		t, _ := time.ParseInLocation("2006-01-02 15:04", v, loc)
		return &t
	}
	cases := []struct {
		at   *time.Time
		want string
	}{
		{at("2026-11-15 18:00"), "dimanche 15 novembre à 18:00"},
		{at("2026-11-15 00:00"), "samedi 14 novembre inclus"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := deadline(c.at, loc); got != c.want {
			t.Errorf("deadline(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}

func TestFormatPhone(t *testing.T) {
	for in, want := range map[string]string{
		"0611223344":        "06 11 22 33 44",
		"06.11.22.33.44":    "06 11 22 33 44",
		"+33 6 11 22 33 44": "+33 6 11 22 33 44",
		"112":               "112",
	} {
		if got := formatPhone(in); got != want {
			t.Errorf("formatPhone(%q) = %q, want %q", in, got, want)
		}
	}
	if got := telHref("+33 6 11-22 33 44"); got != "+33611223344" {
		t.Errorf("telHref = %q", got)
	}
}

func TestOrgInitials(t *testing.T) {
	for in, want := range map[string]string{"Example Conf": "CL", "Bénévoles": "B", "festival": "F", "": ""} {
		if got := orgInitials(in); got != want {
			t.Errorf("orgInitials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuggestEventName(t *testing.T) {
	for _, c := range []struct {
		org  string
		year int
		want string
	}{
		{"Example Conf", 2026, "Example Conf 2026"},
		{"Example Conf 2026", 2026, "Example Conf 2026"},
		{"Example Conf 2026", 2027, "Example Conf 2027"},
		{"", 2026, ""},
	} {
		if got := suggestEventName(c.org, c.year); got != c.want {
			t.Errorf("suggestEventName(%q, %d) = %q, want %q", c.org, c.year, got, c.want)
		}
	}
}
