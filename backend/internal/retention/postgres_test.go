package retention

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/database"
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

func TestPostgresDiscoveryWorkerPersistsResultsAndHonoursCancellation(t *testing.T) {
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
	schema := "retention_worker_" + hex.EncodeToString(random)
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
	dir := t.TempDir()
	username, password := filepath.Join(dir, "user"), filepath.Join(dir, "pass")
	if err := os.WriteFile(username, []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(password, []byte("pass"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "a", UsernameFile: username, PasswordFile: password, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "p", AccountID: "a", Host: "news.example", Port: 563, TLS: true, Primary: true}}}
	if err := db.SyncConfiguration(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	jobStore, observations := jobs.NewStore(db.Pool), NewStore(db.Pool)
	service := New(cfg, observations, accounts.NewGuard(cfg), accounts.NewQuotaStore(db.Pool))
	client := &probeClient{transfer: 55}
	service.dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return client, nil }
	id, err := jobStore.CreateDiscovery(ctx, "comp.new")
	if err != nil {
		t.Fatal(err)
	}
	child, claimed, err := jobStore.ClaimNext(ctx)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	result, err := service.ProbeProvider(ctx, child.ID, child.Endpoint, child.Newsgroup)
	if err != nil || result.Outcome != "found" || result.ArticleID == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	job, err := jobStore.Get(ctx, id)
	if err != nil || job.State != jobs.Completed || job.HeadersRetrieved != 1 || job.TransferUsedBytes != 55 {
		t.Fatalf("job=%#v err=%v", job, err)
	}
	if next, _, found, err := observations.Cursor(ctx, "p", "comp.new"); err != nil || !found || next != 12 {
		t.Fatalf("cursor=%d found=%v err=%v", next, found, err)
	}
	if items, err := observations.ListLatest(ctx, "comp.new"); err != nil || len(items) != 1 || items[0].NextArticle == nil || *items[0].NextArticle != 12 {
		t.Fatalf("observations=%#v err=%v", items, err)
	}
	client.overviews = []nntp.Overview{{ArticleNumber: 13, MessageID: "<next-two@test>"}, {ArticleNumber: 12, MessageID: "<next-one@test>"}}
	if err := service.RetrieveNext(ctx, "comp.new", 1); err != nil {
		t.Fatal(err)
	}
	if next, _, found, err := observations.Cursor(ctx, "p", "comp.new"); err != nil || !found || next != 13 {
		t.Fatalf("first batch cursor=%d found=%v err=%v", next, found, err)
	}
	if err := service.RetrieveNext(ctx, "comp.new", 1); err != nil {
		t.Fatal(err)
	}
	if next, _, found, err := observations.Cursor(ctx, "p", "comp.new"); err != nil || !found || next != 14 {
		t.Fatalf("second batch cursor=%d found=%v err=%v", next, found, err)
	}
	var total int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM article_newsgroups ag JOIN newsgroups g ON g.id=ag.newsgroup_id WHERE g.name='comp.new'`).Scan(&total); err != nil || total != 3 {
		t.Fatalf("stored headers=%d err=%v", total, err)
	}
	emptyID, err := jobStore.CreateDiscovery(ctx, "comp.empty")
	if err != nil {
		t.Fatal(err)
	}
	child, claimed, err = jobStore.ClaimNext(ctx)
	if err != nil || !claimed {
		t.Fatalf("empty claim=%v err=%v", claimed, err)
	}
	client.overviews = []nntp.Overview{}
	result, err = service.ProbeProvider(ctx, child.ID, child.Endpoint, child.Newsgroup)
	if err != nil || result.Outcome != "no_header_in_probe" || result.ArticleID != nil {
		t.Fatalf("empty=%#v err=%v", result, err)
	}
	job, err = jobStore.Get(ctx, emptyID)
	if err != nil || job.State != jobs.Completed {
		t.Fatalf("empty job=%#v err=%v", job, err)
	}
	cancelID, err := jobStore.CreateDiscovery(ctx, "comp.cancel")
	if err != nil {
		t.Fatal(err)
	}
	child, claimed, err = jobStore.ClaimNext(ctx)
	if err != nil || !claimed {
		t.Fatalf("cancel claim=%v err=%v", claimed, err)
	}
	if err := jobStore.Transition(ctx, cancelID, jobs.Cancelled); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProbeProvider(ctx, child.ID, child.Endpoint, child.Newsgroup); err == nil {
		t.Fatal("cancelled discovery ran")
	}
	if items, err := observations.ListLatest(ctx, "comp.cancel"); err != nil || len(items) != 0 {
		t.Fatalf("cancel observations=%#v err=%v", items, err)
	}
}
