-- M10 retention jobs delete by time across tables rather than by parent ID.
CREATE INDEX run_events_retention_idx ON run_events (created_at);
CREATE INDEX automation_runs_retention_idx ON automation_runs (created_at)
    WHERE status IN ('completed', 'failed', 'cancelled');
