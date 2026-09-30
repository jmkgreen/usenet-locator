CREATE TABLE provider_accounts (
    id TEXT PRIMARY KEY,
    username_secret_ref TEXT NOT NULL,
    password_secret_ref TEXT NOT NULL,
    connection_limit INTEGER NOT NULL CHECK (connection_limit > 0),
    transfer_limit_bytes BIGINT CHECK (transfer_limit_bytes > 0),
    transfer_used_bytes BIGINT NOT NULL DEFAULT 0 CHECK (transfer_used_bytes >= 0),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE nntp_endpoints (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES provider_accounts(id),
    host TEXT NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    tls_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    plaintext_acknowledged BOOLEAN NOT NULL DEFAULT FALSE,
    is_primary BOOLEAN NOT NULL DEFAULT FALSE,
    priority INTEGER NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    CHECK (tls_enabled OR plaintext_acknowledged),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX nntp_endpoints_one_primary ON nntp_endpoints (is_primary) WHERE is_primary;

CREATE TABLE newsgroups (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (name = lower(name))
);

CREATE TABLE articles (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id TEXT NOT NULL UNIQUE,
    subject TEXT,
    author TEXT,
    article_date TIMESTAMPTZ,
    raw_date TEXT,
    references_header TEXT,
    byte_count BIGINT CHECK (byte_count >= 0),
    line_count BIGINT CHECK (line_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX articles_date_desc ON articles (article_date DESC, id DESC);
CREATE INDEX articles_author_lower ON articles (lower(author));

CREATE TABLE article_newsgroups (
    article_id BIGINT NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    newsgroup_id BIGINT NOT NULL REFERENCES newsgroups(id) ON DELETE CASCADE,
    PRIMARY KEY (article_id, newsgroup_id)
);
CREATE INDEX article_newsgroups_group_article ON article_newsgroups (newsgroup_id, article_id);

CREATE TABLE article_locations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    article_id BIGINT NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    endpoint_id TEXT NOT NULL REFERENCES nntp_endpoints(id),
    newsgroup_id BIGINT NOT NULL REFERENCES newsgroups(id),
    article_number BIGINT NOT NULL CHECK (article_number > 0),
    available BOOLEAN NOT NULL DEFAULT TRUE,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (endpoint_id, newsgroup_id, article_number)
);
CREATE INDEX article_locations_article ON article_locations (article_id);

CREATE TABLE article_bodies (
    article_id BIGINT PRIMARY KEY REFERENCES articles(id) ON DELETE CASCADE,
    decoded_text TEXT NOT NULL,
    mime_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_endpoint_id TEXT REFERENCES nntp_endpoints(id),
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE article_preferences (
    article_id BIGINT PRIMARY KEY REFERENCES articles(id) ON DELETE CASCADE,
    unwanted BOOLEAN NOT NULL DEFAULT FALSE,
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX article_preferences_unwanted ON article_preferences (article_id) WHERE unwanted;

CREATE TYPE index_job_state AS ENUM ('queued', 'running', 'paused', 'interrupted', 'completed', 'failed', 'cancelled');
CREATE TYPE coverage_state AS ENUM ('complete', 'partial', 'failed', 'unavailable');

CREATE TABLE index_jobs (
    id UUID PRIMARY KEY,
    newsgroup_id BIGINT NOT NULL REFERENCES newsgroups(id),
    endpoint_id TEXT NOT NULL REFERENCES nntp_endpoints(id),
    requested_start_date DATE NOT NULL,
    requested_end_date DATE NOT NULL,
    margin_days INTEGER NOT NULL DEFAULT 2 CHECK (margin_days >= 0),
    state index_job_state NOT NULL DEFAULT 'queued',
    headers_retrieved BIGINT NOT NULL DEFAULT 0 CHECK (headers_retrieved >= 0),
    articles_stored BIGINT NOT NULL DEFAULT 0 CHECK (articles_stored >= 0),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CHECK (requested_start_date <= requested_end_date)
);
CREATE INDEX index_jobs_dispatch ON index_jobs (state, created_at) WHERE state IN ('queued', 'running');

CREATE TABLE job_checkpoints (
    job_id UUID PRIMARY KEY REFERENCES index_jobs(id) ON DELETE CASCADE,
    discovery_low BIGINT,
    discovery_high BIGINT,
    next_article_number BIGINT,
    last_committed_article_number BIGINT,
    current_article_date TIMESTAMPTZ,
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE scan_coverage (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES nntp_endpoints(id),
    newsgroup_id BIGINT NOT NULL REFERENCES newsgroups(id),
    job_id UUID REFERENCES index_jobs(id) ON DELETE SET NULL,
    article_number_start BIGINT NOT NULL CHECK (article_number_start > 0),
    article_number_end BIGINT NOT NULL CHECK (article_number_end >= article_number_start),
    observed_start_date TIMESTAMPTZ,
    observed_end_date TIMESTAMPTZ,
    state coverage_state NOT NULL,
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX scan_coverage_endpoint_group_range ON scan_coverage (endpoint_id, newsgroup_id, article_number_start, article_number_end);
