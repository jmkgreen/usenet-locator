CREATE TABLE retention_observations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES nntp_endpoints(id),
    newsgroup_id BIGINT NOT NULL REFERENCES newsgroups(id) ON DELETE CASCADE,
    group_low BIGINT CHECK (group_low > 0),
    group_high BIGINT CHECK (group_high >= group_low),
    article_number BIGINT CHECK (article_number > 0),
    article_id BIGINT REFERENCES articles(id) ON DELETE SET NULL,
    observed_date TIMESTAMPTZ,
    outcome TEXT NOT NULL CHECK (outcome IN ('found', 'undated', 'group_unavailable', 'connection_failed', 'authentication_failed', 'capability_failed', 'reader_mode_failed', 'overview_failed', 'quota_reached')),
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX retention_observations_group_endpoint_observed ON retention_observations (newsgroup_id, endpoint_id, observed_at DESC);
