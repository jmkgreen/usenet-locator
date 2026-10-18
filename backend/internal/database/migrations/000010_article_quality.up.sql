-- Existing records predate this pipeline; NULL means their source was not
-- captured and must not be represented as a versioned normalization.
ALTER TABLE articles ADD COLUMN source_overview JSONB;
ALTER TABLE articles ADD COLUMN source_endpoint_id TEXT REFERENCES nntp_endpoints(id);
ALTER TABLE articles ADD COLUMN source_newsgroup_id BIGINT REFERENCES newsgroups(id);
ALTER TABLE articles ADD COLUMN source_article_number BIGINT;
ALTER TABLE articles ADD COLUMN normalizer_version INTEGER;
ALTER TABLE articles ADD COLUMN normalized_subject TEXT;
ALTER TABLE articles ADD COLUMN quality_flags TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE article_bodies ADD COLUMN raw_headers BYTEA;
ALTER TABLE article_bodies ADD COLUMN raw_body BYTEA;
ALTER TABLE article_bodies ADD COLUMN sanitized_html TEXT;
ALTER TABLE article_bodies ADD COLUMN normalizer_version INTEGER;
ALTER TABLE article_bodies ADD COLUMN quality_flags TEXT[] NOT NULL DEFAULT '{}';

-- Preserve previous identities when a provider reuses an article number.
CREATE TABLE article_location_conflicts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES nntp_endpoints(id),
    newsgroup_id BIGINT NOT NULL REFERENCES newsgroups(id),
    article_number BIGINT NOT NULL,
    previous_article_id BIGINT NOT NULL REFERENCES articles(id),
    observed_article_id BIGINT NOT NULL REFERENCES articles(id),
    observed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE FUNCTION record_article_location_conflict() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.article_id <> NEW.article_id THEN
        INSERT INTO article_location_conflicts (endpoint_id, newsgroup_id, article_number, previous_article_id, observed_article_id)
        VALUES (OLD.endpoint_id, OLD.newsgroup_id, OLD.article_number, OLD.article_id, NEW.article_id);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER article_location_identity_change BEFORE UPDATE OF article_id ON article_locations
    FOR EACH ROW EXECUTE FUNCTION record_article_location_conflict();

-- Keep malformed identities outside the canonical index while preserving the
-- provider's exact overview bytes for later inspection and repair.
CREATE TABLE quarantined_overviews (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES nntp_endpoints(id),
    newsgroup_id BIGINT NOT NULL REFERENCES newsgroups(id),
    article_number BIGINT NOT NULL CHECK (article_number > 0),
    source_overview JSONB NOT NULL,
    reason TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (endpoint_id, newsgroup_id, article_number)
);
