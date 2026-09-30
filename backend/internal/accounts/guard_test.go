package accounts

import (
	"context"
	"testing"
	"time"

	"github.com/james/usenet-locator/backend/internal/config"
)

func TestGuardHonoursAccountConnectionLimit(t *testing.T) {
	guard := NewGuard(config.Config{Accounts: []config.AccountConfig{{ID: "account", ConnectionLimit: 1}}})
	release, err := guard.Acquire(context.Background(), "account")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := guard.Acquire(ctx, "account"); err == nil {
		t.Fatal("second acquire exceeded limit")
	}
	release()
	if release, err := guard.Acquire(context.Background(), "account"); err != nil {
		t.Fatalf("acquire after release: %v", err)
	} else {
		release()
	}
}
