package articles

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type SearchRequest struct {
	Subject, Author, MessageID, Newsgroup string
	Start, End                            *time.Time // End is exclusive.
	IncludeUnwanted                       bool
	Limit                                 int
	Cursor                                string
}

type SearchResult struct {
	ID        int64      `json:"id"`
	MessageID string     `json:"message_id"`
	Subject   string     `json:"subject"`
	Author    string     `json:"author"`
	Date      *time.Time `json:"date"`
	Unwanted  bool       `json:"unwanted"`
}

type SearchPage struct {
	Articles   []SearchResult
	NextCursor string
}

type Searcher interface {
	Search(context.Context, SearchRequest) (SearchPage, error)
}

func (s Store) Search(ctx context.Context, request SearchRequest) (SearchPage, error) {
	request = request.withDefaults()
	if err := request.validate(); err != nil {
		return SearchPage{}, err
	}
	lastID, err := decodeCursor(request.Cursor)
	if err != nil {
		return SearchPage{}, err
	}
	query := strings.Builder{}
	query.WriteString(`SELECT a.id, a.message_id, COALESCE(a.subject, ''), COALESCE(a.author, ''), a.article_date,
        COALESCE(p.unwanted, false) FROM articles a LEFT JOIN article_preferences p ON p.article_id = a.id WHERE true`)
	args := make([]any, 0, 8)
	add := func(condition string, value any) {
		args = append(args, value)
		query.WriteString(" AND ")
		query.WriteString(fmt.Sprintf(condition, len(args)))
	}
	if request.Subject != "" {
		add("a.subject ILIKE '%%' || $%d || '%%'", request.Subject)
	}
	if request.Author != "" {
		add("a.author ILIKE '%%' || $%d || '%%'", request.Author)
	}
	if request.MessageID != "" {
		add("a.message_id = $%d", request.MessageID)
	}
	if request.Newsgroup != "" {
		add("EXISTS (SELECT 1 FROM article_newsgroups ag JOIN newsgroups g ON g.id = ag.newsgroup_id WHERE ag.article_id = a.id AND g.name = lower($%d))", request.Newsgroup)
	}
	if request.Start != nil {
		add("a.article_date >= $%d", *request.Start)
	}
	if request.End != nil {
		add("a.article_date < $%d", *request.End)
	}
	if !request.IncludeUnwanted {
		query.WriteString(" AND COALESCE(p.unwanted, false) = false")
	}
	if lastID > 0 {
		add("a.id < $%d", lastID)
	}
	args = append(args, request.Limit+1)
	query.WriteString(fmt.Sprintf(" ORDER BY a.id DESC LIMIT $%d", len(args)))
	rows, err := s.pool.Query(ctx, query.String(), args...)
	if err != nil {
		return SearchPage{}, fmt.Errorf("search articles: %w", err)
	}
	defer rows.Close()
	page := SearchPage{Articles: make([]SearchResult, 0, request.Limit)}
	for rows.Next() {
		var result SearchResult
		if err := rows.Scan(&result.ID, &result.MessageID, &result.Subject, &result.Author, &result.Date, &result.Unwanted); err != nil {
			return SearchPage{}, fmt.Errorf("scan article result: %w", err)
		}
		page.Articles = append(page.Articles, result)
	}
	if err := rows.Err(); err != nil {
		return SearchPage{}, fmt.Errorf("iterate article results: %w", err)
	}
	if len(page.Articles) > request.Limit {
		page.Articles = page.Articles[:request.Limit]
		page.NextCursor = encodeCursor(page.Articles[len(page.Articles)-1].ID)
	}
	return page, nil
}

func (r SearchRequest) validate() error {
	if r.Limit < 1 || r.Limit > 100 {
		return fmt.Errorf("search limit must be between 1 and 100")
	}
	if r.Start != nil && r.End != nil && !r.Start.Before(*r.End) {
		return fmt.Errorf("start date must precede end date")
	}
	return nil
}

func (r SearchRequest) withDefaults() SearchRequest {
	if r.Limit == 0 {
		r.Limit = 50
	}
	return r
}
func encodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}
func decodeCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	value, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid search cursor")
	}
	id, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid search cursor")
	}
	return id, nil
}
