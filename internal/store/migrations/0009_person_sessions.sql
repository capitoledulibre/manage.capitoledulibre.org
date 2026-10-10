-- A session now belongs to a person: one login, whatever the person comes
-- for. In a person session, manager_id is the partner organization being
-- looked at, if any; losing it (the contact removed, or moved to another
-- address) leaves the person logged in, only without that organization.
-- Staff sessions keep their admin_id. Sessions are short-lived, so the
-- existing ones are dropped: everyone logs in once more.
DROP TABLE sessions;

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
