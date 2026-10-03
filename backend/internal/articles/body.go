package articles

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrUnwanted = errors.New("article is marked unwanted")
var ErrBodyNotCached = errors.New("article body is not cached")

type BodyTarget struct {
	ArticleID, ArticleNumber int64
	EndpointID               string
	Newsgroup                string
}

// BodyTarget returns one known endpoint-local location only when a new body
// transfer is permitted. The unwanted check happens in the database before any
// caller can open an NNTP connection.
func (s Store) BodyTarget(ctx context.Context, articleID int64) (BodyTarget, error) {
	var target BodyTarget
	err := s.pool.QueryRow(ctx, `SELECT al.article_id, al.article_number, al.endpoint_id, ng.name
        FROM article_locations al
        JOIN newsgroups ng ON ng.id = al.newsgroup_id
        LEFT JOIN article_preferences p ON p.article_id = al.article_id
        WHERE al.article_id = $1 AND al.available = true AND COALESCE(p.unwanted, false) = false
		ORDER BY al.last_seen_at DESC LIMIT 1`, articleID).Scan(&target.ArticleID, &target.ArticleNumber, &target.EndpointID, &target.Newsgroup)
	if errors.Is(err, pgx.ErrNoRows) {
		var unwanted bool
		checkErr := s.pool.QueryRow(ctx, `SELECT COALESCE(p.unwanted, false) FROM articles a LEFT JOIN article_preferences p ON p.article_id = a.id WHERE a.id = $1`, articleID).Scan(&unwanted)
		if errors.Is(checkErr, pgx.ErrNoRows) {
			return BodyTarget{}, ErrNotFound
		}
		if checkErr != nil {
			return BodyTarget{}, fmt.Errorf("check body eligibility: %w", checkErr)
		}
		if unwanted {
			return BodyTarget{}, ErrUnwanted
		}
		return BodyTarget{}, ErrNotFound
	}
	if err != nil {
		return BodyTarget{}, fmt.Errorf("get body target: %w", err)
	}
	return target, nil
}

func (s Store) CachedBody(ctx context.Context, articleID int64) (string, error) {
	var body string
	err := s.pool.QueryRow(ctx, "SELECT decoded_text FROM article_bodies WHERE article_id = $1", articleID).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrBodyNotCached
	}
	if err != nil {
		return "", fmt.Errorf("get cached body: %w", err)
	}
	return body, nil
}

func (s Store) SaveBody(ctx context.Context, articleID int64, endpointID, body string) error {
	if body == "" {
		return fmt.Errorf("decoded body must not be empty")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO article_bodies (article_id, decoded_text, source_endpoint_id)
        VALUES ($1, $2, $3) ON CONFLICT (article_id) DO UPDATE SET decoded_text = EXCLUDED.decoded_text,
        source_endpoint_id = EXCLUDED.source_endpoint_id, fetched_at = now()`, articleID, body, endpointID)
	if err != nil {
		return fmt.Errorf("save cached body: %w", err)
	}
	return nil
}
