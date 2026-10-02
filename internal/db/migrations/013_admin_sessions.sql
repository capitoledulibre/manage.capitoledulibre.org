-- Administrator rights are no longer a property of the account: they come
-- with a session opened through the staff (OIDC) login, and last as long as
-- it does. The same address logged in by e-mail link is a volunteer.
ALTER TABLE sessions ADD COLUMN admin INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users DROP COLUMN is_admin;
