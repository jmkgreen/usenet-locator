package jobs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) Store { return Store{pool: pool} }

// Create writes one durable request parent and one endpoint-local child per
// enabled provider. The parent is a status/control record and is never claimed
// by the NNTP dispatcher.
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
	rows, err := tx.Query(ctx, `SELECT e.id FROM nntp_endpoints e JOIN provider_accounts a ON a.id = e.account_id
        WHERE e.enabled AND a.enabled ORDER BY e.priority, e.id`)
	if err != nil {
		return "", fmt.Errorf("list enabled endpoints: %w", err)
	}
	defer rows.Close()
	var endpoints []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("read enabled endpoint: %w", err)
		}
		endpoints = append(endpoints, id)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("list enabled endpoints: %w", err)
	}
	if len(endpoints) == 0 {
		return "", ErrUnknownEndpoint
	}
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
		(id, newsgroup_id, endpoint_id, requested_start_date, requested_end_date, margin_days, scan_reason, source_job_id, transfer_limit_bytes, is_fanout_parent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid, $9, TRUE)`, id, groupID, endpoints[0], request.StartDate, request.EndDate, request.MarginDays, request.ScanReason, request.SourceJobID, request.TransferLimitBytes)
	if err != nil {
		return "", fmt.Errorf("insert fan-out parent: %w", err)
	}
	var childLimit *int64
	if request.TransferLimitBytes != nil {
		share := *request.TransferLimitBytes / int64(len(endpoints))
		if share < 1 {
			return "", fmt.Errorf("transfer limit must cover each enabled provider")
		}
		childLimit = &share
	}
	for _, endpointID := range endpoints {
		childID, err := newID()
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO index_jobs
            (id, parent_job_id, newsgroup_id, endpoint_id, requested_start_date, requested_end_date, margin_days, scan_reason, source_job_id, transfer_limit_bytes)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::uuid, $10)`, childID, id, groupID, endpointID, request.StartDate, request.EndDate, request.MarginDays, request.ScanReason, request.SourceJobID, childLimit); err != nil {
			return "", fmt.Errorf("insert provider child: %w", err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO job_checkpoints (job_id) VALUES ($1)", childID); err != nil {
			return "", fmt.Errorf("insert provider checkpoint: %w", err)
		}
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
	var parent bool
	err := s.pool.QueryRow(ctx, `SELECT j.id, g.name, j.endpoint_id,
        j.requested_start_date, j.requested_end_date, j.margin_days, j.state,
		j.headers_retrieved, j.articles_stored, j.last_error, j.scan_reason, j.source_job_id, j.transfer_limit_bytes, j.transfer_used_bytes, j.created_at, j.updated_at, j.is_fanout_parent
        FROM index_jobs j JOIN newsgroups g ON g.id = j.newsgroup_id WHERE j.id = $1`, id).Scan(
		&job.ID, &job.Newsgroup, &job.Endpoint, &startDate, &endDate, &job.MarginDays,
		&job.State, &job.HeadersRetrieved, &job.ArticlesStored, &job.LastError, &job.ScanReason, &job.SourceJobID, &job.TransferLimitBytes, &job.TransferUsedBytes,
		&job.CreatedAt, &job.UpdatedAt, &parent,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get job: %w", err)
	}
	job.StartDate = dateOnlyUTC(startDate)
	job.EndDate = dateOnlyUTC(endDate)
	if parent {
		if err := s.aggregateParent(ctx, &job); err != nil {
			return Job{}, err
		}
	}
	return job, nil
}

func (s Store) aggregateParent(ctx context.Context, job *Job) error {
	rows, err := s.pool.Query(ctx, `SELECT endpoint_id, state, headers_retrieved, articles_stored, transfer_used_bytes, last_error
        FROM index_jobs WHERE parent_job_id = $1 ORDER BY endpoint_id`, job.ID)
	if err != nil {
		return fmt.Errorf("list provider jobs: %w", err)
	}
	defer rows.Close()
	var running, queued, paused, interrupted, cancelled, failed, nonCompleted int
	for rows.Next() {
		var child ProviderJob
		if err := rows.Scan(&child.Endpoint, &child.State, &child.HeadersRetrieved, &child.ArticlesStored, &child.TransferUsedBytes, &child.LastError); err != nil {
			return fmt.Errorf("read provider job: %w", err)
		}
		job.ProviderJobs = append(job.ProviderJobs, child)
		job.HeadersRetrieved += child.HeadersRetrieved
		job.ArticlesStored += child.ArticlesStored
		job.TransferUsedBytes += child.TransferUsedBytes
		switch child.State {
		case Running:
			running++
		case Queued:
			queued++
		case Paused:
			paused++
		case Interrupted:
			interrupted++
		case Completed:
		case Cancelled:
			cancelled++
			nonCompleted++
		case Failed:
			failed++
			nonCompleted++
		default:
			nonCompleted++
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list provider jobs: %w", err)
	}
	job.Endpoint = "all configured providers"
	switch {
	case running > 0:
		job.State = Running
	case queued > 0:
		job.State = Queued
	case paused > 0:
		job.State = Paused
	case interrupted > 0:
		job.State = Interrupted
	case cancelled == len(job.ProviderJobs):
		job.State = Cancelled
	case failed == len(job.ProviderJobs):
		job.State = Failed
	case nonCompleted == 0:
		job.State = Completed
	default:
		job.State = Completed
	}
	if nonCompleted > 0 {
		message := fmt.Sprintf("%d of %d provider scans did not complete", nonCompleted, len(job.ProviderJobs))
		job.LastError = &message
	}
	return nil
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
		SELECT id FROM index_jobs WHERE state = $1 AND NOT is_fanout_parent ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
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
	var parent bool
	if err := tx.QueryRow(ctx, "SELECT state, is_fanout_parent FROM index_jobs WHERE id = $1 FOR UPDATE", id).Scan(&current, &parent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read job state: %w", err)
	}
	if parent {
		var command pgconn.CommandTag
		switch target {
		case Paused:
			command, err = tx.Exec(ctx, "UPDATE index_jobs SET state = $1, updated_at = now() WHERE parent_job_id = $2 AND state = $3", target, id, Running)
		case Queued:
			command, err = tx.Exec(ctx, "UPDATE index_jobs SET state = $1, updated_at = now() WHERE parent_job_id = $2 AND state IN ($3, $4)", target, id, Paused, Interrupted)
		case Cancelled:
			command, err = tx.Exec(ctx, "UPDATE index_jobs SET state = $1, updated_at = now() WHERE parent_job_id = $2 AND state IN ($3, $4, $5, $6)", target, id, Queued, Running, Paused, Interrupted)
		}
		if err != nil {
			return fmt.Errorf("update provider jobs: %w", err)
		}
		if command.RowsAffected() == 0 {
			return ErrInvalidTransition
		}
		if _, err := tx.Exec(ctx, "UPDATE index_jobs SET state = $1, updated_at = now() WHERE id = $2", target, id); err != nil {
			return fmt.Errorf("update fan-out parent: %w", err)
		}
		return tx.Commit(ctx)
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
