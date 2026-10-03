// Package retention probes and records the oldest article currently observed
// at each configured provider without attempting an unbounded history scan.
package retention

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

const probeWindow = int64(100)
const maxProbeWindows = 10

type Observation struct {
	Endpoint      string         `json:"endpoint"`
	Newsgroup     string         `json:"newsgroup"`
	GroupLow      int64          `json:"group_low"`
	GroupHigh     int64          `json:"group_high"`
	ArticleNumber *int64         `json:"article_number,omitempty"`
	ArticleID     *int64         `json:"article_id,omitempty"`
	ObservedDate  *time.Time     `json:"observed_date,omitempty"`
	Outcome       string         `json:"outcome"`
	ObservedAt    time.Time      `json:"observed_at"`
	Article       *nntp.Overview `json:"-"`
}

type Store interface {
	Record(context.Context, Observation) (Observation, error)
	ListLatest(context.Context, string) ([]Observation, error)
	Cursor(context.Context, string, string) (int64, int64, bool, error)
	StoreHeaders(context.Context, string, string, []nntp.Overview, int64, int64) error
}

// RetrieveNext stores up to limit additional overview headers per enabled
// provider, starting from that provider's durable cursor. It never fetches
// article bodies. The UI presents the merged canonical results.
func (s Service) RetrieveNext(ctx context.Context, group string, limit int) error {
	if group == "" || limit < 1 || limit > 100 {
		return fmt.Errorf("newsgroup and a limit from 1 to 100 are required")
	}
	for _, endpoint := range s.endpoints {
		start, high, found, err := s.store.Cursor(ctx, endpoint.ID, group)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("find earliest retained article before retrieving older headers")
		}
		if start > high {
			continue
		}
		if err := s.retrieveEndpoint(ctx, endpoint, group, start, high, limit); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) retrieveEndpoint(ctx context.Context, endpoint config.EndpointConfig, group string, start, high int64, limit int) error {
	account, ok := s.accounts[endpoint.AccountID]
	if !ok {
		return fmt.Errorf("configured account unavailable")
	}
	release, err := s.guard.Acquire(ctx, account.ID)
	if err != nil {
		return err
	}
	defer release()
	username, password, err := account.Credentials()
	if err != nil {
		return fmt.Errorf("credentials unavailable")
	}
	client, err := s.dial(ctx, nntp.Endpoint{Address: net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), ServerName: endpoint.Host, TLS: endpoint.TLS, ConnectTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second, OverviewCommand: endpoint.OverviewCommand})
	if err != nil {
		return fmt.Errorf("connection failed")
	}
	defer client.Close()
	if err := client.Authenticate(ctx, username, password); err != nil {
		return fmt.Errorf("authentication failed")
	}
	if _, err := client.Capabilities(ctx); err != nil {
		return fmt.Errorf("capability negotiation failed")
	}
	if err := client.ModeReader(ctx); err != nil {
		return fmt.Errorf("reader mode failed")
	}
	if _, err := client.Group(ctx, group); err != nil {
		return fmt.Errorf("newsgroup unavailable")
	}
	var headers []nntp.Overview
	next := start
	for next <= high && len(headers) < limit {
		end := min(next+probeWindow-1, high)
		if err := client.Overview(ctx, next, end, func(item nntp.Overview) error {
			if item.MessageID != "" && !item.Date.IsZero() && len(headers) < limit {
				headers = append(headers, item)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("overview retrieval failed")
		}
		next = end + 1
	}
	if s.quota != nil {
		if meter, ok := client.(nntp.TransferMeter); ok && meter.TransferBytes() > 0 {
			if err := s.quota.Consume(ctx, account.ID, meter.TransferBytes()); err != nil {
				return err
			}
		}
	}
	return s.store.StoreHeaders(ctx, endpoint.ID, group, headers, next, high)
}

type DialFunc func(context.Context, nntp.Endpoint) (nntp.Client, error)

type Service struct {
	store     Store
	endpoints []config.EndpointConfig
	accounts  map[string]config.AccountConfig
	guard     accounts.Guard
	quota     accounts.QuotaConsumer
	dial      DialFunc
}

func New(cfg config.Config, store Store, guard accounts.Guard, quota accounts.QuotaConsumer) Service {
	accounts := make(map[string]config.AccountConfig, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		accounts[account.ID] = account
	}
	return Service{store: store, endpoints: cfg.Endpoints, accounts: accounts, guard: guard, quota: quota, dial: nntp.Dial}
}

func (s Service) List(ctx context.Context, group string) ([]Observation, error) {
	return s.store.ListLatest(ctx, group)
}

// Probe uses no more than ten 100-record overview windows per provider. It
// stores fixed outcomes rather than provider response text.
func (s Service) Probe(ctx context.Context, group string) ([]Observation, error) {
	if group == "" {
		return nil, fmt.Errorf("newsgroup is required")
	}
	var result []Observation
	for _, endpoint := range s.endpoints {
		observation := s.probeEndpoint(ctx, endpoint, group)
		stored, err := s.store.Record(ctx, observation)
		if err != nil {
			return nil, fmt.Errorf("record retention observation: %w", err)
		}
		result = append(result, stored)
	}
	return result, nil
}

func (s Service) probeEndpoint(ctx context.Context, endpoint config.EndpointConfig, group string) Observation {
	observation := Observation{Endpoint: endpoint.ID, Newsgroup: group, Outcome: "connection_failed"}
	account, ok := s.accounts[endpoint.AccountID]
	if !ok {
		return observation
	}
	release, err := s.guard.Acquire(ctx, account.ID)
	if err != nil {
		return observation
	}
	defer release()
	username, password, err := account.Credentials()
	if err != nil {
		return observation
	}
	client, err := s.dial(ctx, nntp.Endpoint{Address: net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), ServerName: endpoint.Host, TLS: endpoint.TLS, ConnectTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second, OverviewCommand: endpoint.OverviewCommand})
	if err != nil {
		return observation
	}
	defer client.Close()
	if err := client.Authenticate(ctx, username, password); err != nil {
		observation.Outcome = "authentication_failed"
		return observation
	}
	if _, err := client.Capabilities(ctx); err != nil {
		observation.Outcome = "capability_failed"
		return observation
	}
	if err := client.ModeReader(ctx); err != nil {
		observation.Outcome = "reader_mode_failed"
		return observation
	}
	bounds, err := client.Group(ctx, group)
	if err != nil {
		observation.Outcome = "group_unavailable"
		return observation
	}
	observation.GroupLow, observation.GroupHigh = bounds.Low, bounds.High
	var candidate *nntp.Overview
	for start := bounds.Low; start <= bounds.High && start < bounds.Low+probeWindow*maxProbeWindows; start += probeWindow {
		end := min(start+probeWindow-1, bounds.High)
		if err := client.Overview(ctx, start, end, func(item nntp.Overview) error {
			if candidate == nil && item.MessageID != "" {
				copy := item
				candidate = &copy
			}
			if !item.Date.IsZero() && observation.Article == nil {
				copy := item
				observation.Article = &copy
			}
			return nil
		}); err != nil {
			observation.Outcome = "overview_failed"
			break
		}
		if observation.Article != nil {
			break
		}
	}
	if observation.Article == nil && candidate != nil {
		observation.Article = candidate
		observation.Outcome = "undated"
	}
	if observation.Article != nil && observation.Outcome != "undated" {
		observation.Outcome = "found"
	}
	if observation.Article != nil {
		number := observation.Article.ArticleNumber
		observation.ArticleNumber = &number
		if !observation.Article.Date.IsZero() {
			date := observation.Article.Date.UTC()
			observation.ObservedDate = &date
		}
	}
	if s.quota != nil {
		if meter, ok := client.(nntp.TransferMeter); ok && meter.TransferBytes() > 0 {
			if err := s.quota.Consume(ctx, account.ID, meter.TransferBytes()); err != nil && errors.Is(err, accounts.ErrQuotaExceeded) {
				observation.Outcome = "quota_reached"
			}
		}
	}
	return observation
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
