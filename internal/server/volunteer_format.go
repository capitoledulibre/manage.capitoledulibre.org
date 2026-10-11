package server

import (
	"fmt"
	"strings"
	"time"

	"manage/internal/i18n"
)

// The volunteer pages speak of days and hours more than of dates: a shift is
// "samedi 14 novembre, 14:00 – 18:00". These helpers render them in the
// reader's language, in the event's time zone (paris).

var (
	weekdaysFR = []string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}
	monthsFR   = []string{"", "janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
)

// dayName renders a day as "samedi 14 novembre" or "Saturday 14 November".
func dayName(lang string, t time.Time) string {
	t = t.In(paris)
	if lang == "en" {
		return t.Format("Monday 2 January")
	}

	return fmt.Sprintf("%s %d %s", weekdaysFR[t.Weekday()], t.Day(), monthsFR[t.Month()])
}

// monthYear renders "novembre 2026" or "November 2026".
func monthYear(lang string, t time.Time) string {
	if lang == "en" {
		return t.Format("January 2006")
	}

	return fmt.Sprintf("%s %d", monthsFR[t.Month()], t.Year())
}

// clock renders the time of day, "14:00".
func clock(t time.Time) string { return t.In(paris).Format("15:04") }

// dayKey is the calendar day of t in the event's time zone, to group slots.
func dayKey(t time.Time) string { return t.In(paris).Format(time.DateOnly) }

// dayKeyName renders a dayKey as dayName does.
func dayKeyName(lang, key string) string {
	t, err := time.ParseInLocation(time.DateOnly, key, paris)
	if err != nil {
		return key
	}

	return dayName(lang, t)
}

// dateSpan renders the dates of an edition: "du 14 au 15 novembre 2026",
// "14–15 November 2026". The dates are calendar dates (no time zone), and
// either may be missing.
func dateSpan(lang string, from, to *time.Time) string {
	switch {
	case from == nil && to == nil:
		return ""
	case from == nil:
		from = to
	case to == nil:
		to = from
	}

	a, b := *from, *to
	if a.Equal(b) {
		if lang == "en" {
			return a.Format("Monday 2 January 2006")
		}

		return fmt.Sprintf("le %s %d %s %d", weekdaysFR[a.Weekday()], a.Day(), monthsFR[a.Month()], a.Year())
	}

	if a.Month() == b.Month() && a.Year() == b.Year() {
		if lang == "en" {
			return fmt.Sprintf("%d–%d %s", a.Day(), b.Day(), monthYear(lang, b))
		}

		return fmt.Sprintf("du %d au %d %s", a.Day(), b.Day(), monthYear(lang, b))
	}

	if lang == "en" {
		return fmt.Sprintf("%d %s – %d %s", a.Day(), a.Format("January"), b.Day(), monthYear(lang, b))
	}

	return fmt.Sprintf("du %d %s au %d %s", a.Day(), monthsFR[a.Month()], b.Day(), monthYear(lang, b))
}

// deadline renders when sign-ups close, for a sentence starting with
// "jusqu'au" / "until". A freeze at midnight reads as the whole previous
// day, which is what the organiser meant and what a volunteer understands.
func deadline(lang string, t *time.Time) string {
	if t == nil {
		return ""
	}

	d := t.In(paris)
	if d.Hour() == 0 && d.Minute() == 0 {
		return i18n.T(lang, "vol.deadline_day", dayName(lang, d.AddDate(0, 0, -1)))
	}

	return i18n.T(lang, "vol.deadline_time", dayName(lang, d), d.Format("15:04"))
}

// slotWhen names a slot's time in a sentence: "samedi 14 novembre de 14:00 à
// 18:00".
func slotWhen(lang string, start, end time.Time) string {
	return i18n.T(lang, "vol.slot_when", dayName(lang, start), clock(start), clock(end))
}

// plural picks key+".one" or key+".other" for n and formats it with n
// first, when the text has a verb: "Responsables :" needs no number. French
// counts 0 and 1 as singular, English only 1.
func plural(lang, key string, n int, args ...any) string {
	key += ".other"
	if n == 1 || (n == 0 && lang != "en") {
		key = strings.TrimSuffix(key, ".other") + ".one"
	}

	if text := i18n.T(lang, key); !strings.Contains(text, "%") {
		return text
	}

	return i18n.T(lang, key, append([]any{n}, args...)...)
}

// formatPhone groups a French ten-digit number by pairs, "06 11 22 33 44",
// which is how it is read and dialled; anything else is shown as typed.
func formatPhone(p string) string {
	digits := telHref(p)
	if len(digits) != 10 || digits[0] != '0' {
		return strings.TrimSpace(p)
	}

	return digits[0:2] + " " + digits[2:4] + " " + digits[4:6] + " " + digits[6:8] + " " + digits[8:10]
}

// telHref keeps the digits and a leading + of a phone number, for tel: links.
func telHref(p string) string {
	var b strings.Builder

	for i, r := range strings.TrimSpace(p) {
		if (r >= '0' && r <= '9') || (r == '+' && i == 0) {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// firstName is the first word of a name, for a greeting.
func firstName(name, email string) string {
	if f := strings.Fields(name); len(f) > 0 {
		return f[0]
	}

	return email
}
