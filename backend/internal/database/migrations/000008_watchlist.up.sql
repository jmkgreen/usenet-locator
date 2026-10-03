CREATE TABLE newsgroup_watchlist (
    newsgroup_id BIGINT PRIMARY KEY REFERENCES newsgroups(id) ON DELETE CASCADE,
    interval_hours INTEGER NOT NULL DEFAULT 24 CHECK (interval_hours BETWEEN 1 AND 168),
    last_checked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX newsgroup_watchlist_due ON newsgroup_watchlist (last_checked_at NULLS FIRST);
