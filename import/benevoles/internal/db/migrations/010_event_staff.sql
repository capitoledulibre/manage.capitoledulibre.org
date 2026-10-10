-- Staff of one event: they run that edition (every pole, the dashboard, the
-- planning, the volunteer list) without the site-wide administrator rights.
CREATE TABLE event_staff (
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (event_id, user_id)
);
CREATE INDEX event_staff_user ON event_staff(user_id);
