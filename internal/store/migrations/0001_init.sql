CREATE TABLE events (
    id                 INTEGER PRIMARY KEY,
    slug               TEXT NOT NULL UNIQUE,
    name               TEXT NOT NULL,
    year               INTEGER NOT NULL,
    starts_on          DATETIME,
    ends_on            DATETIME,
    is_current         BOOLEAN NOT NULL DEFAULT 0,
    logo_deadline      DATETIME,
    attendees_deadline DATETIME,
    brochure_fr_file   TEXT NOT NULL DEFAULT '',
    brochure_en_file   TEXT NOT NULL DEFAULT '',
    guide_fr_file      TEXT NOT NULL DEFAULT '',
    guide_en_file      TEXT NOT NULL DEFAULT '',
    created_at         DATETIME NOT NULL
);

CREATE TABLE levels (
    id            INTEGER PRIMARY KEY,
    event_id      INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    position      INTEGER NOT NULL DEFAULT 0,
    price         INTEGER NOT NULL DEFAULT 0,
    max_partners  INTEGER NOT NULL DEFAULT 0,
    max_attendees INTEGER NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL
);
CREATE INDEX levels_event ON levels(event_id);

CREATE TABLE perks (
    id       INTEGER PRIMARY KEY,
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
    logo_file      TEXT NOT NULL DEFAULT '',
    logo_dark_file TEXT NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    updated_at     DATETIME NOT NULL
);

CREATE TABLE logos (
    id              INTEGER PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    variant         TEXT NOT NULL,
    file            TEXT NOT NULL,
    uploaded_by     TEXT NOT NULL,
    uploaded_at     DATETIME NOT NULL
);
CREATE INDEX logos_org ON logos(organization_id);

CREATE TABLE managers (
    id              INTEGER PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           TEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    lang            TEXT NOT NULL DEFAULT 'fr',
    invited_by      TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL,
    last_login_at   DATETIME,
    UNIQUE (organization_id, email)
);
CREATE INDEX managers_email ON managers(email);

CREATE TABLE partnerships (
    id              INTEGER PRIMARY KEY,
    organization_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    event_id        INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    level_id        INTEGER REFERENCES levels(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'prospect',
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

CREATE TABLE admins (
    id            INTEGER PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL,
    last_login_at DATETIME
);

CREATE TABLE magic_tokens (
    token_hash TEXT PRIMARY KEY,
    email      TEXT NOT NULL,
    expires_at DATETIME NOT NULL,
    used_at    DATETIME,
    created_at DATETIME NOT NULL
);
CREATE INDEX magic_tokens_email ON magic_tokens(email, created_at);

CREATE TABLE sessions (
    token_hash         TEXT PRIMARY KEY,
    kind               TEXT NOT NULL,
    manager_id         INTEGER REFERENCES managers(id) ON DELETE CASCADE,
    admin_id           INTEGER REFERENCES admins(id) ON DELETE CASCADE,
    impersonate_org_id INTEGER REFERENCES organizations(id) ON DELETE SET NULL,
    expires_at         DATETIME NOT NULL,
    created_at         DATETIME NOT NULL
);

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

CREATE TABLE reminders_sent (
    partnership_id INTEGER NOT NULL REFERENCES partnerships(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    sent_at        DATETIME NOT NULL,
    PRIMARY KEY (partnership_id, kind)
);
