-- One events table for every module. The volunteer side adds its own
-- lifecycle, separate from is_current (the edition the partner pipeline is
-- working on):
--   volunteer_status: draft (volunteers do not see it), open (signups
--   open), frozen (visible, signups closed) or archived (hidden);
--   volunteer_freeze_at: optional moment signups close on their own.
-- logo_file is the edition's own logo, shown next to its name.
-- Existing editions start as drafts: nothing appears to volunteers until the
-- staff open signups.
ALTER TABLE events ADD COLUMN volunteer_status TEXT NOT NULL DEFAULT 'draft';
ALTER TABLE events ADD COLUMN volunteer_freeze_at DATETIME;
ALTER TABLE events ADD COLUMN logo_file TEXT NOT NULL DEFAULT '';
