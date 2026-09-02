package db

import (
	"database/sql"
	"strings"
	"unicode"
)

// reservedSlugs are first path segments used by application routes; an event
// or pole slug may not collide with them.
var reservedSlugs = map[string]bool{
	"admin": true, "me": true, "login": true, "logout": true, "auth": true, "profile": true,
	"slots": true, "tasks": true, "manage": true, "static": true, "uploads": true, "api": true,
	"new": true, "join": true, "leave": true, "events": true, "poles": true, "me.ics": true,
}

var accents = map[rune]string{
	'à': "a", 'â': "a", 'ä': "a", 'á': "a", 'ã': "a", 'å': "a",
	'ç': "c", 'é': "e", 'è': "e", 'ê': "e", 'ë': "e",
	'î': "i", 'ï': "i", 'í': "i", 'ô': "o", 'ö': "o", 'ó': "o", 'õ': "o",
	'ù': "u", 'û': "u", 'ü': "u", 'ú': "u", 'ÿ': "y", 'ñ': "n", 'œ': "oe", 'æ': "ae", 'ß': "ss",
}

// Slugify turns a name into a URL slug: lowercase ASCII letters, digits and
// dashes. It returns "" when nothing usable remains.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if rep, ok := accents[r]; ok {
			b.WriteString(rep)
			dash = false
			continue
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// Non-Latin letters are dropped; keep a separator.
			fallthrough
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s
}

// ValidSlug tells whether s is an acceptable, non-reserved slug.
func ValidSlug(s string) bool {
	return s != "" && s == Slugify(s) && !reservedSlugs[s]
}

// uniqueSlug appends -2, -3… to base until taken(candidate) is false.
func uniqueSlug(base string, taken func(string) bool) string {
	if base == "" || reservedSlugs[base] {
		base = "p"
	}
	s := base
	for i := 2; taken(s); i++ {
		s = base + "-" + itoa(i)
	}
	return s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// querier is satisfied by both *DB and *sql.Tx. Slug lookups must run on the
// transaction when one is open: the pool has a single connection.
type querier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// UniqueEventSlug returns a free event slug derived from base (excluding the
// event with id exceptID when renaming).
func (d *DB) UniqueEventSlug(base string, exceptID int64) string {
	return uniqueEventSlug(d, base, exceptID)
}

func uniqueEventSlug(q querier, base string, exceptID int64) string {
	return uniqueSlug(Slugify(base), func(s string) bool {
		var n int
		q.QueryRow(`SELECT COUNT(*) FROM events WHERE slug = ? AND id != ?`, s, exceptID).Scan(&n)
		return n > 0
	})
}

// UniquePoleSlug returns a free pole slug within an event.
func (d *DB) UniquePoleSlug(eventID int64, base string, exceptID int64) string {
	return uniquePoleSlug(d, eventID, base, exceptID)
}

func uniquePoleSlug(q querier, eventID int64, base string, exceptID int64) string {
	return uniqueSlug(Slugify(base), func(s string) bool {
		var n int
		q.QueryRow(`SELECT COUNT(*) FROM poles WHERE event_id = ? AND slug = ? AND id != ?`, eventID, s, exceptID).Scan(&n)
		return n > 0
	})
}

// backfillSlugs gives a slug to rows created before slugs existed.
func (d *DB) backfillSlugs() error {
	rows, err := d.Query(`SELECT id, name FROM events WHERE slug = ''`)
	if err != nil {
		return err
	}
	type row struct {
		id   int64
		name string
	}
	var todo []row
	for rows.Next() {
		var r row
		rows.Scan(&r.id, &r.name)
		todo = append(todo, r)
	}
	rows.Close()
	for _, r := range todo {
		if _, err := d.Exec(`UPDATE events SET slug = ? WHERE id = ?`, d.UniqueEventSlug(r.name, r.id), r.id); err != nil {
			return err
		}
	}
	prow, err := d.Query(`SELECT id, event_id, name FROM poles WHERE slug = ''`)
	if err != nil {
		return err
	}
	type prow_ struct {
		id, ev int64
		name   string
	}
	var ptodo []prow_
	for prow.Next() {
		var r prow_
		prow.Scan(&r.id, &r.ev, &r.name)
		ptodo = append(ptodo, r)
	}
	prow.Close()
	for _, r := range ptodo {
		if _, err := d.Exec(`UPDATE poles SET slug = ? WHERE id = ?`, d.UniquePoleSlug(r.ev, r.name, r.id), r.id); err != nil {
			return err
		}
	}
	return nil
}
