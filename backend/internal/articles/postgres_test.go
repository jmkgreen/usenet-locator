package articles

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
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/database"
	"github.com/jmkgreen/usenet-locator/backend/internal/indexing"
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

// This black-box acceptance test covers the stored-header journey: durable
// indexing, safe visibility preferences, cached-body access, filters, and
// cursor pagination. It does not contact a news server.
func TestPostgresStoredArticleJourney(t *testing.T) {
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
	schema := "articles_test_" + articlesTestSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	db, err := database.Open(ctx, articlesTestURL(t, rawURL, schema), 3)
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SyncConfiguration(ctx, config.Config{
		Accounts:  []config.AccountConfig{{ID: "account", UsernameFile: "user-ref", PasswordFile: "password-ref", ConnectionLimit: 1}},
		Endpoints: []config.EndpointConfig{{ID: "primary", AccountID: "account", Host: "news.example.test", Port: 563, TLS: true, Primary: true}},
	}); err != nil {
		t.Fatalf("sync configuration: %v", err)
	}

	jobStore := jobs.NewStore(db.Pool)
	date := time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC)
	parentID, err := jobStore.Create(ctx, jobs.CreateRequest{NewsgroupID: "comp.acceptance", EndpointID: "primary", StartDate: date, EndDate: date})
	if err != nil {
		t.Fatalf("create index job: %v", err)
	}
	job, claimed, err := jobStore.ClaimNext(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim index job = %#v, %v, %v", job, claimed, err)
	}
	if parent, err := jobStore.Get(ctx, parentID); err != nil || parent.State != jobs.Running {
		t.Fatalf("running parent = %#v, err = %v", parent, err)
	}
	indexStore := indexing.NewStore(db.Pool)
	batch := indexing.Batch{JobID: job.ID, RangeStart: 10, RangeEnd: 12, NextArticle: 13, HeadersRetrieved: 3, Overviews: []nntp.Overview{
		{ArticleNumber: 10, MessageID: "<first@example.test>", Subject: "First subject", Author: "Alice", Date: date, RawDate: date.Format(time.RFC1123Z), Bytes: 10, Lines: 1},
		{ArticleNumber: 11, MessageID: "invalid message id", Subject: "ignored", Date: date},
		{ArticleNumber: 12, MessageID: "<second@example.test>", Subject: "Second subject", Author: "Bob", Date: date.Add(24 * time.Hour), RawDate: date.Add(24 * time.Hour).Format(time.RFC1123Z)},
	}}
	result, err := indexStore.PersistBatch(ctx, batch)
	if err != nil || result.ArticlesStored != 2 || result.InvalidMessageID != 1 {
		t.Fatalf("persist batch = %#v, err = %v", result, err)
	}
	// A retry planner must be able to skip durable coverage without opening an
	// NNTP connection or writing duplicate articles.
	if err := indexStore.AdvanceCovered(ctx, indexing.Batch{JobID: job.ID, RangeStart: 10, RangeEnd: 12, NextArticle: 13, HeadersRetrieved: 3}); err != nil {
		t.Fatalf("advance known coverage: %v", err)
	}
	if err := jobStore.Complete(ctx, job.ID); err != nil {
		t.Fatalf("complete index job: %v", err)
	}

	store := NewStore(db.Pool)
	page, err := store.Search(ctx, SearchRequest{Newsgroup: "COMP.ACCEPTANCE", Subject: "subject", Limit: 1})
	if err != nil || len(page.Articles) != 1 || page.NextCursor == "" {
		t.Fatalf("first search page = %#v, err = %v", page, err)
	}
	secondPage, err := store.Search(ctx, SearchRequest{Newsgroup: "comp.acceptance", Cursor: page.NextCursor, Limit: 1})
	if err != nil || len(secondPage.Articles) != 1 || secondPage.Articles[0].ID == page.Articles[0].ID {
		t.Fatalf("second search page = %#v, err = %v", secondPage, err)
	}
	articleID := page.Articles[0].ID
	detail, err := store.GetDetail(ctx, articleID)
	if err != nil || detail.MessageID == "" || len(detail.Newsgroups) != 1 || detail.CachedBody {
		t.Fatalf("article detail = %#v, err = %v", detail, err)
	}
	if _, err := store.CachedBody(ctx, articleID); !errors.Is(err, ErrBodyNotCached) {
		t.Fatalf("uncached body error = %v", err)
	}
	if _, err := store.GetDetail(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing detail error = %v", err)
	}
	if err := store.SetUnwanted(ctx, []int64{articleID}, true); err != nil {
		t.Fatalf("mark unwanted: %v", err)
	}
	if _, err := store.BodyTarget(ctx, articleID); !errors.Is(err, ErrUnwanted) {
		t.Fatalf("unwanted body target error = %v", err)
	}
	hidden, err := store.Search(ctx, SearchRequest{Newsgroup: "comp.acceptance", Limit: 10})
	if err != nil || len(hidden.Articles) != 1 {
		t.Fatalf("ordinary search after unwanted mark = %#v, err = %v", hidden, err)
	}
	included, err := store.Search(ctx, SearchRequest{Newsgroup: "comp.acceptance", IncludeUnwanted: true, Limit: 10})
	if err != nil || len(included.Articles) != 2 {
		t.Fatalf("include-unwanted search = %#v, err = %v", included, err)
	}
	if err := store.SetUnwanted(ctx, []int64{articleID}, false); err != nil {
		t.Fatalf("clear unwanted: %v", err)
	}
	target, err := store.BodyTarget(ctx, articleID)
	if err != nil || target.EndpointID != "primary" || target.ArticleID != articleID {
		t.Fatalf("body target = %#v, err = %v", target, err)
	}
	if err := store.SaveBody(ctx, articleID, target.EndpointID, "cached article text"); err != nil {
		t.Fatalf("save body: %v", err)
	}
	if body, err := store.CachedBody(ctx, articleID); err != nil || body != "cached article text" {
		t.Fatalf("cached body = %q, err = %v", body, err)
	}
	if refreshed, err := store.GetDetail(ctx, articleID); err != nil || !refreshed.CachedBody {
		t.Fatalf("refreshed detail = %#v, err = %v", refreshed, err)
	}
	chronological, err := store.Chronological(ctx, "comp.acceptance", "", 1)
	if err != nil || len(chronological.Articles) != 1 || chronological.NextCursor == "" {
		t.Fatalf("chronological first page = %#v, err = %v", chronological, err)
	}
	if next, err := store.Chronological(ctx, "comp.acceptance", chronological.NextCursor, 1); err != nil || len(next.Articles) != 1 || next.Articles[0].ID == chronological.Articles[0].ID {
		t.Fatalf("chronological second page = %#v, err = %v", next, err)
	}
	groups, err := store.ListGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "comp.acceptance" || groups[0].Articles != 2 {
		t.Fatalf("stored groups = %#v, err = %v", groups, err)
	}
}

func articlesTestSuffix(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatalf("random schema name: %v", err)
	}
	return hex.EncodeToString(bytes)
}

func articlesTestURL(t *testing.T, rawURL, schema string) string {
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
