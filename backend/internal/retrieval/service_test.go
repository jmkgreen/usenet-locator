package retrieval

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/archive"
	"github.com/jmkgreen/usenet-locator/backend/internal/articles"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type fakeStore struct {
	cached    string
	cacheErr  error
	targetErr error
	target    articles.BodyTarget
	saved     string
}

func (f fakeStore) CachedBody(context.Context, int64) (string, error) { return f.cached, f.cacheErr }
func (f fakeStore) BodyTarget(context.Context, int64) (articles.BodyTarget, error) {
	return f.target, f.targetErr
}
func (f *fakeStore) SaveBody(_ context.Context, _ int64, _ string, body string) error {
	f.saved = body
	return nil
}

type fakeClient struct{ group string }

type mimeClient struct{ fakeClient }

type failingMeterClient struct{ fakeClient }

func (c *failingMeterClient) Authenticate(context.Context, string, string) error {
	return errors.New("authentication failed")
}
func (c *failingMeterClient) TransferBytes() int64 { return 37 }

func (c *mimeClient) Head(_ context.Context, _ int64, _ int64, w io.Writer) error {
	_, err := io.WriteString(w, "Content-Type: text/plain; charset=iso-8859-1\nContent-Transfer-Encoding: quoted-printable\n")
	return err
}
func (c *mimeClient) Body(_ context.Context, _ int64, _ int64, w io.Writer) error {
	_, err := io.WriteString(w, "caf=E9")
	return err
}

type readerMemoryStore struct {
	fakeStore
	reader  archive.Reader
	rawBody string
}

func (s *readerMemoryStore) SaveReader(_ context.Context, _ int64, _ string, _ []byte, body []byte, reader archive.Reader) error {
	s.reader = reader
	s.rawBody = string(body)
	return nil
}
func (s *readerMemoryStore) CachedReader(context.Context, int64) (archive.Reader, error) {
	return s.reader, nil
}

func (f *fakeClient) Authenticate(context.Context, string, string) error { return nil }
func (f *fakeClient) Capabilities(context.Context) ([]string, error)     { return nil, nil }
func (f *fakeClient) ModeReader(context.Context) error                   { return nil }
func (f *fakeClient) Group(_ context.Context, group string) (nntp.Group, error) {
	f.group = group
	return nntp.Group{Name: group, Low: 1, High: 1}, nil
}
func (f *fakeClient) Overview(context.Context, int64, int64, func(nntp.Overview) error) error {
	return nil
}
func (f *fakeClient) Body(_ context.Context, number, _ int64, destination io.Writer) error {
	if number != 17 {
		return errors.New("unexpected article number")
	}
	_, err := io.WriteString(destination, "body text")
	return err
}
func (f *fakeClient) Close() error { return nil }

type quotaRecorder struct{ bytes int64 }

func (q *quotaRecorder) Consume(_ context.Context, _ string, bytes int64) error {
	q.bytes += bytes
	return nil
}

func TestGetOrFetchReturnsCacheBeforeEligibilityCheck(t *testing.T) {
	service := Service{store: &fakeStore{cached: "already cached"}}
	body, err := service.GetOrFetch(context.Background(), 4)
	if err != nil || body != "already cached" {
		t.Fatalf("body = %q, err = %v", body, err)
	}
}

func TestGetOrFetchDoesNotMaskUnwanted(t *testing.T) {
	service := Service{store: &fakeStore{cacheErr: articles.ErrBodyNotCached, targetErr: articles.ErrUnwanted}}
	_, err := service.GetOrFetch(context.Background(), 4)
	if !errors.Is(err, articles.ErrUnwanted) {
		t.Fatalf("err = %v", err)
	}
}

func TestGetOrFetchSelectsLocationNewsgroupBeforeBody(t *testing.T) {
	secretDir := t.TempDir()
	usernameFile := filepath.Join(secretDir, "username")
	passwordFile := filepath.Join(secretDir, "password")
	if err := os.WriteFile(usernameFile, []byte("user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordFile, []byte("password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Accounts:  []config.AccountConfig{{ID: "account", UsernameFile: usernameFile, PasswordFile: passwordFile, ConnectionLimit: 1}},
		Endpoints: []config.EndpointConfig{{ID: "endpoint", AccountID: "account", Host: "news.example", Port: 563, TLS: true}},
		Resources: config.ResourceConfig{MaxBodyBytes: 1024},
	}
	store := &fakeStore{cacheErr: articles.ErrBodyNotCached, target: articles.BodyTarget{ArticleID: 4, ArticleNumber: 17, EndpointID: "endpoint", Newsgroup: "alt.test"}}
	client := &fakeClient{}
	service := New(cfg, store, accounts.NewGuard(cfg), nil)
	service.dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return client, nil }

	body, err := service.GetOrFetch(context.Background(), 4)
	if err != nil || body != "body text" || store.saved != "body text" || client.group != "alt.test" {
		t.Fatalf("body = %q, saved = %q, group = %q, err = %v", body, store.saved, client.group, err)
	}
}

