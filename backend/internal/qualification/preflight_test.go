package qualification

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	authErr            error
	capsErr            error
	modeErr            error
	formatErr          error
	statErr            error
	groupErr           error
	overviewErr        error
}

func (c *preflightClient) Authenticate(_ context.Context, username, password string) error {
	c.username, c.password = username, password
	return c.authErr
}
func (c *preflightClient) Capabilities(context.Context) ([]string, error) {
	return []string{"reader", "OVER", "X-Provider-Internal secret", "list active"}, c.capsErr
}
func (c *preflightClient) ModeReader(context.Context) error { return c.modeErr }
func (c *preflightClient) Group(context.Context, string) (nntp.Group, error) {
	return c.group, c.groupErr
}
func (c *preflightClient) Overview(_ context.Context, _, _ int64, emit func(nntp.Overview) error) error {
	if c.overviewErr != nil {
		return c.overviewErr
	}
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
	return nntp.OverviewFormat{Code: 215, Fields: []string{"subject", "date"}}, c.formatErr
}
func (c *preflightClient) Stat(context.Context, string) (int, error) { return 42, c.statErr }
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

func TestRunUsesFixedFailureStagesWithoutProviderText(t *testing.T) {
	providerErr := errors.New("provider transcript secret")
	for _, tc := range []struct {
		name, messageID, group, want string
		article                      int64
		configure                    func(*preflightClient)
		history                      HistoryStore
	}{
		{name: "authentication", want: "authentication failed", configure: func(c *preflightClient) { c.authErr = providerErr }},
		{name: "capabilities", want: "capability negotiation failed", configure: func(c *preflightClient) { c.capsErr = providerErr }},
		{name: "reader mode", want: "reader mode failed", configure: func(c *preflightClient) { c.modeErr = providerErr }},
		{name: "overview format", want: "overview format check failed", configure: func(c *preflightClient) { c.formatErr = providerErr }},
		{name: "stat", messageID: "<probe@test>", want: "STAT check failed", configure: func(c *preflightClient) { c.statErr = providerErr }},
		{name: "group", group: "alt.test", want: "newsgroup selection failed", configure: func(c *preflightClient) { c.groupErr = providerErr }},
		{name: "empty group", group: "alt.test", want: "newsgroup has no articles", configure: func(c *preflightClient) { c.group = nntp.Group{Low: 1, High: 0} }},
		{name: "outside group", group: "alt.test", article: 2, want: "article number outside selected group", configure: func(c *preflightClient) { c.group = nntp.Group{Low: 1, High: 1} }},
		{name: "overview", group: "alt.test", article: 1, want: "overview check failed", configure: func(c *preflightClient) { c.group = nntp.Group{Low: 1, High: 1}; c.overviewErr = providerErr }},
		{name: "history", want: "record qualification failed", configure: func(c *preflightClient) {}, history: &recordedHistory{err: providerErr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &preflightClient{}
			tc.configure(client)
			_, err := preflightService(t, client, nil, tc.history).Run(context.Background(), "endpoint", tc.messageID, tc.group, tc.article)
			if err == nil || err.Error() != tc.want || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "transcript") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
