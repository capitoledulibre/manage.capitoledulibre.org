-- Medal color of a level, as #rrggbb. Empty means a default derived from
-- the level name.
ALTER TABLE levels ADD COLUMN color TEXT NOT NULL DEFAULT '';
