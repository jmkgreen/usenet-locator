CREATE TABLE endpoint_qualifications (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES nntp_endpoints(id) ON DELETE CASCADE,
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    overview_format_code INTEGER NOT NULL,
    overview_fields JSONB NOT NULL DEFAULT '[]'::jsonb,
    stat_code INTEGER NOT NULL DEFAULT 0,
    group_selected BOOLEAN NOT NULL DEFAULT FALSE,
    overview_code INTEGER NOT NULL DEFAULT 0,
    overview_rows INTEGER NOT NULL DEFAULT 0 CHECK (overview_rows >= 0),
    overview_dates INTEGER NOT NULL DEFAULT 0 CHECK (overview_dates >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX endpoint_qualifications_history ON endpoint_qualifications (endpoint_id, created_at DESC);
