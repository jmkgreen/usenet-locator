package archive

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

// UpsertOverview keeps the first canonical source and its derived values. A
// later provider observation adds its location without rewriting that evidence.
func UpsertOverview(ctx context.Context, tx pgx.Tx, overview nntp.Overview, endpoint string, groupID int64) (int64, error) {
	item := NormalizeOverview(overview)
	var date *time.Time
	if !item.Overview.Date.IsZero() {
		value := item.Overview.Date.UTC()
		date = &value
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO articles (message_id, subject, author, article_date, raw_date, references_header, byte_count, line_count, source_overview, normalizer_version, normalized_subject, quality_flags, source_endpoint_id, source_newsgroup_id, source_article_number)
        VALUES ($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,0),NULLIF($8,0),$9::jsonb,$10,$11,$12,$13,$14,$15)
        ON CONFLICT (message_id) DO UPDATE SET updated_at = now() RETURNING id`,
		item.Overview.MessageID, item.Overview.Subject, item.Overview.Author, date, item.Overview.RawDate, item.Overview.References,
		item.Overview.Bytes, item.Overview.Lines, string(item.Source), NormalizerVersion, item.SubjectKey, item.Flags, endpoint, groupID, overview.ArticleNumber).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert article: %w", err)
	}
	return id, nil
}
