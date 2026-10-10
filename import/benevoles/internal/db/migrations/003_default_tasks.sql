-- Default checklist of a pole, one task per line, pre-filled on every new slot.
ALTER TABLE poles ADD COLUMN default_tasks TEXT NOT NULL DEFAULT '';
