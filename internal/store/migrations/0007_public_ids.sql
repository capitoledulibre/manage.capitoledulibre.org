-- Public identifiers. Integer ids stay internal (foreign keys); URLs and
-- forms use slugs (events, organizations) or these random UUIDs, so they do
-- not reveal sequences that could be guessed.

ALTER TABLE levels ADD COLUMN uid TEXT NOT NULL DEFAULT '';
UPDATE levels SET uid = lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)));
CREATE UNIQUE INDEX levels_uid ON levels(uid);

ALTER TABLE perks ADD COLUMN uid TEXT NOT NULL DEFAULT '';
UPDATE perks SET uid = lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)));
CREATE UNIQUE INDEX perks_uid ON perks(uid);

ALTER TABLE managers ADD COLUMN uid TEXT NOT NULL DEFAULT '';
UPDATE managers SET uid = lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)));
CREATE UNIQUE INDEX managers_uid ON managers(uid);

ALTER TABLE attendees ADD COLUMN uid TEXT NOT NULL DEFAULT '';
UPDATE attendees SET uid = lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)));
CREATE UNIQUE INDEX attendees_uid ON attendees(uid);

ALTER TABLE job_offers ADD COLUMN uid TEXT NOT NULL DEFAULT '';
UPDATE job_offers SET uid = lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)));
CREATE UNIQUE INDEX job_offers_uid ON job_offers(uid);

ALTER TABLE social_posts ADD COLUMN uid TEXT NOT NULL DEFAULT '';
UPDATE social_posts SET uid = lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)));
CREATE UNIQUE INDEX social_posts_uid ON social_posts(uid);

ALTER TABLE admins ADD COLUMN uid TEXT NOT NULL DEFAULT '';
UPDATE admins SET uid = lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)));
CREATE UNIQUE INDEX admins_uid ON admins(uid);
