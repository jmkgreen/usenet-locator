package indexing

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/james/usenet-locator/backend/internal/jobs"
	"github.com/james/usenet-locator/backend/internal/nntp"
)

type BatchWriter interface {
	PersistBatch(context.Context, Batch) (Result, error)
}
type CoverageSkipper interface {
	IsRangeCovered(context.Context, string, int64, int64) (bool, error)
	AdvanceCovered(context.Context, Batch) error
}

// Scanner owns bounded protocol traversal. It contains no database knowledge
// beyond the transactional batch-writer boundary.
type Scanner struct {
	Client         nntp.Client
	Writer         BatchWriter
	BatchSize      int64
	RetryAttempts  int
	RetryBaseDelay time.Duration
	RecordTransfer func(context.Context, int64) error
}

type Range struct{ Low, High int64 }

// Discover finds an approximate article-number range by probing endpoint-local
// overview records. If sparse or malformed probes make the binary search
// ambiguous, it returns the entire advertised group range: slower, but it
// cannot silently skip history.
func (s Scanner) Discover(ctx context.Context, job jobs.Job) (Range, error) {
	before := s.transferBytes()
	group, err := s.Client.Group(ctx, job.Newsgroup)
	if recordErr := s.recordTransfer(ctx, before); recordErr != nil {
		return Range{}, recordErr
	}
	if err != nil {
		return Range{}, fmt.Errorf("select newsgroup: %w", err)
	}
	if group.Low > group.High {
		return Range{}, fmt.Errorf("server returned invalid group bounds")
	}
	start := job.StartDate.AddDate(0, 0, -job.MarginDays)
	end := job.EndDate.AddDate(0, 0, job.MarginDays+1) // exclusive
	low, reliable := s.lowerBound(ctx, group.Low, group.High, start)
	if !reliable {
		return Range{Low: group.Low, High: group.High}, nil
	}
	high, reliable := s.lowerBound(ctx, low, group.High, end)
	if !reliable {
		return Range{Low: group.Low, High: group.High}, nil
	}
	if high <= low {
		return Range{Low: low, High: low}, nil
	}
	return Range{Low: low, High: high - 1}, nil
}

// lowerBound returns the first article number whose supplied date is not
// before target. It only trusts exact-number probes with a parseable date.
func (s Scanner) lowerBound(ctx context.Context, low, high int64, target time.Time) (int64, bool) {
	for low < high {
		middle := low + (high-low)/2
		var sample *nntp.Overview
		err := s.overview(ctx, middle, middle, func(item nntp.Overview) error { value := item; sample = &value; return nil })
		if err != nil || sample == nil || sample.Date.IsZero() {
			return 0, false
		}
		if sample.Date.Before(target) {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low, true
}

// Scan discovers and streams the range in bounded batches. It retains an
// undated header (for auditability) but filters parseable dates to the job's
// requested range plus margin before persistence.
func (s Scanner) Scan(ctx context.Context, job jobs.Job) error {
	if s.Client == nil || s.Writer == nil || s.BatchSize < 1 {
		return fmt.Errorf("scanner client, writer, and positive batch size are required")
	}
	rangeToScan, err := s.Discover(ctx, job)
	if err != nil {
		return err
	}
	start := job.StartDate.AddDate(0, 0, -job.MarginDays)
	end := job.EndDate.AddDate(0, 0, job.MarginDays+1)
	for from := rangeToScan.Low; from <= rangeToScan.High; {
		to := from + s.BatchSize - 1
		if to > rangeToScan.High {
			to = rangeToScan.High
		}
		batch := Batch{JobID: job.ID, RangeStart: from, RangeEnd: to, NextArticle: to + 1}
		if skipper, ok := s.Writer.(CoverageSkipper); ok {
			covered, err := skipper.IsRangeCovered(ctx, job.ID, from, to)
			if err != nil {
				return fmt.Errorf("check overview coverage %d-%d: %w", from, to, err)
			}
			if covered {
				if err := skipper.AdvanceCovered(ctx, batch); err != nil {
					return fmt.Errorf("advance covered range %d-%d: %w", from, to, err)
				}
				from = to + 1
				continue
			}
		}
		var overviews []nntp.Overview
		headers := 0
		err := s.overview(ctx, from, to, func(item nntp.Overview) error {
			headers++
			if item.Date.IsZero() || (!item.Date.Before(start) && item.Date.Before(end)) {
				overviews = append(overviews, item)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("read overview %d-%d: %w", from, to, err)
		}
		batch.HeadersRetrieved, batch.Overviews = headers, overviews
		if _, err := s.Writer.PersistBatch(ctx, batch); err != nil {
			return fmt.Errorf("persist overview %d-%d: %w", from, to, err)
		}
		from = to + 1
	}
	return nil
}

func (s Scanner) overview(ctx context.Context, low, high int64, receive func(nntp.Overview) error) error {
	attempts := s.RetryAttempts
	if attempts == 0 {
		attempts = 3
	}
	base := s.RetryBaseDelay
	if base == 0 {
		base = 100 * time.Millisecond
	}
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		before := s.transferBytes()
		err = s.Client.Overview(ctx, low, high, receive)
		if recordErr := s.recordTransfer(ctx, before); recordErr != nil {
			return recordErr
		}
		if err == nil {
			return nil
		}
		if attempt == attempts-1 {
			break
		}
		delay := base << attempt
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
		if jitter, randomErr := rand.Int(rand.Reader, big.NewInt(int64(delay/2)+1)); randomErr == nil {
			delay += time.Duration(jitter.Int64())
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func (s Scanner) transferBytes() int64 {
	if meter, ok := s.Client.(nntp.TransferMeter); ok {
		return meter.TransferBytes()
	}
	return 0
}

func (s Scanner) recordTransfer(ctx context.Context, before int64) error {
	if s.RecordTransfer == nil {
		return nil
	}
	if transferred := s.transferBytes() - before; transferred > 0 {
		return s.RecordTransfer(ctx, transferred)
	}
	return nil
}
