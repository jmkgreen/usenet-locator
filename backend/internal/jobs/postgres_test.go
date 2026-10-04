package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/database"
)

// This acceptance test exercises the durable fan-out scheduler using a real
// database. In particular, one operator request must create endpoint-local
// work and its aggregate status must never claim completion prematurely.
func TestPostgresFanoutLifecycle(t *testing.T) {
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
	schema := "jobs_test_" + jobsTestSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	db, err := database.Open(ctx, jobsTestURL(t, rawURL, schema), 4)
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SyncConfiguration(ctx, config.Config{
		Accounts: []config.AccountConfig{{ID: "account", UsernameFile: "user-ref", PasswordFile: "password-ref", ConnectionLimit: 2}},
		Endpoints: []config.EndpointConfig{
			{ID: "primary", AccountID: "account", Host: "primary.example.test", Port: 563, TLS: true, Primary: true},
			{ID: "secondary", AccountID: "account", Host: "secondary.example.test", Port: 563, TLS: true, Priority: 1},
		},
	}); err != nil {
		t.Fatalf("sync configuration: %v", err)
	}

	store := NewStore(db.Pool)
	date := time.Date(2024, 2, 3, 0, 0, 0, 0, time.UTC)
	budget := int64(10)
	parentID, err := store.Create(ctx, CreateRequest{NewsgroupID: "comp.acceptance", EndpointID: "primary", StartDate: date, EndDate: date, TransferLimitBytes: &budget})
	if err != nil {
		t.Fatalf("create fan-out request: %v", err)
	}
	parent, err := store.Get(ctx, parentID)
	if err != nil || parent.State != Queued || len(parent.ProviderJobs) != 2 || parent.Endpoint != "all configured providers" {
		t.Fatalf("new parent = %#v, err = %v", parent, err)
	}

	first, claimed, err := store.ClaimNext(ctx)
	if err != nil || !claimed || first.State != Running || first.TransferLimitBytes == nil || *first.TransferLimitBytes != 5 {
		t.Fatalf("first child claim = %#v, %v, %v", first, claimed, err)
	}
	if err := store.ConsumeTransfer(ctx, first.ID, 5); err != nil {
		t.Fatalf("consume child budget: %v", err)
	}
	if err := store.ConsumeTransfer(ctx, first.ID, 1); err == nil {
		t.Fatal("accepted transfer beyond child budget")
	}
	if err := store.Complete(ctx, first.ID); err != nil {
		t.Fatalf("complete first child: %v", err)
	}
	second, claimed, err := store.ClaimNext(ctx)
	if err != nil || !claimed || second.ID == first.ID {
		t.Fatalf("second child claim = %#v, %v, %v", second, claimed, err)
	}
	if parent, err = store.Get(ctx, parentID); err != nil || parent.State != Running || parent.TransferUsedBytes != 6 {
		t.Fatalf("partly completed parent = %#v, err = %v", parent, err)
	}
	if err := store.Interrupt(ctx, second.ID, "network stopped"); err != nil {
		t.Fatalf("interrupt second child: %v", err)
	}
	if parent, err = store.Get(ctx, parentID); err != nil || parent.State != Interrupted || parent.LastError == nil {
		t.Fatalf("interrupted parent = %#v, err = %v", parent, err)
	}
	if err := store.Transition(ctx, parentID, Queued); err != nil {
		t.Fatalf("resume parent: %v", err)
	}
	resumed, claimed, err := store.ClaimNext(ctx)
	if err != nil || !claimed || resumed.ID != second.ID {
		t.Fatalf("resumed child = %#v, %v, %v", resumed, claimed, err)
	}
	if err := store.Complete(ctx, resumed.ID); err != nil {
		t.Fatalf("complete resumed child: %v", err)
	}
	if parent, err = store.Get(ctx, parentID); err != nil || parent.State != Completed || parent.LastError != nil {
		t.Fatalf("completed parent = %#v, err = %v", parent, err)
	}
	if err := store.Transition(ctx, parentID, Paused); err == nil {
		t.Fatal("accepted pause of an already completed parent")
	}
}

func jobsTestSuffix(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatalf("random schema name: %v", err)
	}
	return hex.EncodeToString(bytes)
}

func jobsTestURL(t *testing.T, rawURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
