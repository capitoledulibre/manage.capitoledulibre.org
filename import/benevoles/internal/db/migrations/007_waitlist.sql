-- Waiting list on a full slot: whoever asked is told when a place frees up.
CREATE TABLE waitlist (
    slot_id    INTEGER NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (slot_id, user_id)
);
CREATE INDEX waitlist_user ON waitlist(user_id);
