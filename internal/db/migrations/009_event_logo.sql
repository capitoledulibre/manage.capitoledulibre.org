-- Optional logo of one edition (file name inside the upload directory), shown
-- next to its name.
ALTER TABLE events ADD COLUMN image TEXT NOT NULL DEFAULT '';
