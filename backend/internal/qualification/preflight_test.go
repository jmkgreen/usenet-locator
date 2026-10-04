package qualification

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type preflightClient struct {
	username, password string
	group              nntp.Group
	overview           []nntp.Overview
	transfer           int64
}

func (c *preflightClient) Authenticate(_ context.Context, username, password string) error {
	c.username, c.password = username, password
	return nil
}
func (c *preflightClient) Capabilities(context.Context) ([]string, error) {
	return []string{"reader", "OVER", "X-Provider-Internal secret", "list active"}, nil
}
func (c *preflightClient) ModeReader(context.Context) error { return nil }
func (c *preflightClient) Group(context.Context, string) (nntp.Group, error) {
	return c.group, nil
}
func (c *preflightClient) Overview(_ context.Context, _, _ int64, emit func(nntp.Overview) error) error {
	for _, item := range c.overview {
		if err := emit(item); err != nil {
			return err
		}
	}
	return nil
}
func (c *preflightClient) Body(context.Context, int64, int64, io.Writer) error { return nil }
func (c *preflightClient) Close() error                                        { return nil }
func (c *preflightClient) OverviewFormat(context.Context) (nntp.OverviewFormat, error) {
	return nntp.OverviewFormat{Code: 215, Fields: []string{"subject", "date"}}, nil
}
func (c *preflightClient) Stat(context.Context, string) (int, error) { return 42, nil }
func (c *preflightClient) TransferBytes() int64                      { return c.transfer }

type recordedQuota struct {
	account string
	bytes   int64
	err     error
}

func (q *recordedQuota) Consume(_ context.Context, account string, bytes int64) error {
	q.account, q.bytes = account, bytes
	return q.err
}

type recordedHistory struct {
	endpoint string
	result   Result
	err      error
}

func (h *recordedHistory) Record(_ context.Context, endpoint string, result Result) error {
	h.endpoint, h.result = endpoint, result
	return h.err
}
func (h *recordedHistory) List(context.Context, string) ([]History, error) { return nil, nil }

func preflightService(t *testing.T, client nntp.Client, quota accounts.QuotaConsumer, history HistoryStore) Service {
	t.Helper()
	dir := t.TempDir()
	usernameFile, passwordFile := filepath.Join(dir, "username"), filepath.Join(dir, "password")
	if err := os.WriteFile(usernameFile, []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordFile, []byte("not-for-logs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Accounts:  []config.AccountConfig{{ID: "account", UsernameFile: usernameFile, PasswordFile: passwordFile, ConnectionLimit: 1}},
		Endpoints: []config.EndpointConfig{{ID: "endpoint", AccountID: "account", Host: "news.example", Port: 563, TLS: true, Primary: true}},
		Resources: config.ResourceConfig{ActiveJobs: 1, WorkersPerJob: 1, BatchSize: 1, MaxBodyBytes: 1},
	}
	s := New(cfg, accounts.NewGuard(cfg), quota)
	s.History = history
	s.Dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return client, nil }
	return s
}

func TestRunRecordsSafeQualificationResultAndChargesMeasuredTransfer(t *testing.T) {
	client := &preflightClient{
		group:    nntp.Group{Name: "alt.test", Low: 1, High: 10},
		transfer: 321,
		overview: []nntp.Overview{{ArticleNumber: 10, MessageID: "<dated@test>", Date: time.Now()}, {ArticleNumber: 10, MessageID: "<undated@test>"}},
	}
	quota, history := &recordedQuota{}, &recordedHistory{}
	result, err := preflightService(t, client, quota, history).Run(context.Background(), "endpoint", "<probe@test>", "alt.test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if client.username != "operator" || client.password != "not-for-logs" || quota.account != "account" || quota.bytes != 321 {
		t.Fatalf("credentials or quota = %#v %#v", client, quota)
	}
	if !result.GroupSelected || result.GroupLow != 1 || result.GroupHigh != 10 || result.StatCode != 223 || result.OverviewCode != 224 || result.OverviewRows != 2 || result.OverviewDates != 1 {
		t.Fatalf("result = %#v", result)
	}
	if got := result.Capabilities; len(got) != 3 || got[0] != "READER" || got[1] != "OVER" || got[2] != "LIST" {
		t.Fatalf("safe capabilities = %#v", got)
	}
	if history.endpoint != "endpoint" || history.result.OverviewCode != 224 {
		t.Fatalf("history = %#v", history)
	}
}

func TestRunRejectsUnavailableEndpointBeforeDialling(t *testing.T) {
	s := Service{Endpoints: map[string]config.EndpointConfig{}, Accounts: map[string]config.AccountConfig{}}
	_, err := s.Run(context.Background(), "missing", "", "", 0)
	if err == nil || err.Error() != "endpoint unavailable" {
		t.Fatalf("error = %v", err)
	}
}

func TestRunRejectsArticleOutsideSelectedGroup(t *testing.T) {
	client := &preflightClient{group: nntp.Group{Name: "alt.test", Low: 10, High: 20}}
	_, err := preflightService(t, client, nil, nil).Run(context.Background(), "endpoint", "", "alt.test", 9)
	if err == nil || err.Error() != "article number outside selected group" {
		t.Fatalf("error = %v", err)
	}
}

func TestRunReturnsQuotaErrorOnlyAfterSuccessfulProbe(t *testing.T) {
	client := &preflightClient{transfer: 1}
	quota := &recordedQuota{err: accounts.ErrQuotaExceeded}
	_, err := preflightService(t, client, quota, nil).Run(context.Background(), "endpoint", "", "", 0)
	if !errors.Is(err, accounts.ErrQuotaExceeded) {
		t.Fatalf("error = %v", err)
	}
}
