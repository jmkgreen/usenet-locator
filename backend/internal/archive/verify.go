package archive

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidVerificationRequest = errors.New("invalid verification request")

// Findings are evidence to review, not instructions to delete or alter data.
// An uncovered location may have come from retention discovery, which does not
// create scan coverage; it is not by itself evidence of an inconsistency.
type Finding struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}
type Report struct {
	Category   string    `json:"category"`
	Findings   []Finding `json:"findings"`
	NextCursor string    `json:"next_cursor,omitempty"`
}
type Verifier struct{ pool *pgxpool.Pool }

func NewVerifier(pool *pgxpool.Pool) Verifier { return Verifier{pool: pool} }

// Every query is read-only, paginated, and executed under a deadline. Entries
// are not a consistent snapshot across pages if indexing runs concurrently.
var verificationQueries = map[string]string{
	"possible_identity_conflicts": `SELECT a.id::text, 'Message-ID differs only in case from another stored identity' FROM articles a WHERE EXISTS (SELECT 1 FROM articles b WHERE b.id <> a.id AND lower(b.message_id) = lower(a.message_id))`,
	"invalid_identities":          `SELECT q.id::text, 'Overview retained outside the index because its Message-ID is invalid' FROM quarantined_overviews q`,
	"repeated_bodies":             `SELECT b.article_id::text, 'Identical cached text exists under another Message-ID' FROM article_bodies b WHERE EXISTS (SELECT 1 FROM article_bodies other WHERE other.article_id <> b.article_id AND md5(other.decoded_text) = md5(b.decoded_text) AND other.decoded_text = b.decoded_text)`,
	"membership_inconsistencies":  `SELECT l.id::text, 'Location has no matching article/newsgroup membership' FROM article_locations l LEFT JOIN article_newsgroups m ON m.article_id=l.article_id AND m.newsgroup_id=l.newsgroup_id WHERE m.article_id IS NULL`,
	"location_conflicts":          `SELECT c.id::text, 'Provider location has been observed with conflicting article identities' FROM article_location_conflicts c`,
	"orphaned_locations":          `SELECT l.id::text, 'Location references a missing article, endpoint, or newsgroup' FROM article_locations l LEFT JOIN articles a ON a.id=l.article_id LEFT JOIN nntp_endpoints e ON e.id=l.endpoint_id LEFT JOIN newsgroups g ON g.id=l.newsgroup_id WHERE a.id IS NULL OR e.id IS NULL OR g.id IS NULL`,
	"orphaned_bodies":             `SELECT b.article_id::text, 'Cached body has no canonical article' FROM article_bodies b LEFT JOIN articles a ON a.id=b.article_id WHERE a.id IS NULL`,
	"locations_without_coverage":  `SELECT l.id::text, 'Location has no complete scan coverage; retention discovery may explain this' FROM article_locations l WHERE NOT EXISTS (SELECT 1 FROM scan_coverage c WHERE c.endpoint_id=l.endpoint_id AND c.newsgroup_id=l.newsgroup_id AND c.state='complete' AND l.article_number BETWEEN c.article_number_start AND c.article_number_end)`,
	"coverage_without_locations":  `SELECT c.id::text, 'Complete scan range has no stored locations; empty or filtered ranges may explain this' FROM scan_coverage c WHERE c.state='complete' AND NOT EXISTS (SELECT 1 FROM article_locations l WHERE l.endpoint_id=c.endpoint_id AND l.newsgroup_id=c.newsgroup_id AND l.article_number BETWEEN c.article_number_start AND c.article_number_end)`,
	"suspicious_dates":            `SELECT a.id::text, 'Article date is invalid, absent, or in the future' FROM articles a WHERE (a.raw_date IS NOT NULL AND a.article_date IS NULL) OR a.article_date > now() + interval '1 day'`,
	"body_warnings":               `SELECT b.article_id::text, 'Body decoding or truncation warning' FROM article_bodies b WHERE b.quality_flags && ARRAY['unknown_encoding','body_truncated','body_decode_failed']::text[]`,
	"job_counters":                `SELECT j.id::text, 'Interrupted job or stored count exceeds retrieved count' FROM index_jobs j WHERE j.state = 'interrupted' OR j.articles_stored > j.headers_retrieved`,
}

func (v Verifier) Check(ctx context.Context, category, after string, limit int) (Report, error) {
	query, ok := verificationQueries[category]
	if !ok || limit < 1 || limit > 200 || len(after) > 64 {
		return Report{}, ErrInvalidVerificationRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := v.pool.Query(ctx, `SELECT key, description FROM (`+query+`) AS checks(key, description) WHERE key > $1 ORDER BY key LIMIT $2`, after, limit+1)
	if err != nil {
		return Report{}, fmt.Errorf("run verification: %w", err)
	}
	defer rows.Close()
	report := Report{Category: category, Findings: make([]Finding, 0, limit)}
	for rows.Next() {
		var finding Finding
		if err := rows.Scan(&finding.Key, &finding.Description); err != nil {
			return Report{}, fmt.Errorf("read verification finding: %w", err)
		}
		if len(report.Findings) == limit {
			report.NextCursor = report.Findings[len(report.Findings)-1].Key
			break
		}
		report.Findings = append(report.Findings, finding)
	}
	if err := rows.Err(); err != nil {
		return Report{}, fmt.Errorf("iterate verification findings: %w", err)
	}
	return report, nil
}
