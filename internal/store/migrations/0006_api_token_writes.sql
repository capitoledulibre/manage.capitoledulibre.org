-- Whether an API token may change things (sign people up, post a notice…),
-- or only read. Read-only unless its owner chose otherwise.
ALTER TABLE api_tokens ADD COLUMN writes INTEGER NOT NULL DEFAULT 0;
