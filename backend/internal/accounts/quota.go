package accounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrQuotaExceeded = errors.New("account transfer quota exceeded")

// QuotaConsumer records bytes already transferred, including failed operations. A nil
// consumer is deliberately supported by callers used in narrow unit tests.
type QuotaConsumer interface {
	Consume(context.Context, string, int64) error
}

type Usage struct {
	AccountID          string
	TransferUsedBytes  int64
	TransferLimitBytes *int64
}

type UsageReader interface {
	ListUsage(context.Context) ([]Usage, error)
}

// QuotaStore records actual transferred bytes even if the last bounded
// operation crossed the limit. Subsequent operations must check availability.
type QuotaStore struct{ pool *pgxpool.Pool }

func NewQuotaStore(pool *pgxpool.Pool) QuotaStore { return QuotaStore{pool: pool} }

func (s QuotaStore) Consume(ctx context.Context, accountID string, bytes int64) error {
	if bytes < 0 {
		return fmt.Errorf("transfer bytes must not be negative")
	}
	if bytes == 0 {
		return nil
	}
	var used int64
	var limit *int64
	err := s.pool.QueryRow(ctx, `UPDATE provider_accounts
        SET transfer_used_bytes = transfer_used_bytes + $2, updated_at = now()
        WHERE id = $1 RETURNING transfer_used_bytes, transfer_limit_bytes`, accountID, bytes).Scan(&used, &limit)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrQuotaExceeded
	}
	if err != nil {
		return fmt.Errorf("record account transfer: %w", err)
	}
	if limit != nil && used > *limit {
		return ErrQuotaExceeded
	}
	return nil
}

// CheckAvailable prevents a new network operation once an account is at its
// limit. Concurrent bounded operations can still cross the limit together.
func CheckAvailable(ctx context.Context, quota QuotaConsumer, accountID string) error {
	reader, ok := quota.(UsageReader)
	if !ok {
		return nil
	}
	usage, err := reader.ListUsage(ctx)
	if err != nil {
		return err
	}
	for _, item := range usage {
		if item.AccountID == accountID {
			if item.TransferLimitBytes != nil && item.TransferUsedBytes >= *item.TransferLimitBytes {
				return ErrQuotaExceeded
			}
			return nil
		}
	}
	return ErrQuotaExceeded
}

func (s QuotaStore) ListUsage(ctx context.Context) ([]Usage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, transfer_used_bytes, transfer_limit_bytes FROM provider_accounts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list account quota usage: %w", err)
	}
	defer rows.Close()
	var usage []Usage
	for rows.Next() {
		var item Usage
		if err := rows.Scan(&item.AccountID, &item.TransferUsedBytes, &item.TransferLimitBytes); err != nil {
			return nil, fmt.Errorf("scan account quota usage: %w", err)
		}
		usage = append(usage, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account quota usage: %w", err)
	}
	return usage, nil
}
