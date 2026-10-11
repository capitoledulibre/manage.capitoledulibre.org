-- When the personal data of an edition was purged (see Store.PurgeEvent).
ALTER TABLE events ADD COLUMN purged_at DATETIME;
