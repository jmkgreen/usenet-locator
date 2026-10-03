ALTER TABLE index_jobs ADD COLUMN parent_job_id UUID REFERENCES index_jobs(id) ON DELETE CASCADE;
ALTER TABLE index_jobs ADD COLUMN is_fanout_parent BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX index_jobs_parent ON index_jobs (parent_job_id) WHERE parent_job_id IS NOT NULL;
