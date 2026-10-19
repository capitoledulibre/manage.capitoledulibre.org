-- The choices a volunteer saw when they answered: once the coordination
-- changes them, the answer is to be checked again. Answers given so far
-- saw the current ones.
ALTER TABLE volunteer_details ADD COLUMN asked_sizes TEXT NOT NULL DEFAULT '';
ALTER TABLE volunteer_details ADD COLUMN asked_meals TEXT NOT NULL DEFAULT '';
UPDATE volunteer_details SET
    asked_sizes = (SELECT tshirt_sizes FROM events WHERE id = event_id),
    asked_meals = (SELECT meals FROM events WHERE id = event_id);
