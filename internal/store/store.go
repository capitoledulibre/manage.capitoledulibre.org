// Package store wraps the SQLite database: schema migrations, models and
// queries.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a looked-up row does not exist.
var ErrNotFound = errors.New("not found")

// ErrDuplicate is returned when a unique constraint would be violated.
var ErrDuplicate = errors.New("duplicate")

// ErrBadInput is returned when a value is not one the store accepts.
var ErrBadInput = errors.New("bad input")

type Store struct {
	db *sql.DB
}

// Open opens (and creates if needed) the SQLite database at path and applies
// pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	s, err := openDB(path)
	if err != nil {
		return nil, err
	}

	if err := s.migrate(ctx); err != nil {
		s.db.Close()

		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// openDB opens the database without migrating it.
func openDB(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_time_format=sqlite"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// SQLite serializes writes anyway; a single connection avoids
	// SQLITE_BUSY errors and keeps memory usage low.
	db.SetMaxOpenConns(1)

	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error { return s.migrateTo(ctx, 0) }

// foreignKeysOff marks a migration that rebuilds a table other tables
// reference. With foreign keys enforced, dropping that table would run the
// ON DELETE actions of its children (deleting sessions, clearing the staff
// owner of every partnership), so such a migration runs with them off and is
// checked with foreign_key_check before it commits, as SQLite documents for
// schema changes.
const foreignKeysOff = "-- manage:foreign-keys-off"

// migrateTo applies the pending migrations up to version limit, or all of
// them when limit is 0. Tests use the limit to build an older database.
func (s *Store) migrateTo(ctx context.Context, limit int) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`); err != nil {
		return err
	}

	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}

	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}

	sort.Strings(files)

	for _, f := range files {
		name := strings.TrimPrefix(f, "migrations/")

		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %q", name)
		}

		if version <= current || (limit > 0 && version > limit) {
			continue
		}

		body, err := migrationsFS.ReadFile(f)
		if err != nil {
			return err
		}

		if err := s.applyMigration(ctx, name, version, string(body)); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) applyMigration(ctx context.Context, name string, version int, body string) error {
	keysOff := strings.HasPrefix(body, foreignKeysOff)

	// The pragma has no effect inside a transaction, so it is set around it;
	// the single connection keeps it for the transaction.
	if keysOff {
		if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}

		defer func() { _, _ = s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`) }()
	}

	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, body); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}

		if keysOff {
			var table string

			err := tx.QueryRowContext(ctx, `SELECT "table" FROM pragma_foreign_key_check`).Scan(&table)
			if err == nil {
				return fmt.Errorf("%s: broken foreign key in %s", name, table)
			}

			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%s: foreign key check: %w", name, err)
			}
		}

		_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, now())

		return err
	})
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()

		return err
	}

	return tx.Commit()
}

func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

// NullTime converts a *time.Time into a value suitable for a nullable
// DATETIME column.
func nullTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}

	return t.UTC()
}

func timePtr(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}

	t := nt.Time

	return &t
}

func nullInt(v *int64) any {
	if v == nil {
		return nil
	}

	return *v
}

func int64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}

	v := n.Int64

	return &v
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	return err
}
