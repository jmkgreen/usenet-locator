package retention

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

type memoryStore struct {
	observations []Observation
	start, high  int64
	found        bool
	stored       []nntp.Overview
	next         int64
}

func (s *memoryStore) Record(_ context.Context, item Observation) (Observation, error) {
	item.ObservedAt = time.Now().UTC()
	s.observations = append(s.observations, item)
	return item, nil
}
func (s *memoryStore) ListLatest(context.Context, string) ([]Observation, error) {
	return s.observations, nil
}
func (s *memoryStore) Cursor(context.Context, string, string) (int64, int64, bool, error) {
	if s.found {
		return s.start, s.high, true, nil
	}
	return 12, 9999, true, nil
}
func (s *memoryStore) StoreHeaders(_ context.Context, _ string, _ string, headers []nntp.Overview, next, _ int64) error {
	s.stored = append([]nntp.Overview(nil), headers...)
	s.next = next
	return nil
}

type probeClient struct {
	group       string
	ranges      [][2]int64
	overviews   []nntp.Overview
	transfer    int64
	authErr     error
	capsErr     error
	modeErr     error
	groupErr    error
	overviewErr error
}

func (c *probeClient) Authenticate(context.Context, string, string) error { return c.authErr }
func (c *probeClient) Capabilities(context.Context) ([]string, error) {
	return []string{"READER"}, c.capsErr
}
func (c *probeClient) ModeReader(context.Context) error { return c.modeErr }
func (c *probeClient) Group(_ context.Context, group string) (nntp.Group, error) {
	c.group = group
	return nntp.Group{Name: group, Low: 10, High: 9999}, c.groupErr
}
func (c *probeClient) Overview(_ context.Context, start, end int64, emit func(nntp.Overview) error) error {
	c.ranges = append(c.ranges, [2]int64{start, end})
	if c.overviewErr != nil {
		return c.overviewErr
	}
	if c.overviews != nil {
		for _, item := range c.overviews {
			if err := emit(item); err != nil {
				return err
			}
		}
		return nil
	}
	return emit(nntp.Overview{ArticleNumber: start + 1, MessageID: "<old@test>", Date: time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)})
}
func (c *probeClient) Body(context.Context, int64, int64, io.Writer) error { return nil }
func (c *probeClient) Close() error                                        { return nil }
func (c *probeClient) TransferBytes() int64                                { return c.transfer }

type quotaRecorder struct{ bytes int64 }

func (q *quotaRecorder) Consume(_ context.Context, _ string, bytes int64) error {
	q.bytes += bytes
	return nil
}

func TestProbeRecordsEarliestObservedArticleInOneBoundedWindow(t *testing.T) {
	secretDir := t.TempDir()
	usernameFile := filepath.Join(secretDir, "username")
	passwordFile := filepath.Join(secretDir, "password")
	if err := os.WriteFile(usernameFile, []byte("user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordFile, []byte("password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: usernameFile, PasswordFile: passwordFile, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "provider", AccountID: "account", Host: "news.example", Port: 563, TLS: true}}, Resources: config.ResourceConfig{MaxBodyBytes: 1}}
	store, client := &memoryStore{}, &probeClient{}
	service := New(cfg, store, accounts.NewGuard(cfg), nil)
	service.dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return client, nil }
	items, err := service.Probe(context.Background(), "alt.test")
	if err != nil || len(items) != 1 || items[0].Outcome != "found" || items[0].ArticleNumber == nil || *items[0].ArticleNumber != 11 || client.group != "alt.test" || len(client.ranges) != 1 || client.ranges[0] != [2]int64{10, 109} {
		t.Fatalf("items = %#v, group = %q, ranges = %#v, err = %v", items, client.group, client.ranges, err)
	}
}

func TestRetrieveNextStoresOnlyDatedIdentifiedHeadersAndChargesTransfer(t *testing.T) {
	secretDir := t.TempDir()
	usernameFile := filepath.Join(secretDir, "username")
	passwordFile := filepath.Join(secretDir, "password")
	if err := os.WriteFile(usernameFile, []byte("user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordFile, []byte("password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: usernameFile, PasswordFile: passwordFile, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "provider", AccountID: "account", Host: "news.example", Port: 563, TLS: true}}}
	store := &memoryStore{start: 10, high: 12, found: true}
	client := &probeClient{transfer: 55, overviews: []nntp.Overview{
		{ArticleNumber: 10, MessageID: "<keep@test>", Date: time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)},
		{ArticleNumber: 11, Date: time.Now()},
		{ArticleNumber: 12, MessageID: "<undated@test>"},
	}}
	quota := &quotaRecorder{}
	service := New(cfg, store, accounts.NewGuard(cfg), quota)
	service.dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return client, nil }
	if err := service.RetrieveNext(context.Background(), "alt.test", 10); err != nil {
		t.Fatal(err)
	}
	if len(store.stored) != 1 || store.stored[0].MessageID != "<keep@test>" || store.next != 13 || quota.bytes != 55 || client.group != "alt.test" {
		t.Fatalf("stored=%#v next=%d quota=%d group=%q", store.stored, store.next, quota.bytes, client.group)
	}
}

func TestRetrieveNextRejectsUnsafeInputAndMissingCursor(t *testing.T) {
	service := Service{store: &memoryStore{found: false}}
	for _, request := range []struct {
		group string
		limit int
	}{{"", 1}, {"alt.test", 0}, {"alt.test", 101}} {
		if err := service.RetrieveNext(context.Background(), request.group, request.limit); err == nil {
			t.Fatalf("RetrieveNext(%q, %d) accepted invalid input", request.group, request.limit)
		}
	}
}

func TestProbeUsesSafeFixedOutcomesForProviderFailures(t *testing.T) {
	dir := t.TempDir()
	username, password := filepath.Join(dir, "username"), filepath.Join(dir, "password")
	if err := os.WriteFile(username, []byte("operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(password, []byte("credential\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: username, PasswordFile: password, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "provider", AccountID: "account", Host: "news.example", Port: 563, TLS: true}}}
	cases := []struct {
		name, want string
		set        func(*probeClient)
	}{
		{"authentication", "authentication_failed", func(c *probeClient) { c.authErr = errors.New("provider said secret") }},
		{"capabilities", "capability_failed", func(c *probeClient) { c.capsErr = errors.New("provider transcript") }},
		{"reader mode", "reader_mode_failed", func(c *probeClient) { c.modeErr = errors.New("provider transcript") }},
		{"group", "group_unavailable", func(c *probeClient) { c.groupErr = errors.New("provider transcript") }},
		{"overview", "overview_failed", func(c *probeClient) { c.overviewErr = errors.New("provider transcript") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &probeClient{}
			tc.set(client)
			service := New(cfg, &memoryStore{}, accounts.NewGuard(cfg), nil)
			service.dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return client, nil }
			observation := service.probeEndpoint(context.Background(), cfg.Endpoints[0], "alt.test")
			if observation.Outcome != tc.want || strings.Contains(observation.Outcome, "transcript") || strings.Contains(observation.Outcome, "credential") {
				t.Fatalf("observation = %#v", observation)
			}
		})
	}
}
