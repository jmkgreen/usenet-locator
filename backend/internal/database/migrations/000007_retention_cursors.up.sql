CREATE TABLE retention_cursors (
    endpoint_id TEXT NOT NULL REFERENCES nntp_endpoints(id),
    newsgroup_id BIGINT NOT NULL REFERENCES newsgroups(id) ON DELETE CASCADE,
    next_article_number BIGINT NOT NULL CHECK (next_article_number > 0),
    group_high BIGINT NOT NULL CHECK (group_high >= next_article_number - 1),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (endpoint_id, newsgroup_id)
);
