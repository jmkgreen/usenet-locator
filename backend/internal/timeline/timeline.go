// Package timeline aggregates endpoint-local evidence into an honest group
// calendar. It never turns an endpoint scan into a claim about Usenet as a
// whole.
package timeline

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
)

type EndpointState struct {
	Endpoint string `json:"endpoint"`
	State    string `json:"state"`
}
type Period struct {
	Level        string          `json:"level"`
	StartDate    string          `json:"start_date"`
	EndDate      string          `json:"end_date"`
	ArticleCount int64           `json:"article_count"`
	State        string          `json:"state"` // complete, pending, or gaps
	Endpoints    []EndpointState `json:"endpoints"`
}

type Store struct {
	pool    *pgxpool.Pool
	creator jobs.Creator
}

func NewStore(pool *pgxpool.Pool, creator jobs.Creator) Store {
	return Store{pool: pool, creator: creator}
}

// List returns known years, or the complete set of child months/days under an
// explicitly selected UTC parent. This makes empty units actionable after a
// date jump while avoiding an unbounded full-calendar UI.
func (s Store) List(ctx context.Context, group, level string, start *time.Time) ([]Period, error) {
	group = strings.ToLower(strings.TrimSpace(group))
	if group == "" {
		return nil, fmt.Errorf("newsgroup is required")
	}
	var begins []time.Time
	switch level {
	case "year":
		if start != nil {
			begins = []time.Time{time.Date(start.Year(), 1, 1, 0, 0, 0, 0, time.UTC)}
		} else {
			rows, err := s.pool.Query(ctx, `SELECT DISTINCT date_trunc('year', value)::date FROM (
                SELECT a.article_date AS value FROM articles a JOIN article_newsgroups ag ON ag.article_id=a.id JOIN newsgroups g ON g.id=ag.newsgroup_id WHERE g.name=lower($1) AND a.article_date IS NOT NULL
                UNION SELECT j.requested_start_date::timestamptz FROM index_jobs j JOIN newsgroups g ON g.id=j.newsgroup_id WHERE g.name=lower($1)
                UNION SELECT j.requested_end_date::timestamptz FROM index_jobs j JOIN newsgroups g ON g.id=j.newsgroup_id WHERE g.name=lower($1)
            ) periods ORDER BY 1`, group)
			if err != nil {
				return nil, fmt.Errorf("list known years: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var item time.Time
				if err := rows.Scan(&item); err != nil {
					return nil, err
				}
				begins = append(begins, item.UTC())
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	case "month":
		if start == nil {
			return nil, fmt.Errorf("year start is required for months")
		}
		for month := time.January; month <= time.December; month++ {
			begins = append(begins, time.Date(start.Year(), month, 1, 0, 0, 0, 0, time.UTC))
		}
	case "day":
		if start == nil {
			return nil, fmt.Errorf("month start is required for days")
		}
		first := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
		for day := first; day.Month() == first.Month(); day = day.AddDate(0, 0, 1) {
			begins = append(begins, day)
		}
	default:
		return nil, fmt.Errorf("level must be year, month, or day")
	}
	result := make([]Period, 0, len(begins))
	for _, begin := range begins {
		period, err := s.period(ctx, group, level, begin)
		if err != nil {
			return nil, err
		}
		result = append(result, period)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartDate < result[j].StartDate })
	return result, nil
}

func endFor(level string, start time.Time) time.Time {
	switch level {
	case "year":
		return start.AddDate(1, 0, 0)
	case "month":
		return start.AddDate(0, 1, 0)
	default:
		return start.AddDate(0, 0, 1)
	}
}

func (s Store) period(ctx context.Context, group, level string, begin time.Time) (Period, error) {
	end := endFor(level, begin)
	p := Period{Level: level, StartDate: begin.Format("2006-01-02"), EndDate: end.AddDate(0, 0, -1).Format("2006-01-02")}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM articles a JOIN article_newsgroups ag ON ag.article_id=a.id JOIN newsgroups g ON g.id=ag.newsgroup_id WHERE g.name=lower($1) AND a.article_date >= $2 AND a.article_date < $3`, group, begin, end).Scan(&p.ArticleCount); err != nil {
		return p, fmt.Errorf("count timeline articles: %w", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT e.id FROM nntp_endpoints e JOIN provider_accounts a ON a.id=e.account_id WHERE e.enabled AND a.enabled ORDER BY e.priority,e.id`)
	if err != nil {
		return p, fmt.Errorf("list enabled endpoints: %w", err)
	}
	defer rows.Close()
	allComplete := true
	pending := false
	for rows.Next() {
		var endpoint string
		if err := rows.Scan(&endpoint); err != nil {
			return p, err
		}
		var complete, active bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM index_jobs j JOIN newsgroups g ON g.id=j.newsgroup_id WHERE g.name=lower($1) AND j.endpoint_id=$2 AND NOT j.is_fanout_parent AND j.state='completed' AND j.requested_start_date <= $3 AND j.requested_end_date >= $4), EXISTS(SELECT 1 FROM index_jobs j JOIN newsgroups g ON g.id=j.newsgroup_id WHERE g.name=lower($1) AND j.endpoint_id=$2 AND NOT j.is_fanout_parent AND j.state IN ('queued','running','paused','interrupted') AND daterange(j.requested_start_date, j.requested_end_date, '[]') && daterange($3,$4,'[]'))`, group, endpoint, begin, end.AddDate(0, 0, -1)).Scan(&complete, &active)
		if err != nil {
			return p, fmt.Errorf("read endpoint coverage: %w", err)
		}
		state := "gaps"
		if complete {
			state = "complete"
		} else if active {
			state = "pending"
			pending = true
		}
		if !complete {
			allComplete = false
		}
		p.Endpoints = append(p.Endpoints, EndpointState{Endpoint: endpoint, State: state})
	}
	if err := rows.Err(); err != nil {
		return p, err
	}
	p.State = "gaps"
	if allComplete && len(p.Endpoints) > 0 {
		p.State = "complete"
	} else if pending {
		p.State = "pending"
	}
	return p, nil
}

// Complete queues only endpoints that have neither success nor outstanding
// work for the requested interval.
func (s Store) Complete(ctx context.Context, group string, start, end time.Time) (string, error) {
	if end.Before(start) {
		return "", fmt.Errorf("end date must not precede start date")
	}
	period, err := s.period(ctx, strings.ToLower(group), "day", start)
	if err != nil {
		return "", err
	}
	var targets []string
	for _, endpoint := range period.Endpoints {
		if endpoint.State == "gaps" {
			targets = append(targets, endpoint.Endpoint)
		}
	}
	// For multi-day units evaluate every enabled endpoint against the exact range.
	if !end.Equal(start) {
		targets, err = s.gaps(ctx, group, start, end)
		if err != nil {
			return "", err
		}
	}
	if len(targets) == 0 {
		return "", nil
	}
	return s.creator.Create(ctx, jobs.CreateRequest{NewsgroupID: strings.ToLower(group), EndpointID: targets[0], EndpointIDs: targets, StartDate: start, EndDate: end, ScanReason: "missing_range"})
}

func (s Store) gaps(ctx context.Context, group string, start, end time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id FROM nntp_endpoints e JOIN provider_accounts a ON a.id=e.account_id WHERE e.enabled AND a.enabled ORDER BY e.priority,e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		var complete, active bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM index_jobs j JOIN newsgroups g ON g.id=j.newsgroup_id WHERE g.name=lower($1) AND j.endpoint_id=$2 AND NOT j.is_fanout_parent AND j.state='completed' AND j.requested_start_date <= $3 AND j.requested_end_date >= $4), EXISTS(SELECT 1 FROM index_jobs j JOIN newsgroups g ON g.id=j.newsgroup_id WHERE g.name=lower($1) AND j.endpoint_id=$2 AND NOT j.is_fanout_parent AND j.state IN ('queued','running','paused','interrupted') AND daterange(j.requested_start_date,j.requested_end_date,'[]') && daterange($3,$4,'[]'))`, group, id, start, end).Scan(&complete, &active)
		if err != nil {
			return nil, err
		}
		if !complete && !active {
			result = append(result, id)
		}
	}
	return result, rows.Err()
}
