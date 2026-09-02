-- Optional uploaded image for a pole (file name inside the upload directory).
ALTER TABLE poles ADD COLUMN image TEXT NOT NULL DEFAULT '';
