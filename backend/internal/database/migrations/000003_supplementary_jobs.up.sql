ALTER TABLE index_jobs ADD COLUMN scan_reason TEXT NOT NULL DEFAULT 'operator_requested'
    CHECK (scan_reason IN ('operator_requested', 'missing_range', 'failed_batch'));
ALTER TABLE index_jobs ADD COLUMN source_job_id UUID REFERENCES index_jobs(id) ON DELETE SET NULL;
CREATE INDEX index_jobs_source_job ON index_jobs (source_job_id) WHERE source_job_id IS NOT NULL;
