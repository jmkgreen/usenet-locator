package watchlist

import (
	"context"
	"fmt"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/retention"
)

type RetentionProber interface {
	Probe(context.Context, string) ([]retention.Observation, error)
}
type DueStore interface {
	Due(context.Context, time.Time) ([]string, error)
	MarkChecked(context.Context, string, time.Time) error
}

// RunDue does one bounded probe per due group. A failure leaves that group due
// for a later retry, while the remaining watchlist entries still get a turn.
func RunDue(ctx context.Context, store DueStore, prober RetentionProber, now time.Time) error {
	groups, err := store.Due(ctx, now)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if _, err := prober.Probe(ctx, group); err != nil {
			continue
		}
		if err := store.MarkChecked(ctx, group, now); err != nil {
			return fmt.Errorf("mark %s checked: %w", group, err)
		}
	}
	return nil
}
