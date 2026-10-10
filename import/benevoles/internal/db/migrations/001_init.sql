-- Core schema. Times are stored as RFC3339 UTC text; the application converts
-- to the configured display timezone.

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name          TEXT NOT NULL DEFAULT '',
    phone         TEXT NOT NULL DEFAULT '',
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    last_login_at TEXT
);

-- One-time login tokens. Only the SHA-256 of the token is stored.
CREATE TABLE magic_links (
    token_hash TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);

-- One edition = one year of the event.
-- status: draft | open | frozen | archived
CREATE TABLE editions (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'draft',
    starts_on  TEXT NOT NULL, -- YYYY-MM-DD
    ends_on    TEXT NOT NULL, -- YYYY-MM-DD
    freeze_at  TEXT,          -- after this instant volunteers can no longer change signups
    created_at TEXT NOT NULL
);

-- A pole is a team inside an edition. access_key NULL means anyone can join.
-- is_default marks the pole automatically created with the edition, used for
-- organizer-wide ("global") slots. Every volunteer of the edition is a member.
CREATE TABLE poles (
    id          INTEGER PRIMARY KEY,
    edition_id  INTEGER NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    access_key  TEXT,
    is_default  INTEGER NOT NULL DEFAULT 0,
    freeze_at   TEXT,  -- optional override of editions.freeze_at
    position    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL
);
CREATE INDEX poles_edition ON poles(edition_id);

CREATE TABLE pole_managers (
    pole_id INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (pole_id, user_id)
);

CREATE TABLE pole_members (
    pole_id   INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TEXT NOT NULL,
    PRIMARY KEY (pole_id, user_id)
);
CREATE INDEX pole_members_user ON pole_members(user_id);

-- Optional subdivision of a pole's calendar (a room, a desk, a stand...).
CREATE TABLE locations (
    id       INTEGER PRIMARY KEY,
    pole_id  INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    name     TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX locations_pole ON locations(pole_id);

-- A slot = time range x location x number of volunteers wanted.
CREATE TABLE slots (
    id          INTEGER PRIMARY KEY,
    pole_id     INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    location_id INTEGER REFERENCES locations(id) ON DELETE SET NULL,
    title       TEXT NOT NULL DEFAULT '',
    starts_at   TEXT NOT NULL,
    ends_at     TEXT NOT NULL,
    capacity    INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL
);
CREATE INDEX slots_pole_start ON slots(pole_id, starts_at);

-- Shared checklist attached to a slot. Anyone signed up on the slot can tick
-- an item; we remember who did it.
CREATE TABLE slot_tasks (
    id       INTEGER PRIMARY KEY,
    slot_id  INTEGER NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    label    TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    done_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    done_at  TEXT
);
CREATE INDEX slot_tasks_slot ON slot_tasks(slot_id);

CREATE TABLE signups (
    slot_id    INTEGER NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (slot_id, user_id)
);
CREATE INDEX signups_user ON signups(user_id);

-- Messages displayed on pages. Exactly one scope is set:
--   edition only          -> shown everywhere in the edition (organizers)
--   pole                  -> shown on the pole page (pole managers)
--   slot                  -> shown on the slot page (pole managers)
CREATE TABLE messages (
    id         INTEGER PRIMARY KEY,
    edition_id INTEGER NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
    pole_id    INTEGER REFERENCES poles(id) ON DELETE CASCADE,
    slot_id    INTEGER REFERENCES slots(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX messages_edition ON messages(edition_id);
CREATE INDEX messages_pole ON messages(pole_id);
CREATE INDEX messages_slot ON messages(slot_id);

-- Reminder e-mails already sent, to avoid duplicates.
CREATE TABLE reminders_sent (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day     TEXT NOT NULL, -- YYYY-MM-DD of the slots covered
    sent_at TEXT NOT NULL,
    PRIMARY KEY (user_id, day)
);
