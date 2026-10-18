package timeline

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
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
)

func TestDiscoveryDoesNotCreateTimelineCoverage(t *testing.T) {
	rawURL := os.Getenv("USENET_LOCATOR_TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("USENET_LOCATOR_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	schema := "timeline_discovery_" + hex.EncodeToString(random)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := database.Open(ctx, parsed.String(), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncConfiguration(ctx, config.Config{Accounts: []config.AccountConfig{{ID: "a", UsernameFile: "user", PasswordFile: "pass", ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "p", AccountID: "a", Host: "news.example", Port: 563, TLS: true, Primary: true}}}); err != nil {
		t.Fatal(err)
	}
	jobStore := jobs.NewStore(db.Pool)
	if _, err := jobStore.CreateDiscovery(ctx, "comp.timeline"); err != nil {
		t.Fatal(err)
	}
	calendar := NewStore(db.Pool, jobStore)
	if years, err := calendar.List(ctx, "comp.timeline", "year", nil); err != nil || len(years) != 0 {
		t.Fatalf("years=%#v err=%v", years, err)
	}
	date := time.Date(2001, 2, 1, 0, 0, 0, 0, time.UTC)
	if years, err := calendar.List(ctx, "comp.timeline", "year", &date); err != nil || len(years) != 1 || years[0].State != "gaps" {
		t.Fatalf("years=%#v err=%v", years, err)
	}
	if months, err := calendar.List(ctx, "comp.timeline", "month", &date); err != nil || len(months) != 12 || months[1].State != "gaps" {
		t.Fatalf("months=%#v err=%v", months, err)
	}
	if days, err := calendar.List(ctx, "comp.timeline", "day", &date); err != nil || len(days) != 28 || days[0].State != "gaps" {
		t.Fatalf("days=%#v err=%v", days, err)
	}
	if id, err := calendar.Complete(ctx, "comp.timeline", date, date); err != nil || id == "" {
		t.Fatalf("queue coverage=%q err=%v", id, err)
	}
	if days, err := calendar.List(ctx, "comp.timeline", "day", &date); err != nil || len(days) != 28 || days[0].State != "pending" {
		t.Fatalf("pending=%#v err=%v", days, err)
	}
	if id, err := calendar.Complete(ctx, "comp.timeline", date, date); err != nil || id != "" {
		t.Fatalf("duplicate coverage=%q err=%v", id, err)
	}
	multiStart := date.AddDate(0, 1, 0)
	if id, err := calendar.Complete(ctx, "comp.timeline", multiStart, multiStart.AddDate(0, 0, 1)); err != nil || id == "" {
		t.Fatalf("multi-day coverage=%q err=%v", id, err)
	}
	if id, err := calendar.Complete(ctx, "comp.timeline", multiStart, multiStart.AddDate(0, 0, 1)); err != nil || id != "" {
		t.Fatalf("duplicate multi-day coverage=%q err=%v", id, err)
	}
	if _, err := calendar.Complete(ctx, "comp.timeline", multiStart, date); err == nil {
		t.Fatal("accepted reversed dates")
	}
	if _, err := calendar.List(ctx, "comp.timeline", "month", nil); err == nil {
		t.Fatal("accepted a month without a year")
	}
	if _, err := calendar.List(ctx, "comp.timeline", "day", nil); err == nil {
		t.Fatal("accepted a day without a month")
	}
	if _, err := calendar.List(ctx, "comp.timeline", "invalid", nil); err == nil {
		t.Fatal("accepted an invalid level")
	}
}
