// Package indexing persists bounded, endpoint-local NNTP scan batches.
package indexing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/james/usenet-locator/backend/internal/jobs"
	"github.com/james/usenet-locator/backend/internal/nntp"
)

var ErrJobNotRunning = errors.New("job is not running")

type Batch struct {
	JobID            string
	RangeStart       int64 // Every number attempted, including missing records.
	RangeEnd         int64
	NextArticle      int64
	HeadersRetrieved int // Valid protocol overview records before date filtering.
	Overviews        []nntp.Overview
}

type Result struct {
	HeadersRetrieved int
	ArticlesStored   int
	InvalidMessageID int
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) Store { return Store{pool: pool} }

// PersistBatch records all durable outcomes before advancing the checkpoint.
// Retrying the same batch is safe: canonical IDs, memberships, and locations
// all have unique constraints and counters only advance with a new commit.
func (s Store) PersistBatch(ctx context.Context, batch Batch) (Result, error) {
	if err := batch.validate(); err != nil {
		return Result{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("begin index batch: %w", err)
	}
	defer tx.Rollback(ctx)

	var groupID int64
	var endpointID string
	var state jobs.State
	if err := tx.QueryRow(ctx, `SELECT newsgroup_id, endpoint_id, state FROM index_jobs WHERE id = $1 FOR UPDATE`, batch.JobID).Scan(&groupID, &endpointID, &state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, jobs.ErrNotFound
		}
		return Result{}, fmt.Errorf("lock index job: %w", err)
	}
	if state != jobs.Running {
		return Result{}, ErrJobNotRunning
	}

	result := Result{HeadersRetrieved: batch.HeadersRetrieved}
	for _, overview := range batch.Overviews {
		if !validMessageID(overview.MessageID) {
			result.InvalidMessageID++
			continue
		}
		articleID, err := upsertArticle(ctx, tx, overview)
		if err != nil {
			return Result{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO article_newsgroups (article_id, newsgroup_id) VALUES ($1, $2)
            ON CONFLICT DO NOTHING`, articleID, groupID); err != nil {
			return Result{}, fmt.Errorf("upsert article membership: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO article_locations (article_id, endpoint_id, newsgroup_id, article_number)
            VALUES ($1, $2, $3, $4)
            ON CONFLICT (endpoint_id, newsgroup_id, article_number) DO UPDATE
            SET article_id = EXCLUDED.article_id, available = TRUE, last_seen_at = now()`, articleID, endpointID, groupID, overview.ArticleNumber); err != nil {
			return Result{}, fmt.Errorf("upsert article location: %w", err)
		}
		result.ArticlesStored++
	}
	if _, err := tx.Exec(ctx, `INSERT INTO scan_coverage (endpoint_id, newsgroup_id, job_id, article_number_start, article_number_end, state)
        VALUES ($1, $2, $3, $4, $5, 'complete')`, endpointID, groupID, batch.JobID, batch.RangeStart, batch.RangeEnd); err != nil {
		return Result{}, fmt.Errorf("record scan coverage: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE index_jobs SET headers_retrieved = headers_retrieved + $1,
        articles_stored = articles_stored + $2, updated_at = now() WHERE id = $3`, result.HeadersRetrieved, result.ArticlesStored, batch.JobID); err != nil {
		return Result{}, fmt.Errorf("update job counters: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE job_checkpoints SET next_article_number = $1,
        last_committed_article_number = $2, revision = revision + 1, updated_at = now() WHERE job_id = $3`, batch.NextArticle, batch.RangeEnd, batch.JobID); err != nil {
		return Result{}, fmt.Errorf("advance job checkpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit index batch: %w", err)
	}
	return result, nil
}

// IsRangeCovered compares evidence through the current job's endpoint and
// newsgroup, never by article number alone.
func (s Store) IsRangeCovered(ctx context.Context, jobID string, start, end int64) (bool, error) {
	var covered bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM scan_coverage c JOIN index_jobs j ON j.id = $1
        WHERE c.endpoint_id = j.endpoint_id AND c.newsgroup_id = j.newsgroup_id AND c.state = 'complete'
        AND c.article_number_start <= $2 AND c.article_number_end >= $3)`, jobID, start, end).Scan(&covered)
	if err != nil {
		return false, fmt.Errorf("check scan coverage: %w", err)
	}
	return covered, nil
}

// AdvanceCovered moves a new job checkpoint over known-complete evidence
// without fetching NNTP again or writing a duplicate coverage row.
func (s Store) AdvanceCovered(ctx context.Context, batch Batch) error {
	if err := batch.validate(); err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `UPDATE job_checkpoints c SET next_article_number = $1,
        last_committed_article_number = $2, revision = revision + 1, updated_at = now()
        FROM index_jobs j WHERE c.job_id = j.id AND j.id = $3 AND j.state = 'running'`, batch.NextArticle, batch.RangeEnd, batch.JobID)
	if err != nil {
		return fmt.Errorf("advance covered checkpoint: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrJobNotRunning
	}
	return nil
}

func (b Batch) validate() error {
	if b.JobID == "" || b.RangeStart < 1 || b.RangeEnd < b.RangeStart || b.NextArticle != b.RangeEnd+1 {
		return fmt.Errorf("invalid index batch bounds")
	}
	if b.HeadersRetrieved < len(b.Overviews) {
		return fmt.Errorf("header count cannot be smaller than stored overviews")
	}
	for _, overview := range b.Overviews {
		if overview.ArticleNumber < b.RangeStart || overview.ArticleNumber > b.RangeEnd {
			return fmt.Errorf("overview article number outside batch bounds")
		}
	}
	return nil
}

func upsertArticle(ctx context.Context, tx pgx.Tx, overview nntp.Overview) (int64, error) {
	var articleID int64
	var date *time.Time
	if !overview.Date.IsZero() {
		value := overview.Date.UTC()
		date = &value
	}
	err := tx.QueryRow(ctx, `INSERT INTO articles (message_id, subject, author, article_date, raw_date, references_header, byte_count, line_count)
        VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, 0), NULLIF($8, 0))
        ON CONFLICT (message_id) DO UPDATE SET updated_at = now()
		RETURNING id`, overview.MessageID, overview.Subject, overview.Author, date, overview.RawDate, overview.References, overview.Bytes, overview.Lines).Scan(&articleID)
	if err != nil {
		return 0, fmt.Errorf("upsert article: %w", err)
	}
	return articleID, nil
}

func validMessageID(value string) bool {
	return strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") && strings.Contains(value, "@") && !strings.ContainsAny(value, " \t\r\n")
}
