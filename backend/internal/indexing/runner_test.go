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

type fakeAccountQuota struct {
	consumed int64
	err      error
}

func (f *fakeAccountQuota) Consume(_ context.Context, _ string, bytes int64) error {
	f.consumed += bytes
	return f.err
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

func TestRunnerFailsClosedBeforeConnecting(t *testing.T) {
	baseConfig := config.Config{Accounts: []config.AccountConfig{{ID: "account", ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "endpoint", AccountID: "account"}}, Resources: config.ResourceConfig{BatchSize: 1}}
	for _, tc := range []struct {
		name   string
		job    jobs.Job
		runner func(*fakeFinalizer) Runner
		want   string
	}{
		{
			name: "missing account",
			job:  jobs.Job{ID: "job", Endpoint: "endpoint"},
			runner: func(finalizer *fakeFinalizer) Runner {
				return Runner{Endpoints: map[string]config.EndpointConfig{"endpoint": {ID: "endpoint", AccountID: "gone"}}, Finalizer: finalizer}
			},
			want: "configured account is unavailable",
		},
		{
			name: "incomplete worker",
			job:  jobs.Job{ID: "job", Endpoint: "endpoint"},
			runner: func(finalizer *fakeFinalizer) Runner {
				return Runner{Endpoints: map[string]config.EndpointConfig{"endpoint": baseConfig.Endpoints[0]}, Accounts: map[string]config.AccountConfig{"account": baseConfig.Accounts[0]}, Finalizer: finalizer, Guard: accounts.NewGuard(baseConfig)}
			},
			want: "index worker is incomplete",
		},
		{
			name: "unavailable credentials",
			job:  jobs.Job{ID: "job", Endpoint: "endpoint"},
			runner: func(finalizer *fakeFinalizer) Runner {
				account := baseConfig.Accounts[0]
				account.UsernameFile, account.PasswordFile = "missing-user", "missing-password"
				cfg := baseConfig
				cfg.Accounts = []config.AccountConfig{account}
				return Runner{Endpoints: map[string]config.EndpointConfig{"endpoint": baseConfig.Endpoints[0]}, Accounts: map[string]config.AccountConfig{"account": account}, Writer: &fakeWriter{}, Finalizer: finalizer, BatchSize: 1, Dial: func(context.Context, nntp.Endpoint) (nntp.Client, error) {
					t.Fatal("Dial must not receive missing credentials")
					return nil, nil
				}, Guard: accounts.NewGuard(cfg)}
			},
			want: "NNTP credentials are unavailable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finalizer := &fakeFinalizer{}
			err := tc.runner(finalizer).Run(context.Background(), tc.job)
			if err == nil || finalizer.reason != tc.want || !finalizer.interrupted {
				t.Fatalf("err=%v finalizer=%#v", err, finalizer)
			}
			if strings.Contains(err.Error(), "missing-password") || strings.Contains(err.Error(), "missing-user") {
				t.Fatalf("unsafe error detail leaked: %v", err)
			}
		})
	}
}

func TestRunnerSanitizesDialFailure(t *testing.T) {
	dir := t.TempDir()
	username, password := filepath.Join(dir, "username"), filepath.Join(dir, "password")
	if err := os.WriteFile(username, []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(password, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: username, PasswordFile: password, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "endpoint", AccountID: "account", Host: "news.example", Port: 563}}, Resources: config.ResourceConfig{BatchSize: 1}}
	finalizer := &fakeFinalizer{}
	runner := NewRunner(cfg, &fakeWriter{}, finalizer, accounts.NewGuard(cfg), nil, nil)
	runner.Dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) {
		return nil, errors.New("provider refuses secret diagnostic")
	}
	err := runner.Run(context.Background(), jobs.Job{ID: "job", Endpoint: "endpoint"})
	if err == nil || finalizer.reason != "NNTP connection failed" || strings.Contains(err.Error(), "secret diagnostic") {
		t.Fatalf("err=%v finalizer=%#v", err, finalizer)
	}
}

func TestRunnerSanitizesProtocolSetupFailures(t *testing.T) {
	dir := t.TempDir()
	username, password := filepath.Join(dir, "username"), filepath.Join(dir, "password")
	if err := os.WriteFile(username, []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(password, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: username, PasswordFile: password, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "endpoint", AccountID: "account", Host: "news.example", Port: 563}}, Resources: config.ResourceConfig{BatchSize: 1}}
	for _, tc := range []struct {
		name   string
		client *fakeClient
		want   string
	}{
		{name: "authentication", client: &fakeClient{authenticateErr: errors.New("server echoed credential: secret")}, want: "NNTP authentication failed"},
		{name: "capabilities", client: &fakeClient{capabilitiesErr: errors.New("provider internal diagnostic")}, want: "NNTP capability negotiation failed"},
		{name: "reader mode", client: &fakeClient{modeReaderErr: errors.New("server internal diagnostic")}, want: "NNTP reader mode failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finalizer := &fakeFinalizer{}
			runner := NewRunner(cfg, &fakeWriter{}, finalizer, accounts.NewGuard(cfg), nil, nil)
			runner.Dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return tc.client, nil }
			err := runner.Run(context.Background(), jobs.Job{ID: "job", Endpoint: "endpoint"})
			if err == nil || finalizer.reason != tc.want || strings.Contains(err.Error(), "diagnostic") || strings.Contains(err.Error(), "credential") {
				t.Fatalf("err=%v finalizer=%#v", err, finalizer)
			}
		})
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

func TestRecordTransferHonoursAccountAndZeroTrafficBoundaries(t *testing.T) {
	accountQuota := &fakeAccountQuota{err: accounts.ErrQuotaExceeded}
	jobQuota := &fakeJobQuota{}
	runner := Runner{Quota: accountQuota, JobQuota: jobQuota}
	if err := runner.recordTransfer(context.Background(), "account", "job", 0); err != nil || accountQuota.consumed != 0 || jobQuota.consumed != 0 {
		t.Fatalf("zero transfer err=%v account=%d job=%d", err, accountQuota.consumed, jobQuota.consumed)
	}
	if err := runner.recordTransfer(context.Background(), "account", "job", 7); !errors.Is(err, accounts.ErrQuotaExceeded) || jobQuota.consumed != 0 {
		t.Fatalf("account failure err=%v job=%d", err, jobQuota.consumed)
	}
	accountQuota.err = nil
	if err := runner.recordTransfer(context.Background(), "account", "job", 5); err != nil || accountQuota.consumed != 12 || jobQuota.consumed != 5 {
		t.Fatalf("successful transfer err=%v account=%d job=%d", err, accountQuota.consumed, jobQuota.consumed)
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
