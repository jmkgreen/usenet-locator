// Package jobs owns durable indexing-job invariants independently of HTTP and
// NNTP transport details.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type State string

const (
	Queued      State = "queued"
	Running     State = "running"
	Paused      State = "paused"
	Interrupted State = "interrupted"
	Completed   State = "completed"
	Failed      State = "failed"
	Cancelled   State = "cancelled"
)

type CreateRequest struct {
	NewsgroupID        string
	EndpointID         string
	StartDate          time.Time
	EndDate            time.Time
	MarginDays         int
	ScanReason         string
	SourceJobID        string
	TransferLimitBytes *int64
}

// Job is the safe, durable job status exposed to callers. It deliberately
// excludes credentials and protocol transcripts.
type Job struct {
	ID                 string
	Newsgroup          string
	Endpoint           string
	StartDate          time.Time
	EndDate            time.Time
	MarginDays         int
	State              State
	HeadersRetrieved   int64
	ArticlesStored     int64
	LastError          *string
	ScanReason         string
	SourceJobID        *string
	TransferLimitBytes *int64
	TransferUsedBytes  int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

var ErrNotFound = errors.New("job not found")
var ErrInvalidTransition = errors.New("invalid job state transition")

// Creator is the minimum job dependency needed to accept a new request.
// Keeping this contract here lets HTTP stay independent of PostgreSQL.
type Creator interface {
	Create(context.Context, CreateRequest) (string, error)
}

// Finder is the minimum job dependency needed to retrieve job status.
type Finder interface {
	Get(context.Context, string) (Job, error)
}

// Claimer is the durable scheduler boundary. A worker only receives a job
// after its state has been changed atomically to running.
type Claimer interface {
	RecoverInterrupted(context.Context) error
	ClaimNext(context.Context) (Job, bool, error)
}

type Transitioner interface {
	Transition(context.Context, string, State) error
}

type Finalizer interface {
	Complete(context.Context, string) error
	Interrupt(context.Context, string, string) error
}
type TransferConsumer interface {
	ConsumeTransfer(context.Context, string, int64) error
}

var ErrTransferLimitExceeded = errors.New("job transfer limit exceeded")

func (r CreateRequest) Validate() error {
	if r.NewsgroupID == "" || r.EndpointID == "" {
		return fmt.Errorf("newsgroup and endpoint are required")
	}
	if r.StartDate.IsZero() || r.EndDate.IsZero() {
		return fmt.Errorf("inclusive start and end dates are required")
	}
	if r.StartDate.Location() != time.UTC || r.EndDate.Location() != time.UTC {
		return fmt.Errorf("dates must be normalised to UTC whole days")
	}
	if r.StartDate != dateOnlyUTC(r.StartDate) || r.EndDate != dateOnlyUTC(r.EndDate) {
		return fmt.Errorf("dates must be normalised to UTC whole days")
	}
	if r.StartDate.After(r.EndDate) {
		return fmt.Errorf("start date must not be after end date")
	}
	if r.MarginDays < 0 || r.MarginDays > 31 {
		return fmt.Errorf("margin days must be between 0 and 31")
	}
	if r.ScanReason == "" {
		r.ScanReason = "operator_requested"
	}
	if r.ScanReason != "operator_requested" && r.ScanReason != "missing_range" && r.ScanReason != "failed_batch" {
		return fmt.Errorf("invalid scan reason")
	}
	if r.ScanReason == "operator_requested" && r.SourceJobID != "" {
		return fmt.Errorf("source job requires a supplementary scan reason")
	}
	if r.TransferLimitBytes != nil && *r.TransferLimitBytes < 1 {
		return fmt.Errorf("transfer limit must be positive")
	}
	return nil
}

func CanTransition(from, to State) bool {
	allowed := map[State]map[State]bool{
		Queued:      {Running: true, Cancelled: true},
		Running:     {Paused: true, Interrupted: true, Completed: true, Failed: true, Cancelled: true},
		Paused:      {Queued: true, Cancelled: true},
		Interrupted: {Queued: true, Cancelled: true},
	}
	return allowed[from][to]
}
