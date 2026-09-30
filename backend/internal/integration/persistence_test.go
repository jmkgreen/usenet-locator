package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/james/usenet-locator/backend/internal/accounts"
	"github.com/james/usenet-locator/backend/internal/articles"
	"github.com/james/usenet-locator/backend/internal/config"
	"github.com/james/usenet-locator/backend/internal/database"
	"github.com/james/usenet-locator/backend/internal/indexing"
	"github.com/james/usenet-locator/backend/internal/jobs"
	"github.com/james/usenet-locator/backend/internal/nntp"
)

func TestPersistenceWorkflow(t *testing.T) {
	baseURL := os.Getenv("USENET_LOCATOR_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("USENET_LOCATOR_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer admin.Close()
	schema := "integration_" + randomSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	db, err := database.Open(ctx, withSearchPath(t, baseURL, schema), 4)
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	limit := int64(10)
	cfg := config.Config{Database: config.DatabaseConfig{URLFromEnv: "USENET_LOCATOR_DATABASE_URL", MaxConns: 4}, Accounts: []config.AccountConfig{{ID: "account", UsernameFromEnv: "USENET_LOCATOR_NNTP_USERNAME", PasswordFromEnv: "USENET_LOCATOR_NNTP_PASSWORD", ConnectionLimit: 1, TransferLimitBytes: &limit}}, Endpoints: []config.EndpointConfig{{ID: "primary", AccountID: "account", Host: "news.example.test", Port: 563, TLS: true, Primary: true}}, Resources: config.ResourceConfig{ActiveJobs: 1, WorkersPerJob: 1, BatchSize: 10, MaxBodyBytes: 1024}}
	if err := db.SyncConfiguration(ctx, cfg); err != nil {
		t.Fatalf("sync configuration: %v", err)
	}
	quota := accounts.NewQuotaStore(db.Pool)
	if err := quota.Consume(ctx, "account", 7); err != nil {
		t.Fatalf("record quota use: %v", err)
	}
	if err := quota.Consume(ctx, "account", 4); !errors.Is(err, accounts.ErrQuotaExceeded) {
		t.Fatalf("quota overflow error = %v", err)
	}

	jobStore := jobs.NewStore(db.Pool)
	indexStore := indexing.NewStore(db.Pool)
	articleStore := articles.NewStore(db.Pool)
	date := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	jobID := createAndClaim(t, ctx, jobStore, "comp.integration", date)
	messageID := "<dedupe@example.test>"
	if _, err := indexStore.PersistBatch(ctx, indexing.Batch{JobID: jobID, RangeStart: 10, RangeEnd: 10, NextArticle: 11, HeadersRetrieved: 1, Overviews: []nntp.Overview{{ArticleNumber: 10, Subject: "integration subject", Author: "Alice", Date: date, RawDate: date.Format(time.RFC1123Z), MessageID: messageID, Bytes: 12, Lines: 1}}}); err != nil {
		t.Fatalf("persist first batch: %v", err)
	}
	covered, err := indexStore.IsRangeCovered(ctx, jobID, 10, 10)
	if err != nil || !covered {
		t.Fatalf("coverage = %v, err = %v", covered, err)
	}
	if err := jobStore.Complete(ctx, jobID); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	completed, err := jobStore.Get(ctx, jobID)
	if err != nil || completed.State != jobs.Completed {
		t.Fatalf("completed job = %#v, err = %v", completed, err)
	}

	secondJobID := createAndClaim(t, ctx, jobStore, "comp.integration.crosspost", date)
	if _, err := indexStore.PersistBatch(ctx, indexing.Batch{JobID: secondJobID, RangeStart: 20, RangeEnd: 20, NextArticle: 21, HeadersRetrieved: 1, Overviews: []nntp.Overview{{ArticleNumber: 20, Subject: "integration subject", Author: "Alice", Date: date, RawDate: date.Format(time.RFC1123Z), MessageID: messageID}}}); err != nil {
		t.Fatalf("persist crosspost batch: %v", err)
	}
	page, err := articleStore.Search(ctx, articles.SearchRequest{Newsgroup: "comp.integration", Limit: 10})
	if err != nil || len(page.Articles) != 1 || page.Articles[0].MessageID != messageID {
		t.Fatalf("search = %#v, err = %v", page, err)
	}
	detail, err := articleStore.GetDetail(ctx, page.Articles[0].ID)
	if err != nil || len(detail.Newsgroups) != 2 {
		t.Fatalf("detail = %#v, err = %v", detail, err)
	}
	if err := articleStore.SetUnwanted(ctx, []int64{detail.ID}, true); err != nil {
		t.Fatalf("mark unwanted: %v", err)
	}
	if _, err := articleStore.BodyTarget(ctx, detail.ID); err != articles.ErrUnwanted {
		t.Fatalf("body target error = %v, want unwanted", err)
	}
}

func createAndClaim(t *testing.T, ctx context.Context, store jobs.Store, group string, date time.Time) string {
	t.Helper()
	id, err := store.Create(ctx, jobs.CreateRequest{NewsgroupID: group, EndpointID: "primary", StartDate: date, EndDate: date, MarginDays: 0})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, ok, err := store.ClaimNext(ctx)
	if err != nil || !ok || claimed.ID != id {
		t.Fatalf("claim job = %#v, %v, %v", claimed, ok, err)
	}
	return id
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatalf("random schema name: %v", err)
	}
	return hex.EncodeToString(bytes)
}
func withSearchPath(t *testing.T, rawURL, schema string) string {
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
