-- OAuth 2.1 for the MCP server: the clients an assistant registers
-- (public clients, PKCE, no secret), the one-time authorization codes,
-- and on api_tokens, the client of a grant with its refresh token. A grant
-- is one api_tokens row: its access token (token_hash) lives an hour, its
-- refresh token renews both until refresh_expires_at.
CREATE TABLE oauth_clients (
    id            INTEGER PRIMARY KEY,
    client_id     TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL,
    redirect_uris TEXT NOT NULL,
    created_at    DATETIME NOT NULL
);

CREATE TABLE oauth_codes (
    code_hash      TEXT PRIMARY KEY,
    client_id      INTEGER NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    person_id      INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    admin_id       INTEGER REFERENCES admins(id) ON DELETE CASCADE,
    redirect_uri   TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    writes         INTEGER NOT NULL DEFAULT 0,
    expires_at     DATETIME NOT NULL
);

ALTER TABLE api_tokens ADD COLUMN client_id INTEGER REFERENCES oauth_clients(id) ON DELETE CASCADE;
ALTER TABLE api_tokens ADD COLUMN refresh_hash TEXT;
ALTER TABLE api_tokens ADD COLUMN refresh_expires_at DATETIME;
CREATE UNIQUE INDEX api_tokens_refresh ON api_tokens(refresh_hash);
