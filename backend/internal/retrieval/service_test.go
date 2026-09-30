package retrieval

import (
	"context"
	"errors"
	"testing"

	"github.com/james/usenet-locator/backend/internal/articles"
)

type fakeStore struct {
	cached    string
	cacheErr  error
	targetErr error
}

func (f fakeStore) CachedBody(context.Context, int64) (string, error) { return f.cached, f.cacheErr }
func (f fakeStore) BodyTarget(context.Context, int64) (articles.BodyTarget, error) {
	return articles.BodyTarget{}, f.targetErr
}
func (f fakeStore) SaveBody(context.Context, int64, string, string) error { return nil }

func TestGetOrFetchReturnsCacheBeforeEligibilityCheck(t *testing.T) {
	service := Service{store: fakeStore{cached: "already cached"}}
	body, err := service.GetOrFetch(context.Background(), 4)
	if err != nil || body != "already cached" {
		t.Fatalf("body = %q, err = %v", body, err)
	}
}

func TestGetOrFetchDoesNotMaskUnwanted(t *testing.T) {
	service := Service{store: fakeStore{cacheErr: articles.ErrBodyNotCached, targetErr: articles.ErrUnwanted}}
	_, err := service.GetOrFetch(context.Background(), 4)
	if !errors.Is(err, articles.ErrUnwanted) {
		t.Fatalf("err = %v", err)
	}
}
