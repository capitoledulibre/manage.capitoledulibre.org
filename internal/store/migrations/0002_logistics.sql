-- What an edition provides its volunteers, one item per line: the t-shirt
-- sizes on offer, and the meals. Empty means the edition does not ask.
ALTER TABLE events ADD COLUMN tshirt_sizes TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN meals TEXT NOT NULL DEFAULT '';

-- What a volunteer answers for an edition: their t-shirt size, the meals
-- they take (one per line) and their dietary needs. Kept per edition, and
-- purged with it: dietary needs can tell about health or belief.
CREATE TABLE volunteer_details (
    event_id   INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    person_id  INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    tshirt     TEXT NOT NULL DEFAULT '',
    meals      TEXT NOT NULL DEFAULT '',
    dietary    TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (event_id, person_id)
);
CREATE INDEX volunteer_details_person ON volunteer_details(person_id);
