-- When a staff member last logged in as staff: their API tokens carry the
-- staff's rights only while they keep logging in (staff access given by a
-- group is only known at login).
ALTER TABLE admins ADD COLUMN staff_login_at DATETIME;
UPDATE admins SET staff_login_at = created_at;
