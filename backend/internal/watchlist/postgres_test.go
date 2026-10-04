package watchlist

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/database"
)

func TestPostgresWatchlistScheduleAndRemoval(t *testing.T) {
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
	schema := "watchlist_test_" + watchlistSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	db, err := database.Open(ctx, watchlistURL(t, rawURL, schema), 2)
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := NewStore(db.Pool)
	if _, err := store.Add(ctx, "alt/test", 1); err == nil {
		t.Fatal("accepted unsafe group name")
	}
	item, err := store.Add(ctx, " Comp.Acceptance ", 2)
	if err != nil || item.Newsgroup != "comp.acceptance" || item.IntervalHours != 2 || item.LastCheckedAt != nil {
		t.Fatalf("add = %#v, %v", item, err)
	}
	updated, err := store.Add(ctx, "COMP.ACCEPTANCE", 3)
	if err != nil || updated.IntervalHours != 3 {
		t.Fatalf("idempotent update = %#v, %v", updated, err)
	}
	now := time.Date(2024, 4, 5, 12, 0, 0, 0, time.UTC)
	due, err := store.Due(ctx, now)
	if err != nil || len(due) != 1 || due[0] != "comp.acceptance" {
		t.Fatalf("initial due = %#v, %v", due, err)
	}
	if err := store.MarkChecked(ctx, "COMP.ACCEPTANCE", now); err != nil {
		t.Fatalf("mark checked: %v", err)
	}
	if due, err = store.Due(ctx, now.Add(2*time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("early due = %#v, %v", due, err)
	}
	if due, err = store.Due(ctx, now.Add(3*time.Hour)); err != nil || len(due) != 1 {
		t.Fatalf("scheduled due = %#v, %v", due, err)
	}
	items, err := store.List(ctx)
	if err != nil || len(items) != 1 || items[0].LastCheckedAt == nil || !items[0].NextCheckAt.Equal(now.Add(3*time.Hour)) {
		t.Fatalf("list = %#v, %v", items, err)
	}
	if err := store.Remove(ctx, "comp.acceptance"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := store.Remove(ctx, "comp.acceptance"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing removal error = %v", err)
	}
}

func watchlistSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
func watchlistURL(t *testing.T, rawURL, schema string) string {
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
