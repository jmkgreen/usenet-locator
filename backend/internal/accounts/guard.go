// Package accounts coordinates application-owned NNTP connection limits.
package accounts

import (
	"context"
	"fmt"

	"github.com/james/usenet-locator/backend/internal/config"
)

type Guard struct{ permits map[string]chan struct{} }

func NewGuard(cfg config.Config) Guard {
	permits := make(map[string]chan struct{}, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		permits[account.ID] = make(chan struct{}, account.ConnectionLimit)
	}
	return Guard{permits: permits}
}

func (g Guard) Acquire(ctx context.Context, accountID string) (func(), error) {
	permit, ok := g.permits[accountID]
	if !ok {
		return nil, fmt.Errorf("account connection limit is unavailable")
	}
	select {
	case permit <- struct{}{}:
		return func() { <-permit }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ConnectionUsage returns current in-process occupancy for a configured
// account. It reports no credentials or peer details.
func (g Guard) ConnectionUsage(accountID string) (inUse, limit int, ok bool) {
	permit, ok := g.permits[accountID]
	if !ok {
		return 0, 0, false
	}
	return len(permit), cap(permit), true
}
