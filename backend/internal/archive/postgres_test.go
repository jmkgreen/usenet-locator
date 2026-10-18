package archive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/database"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

func TestPostgresVerificationIsReadOnlyAndPaginated(t *testing.T) {
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
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "archive_test_" + hex.EncodeToString(random[:])
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	db, err := database.Open(ctx, parsed.String(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncConfiguration(ctx, config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: "user", PasswordFile: "password", ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "endpoint", AccountID: "account", Host: "example.test", Port: 563, TLS: true}}}); err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO newsgroups(name) VALUES('comp.test');
		INSERT INTO articles(message_id, raw_date) VALUES('<one@test>', 'bad date'),('<ONE@test>', 'bad date');
		INSERT INTO article_locations(article_id, endpoint_id, newsgroup_id, article_number) SELECT id, 'endpoint', (SELECT id FROM newsgroups WHERE name='comp.test'), id FROM articles;
		INSERT INTO article_bodies(article_id, decoded_text) SELECT id, 'same text' FROM articles`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE article_locations SET article_id=(SELECT id FROM articles WHERE message_id='<ONE@test>') WHERE article_id=(SELECT id FROM articles WHERE message_id='<one@test>')`); err != nil {
		t.Fatal(err)
	}
	verifier := NewVerifier(db.Pool)
	for category := range verificationQueries {
		report, err := verifier.Check(ctx, category, "", 1)
		if err != nil {
			t.Fatalf("%s: %v", category, err)
		}
		if category == "location_conflicts" && len(report.Findings) != 1 {
			t.Fatalf("missing location conflict: %#v", report)
		}
		if category == "membership_inconsistencies" {
			if len(report.Findings) != 1 || report.NextCursor == "" {
				t.Fatalf("first page = %#v", report)
			}
			next, err := verifier.Check(ctx, category, report.NextCursor, 1)
			if err != nil || len(next.Findings) != 1 || next.Findings[0].Key == report.Findings[0].Key {
				t.Fatalf("second page = %#v, %v", next, err)
			}
			repeat, err := verifier.Check(ctx, category, "", 1)
			if err != nil || repeat.Findings[0] != report.Findings[0] {
				t.Fatalf("repeat = %#v, %v", repeat, err)
			}
		}
	}
	var count int
	if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM articles").Scan(&count); err != nil || count != 2 {
		t.Fatalf("article count=%d err=%v", count, err)
	}
	var groupID int64
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM newsgroups WHERE name='comp.test'`).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := QuarantineOverview(ctx, tx, nntp.Overview{ArticleNumber: 4, MessageID: "invalid", Subject: "source"}, "endpoint", groupID); err != nil {
		t.Fatal(err)
	}
	articleID, err := UpsertOverview(ctx, tx, nntp.Overview{ArticleNumber: 3, MessageID: "<rebuild@test>", Subject: "=?utf-8?Q?Re=3A_caf=C3=A9?=", Author: "Alice <alice@example.test>", RawDate: "Mon, 02 Jan 2006 15:04:05 -0700"}, "endpoint", groupID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var quarantineID int64
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM quarantined_overviews WHERE article_number=4`).Scan(&quarantineID); err != nil {
		t.Fatal(err)
	}
	quarantined, err := NewQuarantineStore(db.Pool).Get(ctx, quarantineID)
	if err != nil || quarantined.Endpoint != "endpoint" || quarantined.Newsgroup != "comp.test" || quarantined.Reason != "invalid_message_id" {
		t.Fatalf("quarantine=%#v err=%v", quarantined, err)
	}
	if _, err := NewQuarantineStore(db.Pool).Get(ctx, 999999); !errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("missing quarantined overview: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE articles SET subject='stale' WHERE id=$1`, articleID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO article_bodies(article_id,decoded_text,raw_headers,raw_body) VALUES($1,'stale',$2,$3)`, articleID, []byte("Content-Type: text/plain; charset=utf-8\n"), []byte("original body")); err != nil {
		t.Fatal(err)
	}
	reprocessor := NewReprocessor(db.Pool, 4096)
	var legacyID int64
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM articles WHERE message_id='<one@test>'`).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	if err := reprocessor.Reprocess(ctx, legacyID); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("legacy source error = %v", err)
	}
	if err := reprocessor.Reprocess(ctx, 999999); !errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("missing article error = %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := reprocessor.Reprocess(ctx, articleID); err != nil {
			t.Fatal(err)
		}
		var subject, key, text string
		var version int
		if err := db.Pool.QueryRow(ctx, `SELECT a.subject,a.normalized_subject,a.normalizer_version,b.decoded_text FROM articles a JOIN article_bodies b ON b.article_id=a.id WHERE a.id=$1`, articleID).Scan(&subject, &key, &version, &text); err != nil || subject != "Re: café" || key != "café" || version != NormalizerVersion || text != "original body" {
			t.Fatalf("reprocessed %q %q %d %q: %v", subject, key, version, text, err)
		}
	}
}
