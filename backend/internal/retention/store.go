package retention

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type PGStore struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) PGStore { return PGStore{pool: pool} }

func (s PGStore) Record(ctx context.Context, observation Observation) (Observation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("begin retention record: %w", err)
	}
	defer tx.Rollback(ctx)
	var groupID int64
	if err := tx.QueryRow(ctx, `INSERT INTO newsgroups (name) VALUES (lower($1)) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, observation.Newsgroup).Scan(&groupID); err != nil {
		return Observation{}, fmt.Errorf("upsert retention group: %w", err)
	}
	if observation.Article != nil && validMessageID(observation.Article.MessageID) {
		articleID, err := upsertArticle(ctx, tx, observation.Article)
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
	if err := tx.QueryRow(ctx, `INSERT INTO retention_observations (endpoint_id, newsgroup_id, group_low, group_high, article_number, article_id, observed_date, outcome)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING observed_at`, observation.Endpoint, groupID, low, high, observation.ArticleNumber, observation.ArticleID, observation.ObservedDate, observation.Outcome).Scan(&observedAt); err != nil {
		return Observation{}, fmt.Errorf("insert retention observation: %w", err)
	}
	if observation.ArticleNumber != nil && observation.GroupHigh > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO retention_cursors (endpoint_id, newsgroup_id, next_article_number, group_high)
            VALUES ($1, $2, $3, $4) ON CONFLICT (endpoint_id, newsgroup_id) DO UPDATE
            SET next_article_number = EXCLUDED.next_article_number, group_high = EXCLUDED.group_high, updated_at = now()`, observation.Endpoint, groupID, *observation.ArticleNumber+1, observation.GroupHigh); err != nil {
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
			continue
		}
		articleID, err := upsertArticle(ctx, tx, item)
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
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (r.endpoint_id) r.endpoint_id, g.name, r.group_low, r.group_high, r.article_number, r.article_id, r.observed_date, r.outcome, r.observed_at
        FROM retention_observations r JOIN newsgroups g ON g.id = r.newsgroup_id WHERE g.name = lower($1)
        ORDER BY r.endpoint_id, r.observed_at DESC`, group)
	if err != nil {
		return nil, fmt.Errorf("list retention observations: %w", err)
	}
	defer rows.Close()
	var observations []Observation
	for rows.Next() {
		var item Observation
		var low, high *int64
		if err := rows.Scan(&item.Endpoint, &item.Newsgroup, &low, &high, &item.ArticleNumber, &item.ArticleID, &item.ObservedDate, &item.Outcome, &item.ObservedAt); err != nil {
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
	return strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") && strings.Contains(value, "@") && !strings.ContainsAny(value, " \t\r\n")
}

func upsertArticle(ctx context.Context, tx pgx.Tx, overview *nntp.Overview) (int64, error) {
	var articleID int64
	var date *time.Time
	if !overview.Date.IsZero() {
		value := overview.Date.UTC()
		date = &value
	}
	err := tx.QueryRow(ctx, `INSERT INTO articles (message_id, subject, author, article_date, raw_date, references_header, byte_count, line_count)
        VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, 0), NULLIF($8, 0))
        ON CONFLICT (message_id) DO UPDATE SET updated_at = now() RETURNING id`, overview.MessageID, overview.Subject, overview.Author, date, overview.RawDate, overview.References, overview.Bytes, overview.Lines).Scan(&articleID)
	if err != nil {
		return 0, fmt.Errorf("upsert retention article: %w", err)
	}
	return articleID, nil
}
