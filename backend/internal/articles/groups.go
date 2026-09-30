package articles

import (
	"context"
	"fmt"
)

type GroupCount struct {
	Name     string `json:"name"`
	Articles int64  `json:"articles"`
}
type GroupLister interface {
	ListGroups(context.Context) ([]GroupCount, error)
}

func (s Store) ListGroups(ctx context.Context) ([]GroupCount, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.name, count(ag.article_id) FROM newsgroups g
        LEFT JOIN article_newsgroups ag ON ag.newsgroup_id = g.id GROUP BY g.id, g.name ORDER BY g.name`)
	if err != nil {
		return nil, fmt.Errorf("list stored newsgroups: %w", err)
	}
	defer rows.Close()
	var result []GroupCount
	for rows.Next() {
		var item GroupCount
		if err := rows.Scan(&item.Name, &item.Articles); err != nil {
			return nil, fmt.Errorf("scan newsgroup count: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate newsgroup counts: %w", err)
	}
	return result, nil
}
