-- Re-applying NOT NULL is intentionally left to an operator after any
-- system-triggered tasks have been reassigned to a human actor.
ALTER TABLE tasks ALTER COLUMN created_by SET NOT NULL;
