package database

import (
	"context"
	"fmt"

	"github.com/james/usenet-locator/backend/internal/config"
)

// SyncConfiguration persists non-secret account and endpoint metadata. It is
// idempotent and never stores resolved credential values.
func (db *DB) SyncConfiguration(ctx context.Context, cfg config.Config) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin configuration sync: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, account := range cfg.Accounts {
		_, err := tx.Exec(ctx, `INSERT INTO provider_accounts
            (id, username_secret_ref, password_secret_ref, connection_limit, transfer_limit_bytes)
            VALUES ($1, $2, $3, $4, $5)
            ON CONFLICT (id) DO UPDATE SET username_secret_ref = EXCLUDED.username_secret_ref,
            password_secret_ref = EXCLUDED.password_secret_ref, connection_limit = EXCLUDED.connection_limit,
            transfer_limit_bytes = EXCLUDED.transfer_limit_bytes, updated_at = now()`,
			account.ID, account.UsernameFromEnv, account.PasswordFromEnv, account.ConnectionLimit, account.TransferLimitBytes)
		if err != nil {
			return fmt.Errorf("sync account %q: %w", account.ID, err)
		}
	}
	// The partial unique index permits only one primary endpoint. Clear the old
	// selection before upserting a configuration that chooses another endpoint.
	if _, err := tx.Exec(ctx, "UPDATE nntp_endpoints SET is_primary = FALSE WHERE is_primary"); err != nil {
		return fmt.Errorf("clear previous primary endpoint: %w", err)
	}
	for _, endpoint := range cfg.Endpoints {
		_, err := tx.Exec(ctx, `INSERT INTO nntp_endpoints
            (id, account_id, host, port, tls_enabled, plaintext_acknowledged, is_primary, priority)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
            ON CONFLICT (id) DO UPDATE SET account_id = EXCLUDED.account_id, host = EXCLUDED.host,
            port = EXCLUDED.port, tls_enabled = EXCLUDED.tls_enabled,
            plaintext_acknowledged = EXCLUDED.plaintext_acknowledged,
            is_primary = EXCLUDED.is_primary, priority = EXCLUDED.priority, updated_at = now()`,
			endpoint.ID, endpoint.AccountID, endpoint.Host, endpoint.Port, endpoint.TLS, endpoint.PlaintextAcknowledged, endpoint.Primary, endpoint.Priority)
		if err != nil {
			return fmt.Errorf("sync endpoint %q: %w", endpoint.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit configuration sync: %w", err)
	}
	return nil
}
