-- A pole manager may let the volunteers of their poles see their phone
-- number, to be reached on the day. Off by default: the profile promises a
-- phone number is visible to managers only.
ALTER TABLE users ADD COLUMN share_phone INTEGER NOT NULL DEFAULT 0;
