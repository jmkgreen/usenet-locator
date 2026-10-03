// Package watchlist persists the newsgroups that should receive recurring,
// bounded retention checks. It never stores NNTP credentials or responses.
package watchlist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Item struct {
	Newsgroup     string     `json:"newsgroup"`
	IntervalHours int        `json:"interval_hours"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	NextCheckAt   time.Time  `json:"next_check_at"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) Store { return Store{pool: pool} }

func (s Store) List(ctx context.Context) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.name, w.interval_hours, w.last_checked_at,
        COALESCE(w.last_checked_at + make_interval(hours => w.interval_hours), w.created_at)
        FROM newsgroup_watchlist w JOIN newsgroups g ON g.id = w.newsgroup_id ORDER BY g.name`)
	if err != nil {
		return nil, fmt.Errorf("list watchlist: %w", err)
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.Newsgroup, &item.IntervalHours, &item.LastCheckedAt, &item.NextCheckAt); err != nil {
			return nil, fmt.Errorf("read watchlist: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list watchlist: %w", err)
	}
	return items, nil
}

func (s Store) Add(ctx context.Context, group string, intervalHours int) (Item, error) {
	group = strings.ToLower(strings.TrimSpace(group))
	if group == "" || strings.Contains(group, "/") || intervalHours < 1 || intervalHours > 168 {
		return Item{}, fmt.Errorf("newsgroup and an interval from 1 to 168 hours are required")
	}
	var item Item
	err := s.pool.QueryRow(ctx, `WITH group_row AS (
            INSERT INTO newsgroups (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id
        ), watch AS (
            INSERT INTO newsgroup_watchlist (newsgroup_id, interval_hours) SELECT id, $2 FROM group_row
            ON CONFLICT (newsgroup_id) DO UPDATE SET interval_hours = EXCLUDED.interval_hours, updated_at = now()
            RETURNING interval_hours, last_checked_at, created_at
        ) SELECT $1, interval_hours, last_checked_at, COALESCE(last_checked_at + make_interval(hours => interval_hours), created_at) FROM watch`, group, intervalHours).Scan(&item.Newsgroup, &item.IntervalHours, &item.LastCheckedAt, &item.NextCheckAt)
	if err != nil {
		return Item{}, fmt.Errorf("save watchlist item: %w", err)
	}
	return item, nil
}

func (s Store) Remove(ctx context.Context, group string) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM newsgroup_watchlist w USING newsgroups g WHERE w.newsgroup_id = g.id AND g.name = lower($1)`, group)
	if err != nil {
		return fmt.Errorf("remove watchlist item: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) Due(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.name FROM newsgroup_watchlist w JOIN newsgroups g ON g.id = w.newsgroup_id
        WHERE w.last_checked_at IS NULL OR w.last_checked_at + make_interval(hours => w.interval_hours) <= $1 ORDER BY g.name`, now)
	if err != nil {
		return nil, fmt.Errorf("list due watchlist items: %w", err)
	}
	defer rows.Close()
	var groups []string
	for rows.Next() {
		var group string
		if err := rows.Scan(&group); err != nil {
			return nil, fmt.Errorf("read due watchlist item: %w", err)
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s Store) MarkChecked(ctx context.Context, group string, checkedAt time.Time) error {
	command, err := s.pool.Exec(ctx, `UPDATE newsgroup_watchlist w SET last_checked_at = $2, updated_at = now() FROM newsgroups g WHERE w.newsgroup_id = g.id AND g.name = lower($1)`, group, checkedAt)
	if err != nil {
		return fmt.Errorf("mark watchlist checked: %w", err)
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
