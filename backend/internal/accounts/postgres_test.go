package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/database"
)

// This acceptance test verifies that provider transfer limits are enforced by
// PostgreSQL atomically, rather than being a best-effort in-memory counter.
func TestPostgresQuotaStoreEnforcesDurableTransferLimit(t *testing.T) {
	rawURL := os.Getenv("USENET_LOCATOR_TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("USENET_LOCATOR_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, rawURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer admin.Close()
	schema := "accounts_test_" + accountTestSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	db, err := database.Open(ctx, accountTestURL(t, rawURL, schema), 2)
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	limit := int64(100)
	if err := db.SyncConfiguration(ctx, config.Config{Accounts: []config.AccountConfig{{ID: "limited", UsernameFile: "user-ref", PasswordFile: "password-ref", ConnectionLimit: 1, TransferLimitBytes: &limit}, {ID: "unlimited", UsernameFile: "user-ref", PasswordFile: "password-ref", ConnectionLimit: 1}}}); err != nil {
		t.Fatalf("sync accounts: %v", err)
	}

	store := NewQuotaStore(db.Pool)
	if err := store.Consume(ctx, "limited", 60); err != nil {
		t.Fatalf("first transfer: %v", err)
	}
	if err := store.Consume(ctx, "limited", 41); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("over-limit transfer error = %v", err)
	}
	if err := store.Consume(ctx, "missing", 1); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("missing account error = %v", err)
	}
	if err := store.Consume(ctx, "unlimited", 250); err != nil {
		t.Fatalf("unlimited transfer: %v", err)
	}
	usage, err := store.ListUsage(ctx)
	if err != nil || len(usage) != 2 {
		t.Fatalf("usage = %#v, err = %v", usage, err)
	}
	if usage[0].AccountID != "limited" || usage[0].TransferUsedBytes != 60 || usage[0].TransferLimitBytes == nil || *usage[0].TransferLimitBytes != 100 {
		t.Fatalf("limited usage = %#v", usage[0])
	}
	if usage[1].AccountID != "unlimited" || usage[1].TransferUsedBytes != 250 || usage[1].TransferLimitBytes != nil {
		t.Fatalf("unlimited usage = %#v", usage[1])
	}
}

func accountTestSuffix(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(bytes)
}

func accountTestURL(t *testing.T, rawURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
