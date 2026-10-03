// Package retrieval fetches bounded text bodies only after article eligibility
// has been checked by the local article service.
package retrieval

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/articles"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type Store interface {
	BodyTarget(context.Context, int64) (articles.BodyTarget, error)
	CachedBody(context.Context, int64) (string, error)
	SaveBody(context.Context, int64, string, string) error
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
		endpoints[endpoint.ID] = endpoint
	}
	accounts := make(map[string]config.AccountConfig, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		accounts[account.ID] = account
	}
	return Service{store: store, endpoints: endpoints, accounts: accounts, maxBytes: int64(cfg.Resources.MaxBodyBytes), dial: nntp.Dial, guard: guard, quota: quota}
}

func (s Service) GetOrFetch(ctx context.Context, articleID int64) (string, error) {
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
	release, err := s.guard.Acquire(ctx, account.ID)
	if err != nil {
		return "", fmt.Errorf("NNTP account connection limit unavailable: %w", err)
	}
	defer release()
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
		if err := s.quota.Consume(ctx, account.ID, transferred); err != nil {
			return "", fmt.Errorf("account transfer quota: %w", err)
		}
	}
	if err := s.store.SaveBody(ctx, articleID, target.EndpointID, body.String()); err != nil {
		return "", err
	}
	return body.String(), nil
}

// GetCached returns text already retained locally. It deliberately has no
// fallback to NNTP, making it safe for download endpoints and repeat access.
func (s Service) GetCached(ctx context.Context, articleID int64) (string, error) {
	return s.store.CachedBody(ctx, articleID)
}

func transferBytes(client nntp.Client) (int64, bool) {
	meter, ok := client.(nntp.TransferMeter)
	if !ok {
		return 0, false
	}
	return meter.TransferBytes(), true
}
