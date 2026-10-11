-- The schema of manage. Integer ids stay internal (foreign keys); URLs and
-- forms use slugs (editions, organizations, poles) or random UUIDs (uid), so
-- they reveal no sequence that could be guessed.

-- People -------------------------------------------------------------------

-- One person per e-mail address, whatever they come for: a partner's
-- contact, a staff member, a volunteer.
CREATE TABLE people (
    id                INTEGER PRIMARY KEY,
    uid               TEXT NOT NULL UNIQUE,
    email             TEXT NOT NULL UNIQUE,
    name              TEXT NOT NULL DEFAULT '',
    lang              TEXT NOT NULL DEFAULT 'fr',
    phone             TEXT NOT NULL DEFAULT '',
    -- A pole manager may show their phone to the volunteers of their poles.
    share_phone       BOOLEAN NOT NULL DEFAULT 0,
    -- Secret of the calendar subscription feed, created on first use.
    calendar_token    TEXT,
    avatar_file       TEXT NOT NULL DEFAULT '',
    -- Where avatar_file came from: '' (no picture yet), 'upload',
    -- 'gravatar' (on the person's request) or 'none' (they want none).
    avatar_source     TEXT NOT NULL DEFAULT '',
    avatar_checked_at DATETIME,
    created_at        DATETIME NOT NULL,
    last_login_at     DATETIME
);
CREATE UNIQUE INDEX people_calendar_token ON people(calendar_token) WHERE calendar_token IS NOT NULL;

-- A person who logs in as staff, through the single sign-on.
CREATE TABLE admins (
    id         INTEGER PRIMARY KEY,
    uid        TEXT NOT NULL UNIQUE,
    person_id  INTEGER NOT NULL UNIQUE REFERENCES people(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL
);

CREATE TABLE magic_tokens (
    token_hash TEXT PRIMARY KEY,
    email      TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    used_at    DATETIME,
    created_at DATETIME NOT NULL
);
CREATE INDEX magic_tokens_email ON magic_tokens(email, created_at);

-- Editions -----------------------------------------------------------------

-- An edition of the event, shared by every module. is_current is the
-- edition the partner pipeline works on. The volunteer side has its own
-- lifecycle: volunteer_status is draft (volunteers do not see it), open
-- (sign-ups open), frozen (visible, sign-ups closed) or archived (hidden);
-- volunteer_freeze_at closes sign-ups on its own.
CREATE TABLE events (
    id                  INTEGER PRIMARY KEY,
    slug                TEXT NOT NULL UNIQUE,
    name                TEXT NOT NULL,
    year                INTEGER NOT NULL,
    starts_on           DATETIME,
    ends_on             DATETIME,
    is_current          BOOLEAN NOT NULL DEFAULT 0,
    logo_deadline       DATETIME,
    attendees_deadline  DATETIME,
    brochure_fr_file    TEXT NOT NULL DEFAULT '',
    brochure_en_file    TEXT NOT NULL DEFAULT '',
    guide_fr_file       TEXT NOT NULL DEFAULT '',
    guide_en_file       TEXT NOT NULL DEFAULT '',
    -- Payment instructions and legal mention shown next to an invoice.
    payment_info        TEXT NOT NULL DEFAULT '',
    payment_mention     TEXT NOT NULL DEFAULT '',
    volunteer_status    TEXT NOT NULL DEFAULT 'draft',
    volunteer_freeze_at DATETIME,
    -- The edition's own logo, shown next to its name.
    logo_file           TEXT NOT NULL DEFAULT '',
    -- When its personal data was purged (see Store.PurgeEvent).
    purged_at           DATETIME,
    created_at          DATETIME NOT NULL
);

-- Partners -----------------------------------------------------------------

-- tax_mode: 'ht' (price excluding VAT), 'ttc' (including VAT) or 'none'
-- (not subject to VAT). color is the medal color, #rrggbb, or empty for one
-- derived from the name.
CREATE TABLE levels (
    id            INTEGER PRIMARY KEY,
    uid           TEXT NOT NULL UNIQUE,
    event_id      INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    position      INTEGER NOT NULL DEFAULT 0,
    price         INTEGER NOT NULL DEFAULT 0,
    tax_mode      TEXT NOT NULL DEFAULT 'ht',
    color         TEXT NOT NULL DEFAULT '',
    max_partners  INTEGER NOT NULL DEFAULT 0,
    max_attendees INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL
);
CREATE INDEX levels_event ON levels(event_id);

CREATE TABLE perks (
    id       INTEGER PRIMARY KEY,
    uid      TEXT NOT NULL UNIQUE,
    event_id INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name_fr  TEXT NOT NULL,
    name_en  TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX perks_event ON perks(event_id);

CREATE TABLE level_perks (
    level_id INTEGER NOT NULL REFERENCES levels(id) ON DELETE CASCADE,
    perk_id  INTEGER NOT NULL REFERENCES perks(id) ON DELETE CASCADE,
    PRIMARY KEY (level_id, perk_id)
);

CREATE TABLE organizations (
    id             INTEGER PRIMARY KEY,
    slug           TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    legal_name     TEXT NOT NULL DEFAULT '',
    address_line1  TEXT NOT NULL DEFAULT '',
    address_line2  TEXT NOT NULL DEFAULT '',
    zip_code       TEXT NOT NULL DEFAULT '',
    city           TEXT NOT NULL DEFAULT '',
    country        TEXT NOT NULL DEFAULT '',
    siret          TEXT NOT NULL DEFAULT '',
    vat_number     TEXT NOT NULL DEFAULT '',
    website        TEXT NOT NULL DEFAULT '',
    description_fr TEXT NOT NULL DEFAULT '',
    description_en TEXT NOT NULL DEFAULT '',
    linkedin       TEXT NOT NULL DEFAULT '',
    mastodon       TEXT NOT NULL DEFAULT '',
    bluesky        TEXT NOT NULL DEFAULT '',
    x              TEXT NOT NULL DEFAULT '',
    logo_file      TEXT NOT NULL DEFAULT '',
    logo_dark_file TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    updated_at     DATETIME NOT NULL
);

-- A person in charge of an organization: its partner contacts.
CREATE TABLE managers (
    id              INTEGER PRIMARY KEY,
    uid             TEXT NOT NULL UNIQUE,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    person_id       INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    invited_by      TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL,
    UNIQUE (organization_id, person_id)
);
CREATE INDEX managers_person ON managers(person_id);

-- The logos an organization uploaded, newest kept as current.
CREATE TABLE logos (
    id              INTEGER PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    variant         TEXT NOT NULL,
    file            TEXT NOT NULL,
    uploaded_by     TEXT NOT NULL,
    uploaded_at     DATETIME NOT NULL
);
CREATE INDEX logos_org ON logos(organization_id);

-- An organization's partnership for an edition. price_override is a
-- negotiated price replacing the level's; owner_admin_id the staff member
-- in charge.
CREATE TABLE partnerships (
    id              INTEGER PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    event_id        INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    level_id        INTEGER REFERENCES levels(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'prospect',
    price_override  INTEGER,
    owner_admin_id  INTEGER REFERENCES admins(id) ON DELETE SET NULL,
    po_number       TEXT NOT NULL DEFAULT '',
    quote_file      TEXT NOT NULL DEFAULT '',
    quote_sent_at   DATETIME,
    quote_signed_at DATETIME,
    invoice_file    TEXT NOT NULL DEFAULT '',
    invoice_sent_at DATETIME,
    invoice_paid_at DATETIME,
    partner_notes   TEXT NOT NULL DEFAULT '',
    internal_notes  TEXT NOT NULL DEFAULT '',
    followup_at     DATETIME,
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL,
    UNIQUE (organization_id, event_id)
);
CREATE INDEX partnerships_event ON partnerships(event_id);

CREATE TABLE partnership_perks (
    partnership_id INTEGER NOT NULL REFERENCES partnerships(id) ON DELETE CASCADE,
    perk_id        INTEGER NOT NULL REFERENCES perks(id) ON DELETE CASCADE,
    done_at        DATETIME NOT NULL,
    PRIMARY KEY (partnership_id, perk_id)
);

CREATE TABLE attendees (
    id             INTEGER PRIMARY KEY,
    uid            TEXT NOT NULL UNIQUE,
    partnership_id INTEGER NOT NULL REFERENCES partnerships(id) ON DELETE CASCADE,
    first_name     TEXT NOT NULL,
    last_name      TEXT NOT NULL,
    email          TEXT NOT NULL DEFAULT '',
    role           TEXT NOT NULL DEFAULT 'booth',
    dietary        TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL
);
CREATE INDEX attendees_partnership ON attendees(partnership_id);

CREATE TABLE job_offers (
    id              INTEGER PRIMARY KEY,
    uid             TEXT NOT NULL UNIQUE,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    location        TEXT NOT NULL DEFAULT '',
    contract        TEXT NOT NULL DEFAULT '',
    url             TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    expires_at      DATETIME,
    hidden          BOOLEAN NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL
);
CREATE INDEX job_offers_org ON job_offers(organization_id);

CREATE TABLE social_posts (
    id             INTEGER PRIMARY KEY,
    uid            TEXT NOT NULL UNIQUE,
    partnership_id INTEGER NOT NULL REFERENCES partnerships(id) ON DELETE CASCADE,
    network        TEXT NOT NULL,
    planned_at     DATETIME,
    url            TEXT NOT NULL DEFAULT '',
    note           TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    updated_at     DATETIME NOT NULL
);
CREATE INDEX social_posts_partnership ON social_posts(partnership_id);

-- The change log of an organization and its partnerships.
CREATE TABLE activity (
    id              INTEGER PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    partnership_id  INTEGER REFERENCES partnerships(id) ON DELETE CASCADE,
    actor           TEXT NOT NULL,
    action          TEXT NOT NULL,
    details         TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL
);
CREATE INDEX activity_org ON activity(organization_id, created_at);

-- Reminders already sent to a partnership, so a restart does not send twice.
CREATE TABLE reminders_sent (
    partnership_id INTEGER NOT NULL REFERENCES partnerships(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    sent_at        DATETIME NOT NULL,
    PRIMARY KEY (partnership_id, kind)
);

-- Sessions -----------------------------------------------------------------

-- A session belongs to a person. In a person session, manager_id is the
-- partner organization being looked at, if any; losing it leaves the person
-- logged in, only without that organization. A staff session has its
-- admin_id, and impersonate_org_id while the staff member views a partner's
-- space.
CREATE TABLE sessions (
    token_hash         TEXT PRIMARY KEY,
    kind               TEXT NOT NULL,
    person_id          INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    manager_id         INTEGER REFERENCES managers(id) ON DELETE SET NULL,
    admin_id           INTEGER REFERENCES admins(id) ON DELETE CASCADE,
    impersonate_org_id INTEGER REFERENCES organizations(id) ON DELETE SET NULL,
    expires_at         DATETIME NOT NULL,
    created_at         DATETIME NOT NULL
);
CREATE INDEX sessions_person ON sessions(person_id);

-- Volunteers ---------------------------------------------------------------

-- A pole is a team of an edition. access_mode: public (anyone joins),
-- private (through an invitation link carrying access_key), invitation
-- (managers add members) or restricted (only coordinators and staff add
-- members; the pole is hidden from others). The default pole of each
-- edition holds the shifts of no team in particular, and every volunteer
-- belongs to it.
CREATE TABLE poles (
    id            INTEGER PRIMARY KEY,
    uid           TEXT NOT NULL UNIQUE,
    event_id      INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    slug          TEXT NOT NULL,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    access_mode   TEXT NOT NULL DEFAULT 'public',
    access_key    TEXT NOT NULL DEFAULT '',
    icon          TEXT NOT NULL DEFAULT '',
    image_file    TEXT NOT NULL DEFAULT '',
    default_tasks TEXT NOT NULL DEFAULT '',
    is_default    BOOLEAN NOT NULL DEFAULT 0,
    -- Overrides the edition's volunteer_freeze_at for this pole.
    freeze_at     DATETIME,
    position      INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL,
    UNIQUE (event_id, slug)
);

CREATE TABLE pole_members (
    pole_id   INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    joined_at DATETIME NOT NULL,
    PRIMARY KEY (pole_id, person_id)
);
CREATE INDEX pole_members_person ON pole_members(person_id);

-- Pole managers run a pole; not to be confused with managers, the contacts
-- of a partner organization.
CREATE TABLE pole_managers (
    pole_id   INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (pole_id, person_id)
);
CREATE INDEX pole_managers_person ON pole_managers(person_id);

-- The coordination of an edition runs every pole of it.
CREATE TABLE event_coordinators (
    event_id  INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (event_id, person_id)
);
CREATE INDEX event_coordinators_person ON event_coordinators(person_id);

-- Places of a pole (rooms, desks…), optional on a slot.
CREATE TABLE locations (
    id       INTEGER PRIMARY KEY,
    uid      TEXT NOT NULL UNIQUE,
    pole_id  INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    name     TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX locations_pole ON locations(pole_id);

CREATE TABLE slots (
    id          INTEGER PRIMARY KEY,
    uid         TEXT NOT NULL UNIQUE,
    pole_id     INTEGER NOT NULL REFERENCES poles(id) ON DELETE CASCADE,
    location_id INTEGER REFERENCES locations(id) ON DELETE SET NULL,
    title       TEXT NOT NULL DEFAULT '',
    starts_at   DATETIME NOT NULL,
    ends_at     DATETIME NOT NULL,
    capacity    INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL
);
CREATE INDEX slots_pole ON slots(pole_id, starts_at);

-- The checklist of a slot, ticked on the day.
CREATE TABLE slot_tasks (
    id       INTEGER PRIMARY KEY,
    uid      TEXT NOT NULL UNIQUE,
    slot_id  INTEGER NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    label    TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    done_by  INTEGER REFERENCES people(id) ON DELETE SET NULL,
    done_at  DATETIME
);
CREATE INDEX slot_tasks_slot ON slot_tasks(slot_id);

CREATE TABLE signups (
    slot_id    INTEGER NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    person_id  INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (slot_id, person_id)
);
CREATE INDEX signups_person ON signups(person_id);

-- Volunteers waiting for a place on a full slot, told by e-mail when one
-- frees up (first come, first served).
CREATE TABLE waitlist (
    slot_id    INTEGER NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    person_id  INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (slot_id, person_id)
);
CREATE INDEX waitlist_person ON waitlist(person_id);

-- Notices for the volunteers of an edition (pole_id and slot_id empty), of
-- a pole (pole_id set) or of a slot (slot_id set).
CREATE TABLE messages (
    id         INTEGER PRIMARY KEY,
    uid        TEXT NOT NULL UNIQUE,
    event_id   INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    pole_id    INTEGER REFERENCES poles(id) ON DELETE CASCADE,
    slot_id    INTEGER REFERENCES slots(id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_by INTEGER REFERENCES people(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX messages_event ON messages(event_id);

-- Day-before reminders already sent, so a restart does not send twice.
CREATE TABLE volunteer_reminders (
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    day       TEXT NOT NULL, -- YYYY-MM-DD of the slots covered
    sent_at   DATETIME NOT NULL,
    PRIMARY KEY (person_id, day)
);

-- What happened on the volunteer side of an edition: sign-ups, people added
-- or removed, slots moved or deleted. Names, the pole and the slot are
-- copied into the row, so it still reads once they are gone; a person who
-- deletes their account is wiped from it (see DeletePerson).
CREATE TABLE volunteer_log (
    id           INTEGER PRIMARY KEY,
    event_id     INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    pole_id      INTEGER REFERENCES poles(id) ON DELETE SET NULL,
    actor_id     INTEGER REFERENCES people(id) ON DELETE SET NULL,
    actor_name   TEXT NOT NULL DEFAULT '',
    subject_id   INTEGER REFERENCES people(id) ON DELETE SET NULL,
    subject_name TEXT NOT NULL DEFAULT '',
    action       TEXT NOT NULL,
    pole_name    TEXT NOT NULL DEFAULT '',
    slot_starts  DATETIME,
    slot_ends    DATETIME,
    place        TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    count        INTEGER NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL
);
CREATE INDEX volunteer_log_event ON volunteer_log(event_id, created_at);
CREATE INDEX volunteer_log_pole ON volunteer_log(pole_id, created_at);
CREATE INDEX volunteer_log_actor ON volunteer_log(actor_id);
CREATE INDEX volunteer_log_subject ON volunteer_log(subject_id);
