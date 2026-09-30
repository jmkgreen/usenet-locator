package indexing

import (
	"context"
	"fmt"
	"time"
)

type Coverage struct {
	Endpoint           string     `json:"endpoint"`
	Newsgroup          string     `json:"newsgroup"`
	State              string     `json:"state"`
	ArticleNumberStart int64      `json:"article_number_start"`
	ArticleNumberEnd   int64      `json:"article_number_end"`
	ObservedStart      *time.Time `json:"observed_start"`
	ObservedEnd        *time.Time `json:"observed_end"`
	Reason             *string    `json:"reason"`
}
type CoverageLister interface {
	ListCoverage(context.Context, string) ([]Coverage, error)
}

func (s Store) ListCoverage(ctx context.Context, newsgroup string) ([]Coverage, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.endpoint_id, g.name, c.state, c.article_number_start, c.article_number_end,
        c.observed_start_date, c.observed_end_date, c.reason FROM scan_coverage c JOIN newsgroups g ON g.id = c.newsgroup_id
        WHERE ($1 = '' OR g.name = lower($1)) ORDER BY g.name, c.endpoint_id, c.article_number_start`, newsgroup)
	if err != nil {
		return nil, fmt.Errorf("list scan coverage: %w", err)
	}
	defer rows.Close()
	var result []Coverage
	for rows.Next() {
		var item Coverage
		if err := rows.Scan(&item.Endpoint, &item.Newsgroup, &item.State, &item.ArticleNumberStart, &item.ArticleNumberEnd, &item.ObservedStart, &item.ObservedEnd, &item.Reason); err != nil {
			return nil, fmt.Errorf("scan coverage: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan coverage: %w", err)
	}
	return result, nil
}
