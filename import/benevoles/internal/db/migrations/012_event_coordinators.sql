-- The per-event role is called coordination: "staff" means the site-wide
-- administrators, as in the partners dashboard.
ALTER TABLE event_staff RENAME TO event_coordinators;
DROP INDEX event_staff_user;
CREATE INDEX event_coordinators_user ON event_coordinators(user_id);
