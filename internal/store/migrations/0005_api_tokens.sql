-- Personal tokens for the API (MCP). Only the hash is stored. A token
-- made by a staff member carries the staff's rights, and goes with their
-- staff access (admin_id); one made by a person carries their roles.
CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    uid          TEXT NOT NULL UNIQUE,
    token_hash   TEXT NOT NULL UNIQUE,
    person_id    INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    admin_id     INTEGER REFERENCES admins(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    expires_at   DATETIME NOT NULL,
    last_used_at DATETIME,
    created_at   DATETIME NOT NULL
);
CREATE INDEX api_tokens_person ON api_tokens(person_id);
