package watchlist

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/retention"
)

type dueStore struct {
	due    []string
	marked []string
}

func (s *dueStore) Due(context.Context, time.Time) ([]string, error) { return s.due, nil }
func (s *dueStore) MarkChecked(_ context.Context, group string, _ time.Time) error {
	s.marked = append(s.marked, group)
	return nil
}

type prober struct {
	failed string
	called []string
}

func (p *prober) Probe(_ context.Context, group string) ([]retention.Observation, error) {
	p.called = append(p.called, group)
	if group == p.failed {
		return nil, errors.New("unavailable")
	}
	return nil, nil
}

func TestRunDueMarksOnlySuccessfulChecks(t *testing.T) {
	store := &dueStore{due: []string{"alt.ok", "alt.failed"}}
	checks := &prober{failed: "alt.failed"}
	if err := RunDue(context.Background(), store, checks, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(checks.called) != 2 || len(store.marked) != 1 || store.marked[0] != "alt.ok" {
		t.Fatalf("called=%v marked=%v", checks.called, store.marked)
	}
}
