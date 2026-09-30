package articles

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Detail struct {
	ID                                     int64
	MessageID, Subject, Author, References string
	Date                                   *time.Time
	Bytes, Lines                           *int64
	Newsgroups                             []string
	Unwanted                               bool
	CachedBody                             bool
}

type Detailer interface {
	GetDetail(context.Context, int64) (Detail, error)
}

// GetDetail returns stored metadata only. It never contacts NNTP or returns a
// cached body, so opening the header view is safe for unwanted articles too.
func (s Store) GetDetail(ctx context.Context, id int64) (Detail, error) {
	if id < 1 {
		return Detail{}, ErrNotFound
	}
	var detail Detail
	err := s.pool.QueryRow(ctx, `SELECT a.id, a.message_id, COALESCE(a.subject, ''), COALESCE(a.author, ''),
        COALESCE(a.references_header, ''), a.article_date, a.byte_count, a.line_count,
        COALESCE(p.unwanted, false), EXISTS(SELECT 1 FROM article_bodies b WHERE b.article_id = a.id)
        FROM articles a LEFT JOIN article_preferences p ON p.article_id = a.id WHERE a.id = $1`, id).Scan(
		&detail.ID, &detail.MessageID, &detail.Subject, &detail.Author, &detail.References, &detail.Date,
		&detail.Bytes, &detail.Lines, &detail.Unwanted, &detail.CachedBody)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("get article detail: %w", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT g.name FROM article_newsgroups ag JOIN newsgroups g ON g.id = ag.newsgroup_id WHERE ag.article_id = $1 ORDER BY g.name`, id)
	if err != nil {
		return Detail{}, fmt.Errorf("get article newsgroups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var group string
		if err := rows.Scan(&group); err != nil {
			return Detail{}, fmt.Errorf("scan article newsgroup: %w", err)
		}
		detail.Newsgroups = append(detail.Newsgroups, group)
	}
	if err := rows.Err(); err != nil {
		return Detail{}, fmt.Errorf("iterate article newsgroups: %w", err)
	}
	return detail, nil
}
