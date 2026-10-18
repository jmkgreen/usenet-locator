// Package retrieval fetches bounded text bodies only after article eligibility
// has been checked by the local article service.
package retrieval

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/archive"
	"github.com/jmkgreen/usenet-locator/backend/internal/articles"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type Store interface {
	BodyTarget(context.Context, int64) (articles.BodyTarget, error)
	CachedBody(context.Context, int64) (string, error)
	SaveBody(context.Context, int64, string, string) error
}

type readerStore interface {
	SaveReader(context.Context, int64, string, []byte, []byte, archive.Reader) error
	CachedReader(context.Context, int64) (archive.Reader, error)
}

type Fetcher interface {
	GetOrFetch(context.Context, int64) (string, error)
	GetCached(context.Context, int64) (string, error)
}
type DialFunc func(context.Context, nntp.Endpoint) (nntp.Client, error)

type Service struct {
	store     Store
	endpoints map[string]config.EndpointConfig
	accounts  map[string]config.AccountConfig
	maxBytes  int64
	dial      DialFunc
	guard     accounts.Guard
	quota     accounts.QuotaConsumer
}

func New(cfg config.Config, store Store, guard accounts.Guard, quota accounts.QuotaConsumer) Service {
	endpoints := make(map[string]config.EndpointConfig, len(cfg.Endpoints))
	for _, endpoint := range cfg.Endpoints {
		if endpoint.IsEnabled() {
			endpoints[endpoint.ID] = endpoint
		}
	}
	accounts := make(map[string]config.AccountConfig, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		accounts[account.ID] = account
	}
	return Service{store: store, endpoints: endpoints, accounts: accounts, maxBytes: int64(cfg.Resources.MaxBodyBytes), dial: nntp.Dial, guard: guard, quota: quota}
}

func (s Service) GetOrFetch(ctx context.Context, articleID int64) (text string, resultErr error) {
	if cached, err := s.store.CachedBody(ctx, articleID); err == nil {
		return cached, nil
	} else if err != articles.ErrBodyNotCached {
		return "", err
	}
	target, err := s.store.BodyTarget(ctx, articleID)
	if err != nil {
		return "", err
	}
	endpoint, ok := s.endpoints[target.EndpointID]
	if !ok {
		return "", fmt.Errorf("body source endpoint is unavailable")
	}
	account, ok := s.accounts[endpoint.AccountID]
	if !ok {
		return "", fmt.Errorf("body source account is unavailable")
	}
	if err := accounts.CheckAvailable(ctx, s.quota, account.ID); err != nil {
		return "", err
	}
	release, err := s.guard.Acquire(ctx, account.ID)
	if err != nil {
		return "", fmt.Errorf("NNTP account connection limit unavailable: %w", err)
	}
	defer release()
	if err := accounts.CheckAvailable(ctx, s.quota, account.ID); err != nil {
		return "", err
	}
	username, password, err := account.Credentials()
	if err != nil {
		return "", fmt.Errorf("NNTP credentials are unavailable")
	}
	if s.dial == nil || s.maxBytes < 1 {
		return "", fmt.Errorf("body retrieval is not configured")
	}
	client, err := s.dial(ctx, nntp.Endpoint{Address: net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), ServerName: endpoint.Host, TLS: endpoint.TLS, ConnectTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second})
	if err != nil {
		return "", fmt.Errorf("connect body source: %w", err)
	}
	defer client.Close()
	charged := false
	defer func() {
		if charged || s.quota == nil {
			return
		}
		if transferred, ok := transferBytes(client); ok && transferred > 0 {
			chargeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := s.quota.Consume(chargeCtx, account.ID, transferred); resultErr == nil && err != nil {
				resultErr = err
			}
		}
	}()
	if err := client.Authenticate(ctx, username, password); err != nil {
		return "", fmt.Errorf("authenticate body source: %w", err)
	}
	if err := client.ModeReader(ctx); err != nil {
		return "", fmt.Errorf("enter reader mode: %w", err)
	}
	// NNTP article numbers are scoped to a selected newsgroup.  Locations keep
	// both values, so select that group before issuing BODY <article-number>.
	if _, err := client.Group(ctx, target.Newsgroup); err != nil {
		return "", fmt.Errorf("select body source group: %w", err)
	}
	var headers bytes.Buffer
	if head, ok := client.(nntp.HeaderClient); ok {
		if err := head.Head(ctx, target.ArticleNumber, 64*1024, &headers); err != nil {
			var response *nntp.OverviewError
			if !errors.As(err, &response) || (response.Code != 500 && response.Code != 501) {
				return "", fmt.Errorf("retrieve article headers: %w", err)
			}
		}
	}
	var body bytes.Buffer
	if err := client.Body(ctx, target.ArticleNumber, s.maxBytes, &body); err != nil {
		return "", err
	}
	if s.quota != nil {
		transferred, measured := transferBytes(client)
		if !measured {
			// Third-party adapters that cannot expose transport bytes retain a
			// conservative successful-body fallback until they gain a meter.
			transferred = int64(body.Len())
		}
		charged = true
		if err := s.quota.Consume(ctx, account.ID, transferred); err != nil {
			return "", fmt.Errorf("account transfer quota: %w", err)
		}
	}
	reader := archive.DecodeReader(headers.Bytes(), body.Bytes(), s.maxBytes)
	if store, ok := s.store.(readerStore); ok {
		if err := store.SaveReader(ctx, articleID, target.EndpointID, headers.Bytes(), body.Bytes(), reader); err != nil {
			return "", err
		}
		return reader.Text, nil
	}
	if err := s.store.SaveBody(ctx, articleID, target.EndpointID, reader.Text); err != nil {
		return "", err
	}
	return reader.Text, nil
}

// GetCached returns text already retained locally. It deliberately has no
// fallback to NNTP, making it safe for download endpoints and repeat access.
func (s Service) GetCached(ctx context.Context, articleID int64) (string, error) {
	return s.store.CachedBody(ctx, articleID)
}

// GetCachedReader never opens a provider connection.
func (s Service) GetCachedReader(ctx context.Context, articleID int64) (archive.Reader, error) {
	if store, ok := s.store.(readerStore); ok {
		return store.CachedReader(ctx, articleID)
	}
	text, err := s.store.CachedBody(ctx, articleID)
	return archive.Reader{Text: text}, err
}

func transferBytes(client nntp.Client) (int64, bool) {
	meter, ok := client.(nntp.TransferMeter)
	if !ok {
		return 0, false
	}
	return meter.TransferBytes(), true
}
