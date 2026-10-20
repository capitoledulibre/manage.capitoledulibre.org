-- An authorization code is kept once used, until it expires, with the
-- grant it was traded for: a second use of the same code then revokes that
-- grant (OAuth 2.1), as the code may have been stolen.
ALTER TABLE oauth_codes ADD COLUMN used_at DATETIME;
ALTER TABLE oauth_codes ADD COLUMN token_id INTEGER REFERENCES api_tokens(id) ON DELETE SET NULL;
