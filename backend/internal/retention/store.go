package retention

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/archive"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type PGStore struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) PGStore { return PGStore{pool: pool} }

func (s PGStore) CheckJobRunning(ctx context.Context, jobID string) error {
	var running bool
	if err := s.pool.QueryRow(ctx, `SELECT state = 'running' FROM index_jobs WHERE id = $1`, jobID).Scan(&running); err != nil {
		return fmt.Errorf("check discovery job: %w", err)
	}
	if !running {
		return fmt.Errorf("discovery job is no longer running")
	}
	return nil
}

func (s PGStore) Record(ctx context.Context, observation Observation) (Observation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("begin retention record: %w", err)
	}
	defer tx.Rollback(ctx)
	if observation.JobID != "" {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM index_jobs WHERE id = $1 FOR UPDATE`, observation.JobID).Scan(&state); err != nil {
			return Observation{}, fmt.Errorf("read discovery job: %w", err)
		}
		if state != "running" {
			return Observation{}, fmt.Errorf("discovery job is no longer running")
		}
	}
	var groupID int64
	if err := tx.QueryRow(ctx, `INSERT INTO newsgroups (name) VALUES (lower($1)) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, observation.Newsgroup).Scan(&groupID); err != nil {
		return Observation{}, fmt.Errorf("upsert retention group: %w", err)
	}
	if observation.Article != nil && validMessageID(observation.Article.MessageID) {
		articleID, err := archive.UpsertOverview(ctx, tx, *observation.Article, observation.Endpoint, groupID)
		if err != nil {
			return Observation{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO article_newsgroups (article_id, newsgroup_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, articleID, groupID); err != nil {
			return Observation{}, fmt.Errorf("upsert retention membership: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO article_locations (article_id, endpoint_id, newsgroup_id, article_number) VALUES ($1, $2, $3, $4)
            ON CONFLICT (endpoint_id, newsgroup_id, article_number) DO UPDATE SET article_id = EXCLUDED.article_id, available = TRUE, last_seen_at = now()`, articleID, observation.Endpoint, groupID, observation.Article.ArticleNumber); err != nil {
			return Observation{}, fmt.Errorf("upsert retention location: %w", err)
		}
		observation.ArticleID = &articleID
	}
	var low, high any
	if observation.GroupLow > 0 {
		low = observation.GroupLow
	}
	if observation.GroupHigh > 0 {
		high = observation.GroupHigh
	}
	var observedAt time.Time
	if err := tx.QueryRow(ctx, `INSERT INTO retention_observations (endpoint_id, newsgroup_id, group_low, group_high, article_number, article_id, observed_date, outcome, job_id)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::uuid) RETURNING observed_at`, observation.Endpoint, groupID, low, high, observation.ArticleNumber, observation.ArticleID, observation.ObservedDate, observation.Outcome, observation.JobID).Scan(&observedAt); err != nil {
		return Observation{}, fmt.Errorf("insert retention observation: %w", err)
	}
	if observation.JobID != "" {
		state, reason := "completed", ""
		switch observation.Outcome {
		case "found", "undated", "no_header_in_probe":
		default:
			state, reason = "failed", observation.Outcome
		}
		var count int64
		if observation.ArticleID != nil {
			count = 1
		}
		if _, err := tx.Exec(ctx, `UPDATE index_jobs SET state=$2::index_job_state, headers_retrieved=$3, articles_stored=$3, transfer_used_bytes=$4, last_error=NULLIF($5,''), completed_at=now(), updated_at=now() WHERE id=$1`, observation.JobID, state, count, observation.TransferBytes, reason); err != nil {
			return Observation{}, fmt.Errorf("finish discovery job: %w", err)
		}
	}
	if observation.ArticleNumber != nil && observation.GroupHigh > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO retention_cursors (endpoint_id, newsgroup_id, next_article_number, group_high)
            VALUES ($1, $2, $3, $4) ON CONFLICT (endpoint_id, newsgroup_id) DO UPDATE
            SET next_article_number = CASE WHEN EXCLUDED.group_high < retention_cursors.next_article_number - 1 THEN EXCLUDED.next_article_number ELSE GREATEST(retention_cursors.next_article_number, EXCLUDED.next_article_number) END, group_high = EXCLUDED.group_high, updated_at = now()`, observation.Endpoint, groupID, *observation.ArticleNumber+1, observation.GroupHigh); err != nil {
			return Observation{}, fmt.Errorf("set retention cursor: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Observation{}, fmt.Errorf("commit retention record: %w", err)
	}
	observation.ObservedAt = observedAt
	observation.Article = nil
	return observation, nil
}

func (s PGStore) Cursor(ctx context.Context, endpoint, group string) (int64, int64, bool, error) {
	var next, high int64
	err := s.pool.QueryRow(ctx, `SELECT c.next_article_number, c.group_high FROM retention_cursors c JOIN newsgroups g ON g.id = c.newsgroup_id WHERE c.endpoint_id = $1 AND g.name = lower($2)`, endpoint, group).Scan(&next, &high)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, 0, false, nil
		}
		return 0, 0, false, fmt.Errorf("get retention cursor: %w", err)
	}
	return next, high, true, nil
}

func (s PGStore) StoreHeaders(ctx context.Context, endpoint, group string, headers []nntp.Overview, next, high int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin retained headers: %w", err)
	}
	defer tx.Rollback(ctx)
	var groupID int64
	if err := tx.QueryRow(ctx, `INSERT INTO newsgroups (name) VALUES (lower($1)) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, group).Scan(&groupID); err != nil {
		return fmt.Errorf("upsert retained-header group: %w", err)
	}
	for i := range headers {
		item := &headers[i]
		if !validMessageID(item.MessageID) {
			if err := archive.QuarantineOverview(ctx, tx, *item, endpoint, groupID); err != nil {
				return err
			}
			continue
		}
		articleID, err := archive.UpsertOverview(ctx, tx, *item, endpoint, groupID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO article_newsgroups (article_id, newsgroup_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, articleID, groupID); err != nil {
			return fmt.Errorf("upsert retained-header membership: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO article_locations (article_id, endpoint_id, newsgroup_id, article_number) VALUES ($1, $2, $3, $4)
            ON CONFLICT (endpoint_id, newsgroup_id, article_number) DO UPDATE SET article_id = EXCLUDED.article_id, available = TRUE, last_seen_at = now()`, articleID, endpoint, groupID, item.ArticleNumber); err != nil {
			return fmt.Errorf("upsert retained-header location: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO retention_cursors (endpoint_id, newsgroup_id, next_article_number, group_high) VALUES ($1, $2, $3, $4)
        ON CONFLICT (endpoint_id, newsgroup_id) DO UPDATE SET next_article_number = EXCLUDED.next_article_number, group_high = EXCLUDED.group_high, updated_at = now()`, endpoint, groupID, next, high); err != nil {
		return fmt.Errorf("advance retention cursor: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit retained headers: %w", err)
	}
	return nil
}

func (s PGStore) ListLatest(ctx context.Context, group string) ([]Observation, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (r.endpoint_id) r.endpoint_id, g.name, r.group_low, r.group_high, r.article_number, r.article_id, r.observed_date, r.outcome, r.observed_at, c.next_article_number, c.group_high
        FROM retention_observations r JOIN newsgroups g ON g.id = r.newsgroup_id LEFT JOIN retention_cursors c ON c.endpoint_id = r.endpoint_id AND c.newsgroup_id = r.newsgroup_id WHERE g.name = lower($1)
        ORDER BY r.endpoint_id, r.observed_at DESC`, group)
	if err != nil {
		return nil, fmt.Errorf("list retention observations: %w", err)
	}
	defer rows.Close()
	var observations []Observation
	for rows.Next() {
		var item Observation
		var low, high *int64
		if err := rows.Scan(&item.Endpoint, &item.Newsgroup, &low, &high, &item.ArticleNumber, &item.ArticleID, &item.ObservedDate, &item.Outcome, &item.ObservedAt, &item.NextArticle, &item.CursorHigh); err != nil {
			return nil, fmt.Errorf("read retention observation: %w", err)
		}
		if low != nil {
			item.GroupLow = *low
		}
		if high != nil {
			item.GroupHigh = *high
		}
		observations = append(observations, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list retention observations: %w", err)
	}
	return observations, nil
}

func validMessageID(value string) bool {
	return utf8.ValidString(value) && strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") && strings.Contains(value, "@") && !strings.ContainsAny(value, " \t\r\n\x00")
}
