-- manage:foreign-keys-off
-- One person per e-mail address, whatever they come for: a partner's
-- contact, a staff member and, soon, a volunteer. The address, name,
-- language, avatar and last login move to people; managers (a person in
-- charge of an organization) and admins (a person who logs in as staff) keep
-- their own ids, so partnerships.owner_admin_id and the sessions still point
-- at the same rows.

CREATE TABLE people (
    id            INTEGER PRIMARY KEY,
    uid           TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL DEFAULT '',
    lang          TEXT NOT NULL DEFAULT 'fr',
    avatar_file   TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL,
    last_login_at DATETIME
);

-- A staff name wins over a contact's, and among contacts the most recently
-- active one gives the name and the language.
INSERT INTO people (uid, email, name, lang, avatar_file, created_at, last_login_at)
SELECT lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6))),
       e.email,
       COALESCE((SELECT a.name FROM admins a WHERE a.email = e.email AND a.name != ''),
                (SELECT m.name FROM managers m WHERE m.email = e.email AND m.name != ''
                 ORDER BY m.last_login_at IS NULL, m.last_login_at DESC, m.created_at DESC LIMIT 1),
                ''),
       COALESCE((SELECT m.lang FROM managers m WHERE m.email = e.email
                 ORDER BY m.last_login_at IS NULL, m.last_login_at DESC, m.created_at DESC LIMIT 1),
                'fr'),
       COALESCE((SELECT a.avatar_file FROM admins a WHERE a.email = e.email), ''),
       (SELECT MIN(t) FROM (SELECT created_at AS t FROM admins WHERE email = e.email
                            UNION ALL SELECT created_at FROM managers WHERE email = e.email)),
       (SELECT MAX(t) FROM (SELECT last_login_at AS t FROM admins WHERE email = e.email
                            UNION ALL SELECT last_login_at FROM managers WHERE email = e.email))
FROM (SELECT email FROM admins UNION SELECT email FROM managers) AS e;

CREATE TABLE managers_new (
    id              INTEGER PRIMARY KEY,
    uid             TEXT NOT NULL UNIQUE,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    person_id       INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    invited_by      TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL,
    UNIQUE (organization_id, person_id)
);
INSERT INTO managers_new (id, uid, organization_id, person_id, invited_by, created_at)
SELECT m.id, m.uid, m.organization_id, p.id, m.invited_by, m.created_at
FROM managers m JOIN people p ON p.email = m.email;
DROP TABLE managers;
ALTER TABLE managers_new RENAME TO managers;
CREATE INDEX managers_person ON managers(person_id);

CREATE TABLE admins_new (
    id         INTEGER PRIMARY KEY,
    uid        TEXT NOT NULL UNIQUE,
    person_id  INTEGER NOT NULL UNIQUE REFERENCES people(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL
);
INSERT INTO admins_new (id, uid, person_id, created_at)
SELECT a.id, a.uid, p.id, a.created_at
FROM admins a JOIN people p ON p.email = a.email;
DROP TABLE admins;
ALTER TABLE admins_new RENAME TO admins;
