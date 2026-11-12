-- A pole's locations become its roles (welcome desk, bar...): the slots
-- named their role in their title, which becomes the detail. Each title of
-- a slot with no location becomes a location of its pole, or the one
-- already named so, and the slot moves to it, its title cleared. A slot
-- that already has a location keeps it, and its title.
INSERT INTO locations (uid, pole_id, name, position)
SELECT
    lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
        substr(lower(hex(randomblob(2))), 2) || '-' ||
        substr('89ab', 1 + abs(random()) % 4, 1) || substr(lower(hex(randomblob(2))), 2) || '-' ||
        lower(hex(randomblob(6))),
    t.pole_id,
    t.title,
    (SELECT COALESCE(MAX(position), 0) FROM locations l WHERE l.pole_id = t.pole_id) + t.rank
FROM (
    SELECT pole_id, title, ROW_NUMBER() OVER (PARTITION BY pole_id ORDER BY MIN(starts_at), title) AS rank
    FROM slots s
    WHERE location_id IS NULL AND title <> ''
      AND NOT EXISTS (SELECT 1 FROM locations l WHERE l.pole_id = s.pole_id AND l.name = s.title)
    GROUP BY pole_id, title
) t;

UPDATE slots
SET location_id = (SELECT MIN(id) FROM locations l WHERE l.pole_id = slots.pole_id AND l.name = slots.title),
    title = ''
WHERE location_id IS NULL AND title <> '';
