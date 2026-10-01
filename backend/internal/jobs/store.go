package jobs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) Store { return Store{pool: pool} }

// Create writes one job and its initial checkpoint in one transaction.
func (s Store) Create(ctx context.Context, request CreateRequest) (string, error) {
	if request.ScanReason == "" {
		request.ScanReason = "operator_requested"
	}
	if err := request.Validate(); err != nil {
		return "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin job: %w", err)
	}
	defer tx.Rollback(ctx)
	var groupID int64
	if err := tx.QueryRow(ctx, `INSERT INTO newsgroups (name) VALUES (lower($1))
        ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, request.NewsgroupID).Scan(&groupID); err != nil {
		return "", fmt.Errorf("upsert newsgroup: %w", err)
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO index_jobs
		(id, newsgroup_id, endpoint_id, requested_start_date, requested_end_date, margin_days, scan_reason, source_job_id, transfer_limit_bytes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid, $9)`, id, groupID, request.EndpointID, request.StartDate, request.EndDate, request.MarginDays, request.ScanReason, request.SourceJobID, request.TransferLimitBytes)
	if err != nil {
		return "", fmt.Errorf("insert job: %w", err)
	}
	_, err = tx.Exec(ctx, "INSERT INTO job_checkpoints (job_id) VALUES ($1)", id)
	if err != nil {
		return "", fmt.Errorf("insert job checkpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit job: %w", err)
	}
	return id, nil
}

// Get returns one job's current durable status.
func (s Store) Get(ctx context.Context, id string) (Job, error) {
	var job Job
	var startDate, endDate time.Time
	err := s.pool.QueryRow(ctx, `SELECT j.id, g.name, j.endpoint_id,
        j.requested_start_date, j.requested_end_date, j.margin_days, j.state,
		j.headers_retrieved, j.articles_stored, j.last_error, j.scan_reason, j.source_job_id, j.transfer_limit_bytes, j.transfer_used_bytes, j.created_at, j.updated_at
        FROM index_jobs j JOIN newsgroups g ON g.id = j.newsgroup_id WHERE j.id = $1`, id).Scan(
		&job.ID, &job.Newsgroup, &job.Endpoint, &startDate, &endDate, &job.MarginDays,
		&job.State, &job.HeadersRetrieved, &job.ArticlesStored, &job.LastError, &job.ScanReason, &job.SourceJobID, &job.TransferLimitBytes, &job.TransferUsedBytes,
		&job.CreatedAt, &job.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get job: %w", err)
	}
	job.StartDate = dateOnlyUTC(startDate)
	job.EndDate = dateOnlyUTC(endDate)
	return job, nil
}

// RecoverInterrupted marks work that was running when the process stopped as
// manually resumable. Startup must never silently resume it.
func (s Store) RecoverInterrupted(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE index_jobs SET state = $1, updated_at = now(),
        last_error = COALESCE(last_error, 'application restarted; manual resume required')
        WHERE state = $2`, Interrupted, Running)
	if err != nil {
		return fmt.Errorf("recover interrupted jobs: %w", err)
	}
	return nil
}

// ClaimNext atomically marks the oldest queued job as running. SKIP LOCKED
// makes the operation safe should an operator accidentally run two processes.
func (s Store) ClaimNext(ctx context.Context) (Job, bool, error) {
	var job Job
	var startDate, endDate time.Time
	err := s.pool.QueryRow(ctx, `WITH candidate AS (
        SELECT id FROM index_jobs WHERE state = $1 ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
    ) UPDATE index_jobs j SET state = $2, updated_at = now() FROM candidate
    WHERE j.id = candidate.id
    RETURNING j.id, (SELECT name FROM newsgroups WHERE id = j.newsgroup_id), j.endpoint_id,
    j.requested_start_date, j.requested_end_date, j.margin_days, j.state, j.headers_retrieved,
    j.articles_stored, j.last_error, j.scan_reason, j.source_job_id, j.transfer_limit_bytes, j.transfer_used_bytes, j.created_at, j.updated_at`, Queued, Running).Scan(
		&job.ID, &job.Newsgroup, &job.Endpoint, &startDate, &endDate, &job.MarginDays, &job.State,
		&job.HeadersRetrieved, &job.ArticlesStored, &job.LastError, &job.ScanReason, &job.SourceJobID, &job.TransferLimitBytes, &job.TransferUsedBytes, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("claim next job: %w", err)
	}
	job.StartDate, job.EndDate = dateOnlyUTC(startDate), dateOnlyUTC(endDate)
	return job, true, nil
}

// Transition records an explicit operator command. Resuming moves a paused or
// interrupted job back to queued so that only the dispatcher can make it
// running; this avoids an API request manufacturing unowned running work.
func (s Store) Transition(ctx context.Context, id string, target State) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin job transition: %w", err)
	}
	defer tx.Rollback(ctx)
	var current State
	if err := tx.QueryRow(ctx, "SELECT state FROM index_jobs WHERE id = $1 FOR UPDATE", id).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read job state: %w", err)
	}
	if !CanTransition(current, target) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, current, target)
	}
	if _, err := tx.Exec(ctx, "UPDATE index_jobs SET state = $1, updated_at = now() WHERE id = $2", target, id); err != nil {
		return fmt.Errorf("update job state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit job transition: %w", err)
	}
	return nil
}

// Complete is only valid for a worker-owned running job.
func (s Store) Complete(ctx context.Context, id string) error {
	return s.finish(ctx, id, Completed, "")
}

func (s Store) ConsumeTransfer(ctx context.Context, id string, bytes int64) error {
	if bytes <= 0 {
		return nil
	}
	var limit *int64
	var used int64
	err := s.pool.QueryRow(ctx, `UPDATE index_jobs SET transfer_used_bytes = transfer_used_bytes + $2, updated_at = now() WHERE id = $1 RETURNING transfer_used_bytes, transfer_limit_bytes`, id, bytes).Scan(&used, &limit)
	if err != nil {
		return fmt.Errorf("record job transfer: %w", err)
	}
	if limit != nil && used > *limit {
		return ErrTransferLimitExceeded
	}
	return nil
}

// Interrupt records a safe, manually resumable stop. It only changes a
// running job, so a concurrent pause/cancel command is never overwritten.
func (s Store) Interrupt(ctx context.Context, id, reason string) error {
	return s.finish(ctx, id, Interrupted, reason)
}

func (s Store) finish(ctx context.Context, id string, target State, reason string) error {
	command, err := s.pool.Exec(ctx, `UPDATE index_jobs SET state = $1::index_job_state, last_error = NULLIF($2, ''),
	        completed_at = CASE WHEN $1::index_job_state = 'completed'::index_job_state THEN now() ELSE completed_at END, updated_at = now()
	        WHERE id = $3 AND state = 'running'::index_job_state`, target, reason, id)
	if err != nil {
		return fmt.Errorf("finish job: %w", err)
	}
	if command.RowsAffected() == 0 {
		return nil
	}
	return nil
}

func dateOnlyUTC(value time.Time) time.Time {
	return time.Date(value.UTC().Year(), value.UTC().Month(), value.UTC().Day(), 0, 0, 0, 0, time.UTC)
}

func newID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate job ID: %w", err)
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}
