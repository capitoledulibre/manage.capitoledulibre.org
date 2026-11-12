-- The members of a pole who answer for one of its roles: the volunteers on
-- its slots turn to them with a question. Contacts only, with no rights.
CREATE TABLE location_referents (
    location_id INTEGER NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    person_id   INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    PRIMARY KEY (location_id, person_id)
);
CREATE INDEX location_referents_person ON location_referents(person_id);
