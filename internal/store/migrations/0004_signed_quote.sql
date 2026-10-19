-- The quote as the partner sent it back signed, from their space.
ALTER TABLE partnerships ADD COLUMN signed_quote_file TEXT NOT NULL DEFAULT '';
