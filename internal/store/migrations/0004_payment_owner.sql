-- Payment instructions shown to partners next to their invoice.
ALTER TABLE events ADD COLUMN payment_info TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN payment_mention TEXT NOT NULL DEFAULT '';
-- Staff member in charge of the partnership.
ALTER TABLE partnerships ADD COLUMN owner_admin_id INTEGER REFERENCES admins(id) ON DELETE SET NULL;
