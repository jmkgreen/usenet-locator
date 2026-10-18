ALTER TABLE index_jobs ADD COLUMN kind TEXT NOT NULL DEFAULT 'index' CHECK (kind IN ('index', 'retention_discovery'));
ALTER TABLE index_jobs ALTER COLUMN requested_start_date DROP NOT NULL;
ALTER TABLE index_jobs ALTER COLUMN requested_end_date DROP NOT NULL;
ALTER TABLE index_jobs ADD COLUMN started_at TIMESTAMPTZ;
ALTER TABLE retention_observations ADD COLUMN job_id UUID REFERENCES index_jobs(id) ON DELETE SET NULL;
ALTER TABLE index_jobs ADD CONSTRAINT index_jobs_dates_for_kind CHECK (
    (kind = 'index' AND requested_start_date IS NOT NULL AND requested_end_date IS NOT NULL)
    OR (kind = 'retention_discovery' AND requested_start_date IS NULL AND requested_end_date IS NULL)
);
ALTER TABLE retention_observations DROP CONSTRAINT retention_observations_outcome_check;
ALTER TABLE retention_observations ADD CONSTRAINT retention_observations_outcome_check CHECK (outcome IN (
    'found', 'undated', 'no_header_in_probe', 'group_unavailable', 'connection_failed',
    'authentication_failed', 'capability_failed', 'reader_mode_failed', 'overview_failed', 'quota_reached'
));
