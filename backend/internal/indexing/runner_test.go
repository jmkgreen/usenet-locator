package indexing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/james/usenet-locator/backend/internal/jobs"
	"github.com/james/usenet-locator/backend/internal/nntp"
)

type fakeFinalizer struct {
	completed, interrupted bool
	reason                 string
}

func (f *fakeFinalizer) Complete(context.Context, string) error { f.completed = true; return nil }
func (f *fakeFinalizer) Interrupt(_ context.Context, _ string, reason string) error {
	f.interrupted, f.reason = true, reason
	return nil
}

func TestRunnerInterruptsUnknownEndpoint(t *testing.T) {
	finalizer := &fakeFinalizer{}
	err := (Runner{Finalizer: finalizer}).Run(context.Background(), jobs.Job{ID: "job", Endpoint: "missing"})
	if err == nil || !finalizer.interrupted || !strings.Contains(finalizer.reason, "endpoint") {
		t.Fatalf("err = %v, finalizer = %#v", err, finalizer)
	}
}

func TestScanFailureReasonIsSafeAndSpecific(t *testing.T) {
	if got := scanFailureReason(errors.New("select newsgroup: GROUP returned 411 unexpected server text")); got != "NNTP newsgroup selection failed" {
		t.Fatalf("group reason = %q", got)
	}
	if got := scanFailureReason(errors.New("read overview 1-2: server said something")); got != "NNTP overview retrieval failed" {
		t.Fatalf("overview reason = %q", got)
	}
	if got := scanFailureReason(fmt.Errorf("read overview 1-2: %w", &nntp.OverviewError{Command: "XOVER", Code: 502})); got != "NNTP XOVER returned 502" {
		t.Fatalf("protocol reason = %q", got)
	}
}
