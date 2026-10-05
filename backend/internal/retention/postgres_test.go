package retention

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
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

// This acceptance test follows the durable retention path from a probe
// observation through incremental header storage and cursor advancement.
func TestPostgresRetentionObservationAndHeaderLifecycle(t *testing.T) {
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
	schema := "retention_test_" + retentionTestSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	db, err := database.Open(ctx, retentionTestURL(t, rawURL, schema), 2)
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SyncConfiguration(ctx, config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: "user-ref", PasswordFile: "password-ref", ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "primary", AccountID: "account", Host: "news.example.test", Port: 563, TLS: true, Primary: true}}}); err != nil {
		t.Fatalf("sync configuration: %v", err)
	}

	store := NewStore(db.Pool)
	articleNumber := int64(10)
	date := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	stored, err := store.Record(ctx, Observation{Endpoint: "primary", Newsgroup: "Comp.Retention", GroupLow: 1, GroupHigh: 20, ArticleNumber: &articleNumber, Article: &nntp.Overview{ArticleNumber: articleNumber, MessageID: "<retained@example.test>", Subject: "retained", Author: "author", Date: date}, ObservedDate: date, Outcome: "found"})
	if err != nil || stored.ArticleID == nil || stored.Article != nil || stored.ObservedAt.IsZero() || stored.Newsgroup != "Comp.Retention" {
		t.Fatalf("record = %#v, err = %v", stored, err)
	}
	next, high, found, err := store.Cursor(ctx, "primary", "comp.retention")
	if err != nil || !found || next != 11 || high != 20 {
		t.Fatalf("cursor = %d, %d, %v, %v", next, high, found, err)
	}
	if _, _, found, err := store.Cursor(ctx, "primary", "missing.group"); err != nil || found {
		t.Fatalf("missing cursor found=%v err=%v", found, err)
	}

	if err := store.StoreHeaders(ctx, "primary", "comp.retention", []nntp.Overview{
		{ArticleNumber: 11, MessageID: "<header@example.test>", Subject: "header", Date: date},
		{ArticleNumber: 12, MessageID: "invalid header", Date: date},
	}, 13, 20); err != nil {
		t.Fatalf("store headers: %v", err)
	}
	next, high, found, err = store.Cursor(ctx, "primary", "COMP.RETENTION")
	if err != nil || !found || next != 13 || high != 20 {
		t.Fatalf("advanced cursor = %d, %d, %v, %v", next, high, found, err)
	}
	history, err := store.ListLatest(ctx, "comp.retention")
	if err != nil || len(history) != 1 || history[0].Endpoint != "primary" || history[0].ArticleID == nil || history[0].GroupLow != 1 || history[0].GroupHigh != 20 {
		t.Fatalf("history = %#v, err = %v", history, err)
	}
}

func retentionTestSuffix(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(bytes)
}

func retentionTestURL(t *testing.T, rawURL, schema string) string {
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
