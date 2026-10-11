-- What happened on the volunteer side of an edition: sign-ups, people
-- added or removed, slots moved or deleted. Names, the pole and the slot
-- are copied into the row, so it still reads once they are gone; a person
-- who deletes their account is wiped from it (see DeletePerson).
CREATE TABLE volunteer_log (
    id           INTEGER PRIMARY KEY,
    event_id     INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    pole_id      INTEGER REFERENCES poles(id) ON DELETE SET NULL,
    actor_id     INTEGER REFERENCES people(id) ON DELETE SET NULL,
    actor_name   TEXT NOT NULL DEFAULT '',
    subject_id   INTEGER REFERENCES people(id) ON DELETE SET NULL,
    subject_name TEXT NOT NULL DEFAULT '',
    action       TEXT NOT NULL,
    pole_name    TEXT NOT NULL DEFAULT '',
    slot_starts  DATETIME,
    slot_ends    DATETIME,
    place        TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    count        INTEGER NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL
);
CREATE INDEX volunteer_log_event ON volunteer_log(event_id, created_at);
CREATE INDEX volunteer_log_pole ON volunteer_log(pole_id, created_at);
CREATE INDEX volunteer_log_actor ON volunteer_log(actor_id);
CREATE INDEX volunteer_log_subject ON volunteer_log(subject_id);
