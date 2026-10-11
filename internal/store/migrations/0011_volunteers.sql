-- The volunteer module, ported from benevoles.example.org, which had
-- no data to carry over: its tables are created in their final shape. Rows
-- that show in URLs or forms carry a uid, as everywhere in manage.

-- Volunteer side of a person.
ALTER TABLE people ADD COLUMN phone TEXT NOT NULL DEFAULT '';
-- A pole manager may show their phone to the volunteers of their poles.
ALTER TABLE people ADD COLUMN share_phone BOOLEAN NOT NULL DEFAULT 0;
-- Secret of the calendar subscription feed, created on first use.
ALTER TABLE people ADD COLUMN calendar_token TEXT;
CREATE UNIQUE INDEX people_calendar_token ON people(calendar_token) WHERE calendar_token IS NOT NULL;
-- Where avatar_file came from: '' (not looked up yet), 'upload', 'gravatar'
-- or 'none' (the person removed it, or Gravatar had nothing).
ALTER TABLE people ADD COLUMN avatar_source TEXT NOT NULL DEFAULT '';
ALTER TABLE people ADD COLUMN avatar_checked_at DATETIME;

-- A pole is a team of an edition. access_mode: public (anyone joins),
-- private (through an invitation link carrying access_key), invitation
-- (managers add members) or restricted (only coordinators and staff add
-- members; the pole is hidden from others). The default pole of each
-- edition holds the shifts of no team in particular, and every volunteer
-- belongs to it.
CREATE TABLE poles (
    id            INTEGER PRIMARY KEY,
    uid           TEXT NOT NULL UNIQUE,
    event_id      INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    slug          TEXT NOT NULL,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    access_mode   TEXT NOT NULL DEFAULT 'public',
    access_key    TEXT NOT NULL DEFAULT '',
    icon          TEXT NOT NULL DEFAULT '',
    image_file    TEXT NOT NULL DEFAULT '',
    default_tasks TEXT NOT NULL DEFAULT '',
    is_default    BOOLEAN NOT NULL DEFAULT 0,
    -- Overrides the edition's volunteer_freeze_at for this pole.
    freeze_at     DATETIME,
    position      INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL,
    UNIQUE (event_id, slug)
);

CREATE TABLE pole_members (
    pole_id   INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    joined_at DATETIME NOT NULL,
    PRIMARY KEY (pole_id, person_id)
);
CREATE INDEX pole_members_person ON pole_members(person_id);

-- Pole managers run a pole; not to be confused with managers, the contacts
-- of a partner organization.
CREATE TABLE pole_managers (
    pole_id   INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (pole_id, person_id)
);
CREATE INDEX pole_managers_person ON pole_managers(person_id);

-- The coordination of an edition runs every pole of it.
CREATE TABLE event_coordinators (
    event_id  INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (event_id, person_id)
);
CREATE INDEX event_coordinators_person ON event_coordinators(person_id);

-- Places of a pole (rooms, desks…), optional on a slot.
CREATE TABLE locations (
    id       INTEGER PRIMARY KEY,
    uid      TEXT NOT NULL UNIQUE,
    pole_id  INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    name     TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX locations_pole ON locations(pole_id);

CREATE TABLE slots (
    id          INTEGER PRIMARY KEY,
    uid         TEXT NOT NULL UNIQUE,
    pole_id     INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    location_id INTEGER REFERENCES locations(id) ON DELETE SET NULL,
    title       TEXT NOT NULL DEFAULT '',
    starts_at   DATETIME NOT NULL,
    ends_at     DATETIME NOT NULL,
    capacity    INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL
);
CREATE INDEX slots_pole ON slots(pole_id, starts_at);

CREATE TABLE slot_tasks (
    id       INTEGER PRIMARY KEY,
    uid      TEXT NOT NULL UNIQUE,
    slot_id  INTEGER NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    label    TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    done_by  INTEGER REFERENCES people(id) ON DELETE SET NULL,
    done_at  DATETIME
);
CREATE INDEX slot_tasks_slot ON slot_tasks(slot_id);

CREATE TABLE signups (
    slot_id    INTEGER NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    person_id  INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (slot_id, person_id)
);
CREATE INDEX signups_person ON signups(person_id);

-- Volunteers waiting for a place on a full slot, told by e-mail when one
-- frees up (first come, first served).
CREATE TABLE waitlist (
    slot_id    INTEGER NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    person_id  INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (slot_id, person_id)
);
CREATE INDEX waitlist_person ON waitlist(person_id);

-- Notices for the volunteers of an edition (pole_id and slot_id empty), of
-- a pole (pole_id set) or of a slot (slot_id set).
CREATE TABLE messages (
    id         INTEGER PRIMARY KEY,
    uid        TEXT NOT NULL UNIQUE,
    event_id   INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    pole_id    INTEGER REFERENCES poles(id) ON DELETE CASCADE,
    slot_id    INTEGER REFERENCES slots(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_by INTEGER REFERENCES people(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX messages_event ON messages(event_id);

-- Day-before reminders already sent, so a restart does not send twice.
CREATE TABLE volunteer_reminders (
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    day       TEXT NOT NULL, -- YYYY-MM-DD of the slots covered
    sent_at   DATETIME NOT NULL,
    PRIMARY KEY (person_id, day)
);

-- Every existing edition gets its default pole.
INSERT INTO poles (uid, event_id, slug, name, is_default, position, created_at)
SELECT lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6))), id, 'general', 'Général', 1, -1, strftime('%Y-%m-%d %H:%M:%S+00:00', 'now')
FROM events;
