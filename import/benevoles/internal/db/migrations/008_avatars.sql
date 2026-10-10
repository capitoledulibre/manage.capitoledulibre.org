-- Profile picture of a volunteer. avatar is a file name inside the upload
-- directory; avatar_source records where the picture came from, so that a
-- photo the volunteer uploaded is never replaced by the Gravatar fallback and
-- so that "remove my picture" is remembered instead of being undone by the
-- next lookup:
--   ''         nothing yet, the Gravatar fallback may run
--   'upload'   the volunteer uploaded a photo
--   'gravatar' fetched from gravatar.com and cached in the upload directory
--   'none'     the volunteer asked for no picture at all
ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar_source TEXT NOT NULL DEFAULT '';
-- Last time gravatar.com was asked about this address, hit or miss, so that
-- browsing the planning never triggers a second lookup.
ALTER TABLE users ADD COLUMN avatar_checked_at TEXT;
