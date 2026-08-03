-- Webhook-triggered work has no interactive user session. Keep the creator
-- nullable while preserving UUID attribution for operator-created tasks.
ALTER TABLE tasks ALTER COLUMN created_by DROP NOT NULL;
