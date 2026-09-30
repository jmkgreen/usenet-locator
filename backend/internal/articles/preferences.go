// Package articles owns local article decisions independently of NNTP data.
package articles

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("article not found")
var ErrInvalidSelection = errors.New("invalid article selection")

type PreferenceWriter interface {
	SetUnwanted(context.Context, []int64, bool) error
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) Store { return Store{pool: pool} }

// SetUnwanted atomically applies a local preference to existing canonical
// articles. The headers and cached body are deliberately retained.
func (s Store) SetUnwanted(ctx context.Context, articleIDs []int64, unwanted bool) error {
	if len(articleIDs) == 0 || len(articleIDs) > 1000 {
		return fmt.Errorf("%w: between 1 and 1000 article IDs are required", ErrInvalidSelection)
	}
	seen := make(map[int64]struct{}, len(articleIDs))
	for _, id := range articleIDs {
		if id <= 0 {
			return fmt.Errorf("%w: article IDs must be positive", ErrInvalidSelection)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("%w: article IDs must be unique", ErrInvalidSelection)
		}
		seen[id] = struct{}{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin article preference: %w", err)
	}
	defer tx.Rollback(ctx)
	var existing int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM articles WHERE id = ANY($1)", articleIDs).Scan(&existing); err != nil {
		return fmt.Errorf("validate article IDs: %w", err)
	}
	if existing != len(articleIDs) {
		return ErrNotFound
	}
	_, err = tx.Exec(ctx, `INSERT INTO article_preferences (article_id, unwanted, updated_at)
        SELECT id, $2, now() FROM articles WHERE id = ANY($1)
        ON CONFLICT (article_id) DO UPDATE SET unwanted = EXCLUDED.unwanted, updated_at = EXCLUDED.updated_at`, articleIDs, unwanted)
	if err != nil {
		return fmt.Errorf("set article preference: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit article preference: %w", err)
	}
	return nil
}
