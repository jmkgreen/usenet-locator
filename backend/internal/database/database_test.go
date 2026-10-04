package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
)

func TestMigrationsAreOrderedAndNonEmpty(t *testing.T) {
	migrations, err := migrations()
	if err != nil {
		t.Fatalf("migrations() error = %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("migrations() returned no migrations")
	}
	for i, migration := range migrations {
		if migration.name == "" || migration.sql == "" {
			t.Fatalf("migration %d is incomplete: %#v", i, migration)
		}
		if i > 0 && migrations[i-1].name >= migration.name {
			t.Fatalf("migrations are not ordered: %q then %q", migrations[i-1].name, migration.name)
		}
	}
}

// This is a black-box acceptance test for the database boundary. It verifies
// that migrations and configuration reconciliation work against PostgreSQL,
// including the safety property that a removed endpoint cannot receive work.
func TestPostgresMigrateAndSynchronizeConfiguration(t *testing.T) {
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
	schema := "database_test_" + databaseTestSuffix(t)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	db, err := Open(ctx, databaseTestURL(t, rawURL, schema), 2)
	if err != nil {
		t.Fatalf("open application database: %v", err)
	}
	defer db.Close()
	if err := db.Ready(ctx); err != nil {
		t.Fatalf("database readiness: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}

	limit := int64(1024)
	initial := config.Config{
		Accounts: []config.AccountConfig{{ID: "account", UsernameFile: "username-secret", PasswordFile: "password-secret", ConnectionLimit: 1, TransferLimitBytes: &limit}},
		Endpoints: []config.EndpointConfig{
			{ID: "primary", AccountID: "account", Host: "primary.example.test", Port: 563, TLS: true, Primary: true},
			{ID: "retired", AccountID: "account", Host: "retired.example.test", Port: 563, TLS: true, Priority: 1},
		},
	}
	if err := db.SyncConfiguration(ctx, initial); err != nil {
		t.Fatalf("sync initial configuration: %v", err)
	}
	if err := db.SyncConfiguration(ctx, config.Config{Accounts: initial.Accounts, Endpoints: initial.Endpoints[:1]}); err != nil {
		t.Fatalf("sync reduced configuration: %v", err)
	}
	var enabled, primary bool
	if err := db.Pool.QueryRow(ctx, "SELECT enabled, is_primary FROM nntp_endpoints WHERE id = 'retired'").Scan(&enabled, &primary); err != nil {
		t.Fatalf("read retired endpoint: %v", err)
	}
	if enabled || primary {
		t.Fatalf("removed endpoint remained eligible: enabled=%v primary=%v", enabled, primary)
	}
	var usernameRef, passwordRef string
	if err := db.Pool.QueryRow(ctx, "SELECT username_secret_ref, password_secret_ref FROM provider_accounts WHERE id = 'account'").Scan(&usernameRef, &passwordRef); err != nil {
		t.Fatalf("read account references: %v", err)
	}
	if usernameRef != "username-secret" || passwordRef != "password-secret" {
		t.Fatalf("stored unexpected secret references: %q, %q", usernameRef, passwordRef)
	}
}

func databaseTestSuffix(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatalf("random schema name: %v", err)
	}
	return hex.EncodeToString(bytes)
}

func databaseTestURL(t *testing.T, rawURL, schema string) string {
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
