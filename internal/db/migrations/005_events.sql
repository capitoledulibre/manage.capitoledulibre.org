-- "Edition" becomes "event" in the model, and events and poles get URL slugs.
ALTER TABLE editions RENAME TO events;
ALTER TABLE poles RENAME COLUMN edition_id TO event_id;
ALTER TABLE messages RENAME COLUMN edition_id TO event_id;
ALTER TABLE events ADD COLUMN slug TEXT NOT NULL DEFAULT '';
ALTER TABLE poles ADD COLUMN slug TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX events_slug ON events(slug) WHERE slug != '';
CREATE UNIQUE INDEX poles_event_slug ON poles(event_id, slug) WHERE slug != '';
