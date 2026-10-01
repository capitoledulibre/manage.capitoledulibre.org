CREATE TABLE social_posts (
    id             INTEGER PRIMARY KEY,
    partnership_id INTEGER NOT NULL REFERENCES partnerships(id) ON DELETE CASCADE,
    network        TEXT NOT NULL,
    planned_at     DATETIME,
    url            TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    updated_at     DATETIME NOT NULL
);
CREATE INDEX social_posts_partnership ON social_posts(partnership_id);
