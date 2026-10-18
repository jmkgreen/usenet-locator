package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

var ErrSourceUnavailable = errors.New("original overview source is unavailable")
var ErrArticleNotFound = errors.New("article not found")

type Reprocessor struct {
	pool         *pgxpool.Pool
	maxBodyBytes int64
}

func NewReprocessor(pool *pgxpool.Pool, maxBodyBytes int64) Reprocessor {
	return Reprocessor{pool: pool, maxBodyBytes: maxBodyBytes}
}

// Reprocess derives current values from retained source bytes in one
// transaction. It never fetches from a provider or changes article identity.
func (r Reprocessor) Reprocess(ctx context.Context, id int64) error {
	if id < 1 {
		return ErrArticleNotFound
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reprocessing: %w", err)
	}
	defer tx.Rollback(ctx)
	var source []byte
	var existingBytes, existingLines *int64
	err = tx.QueryRow(ctx, `SELECT source_overview, byte_count, line_count FROM articles WHERE id=$1 FOR UPDATE`, id).Scan(&source, &existingBytes, &existingLines)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArticleNotFound
	}
	if err != nil {
		return fmt.Errorf("load article source: %w", err)
	}
	if len(source) == 0 {
		return ErrSourceUnavailable
	}
	var fields map[string][]byte
	if err := json.Unmarshal(source, &fields); err != nil {
		return fmt.Errorf("read article source: %w", err)
	}
	overview := nntp.Overview{Subject: string(fields["subject"]), Author: string(fields["author"]), RawDate: string(fields["date"]), MessageID: string(fields["message_id"]), References: string(fields["references"])}
	if value, err := mail.ParseDate(overview.RawDate); err == nil {
		overview.Date = value
	}
	if existingBytes != nil {
		overview.Bytes = *existingBytes
	}
	if existingLines != nil {
		overview.Lines = *existingLines
	}
	if value, err := strconv.ParseInt(string(fields["bytes"]), 10, 64); err == nil {
		overview.Bytes = value
	}
	if value, err := strconv.ParseInt(string(fields["lines"]), 10, 64); err == nil {
		overview.Lines = value
	}
	item := NormalizeOverview(overview)
	var date *time.Time
	if !item.Overview.Date.IsZero() {
		value := item.Overview.Date.UTC()
		date = &value
	}
	_, err = tx.Exec(ctx, `UPDATE articles SET subject=$2, author=$3, article_date=$4, raw_date=NULLIF($5,''), references_header=NULLIF($6,''), byte_count=NULLIF($7,0), line_count=NULLIF($8,0), normalizer_version=$9, normalized_subject=$10, quality_flags=$11, updated_at=now() WHERE id=$1`, id, item.Overview.Subject, item.Overview.Author, date, item.Overview.RawDate, item.Overview.References, item.Overview.Bytes, item.Overview.Lines, NormalizerVersion, item.SubjectKey, item.Flags)
	if err != nil {
		return fmt.Errorf("update normalized article: %w", err)
	}
	var headers, body []byte
	err = tx.QueryRow(ctx, `SELECT raw_headers, raw_body FROM article_bodies WHERE article_id=$1 FOR UPDATE`, id).Scan(&headers, &body)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load body source: %w", err)
	}
	if err == nil && body != nil {
		if r.maxBodyBytes < 1 {
			return fmt.Errorf("body normalization limit is unavailable")
		}
		reader := DecodeReader(headers, body, r.maxBodyBytes)
		parts, err := json.Marshal(reader.Parts)
		if err != nil {
			return fmt.Errorf("encode MIME parts: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE article_bodies SET decoded_text=$2, sanitized_html=$3, mime_summary=$4::jsonb, normalizer_version=$5, quality_flags=$6 WHERE article_id=$1`, id, reader.Text, reader.HTML, string(parts), NormalizerVersion, reader.Flags); err != nil {
			return fmt.Errorf("update normalized body: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reprocessing: %w", err)
	}
	return nil
}
