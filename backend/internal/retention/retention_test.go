package retention

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type memoryStore struct{ observations []Observation }

func (s *memoryStore) Record(_ context.Context, item Observation) (Observation, error) {
	item.ObservedAt = time.Now().UTC()
	s.observations = append(s.observations, item)
	return item, nil
}
func (s *memoryStore) ListLatest(context.Context, string) ([]Observation, error) {
	return s.observations, nil
}
func (s *memoryStore) Cursor(context.Context, string, string) (int64, int64, bool, error) {
	return 12, 9999, true, nil
}
func (s *memoryStore) StoreHeaders(context.Context, string, string, []nntp.Overview, int64, int64) error {
	return nil
}

type probeClient struct {
	group  string
	ranges [][2]int64
}

func (c *probeClient) Authenticate(context.Context, string, string) error { return nil }
func (c *probeClient) Capabilities(context.Context) ([]string, error)     { return []string{"READER"}, nil }
func (c *probeClient) ModeReader(context.Context) error                   { return nil }
func (c *probeClient) Group(_ context.Context, group string) (nntp.Group, error) {
	c.group = group
	return nntp.Group{Name: group, Low: 10, High: 9999}, nil
}
func (c *probeClient) Overview(_ context.Context, start, end int64, emit func(nntp.Overview) error) error {
	c.ranges = append(c.ranges, [2]int64{start, end})
	return emit(nntp.Overview{ArticleNumber: start + 1, MessageID: "<old@test>", Date: time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)})
}
func (c *probeClient) Body(context.Context, int64, int64, io.Writer) error { return nil }
func (c *probeClient) Close() error                                        { return nil }

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