func TestGetOrFetchDecodesHeadersAndStoresReaderSource(t *testing.T) {
	dir := t.TempDir()
	username, password := filepath.Join(dir, "user"), filepath.Join(dir, "pass")
	if err := os.WriteFile(username, []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(password, []byte("pass"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "a", UsernameFile: username, PasswordFile: password, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "p", AccountID: "a", Host: "example.test", Port: 563, TLS: true}}, Resources: config.ResourceConfig{MaxBodyBytes: 1024}}
	store := &readerMemoryStore{fakeStore: fakeStore{cacheErr: articles.ErrBodyNotCached, target: articles.BodyTarget{ArticleID: 4, ArticleNumber: 17, EndpointID: "p", Newsgroup: "comp.test"}}}
	service := New(cfg, store, accounts.NewGuard(cfg), nil)
	service.dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return &mimeClient{}, nil }
	text, err := service.GetOrFetch(context.Background(), 4)
	if err != nil || text != "café" || store.rawBody != "caf=E9" || store.reader.NormalizerVersion != archive.NormalizerVersion {
		t.Fatalf("text=%q raw=%q reader=%#v err=%v", text, store.rawBody, store.reader, err)
	}
}

func TestFailedBodyRetrievalChargesMeasuredTraffic(t *testing.T) {
	dir := t.TempDir()
	username, password := filepath.Join(dir, "user"), filepath.Join(dir, "pass")
	if err := os.WriteFile(username, []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(password, []byte("pass"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "a", UsernameFile: username, PasswordFile: password, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "p", AccountID: "a", Host: "example.test", Port: 563, TLS: true}}, Resources: config.ResourceConfig{MaxBodyBytes: 1024}}
	store := &fakeStore{cacheErr: articles.ErrBodyNotCached, target: articles.BodyTarget{EndpointID: "p", ArticleNumber: 17, Newsgroup: "comp.test"}}
	quota := &quotaRecorder{}
	service := New(cfg, store, accounts.NewGuard(cfg), quota)
	service.dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return &failingMeterClient{}, nil }
	if _, err := service.GetOrFetch(context.Background(), 4); err == nil || quota.bytes != 37 {
		t.Fatalf("error=%v charged=%d", err, quota.bytes)
	}
}

func TestDisabledBodyEndpointIsNotDialled(t *testing.T) {
	disabled := false
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "a", ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "p", AccountID: "a", Enabled: &disabled}}}
	store := &fakeStore{cacheErr: articles.ErrBodyNotCached, target: articles.BodyTarget{EndpointID: "p"}}
	service := New(cfg, store, accounts.NewGuard(cfg), nil)
	service.dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) {
		t.Fatal("disabled endpoint was dialled")
		return nil, nil
	}
	if _, err := service.GetOrFetch(context.Background(), 4); err == nil {
		t.Fatal("disabled endpoint was accepted")
	}
}

func TestGetOrFetchRejectsUnavailableSourceBeforeDialling(t *testing.T) {
	store := &fakeStore{cacheErr: articles.ErrBodyNotCached, target: articles.BodyTarget{ArticleID: 4, EndpointID: "missing"}}
	service := Service{store: store, endpoints: map[string]config.EndpointConfig{}, accounts: map[string]config.AccountConfig{}}
	if _, err := service.GetOrFetch(context.Background(), 4); err == nil || err.Error() != "body source endpoint is unavailable" {
		t.Fatalf("error = %v", err)
	}
}

func TestGetOrFetchChargesBodySizeWhenAdapterCannotMeasureTransfer(t *testing.T) {
	dir := t.TempDir()
	username, password := filepath.Join(dir, "username"), filepath.Join(dir, "password")
	if err := os.WriteFile(username, []byte("user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(password, []byte("password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Accounts: []config.AccountConfig{{ID: "account", UsernameFile: username, PasswordFile: password, ConnectionLimit: 1}}, Endpoints: []config.EndpointConfig{{ID: "endpoint", AccountID: "account", Host: "news.example", Port: 563, TLS: true}}, Resources: config.ResourceConfig{MaxBodyBytes: 1024}}
	store := &fakeStore{cacheErr: articles.ErrBodyNotCached, target: articles.BodyTarget{ArticleID: 4, ArticleNumber: 17, EndpointID: "endpoint", Newsgroup: "alt.test"}}
	quota := &quotaRecorder{}
	service := New(cfg, store, accounts.NewGuard(cfg), quota)
	service.dial = func(context.Context, nntp.Endpoint) (nntp.Client, error) { return &fakeClient{}, nil }
	body, err := service.GetOrFetch(context.Background(), 4)
	if err != nil || body != "body text" || quota.bytes != int64(len(body)) || store.saved != body {
		t.Fatalf("body=%q quota=%d saved=%q err=%v", body, quota.bytes, store.saved, err)
	}
}

func TestGetCachedNeverFallsBackToNetwork(t *testing.T) {
	service := Service{store: &fakeStore{cached: "stored only"}}
	if body, err := service.GetCached(context.Background(), 4); err != nil || body != "stored only" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}
