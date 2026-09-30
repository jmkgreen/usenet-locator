package jobs

import (
	"context"
	"fmt"
)

// Dispatcher claims durable work. It deliberately does not restart jobs that
// were active during a process stop: Recover is called at startup and turns
// those jobs into the manually resumable interrupted state.
type Dispatcher struct {
	store Claimer
}

func NewDispatcher(store Claimer) Dispatcher { return Dispatcher{store: store} }

func (d Dispatcher) Recover(ctx context.Context) error {
	if err := d.store.RecoverInterrupted(ctx); err != nil {
		return fmt.Errorf("recover scheduler state: %w", err)
	}
	return nil
}

// RunOnce claims at most one queued job and passes it to execute. The caller
// owns concurrency and the eventual pause/complete/fail transition; this keeps
// scheduling separate from NNTP and batch persistence.
func (d Dispatcher) RunOnce(ctx context.Context, execute func(context.Context, Job) error) (bool, error) {
	job, claimed, err := d.store.ClaimNext(ctx)
	if err != nil || !claimed {
		return claimed, err
	}
	if err := execute(ctx, job); err != nil {
		return true, fmt.Errorf("execute job %s: %w", job.ID, err)
	}
	return true, nil
}
