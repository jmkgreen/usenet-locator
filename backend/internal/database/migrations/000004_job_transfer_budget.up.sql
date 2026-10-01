ALTER TABLE index_jobs ADD COLUMN transfer_limit_bytes BIGINT CHECK (transfer_limit_bytes > 0);
ALTER TABLE index_jobs ADD COLUMN transfer_used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (transfer_used_bytes >= 0);
