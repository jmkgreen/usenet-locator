package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

func QuarantineOverview(ctx context.Context, tx pgx.Tx, overview nntp.Overview, endpoint string, groupID int64) error {
	source := NormalizeOverview(overview).Source
	_, err := tx.Exec(ctx, `INSERT INTO quarantined_overviews(endpoint_id,newsgroup_id,article_number,source_overview,reason)
		VALUES($1,$2,$3,$4::jsonb,'invalid_message_id')
		ON CONFLICT (endpoint_id,newsgroup_id,article_number) DO UPDATE SET last_seen_at=now()`, endpoint, groupID, overview.ArticleNumber, string(source))
	if err != nil {
		return fmt.Errorf("quarantine overview: %w", err)
	}
	return nil
}

type QuarantinedOverview struct {
	ID            int64           `json:"id"`
	Endpoint      string          `json:"endpoint"`
	Newsgroup     string          `json:"newsgroup"`
	ArticleNumber int64           `json:"article_number"`
	Reason        string          `json:"reason"`
	Source        json.RawMessage `json:"source_overview"`
}
type QuarantineStore struct{ pool *pgxpool.Pool }

func NewQuarantineStore(pool *pgxpool.Pool) QuarantineStore { return QuarantineStore{pool: pool} }
func (s QuarantineStore) Get(ctx context.Context, id int64) (QuarantinedOverview, error) {
	if id < 1 {
		return QuarantinedOverview{}, ErrArticleNotFound
	}
	var item QuarantinedOverview
	err := s.pool.QueryRow(ctx, `SELECT q.id,q.endpoint_id,g.name,q.article_number,q.reason,q.source_overview FROM quarantined_overviews q JOIN newsgroups g ON g.id=q.newsgroup_id WHERE q.id=$1`, id).Scan(&item.ID, &item.Endpoint, &item.Newsgroup, &item.ArticleNumber, &item.Reason, &item.Source)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuarantinedOverview{}, ErrArticleNotFound
	}
	if err != nil {
		return QuarantinedOverview{}, fmt.Errorf("read quarantined overview: %w", err)
	}
	return item, nil
}
