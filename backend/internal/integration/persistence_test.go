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
	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/articles"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/database"
	"github.com/jmkgreen/usenet-locator/backend/internal/indexing"
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
	"github.com/jmkgreen/usenet-locator/backend/internal/qualification"
	"github.com/jmkgreen/usenet-locator/backend/internal/retention"
	"github.com/jmkgreen/usenet-locator/backend/internal/watchlist"
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
	cfg := config.Config{Database: config.DatabaseConfig{URLFile: "database-url", MaxConns: 4}, Accounts: []config.AccountConfig{{ID: "account", UsernameFile: "nntp-user", PasswordFile: "nntp-password", ConnectionLimit: 1, TransferLimitBytes: &limit}}, Endpoints: []config.EndpointConfig{{ID: "primary", AccountID: "account", Host: "news.example.test", Port: 563, TLS: true, Primary: true}}, Resources: config.ResourceConfig{ActiveJobs: 1, WorkersPerJob: 1, BatchSize: 10, MaxBodyBytes: 1024}}
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
	if err := articleStore.SetUnwanted(ctx, []int64{detail.ID}, false); err != nil {
		t.Fatalf("clear unwanted: %v", err)
	}
	target, err := articleStore.BodyTarget(ctx, detail.ID)
	if err != nil || target.ArticleID != detail.ID || target.EndpointID != "primary" {
		t.Fatalf("body target = %#v, err = %v", target, err)
	}
	if err := articleStore.SaveBody(ctx, detail.ID, "primary", ""); err == nil {
		t.Fatal("accepted an empty decoded body")
	}
	if err := articleStore.SaveBody(ctx, detail.ID, "primary", "saved text"); err != nil {
		t.Fatalf("save body: %v", err)
	}
	if body, err := articleStore.CachedBody(ctx, detail.ID); err != nil || body != "saved text" {
		t.Fatalf("cached body = %q, err = %v", body, err)
	}
	if refreshed, err := articleStore.GetDetail(ctx, detail.ID); err != nil || !refreshed.CachedBody {
		t.Fatalf("refreshed detail = %#v, err = %v", refreshed, err)
	}
	filtered, err := articleStore.Search(ctx, articles.SearchRequest{Newsgroup: "comp.integration", Subject: "subject", Author: "alice", MessageID: messageID, Limit: 1})
	if err != nil || len(filtered.Articles) != 1 || filtered.Articles[0].ID != detail.ID {
		t.Fatalf("filtered search = %#v, err = %v", filtered, err)
	}
	chronological, err := articleStore.Chronological(ctx, "comp.integration", "", 1)
	if err != nil || len(chronological.Articles) != 1 || chronological.Articles[0].ID != detail.ID {
		t.Fatalf("chronological = %#v, err = %v", chronological, err)
	}
	groups, err := articleStore.ListGroups(ctx)
	if err != nil || len(groups) < 2 {
		t.Fatalf("groups = %#v, err = %v", groups, err)
	}
	coverage, err := indexStore.ListCoverage(ctx, "comp.integration")
	if err != nil || len(coverage) != 1 || coverage[0].Endpoint != "primary" || coverage[0].State != "complete" {
		t.Fatalf("coverage = %#v, err = %v", coverage, err)
	}
	if usage, err := quota.ListUsage(ctx); err != nil || len(usage) != 1 || usage[0].TransferUsedBytes != 7 {
		t.Fatalf("quota usage = %#v, err = %v", usage, err)
	}
	qualificationStore := qualification.NewStore(db.Pool)
	if err := qualificationStore.Record(ctx, "primary", qualification.Result{Capabilities: []string{"READER", "LIST"}, OverviewFormatCode: 215, OverviewFields: []string{"subject", "date"}, OverviewCode: 224, OverviewRows: 1, OverviewDates: 1}); err != nil {
		t.Fatalf("record qualification: %v", err)
	}
	history, err := qualificationStore.List(ctx, "primary")
	if err != nil || len(history) != 1 || history[0].Result.OverviewCode != 224 || len(history[0].Result.OverviewFields) != 2 {
		t.Fatalf("qualification history = %#v, err = %v", history, err)
	}
	retentionStore := retention.NewStore(db.Pool)
	retained := nntp.Overview{ArticleNumber: 1, Subject: "retained", Author: "Alice", Date: date, MessageID: "<retained@example.test>"}
	number := retained.ArticleNumber
	recorded, err := retentionStore.Record(ctx, retention.Observation{Endpoint: "primary", Newsgroup: "comp.retention", GroupLow: 1, GroupHigh: 10, ArticleNumber: &number, Outcome: "found", Article: &retained})
	if err != nil || recorded.ArticleID == nil || recorded.Article != nil {
		t.Fatalf("retention record = %#v, err = %v", recorded, err)
	}
	if observations, err := retentionStore.ListLatest(ctx, "comp.retention"); err != nil || len(observations) != 1 || observations[0].Outcome != "found" {
		t.Fatalf("retention history = %#v, err = %v", observations, err)
	}
	if next, high, found, err := retentionStore.Cursor(ctx, "primary", "comp.retention"); err != nil || !found || next != 2 || high != 10 {
		t.Fatalf("retention cursor = %d, %d, %v, %v", next, high, found, err)
	}
	if err := retentionStore.StoreHeaders(ctx, "primary", "comp.retention", []nntp.Overview{{ArticleNumber: 2, Date: date, MessageID: "<retained-next@example.test>"}}, 3, 10); err != nil {
		t.Fatalf("store retained headers: %v", err)
	}
	watchlistStore := watchlist.NewStore(db.Pool)
	item, err := watchlistStore.Add(ctx, "Comp.Watched", 2)
	if err != nil || item.Newsgroup != "comp.watched" || item.IntervalHours != 2 {
		t.Fatalf("watchlist add = %#v, err = %v", item, err)
	}
	if due, err := watchlistStore.Due(ctx, time.Now().UTC()); err != nil || len(due) != 1 || due[0] != "comp.watched" {
		t.Fatalf("watchlist due = %#v, err = %v", due, err)
	}
	if err := watchlistStore.MarkChecked(ctx, "comp.watched", time.Now().UTC()); err != nil {
		t.Fatalf("mark watchlist checked: %v", err)
	}
	if items, err := watchlistStore.List(ctx); err != nil || len(items) != 1 || items[0].LastCheckedAt == nil {
		t.Fatalf("watchlist list = %#v, err = %v", items, err)
	}
	if err := watchlistStore.Remove(ctx, "comp.watched"); err != nil {
		t.Fatalf("remove watchlist item: %v", err)
	}
	transferBudget := int64(5)
	parentID, err := jobStore.Create(ctx, jobs.CreateRequest{NewsgroupID: "comp.lifecycle", EndpointID: "primary", StartDate: date, EndDate: date, MarginDays: 0, TransferLimitBytes: &transferBudget})
	if err != nil {
		t.Fatalf("create lifecycle job: %v", err)
	}
	child, claimed, err := jobStore.ClaimNext(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim lifecycle child = %#v, %v, %v", child, claimed, err)
	}
	if err := jobStore.ConsumeTransfer(ctx, child.ID, 3); err != nil {
		t.Fatalf("consume job transfer: %v", err)
	}
	if err := jobStore.ConsumeTransfer(ctx, child.ID, 3); !errors.Is(err, jobs.ErrTransferLimitExceeded) {
		t.Fatalf("transfer budget error = %v", err)
	}
	parent, err := jobStore.Get(ctx, parentID)
	if err != nil || parent.State != jobs.Running || len(parent.ProviderJobs) != 1 || parent.TransferUsedBytes != 6 {
		t.Fatalf("running parent = %#v, err = %v", parent, err)
	}
	if err := jobStore.Transition(ctx, parentID, jobs.Paused); err != nil {
		t.Fatalf("pause fan-out parent: %v", err)
	}
	if paused, err := jobStore.Get(ctx, child.ID); err != nil || paused.State != jobs.Paused {
		t.Fatalf("paused child = %#v, err = %v", paused, err)
	}
	if err := jobStore.Transition(ctx, parentID, jobs.Queued); err != nil {
		t.Fatalf("resume fan-out parent: %v", err)
	}
	if resumed, ok, err := jobStore.ClaimNext(ctx); err != nil || !ok || resumed.ID != child.ID {
		t.Fatalf("resumed child = %#v, %v, %v", resumed, ok, err)
	}
	if err := jobStore.Transition(ctx, parentID, jobs.Cancelled); err != nil {
		t.Fatalf("cancel fan-out parent: %v", err)
	}
	if cancelled, err := jobStore.Get(ctx, parentID); err != nil || cancelled.State != jobs.Cancelled {
		t.Fatalf("cancelled parent = %#v, err = %v", cancelled, err)
	}
	restartParent, err := jobStore.Create(ctx, jobs.CreateRequest{NewsgroupID: "comp.restart", EndpointID: "primary", StartDate: date, EndDate: date, MarginDays: 0})
	if err != nil {
		t.Fatalf("create restart job: %v", err)
	}
	restartChild, claimed, err := jobStore.ClaimNext(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim restart child = %#v, %v, %v", restartChild, claimed, err)
	}
	if err := jobStore.RecoverInterrupted(ctx); err != nil {
		t.Fatalf("recover interrupted jobs: %v", err)
	}
	if recovered, err := jobStore.Get(ctx, restartChild.ID); err != nil || recovered.State != jobs.Interrupted {
		t.Fatalf("recovered child = %#v, err = %v", recovered, err)
	}
	if recoveredParent, err := jobStore.Get(ctx, restartParent); err != nil || recoveredParent.State != jobs.Interrupted {
		t.Fatalf("recovered parent = %#v, err = %v", recoveredParent, err)
	}
	timeline := []nntp.Overview{
		{ArticleNumber: 30, MessageID: "<timeline-a@example.test>", Date: date},
		{ArticleNumber: 31, MessageID: "<timeline-b@example.test>", Date: date},
	}
	if err := retentionStore.StoreHeaders(ctx, "primary", "comp.timeline", timeline, 32, 32); err != nil {
		t.Fatalf("store chronological headers: %v", err)
	}
	firstPage, err := articleStore.Chronological(ctx, "comp.timeline", "", 1)
	if err != nil || len(firstPage.Articles) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("first chronological page = %#v, err = %v", firstPage, err)
	}
	secondPage, err := articleStore.Chronological(ctx, "comp.timeline", firstPage.NextCursor, 1)
	if err != nil || len(secondPage.Articles) != 1 || secondPage.Articles[0].ID == firstPage.Articles[0].ID {
		t.Fatalf("second chronological page = %#v, err = %v", secondPage, err)
	}
}

func createAndClaim(t *testing.T, ctx context.Context, store jobs.Store, group string, date time.Time) string {
	t.Helper()
	_, err := store.Create(ctx, jobs.CreateRequest{NewsgroupID: group, EndpointID: "primary", StartDate: date, EndDate: date, MarginDays: 0})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	claimed, ok, err := store.ClaimNext(ctx)
	if err != nil || !ok {
		t.Fatalf("claim job = %#v, %v, %v", claimed, ok, err)
	}
	// Create returns the fan-out parent. The dispatcher claims its provider
	// child, which is the job that owns checkpoints and persisted headers.
	return claimed.ID
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
