package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// PurgeResult says what a purge removed.
type PurgeResult struct {
	Attendees int64
	Signups   int64
	People    int64
	// Files are the profile pictures of the people deleted, for the caller
	// to remove once no one else uses them.
	Files []string
}

// PurgeEvent removes the personal data of a finished edition. What the
// next editions build on stays: levels, partnerships, poles, places, slots
// and messages. What goes:
//
//   - the attendees of its partnerships (names, e-mails, dietary
//     requirements) and the change log of those partnerships;
//   - on the volunteer side, the sign-ups, waiting lists, members, leads and
//     coordinators, and who ticked a task or wrote a message;
//   - the names in its volunteer history, which keeps its counts;
//   - then the people left with no part in anything, unless they logged in
//     after the edition ended: a newcomer of the next edition stays.
//
// ended is the end of the edition.
func (s *Store) PurgeEvent(ctx context.Context, eventID int64, ended time.Time) (*PurgeResult, error) {
	res := &PurgeResult{}

	err := s.tx(ctx, func(tx *sql.Tx) error {
		exec := func(query string, args ...any) (int64, error) {
			r, err := tx.ExecContext(ctx, query, args...)
			if err != nil {
				return 0, err
			}

			return r.RowsAffected()
		}

		const partnerships = `SELECT id FROM partnerships WHERE event_id = ?`
		const slots = `SELECT s.id FROM slots s JOIN poles p ON p.id = s.pole_id WHERE p.event_id = ?`
		const poles = `SELECT id FROM poles WHERE event_id = ?`

		var err error

		if res.Attendees, err = exec(`DELETE FROM attendees WHERE partnership_id IN (`+partnerships+`)`, eventID); err != nil {
			return err
		}

		if res.Signups, err = exec(`DELETE FROM signups WHERE slot_id IN (`+slots+`)`, eventID); err != nil {
			return err
		}

		for _, q := range []string{
			`DELETE FROM activity WHERE partnership_id IN (` + partnerships + `)`,
			`DELETE FROM waitlist WHERE slot_id IN (` + slots + `)`,
			`DELETE FROM pole_members WHERE pole_id IN (` + poles + `)`,
			`DELETE FROM pole_managers WHERE pole_id IN (` + poles + `)`,
			`DELETE FROM event_coordinators WHERE event_id = ?`,
			`UPDATE slot_tasks SET done_by = NULL WHERE slot_id IN (` + slots + `)`,
			`UPDATE messages SET created_by = NULL WHERE event_id = ?`,
			`UPDATE volunteer_log SET actor_id = NULL, actor_name = '', subject_id = NULL, subject_name = '' WHERE event_id = ?`,
		} {
			if _, err := exec(q, eventID); err != nil {
				return err
			}
		}

		// Nobody's part in anything left: deleted, with what named them.
		rows, err := tx.QueryContext(ctx, `SELECT id, email, avatar_file FROM people p
			WHERE NOT EXISTS (SELECT 1 FROM admins WHERE person_id = p.id)
			  AND NOT EXISTS (SELECT 1 FROM managers WHERE person_id = p.id)
			  AND NOT EXISTS (SELECT 1 FROM pole_members WHERE person_id = p.id)
			  AND NOT EXISTS (SELECT 1 FROM pole_managers WHERE person_id = p.id)
			  AND NOT EXISTS (SELECT 1 FROM event_coordinators WHERE person_id = p.id)
			  AND NOT EXISTS (SELECT 1 FROM signups WHERE person_id = p.id)
			  AND NOT EXISTS (SELECT 1 FROM waitlist WHERE person_id = p.id)
			  AND p.created_at < ? AND (p.last_login_at IS NULL OR p.last_login_at < ?)`, ended, ended)
		if err != nil {
			return err
		}

		type gone struct {
			id          int64
			email, file string
		}

		var people []gone

		for rows.Next() {
			var g gone
			if err := rows.Scan(&g.id, &g.email, &g.file); err != nil {
				rows.Close()

				return err
			}

			people = append(people, g)
		}

		rows.Close()

		for _, g := range people {
			if err := forgetInLog(ctx, tx, g.id); err != nil {
				return err
			}

			if err := forgetInActivity(ctx, tx, g.email); err != nil {
				return err
			}

			if _, err := exec(`DELETE FROM people WHERE id = ?`, g.id); err != nil {
				return err
			}

			if g.file != "" {
				res.Files = append(res.Files, g.file)
			}
		}

		res.People = int64(len(people))

		_, err = exec(`UPDATE events SET purged_at = ? WHERE id = ?`, now(), eventID)

		return err
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

// forgetInActivity wipes an address from the partner change log: as the
// actor of a change, and in the details that name it.
func forgetInActivity(ctx context.Context, q querier, email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil
	}

	for _, actor := range []string{email, "staff:" + email} {
		if _, err := q.ExecContext(ctx, `UPDATE activity SET actor = '' WHERE actor = ?`, actor); err != nil {
			return err
		}
	}

	_, err := q.ExecContext(ctx, `UPDATE activity SET details = REPLACE(details, ?, '…') WHERE INSTR(details, ?) > 0`, email, email)

	return err
}
