package indexing

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
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

// This acceptance test verifies that a committed scan batch makes article
// visibility, endpoint-local coverage, and checkpoint advancement durable.
func TestPostgresStorePersistsBoundedBatchAndCoverage(t *testing.T) {
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
	schema := "indexing_test_" + indexingTestSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	db, err := database.Open(ctx, indexingTestURL(t, rawURL, schema), 2)
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

	jobStore := jobs.NewStore(db.Pool)
	day := time.Date(2024, 7, 8, 0, 0, 0, 0, time.UTC)
	if _, err := jobStore.Create(ctx, jobs.CreateRequest{NewsgroupID: "comp.indexing", EndpointID: "primary", StartDate: day, EndDate: day}); err != nil {
		t.Fatalf("create job: %v", err)
	}
	job, claimed, err := jobStore.ClaimNext(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim job = %#v, %v, %v", job, claimed, err)
	}

	store := NewStore(db.Pool)
	batch := Batch{JobID: job.ID, RangeStart: 10, RangeEnd: 12, NextArticle: 13, HeadersRetrieved: 3, Overviews: []nntp.Overview{
		{ArticleNumber: 10, MessageID: "<one@indexing.test>", Subject: "first", Date: day},
		{ArticleNumber: 11, MessageID: "invalid id", Subject: "discarded", Date: day},
		{ArticleNumber: 12, MessageID: "<two@indexing.test>", Subject: "second", Date: day},
	}}
	result, err := store.PersistBatch(ctx, batch)
	if err != nil || result.HeadersRetrieved != 3 || result.ArticlesStored != 2 || result.InvalidMessageID != 1 {
		t.Fatalf("persist batch = %#v, %v", result, err)
	}
	if covered, err := store.IsRangeCovered(ctx, job.ID, 10, 12); err != nil || !covered {
		t.Fatalf("coverage = %v, %v", covered, err)
	}
	if covered, err := store.IsRangeCovered(ctx, job.ID, 9, 12); err != nil || covered {
		t.Fatalf("overextended coverage = %v, %v", covered, err)
	}
	if err := store.AdvanceCovered(ctx, Batch{JobID: job.ID, RangeStart: 13, RangeEnd: 14, NextArticle: 15}); err != nil {
		t.Fatalf("advance durable coverage: %v", err)
	}
	var next, committed, revision int64
	if err := db.Pool.QueryRow(ctx, "SELECT next_article_number, last_committed_article_number, revision FROM job_checkpoints WHERE job_id = $1", job.ID).Scan(&next, &committed, &revision); err != nil || next != 15 || committed != 14 || revision < 2 {
		t.Fatalf("checkpoint = %d, %d, %d, err=%v", next, committed, revision, err)
	}
}

func indexingTestSuffix(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(bytes)
}

func indexingTestURL(t *testing.T, rawURL, schema string) string {
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
