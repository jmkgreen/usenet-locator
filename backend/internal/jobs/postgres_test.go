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
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
	"github.com/jmkgreen/usenet-locator/backend/internal/retention"
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

	// A running fan-out may be paused and later cancelled as a single operator
	// action; its children must change with the parent rather than being left
	// eligible for a worker to claim.
	pausableID, err := store.Create(ctx, CreateRequest{NewsgroupID: "comp.pause", EndpointID: "primary", StartDate: date, EndDate: date})
	if err != nil {
		t.Fatalf("create pausable fan-out: %v", err)
	}
	if _, claimed, err := store.ClaimNext(ctx); err != nil || !claimed {
		t.Fatalf("claim pausable child: claimed=%v err=%v", claimed, err)
	}
	if err := store.Transition(ctx, pausableID, Paused); err != nil {
		t.Fatalf("pause running fan-out: %v", err)
	}
	if parent, err := store.Get(ctx, pausableID); err != nil || parent.State != Paused || len(parent.ProviderJobs) != 2 || parent.ProviderJobs[0].State != Paused || parent.ProviderJobs[1].State != Paused {
		t.Fatalf("paused fan-out = %#v, err=%v", parent, err)
	}
	if err := store.Transition(ctx, pausableID, Cancelled); err != nil {
		t.Fatalf("cancel paused fan-out: %v", err)
	}
	if parent, err := store.Get(ctx, pausableID); err != nil || parent.State != Cancelled || parent.ProviderJobs[0].State != Cancelled || parent.ProviderJobs[1].State != Cancelled {
		t.Fatalf("cancelled fan-out = %#v, err=%v", parent, err)
	}

	// Restart recovery must make worker-owned running jobs visible for an
	// explicit operator resume rather than silently re-running them.
	recoverID, err := store.Create(ctx, CreateRequest{NewsgroupID: "comp.recover", EndpointID: "primary", StartDate: date, EndDate: date})
	if err != nil {
		t.Fatalf("create recovery fan-out: %v", err)
	}
	if _, claimed, err := store.ClaimNext(ctx); err != nil || !claimed {
		t.Fatalf("claim recovery child: claimed=%v err=%v", claimed, err)
	}
	if err := store.RecoverInterrupted(ctx); err != nil {
		t.Fatalf("recover interrupted jobs: %v", err)
	}
	if parent, err := store.Get(ctx, recoverID); err != nil || parent.State != Queued || len(parent.ProviderJobs) != 2 || parent.ProviderJobs[0].State != Interrupted || parent.ProviderJobs[1].State != Queued || parent.LastError == nil {
		t.Fatalf("recovered fan-out = %#v, err=%v", parent, err)
	}
}

func TestPostgresDiscoveryQueueAndRetentionPersistence(t *testing.T) {
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
	schema := "discovery_test_" + jobsTestSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	db, err := database.Open(ctx, jobsTestURL(t, rawURL, schema), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncConfiguration(ctx, config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: "user", PasswordFile: "pass", ConnectionLimit: 2}}, Endpoints: []config.EndpointConfig{{ID: "primary", AccountID: "account", Host: "one.example", Port: 563, TLS: true, Primary: true}, {ID: "secondary", AccountID: "account", Host: "two.example", Port: 563, TLS: true}}}); err != nil {
		t.Fatal(err)
	}
	store, observations := NewStore(db.Pool), retention.NewStore(db.Pool)
	id, err := store.CreateDiscovery(ctx, "Comp.New")
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Get(ctx, id)
	if err != nil || job.Kind != "retention_discovery" || job.Newsgroup != "comp.new" || job.State != Queued || len(job.ProviderJobs) != 2 || !job.StartDate.IsZero() {
		t.Fatalf("created=%#v err=%v", job, err)
	}
	date := time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		child, claimed, err := store.ClaimNext(ctx)
		if err != nil || !claimed || child.Kind != "retention_discovery" {
			t.Fatalf("claim=%#v %v %v", child, claimed, err)
		}
		item := retention.Observation{Endpoint: child.Endpoint, Newsgroup: child.Newsgroup, JobID: child.ID, TransferBytes: 42, GroupLow: 10, GroupHigh: 100, Outcome: "group_unavailable"}
		if child.Endpoint == "primary" {
			number := int64(11)
			item.Outcome = "found"
			item.ArticleNumber = &number
			item.ObservedDate = &date
			item.Article = &nntp.Overview{ArticleNumber: number, MessageID: "<old@test>", Date: date}
		}
		if _, err := observations.Record(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	job, err = store.Get(ctx, id)
	if err != nil || job.State != Failed || job.LastError == nil || job.TransferUsedBytes != 84 || len(job.ProviderJobs) != 2 {
		t.Fatalf("partial=%#v err=%v", job, err)
	}
	listed, err := store.List(ctx, 50)
	if err != nil || len(listed) != 1 || listed[0].ID != id {
		t.Fatalf("list=%#v err=%v", listed, err)
	}
	stored, err := observations.ListLatest(ctx, "comp.new")
	if err != nil || len(stored) != 2 {
		t.Fatalf("observations=%#v err=%v", stored, err)
	}
	if err := observations.StoreHeaders(ctx, "primary", "comp.new", nil, 50, 100); err != nil {
		t.Fatal(err)
	}
	number := int64(11)
	if _, err := observations.Record(ctx, retention.Observation{Endpoint: "primary", Newsgroup: "comp.new", GroupLow: 10, GroupHigh: 100, ArticleNumber: &number, Article: &nntp.Overview{ArticleNumber: number, MessageID: "<old@test>", Date: date}, Outcome: "found"}); err != nil {
		t.Fatal(err)
	}
	next, _, found, err := observations.Cursor(ctx, "primary", "comp.new")
	if err != nil || !found || next != 50 {
		t.Fatalf("cursor=%d found=%v err=%v", next, found, err)
	}
	cancelID, err := store.CreateDiscovery(ctx, "comp.cancel")
	if err != nil {
		t.Fatal(err)
	}
	child, claimed, err := store.ClaimNext(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if err := store.Transition(ctx, cancelID, Cancelled); err != nil {
		t.Fatal(err)
	}
	if _, err := observations.Record(ctx, retention.Observation{Endpoint: child.Endpoint, Newsgroup: child.Newsgroup, JobID: child.ID, Outcome: "no_header_in_probe"}); err == nil {
		t.Fatal("cancelled job persisted a discovery result")
	}
	job, err = store.Get(ctx, cancelID)
	if err != nil || job.State != Cancelled {
		t.Fatalf("cancelled=%#v err=%v", job, err)
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
