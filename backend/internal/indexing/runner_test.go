package indexing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type fakeFinalizer struct {
	completed, interrupted bool
	reason                 string
}

type fakeJobQuota struct {
	consumed int64
	err      error
}

type fakeAccountQuota struct{ consumed int64 }

func (f *fakeAccountQuota) Consume(_ context.Context, _ string, bytes int64) error {
	f.consumed += bytes
	return nil
}

func (f *fakeJobQuota) ConsumeTransfer(_ context.Context, _ string, bytes int64) error {
	f.consumed += bytes
	return f.err
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

func TestTransferFailureReasonDistinguishesJobBudget(t *testing.T) {
	if got := transferFailureReason(jobs.ErrTransferLimitExceeded); got != "job transfer budget reached" {
		t.Fatalf("job budget reason = %q", got)
	}
	if got := transferFailureReason(errors.New("other quota failure")); got != "NNTP account transfer quota reached" {
		t.Fatalf("account quota reason = %q", got)
	}
}

func TestRecordTransferStopsAtJobBudget(t *testing.T) {
	quota := &fakeJobQuota{err: jobs.ErrTransferLimitExceeded}
	runner := Runner{JobQuota: quota}
	err := runner.recordTransfer(context.Background(), "account", "job", 123)
	if !errors.Is(err, jobs.ErrTransferLimitExceeded) || quota.consumed != 123 {
		t.Fatalf("record transfer = %v, quota = %#v", err, quota)
	}
}

func TestRunnerCompletesBoundedScanAndChargesMeasuredTransfers(t *testing.T) {
	dir := t.TempDir()
	username, password := filepath.Join(dir, "username"), filepath.Join(dir, "password")
	if err := os.WriteFile(username, []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(password, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	date := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: username, PasswordFile: password, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "endpoint", AccountID: "account", Host: "news.example", Port: 563, TLS: true}}, Resources: config.ResourceConfig{BatchSize: 1}}
	client := &fakeClient{group: nntp.Group{Low: 1, High: 1}, records: map[int64]nntp.Overview{1: {ArticleNumber: 1, Date: date, MessageID: "<one@test>"}}}
	writer, finalizer, quota, jobQuota := &fakeWriter{}, &fakeFinalizer{}, &fakeAccountQuota{}, &fakeJobQuota{}
	runner := NewRunner(cfg, writer, finalizer, accounts.NewGuard(cfg), quota, jobQuota)
	runner.Dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return client, nil }
	if err := runner.Run(context.Background(), jobs.Job{ID: "job", Endpoint: "endpoint", Newsgroup: "alt.test", StartDate: date, EndDate: date}); err != nil {
		t.Fatal(err)
	}
	if !finalizer.completed || len(writer.batches) != 1 || quota.consumed == 0 || jobQuota.consumed != quota.consumed {
		t.Fatalf("finalizer=%#v batches=%#v quota=%#v jobQuota=%#v", finalizer, writer.batches, quota, jobQuota)
	}
}
