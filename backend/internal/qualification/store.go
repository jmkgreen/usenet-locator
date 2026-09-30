package qualification

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type History struct {
	Endpoint  string    `json:"endpoint"`
	Result    Result    `json:"result"`
	CreatedAt time.Time `json:"created_at"`
}
type HistoryStore interface {
	Record(context.Context, string, Result) error
	List(context.Context, string) ([]History, error)
}
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) Store { return Store{pool: pool} }
func (s Store) Record(ctx context.Context, endpoint string, result Result) error {
	caps, _ := json.Marshal(result.Capabilities)
	fields, _ := json.Marshal(result.OverviewFields)
	_, err := s.pool.Exec(ctx, `INSERT INTO endpoint_qualifications (endpoint_id, capabilities, overview_format_code, overview_fields, stat_code, group_selected, overview_code, overview_rows, overview_dates) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, endpoint, caps, result.OverviewFormatCode, fields, result.StatCode, result.GroupSelected, result.OverviewCode, result.OverviewRows, result.OverviewDates)
	if err != nil {
		return fmt.Errorf("record qualification: %w", err)
	}
	return nil
}
func (s Store) List(ctx context.Context, endpoint string) ([]History, error) {
	rows, err := s.pool.Query(ctx, `SELECT endpoint_id, capabilities, overview_format_code, overview_fields, stat_code, group_selected, overview_code, overview_rows, overview_dates, created_at FROM endpoint_qualifications WHERE endpoint_id=$1 ORDER BY created_at DESC LIMIT 20`, endpoint)
	if err != nil {
		return nil, fmt.Errorf("list qualifications: %w", err)
	}
	defer rows.Close()
	out := make([]History, 0)
	for rows.Next() {
		var h History
		var caps, fields []byte
		if err := rows.Scan(&h.Endpoint, &caps, &h.Result.OverviewFormatCode, &fields, &h.Result.StatCode, &h.Result.GroupSelected, &h.Result.OverviewCode, &h.Result.OverviewRows, &h.Result.OverviewDates, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan qualification: %w", err)
		}
		_ = json.Unmarshal(caps, &h.Result.Capabilities)
		_ = json.Unmarshal(fields, &h.Result.OverviewFields)
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate qualifications: %w", err)
	}
	return out, nil
}
