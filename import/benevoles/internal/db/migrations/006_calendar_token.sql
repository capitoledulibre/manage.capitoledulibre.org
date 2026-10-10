-- Calendar subscription: a per-user secret so an agenda application can poll
-- the planning without a session, and keep it up to date on its own.
ALTER TABLE users ADD COLUMN calendar_token TEXT;
CREATE UNIQUE INDEX users_calendar_token ON users(calendar_token) WHERE calendar_token IS NOT NULL;
