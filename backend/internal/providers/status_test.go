package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
)

type testUsageReader struct {
	usage []accounts.Usage
	err   error
}

func (r testUsageReader) ListUsage(context.Context) ([]accounts.Usage, error) { return r.usage, r.err }

func TestServiceSortsSafeEndpointMetadata(t *testing.T) {
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", ConnectionLimit: 2}}, Endpoints: []config.EndpointConfig{{ID: "second", AccountID: "account", Host: "two.example", Port: 563, TLS: true, Priority: 2}, {ID: "first", AccountID: "account", Host: "one.example", Port: 563, TLS: true, Primary: true, Priority: 1}}}
	service := New(cfg, nil, accounts.NewGuard(cfg))
	items, err := service.List(context.Background())
	if err != nil || len(items) != 2 || items[0].ID != "first" || !items[0].Primary || items[0].ConnectionLimit != 2 {
		t.Fatalf("items = %#v", items)
	}
}

func TestServiceAddsQuotaUsageWithoutSecrets(t *testing.T) {
	limit := int64(100)
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "primary", AccountID: "account", Host: "news.example", Port: 563, TLS: true, Primary: true}}}
	service := New(cfg, testUsageReader{usage: []accounts.Usage{{AccountID: "account", TransferUsedBytes: 40, TransferLimitBytes: &limit}}}, accounts.NewGuard(cfg))
	items, err := service.List(context.Background())
	if err != nil || len(items) != 1 || items[0].TransferUsedBytes != 40 || items[0].TransferLimitBytes == nil || *items[0].TransferLimitBytes != 100 {
		t.Fatalf("items = %#v, err = %v", items, err)
	}
	if _, err := New(cfg, testUsageReader{err: errors.New("database unavailable")}, accounts.NewGuard(cfg)).List(context.Background()); err == nil {
		t.Fatal("usage failure was hidden")
	}
}

func TestServiceReportsLiveConnectionOccupancy(t *testing.T) {
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "primary", AccountID: "account", Host: "news.example", Port: 563, TLS: true, Primary: true}}}
	guard := accounts.NewGuard(cfg)
	release, err := guard.Acquire(context.Background(), "account")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	items, err := New(cfg, nil, guard).List(context.Background())
	if err != nil || len(items) != 1 || items[0].ConnectionInUse != 1 || items[0].ConnectionLimit != 1 {
		t.Fatalf("items = %#v, err = %v", items, err)
	}
}
