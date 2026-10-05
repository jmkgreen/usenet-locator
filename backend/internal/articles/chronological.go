package articles

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type ChronologicalLister interface {
	Chronological(context.Context, string, string, int) (SearchPage, error)
}
type PeriodChronologicalLister interface {
	ChronologicalPeriod(context.Context, string, *time.Time, *time.Time, string, int) (SearchPage, error)
}

// Chronological returns only already-stored, merged group headers. Its opaque
// cursor is the date/ID ordering key; it never reaches NNTP.
func (s Store) Chronological(ctx context.Context, group, cursor string, limit int) (SearchPage, error) {
	return s.ChronologicalPeriod(ctx, group, nil, nil, cursor, limit)
}

func (s Store) ChronologicalPeriod(ctx context.Context, group string, start, end *time.Time, cursor string, limit int) (SearchPage, error) {
	if group == "" || limit < 1 || limit > 100 {
		return SearchPage{}, fmt.Errorf("newsgroup and limit from 1 to 100 are required")
	}
	var date time.Time
	var id int64
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return SearchPage{}, fmt.Errorf("invalid chronological cursor")
		}
		parts := strings.Split(string(raw), "|")
		if len(parts) != 2 {
			return SearchPage{}, fmt.Errorf("invalid chronological cursor")
		}
		date, err = time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return SearchPage{}, fmt.Errorf("invalid chronological cursor")
		}
		id, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || id < 1 {
			return SearchPage{}, fmt.Errorf("invalid chronological cursor")
		}
	}
	args := []any{group}
	where := "g.name = lower($1) AND a.article_date IS NOT NULL"
	if start != nil {
		args = append(args, *start)
		where += fmt.Sprintf(" AND a.article_date >= $%d", len(args))
	}
	if end != nil {
		args = append(args, *end)
		where += fmt.Sprintf(" AND a.article_date < $%d", len(args))
	}
	var total int64
	if err := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM articles a JOIN article_newsgroups ag ON ag.article_id=a.id JOIN newsgroups g ON g.id=ag.newsgroup_id WHERE %s`, where), args...).Scan(&total); err != nil {
		return SearchPage{}, fmt.Errorf("count chronological headers: %w", err)
	}
	if cursor != "" {
		args = append(args, date, id)
		where += " AND (a.article_date, a.id) > ($2, $3)"
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT a.id,a.message_id,COALESCE(a.subject,''),COALESCE(a.author,''),a.article_date,COALESCE(p.unwanted,false) FROM articles a JOIN article_newsgroups ag ON ag.article_id=a.id JOIN newsgroups g ON g.id=ag.newsgroup_id LEFT JOIN article_preferences p ON p.article_id=a.id WHERE %s ORDER BY a.article_date ASC,a.id ASC LIMIT $%d`, where, len(args)), args...)
	if err != nil {
		return SearchPage{}, fmt.Errorf("list chronological headers: %w", err)
	}
	defer rows.Close()
	page := SearchPage{TotalRecords: total}
	for rows.Next() {
		var item SearchResult
		if err := rows.Scan(&item.ID, &item.MessageID, &item.Subject, &item.Author, &item.Date, &item.Unwanted); err != nil {
			return SearchPage{}, err
		}
		page.Articles = append(page.Articles, item)
	}
	if err := rows.Err(); err != nil {
		return SearchPage{}, err
	}
	if len(page.Articles) > limit {
		page.Articles = page.Articles[:limit]
		last := page.Articles[len(page.Articles)-1]
		page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(last.Date.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(last.ID, 10)))
	}
	return page, nil
}
