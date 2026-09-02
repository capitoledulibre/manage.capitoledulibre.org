-- Poles get an explicit access mode and a display icon.
--   public      anyone registered can join
--   private     join through an invitation link carrying a generated key
--   invitation  managers and administrators add members themselves
--   restricted  only administrators add members
ALTER TABLE poles ADD COLUMN access_mode TEXT NOT NULL DEFAULT 'public';
ALTER TABLE poles ADD COLUMN icon TEXT NOT NULL DEFAULT '';
UPDATE poles SET access_mode = 'private' WHERE access_key IS NOT NULL AND access_key != '';
