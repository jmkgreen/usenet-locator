// Package retention probes and records the oldest article currently observed
// at each configured provider without attempting an unbounded history scan.
package retention

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
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
	NextArticle   *int64         `json:"next_article_number,omitempty"`
	CursorHigh    *int64         `json:"cursor_high,omitempty"`
	Article       *nntp.Overview `json:"-"`
	TransferBytes int64          `json:"-"`
	JobID         string         `json:"-"`
	ProbeError    error          `json:"-"`
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
	foundCursor := false
	var failures []error
	for _, endpoint := range s.endpoints {
		if !endpoint.IsEnabled() {
			continue
		}
		start, high, found, err := s.store.Cursor(ctx, endpoint.ID, group)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		foundCursor = true
		if start > high {
			continue
		}
		if err := s.retrieveEndpoint(ctx, endpoint, group, start, high, limit); err != nil {
			failures = append(failures, fmt.Errorf("provider %s: %w", endpoint.ID, err))
			if ctx.Err() != nil {
				return errors.Join(failures...)
			}
		}
	}
	if !foundCursor {
		return fmt.Errorf("find earliest retained article before retrieving older headers")
	}
	return errors.Join(failures...)
}

func (s Service) retrieveEndpoint(ctx context.Context, endpoint config.EndpointConfig, group string, start, high int64, limit int) error {
	account, ok := s.accounts[endpoint.AccountID]
	if !ok {
		return fmt.Errorf("configured account unavailable")
	}
	if err := s.checkQuota(ctx, account.ID); err != nil {
		return err
	}
	release, err := s.guard.Acquire(ctx, account.ID)
	if err != nil {
		return err
	}
	defer release()
	if err := s.checkQuota(ctx, account.ID); err != nil {
		return err
	}
	username, password, err := account.Credentials()
	if err != nil {
		return fmt.Errorf("credentials unavailable")
	}
	client, err := s.dial(ctx, nntp.Endpoint{Address: net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), ServerName: endpoint.Host, TLS: endpoint.TLS, ConnectTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second, OverviewCommand: endpoint.OverviewCommand})
	if err != nil {
		return fmt.Errorf("connection failed")
	}
	defer client.Close()
	charged := false
	defer func() {
		if !charged && s.quota != nil {
			if meter, ok := client.(nntp.TransferMeter); ok && meter.TransferBytes() > 0 {
				chargeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_ = s.quota.Consume(chargeCtx, account.ID, meter.TransferBytes())
			}
		}
	}()
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
	for windows := 0; next <= high && len(headers) < limit && windows < maxProbeWindows; windows++ {
		end := min(next+probeWindow-1, high)
		window := make(map[int64]nntp.Overview)
		if err := client.Overview(ctx, next, end, func(item nntp.Overview) error {
			if item.ArticleNumber >= next && item.ArticleNumber <= end {
				window[item.ArticleNumber] = item
			}
			return nil
		}); err != nil {
			return fmt.Errorf("overview retrieval failed")
		}
		numbers := make([]int64, 0, len(window))
		for number := range window {
			numbers = append(numbers, number)
		}
		sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
		lastStored := int64(0)
		for _, number := range numbers {
			if len(headers) == limit {
				break
			}
			headers = append(headers, window[number])
			lastStored = number
		}
		if len(headers) == limit {
			next = lastStored + 1
		} else {
			next = end + 1
		}
	}
	if s.quota != nil {
		if meter, ok := client.(nntp.TransferMeter); ok && meter.TransferBytes() > 0 {
			charged = true
			if err := s.quota.Consume(ctx, account.ID, meter.TransferBytes()); err != nil {
				return err
			}
		}
	}
	return s.store.StoreHeaders(ctx, endpoint.ID, group, headers, next, high)
}

func (s Service) checkQuota(ctx context.Context, accountID string) error {
	return accounts.CheckAvailable(ctx, s.quota, accountID)
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
		if !endpoint.IsEnabled() {
			continue
		}
		observation := s.probeEndpoint(ctx, endpoint, group, nil)
		stored, err := s.store.Record(ctx, observation)
		if err != nil {
			return nil, fmt.Errorf("record retention observation: %w", err)
		}
		result = append(result, stored)
	}
	return result, nil
}

// ProbeProvider executes and stores one provider's observation for a queued job.
func (s Service) ProbeProvider(ctx context.Context, jobID, endpointID, group string) (Observation, error) {
	for _, endpoint := range s.endpoints {
		if endpoint.ID == endpointID && endpoint.IsEnabled() {
			var check func(context.Context) error
			if checker, ok := s.store.(interface {
				CheckJobRunning(context.Context, string) error
			}); ok {
				check = func(ctx context.Context) error { return checker.CheckJobRunning(ctx, jobID) }
			}
			observation := s.probeEndpoint(ctx, endpoint, group, check)
			if observation.ProbeError != nil {
				return Observation{}, observation.ProbeError
			}
			if err := ctx.Err(); err != nil {
				return Observation{}, err
			}
			observation.JobID = jobID
			return s.store.Record(ctx, observation)
		}
	}
	return Observation{}, fmt.Errorf("configured endpoint unavailable")
}

func (s Service) probeEndpoint(ctx context.Context, endpoint config.EndpointConfig, group string, check func(context.Context) error) (observation Observation) {
	observation = Observation{Endpoint: endpoint.ID, Newsgroup: group, Outcome: "connection_failed"}
	if check != nil {
		if err := check(ctx); err != nil {
			observation.ProbeError = err
			return observation
		}
	}
	account, ok := s.accounts[endpoint.AccountID]
	if !ok {
		return observation
	}
	if err := s.checkQuota(ctx, account.ID); err != nil {
		if errors.Is(err, accounts.ErrQuotaExceeded) {
			observation.Outcome = "quota_reached"
		}
		return observation
	}
	release, err := s.guard.Acquire(ctx, account.ID)
	if err != nil {
		return observation
	}
	defer release()
	if err := s.checkQuota(ctx, account.ID); err != nil {
		if errors.Is(err, accounts.ErrQuotaExceeded) {
			observation.Outcome = "quota_reached"
		}
		return observation
	}
	username, password, err := account.Credentials()
	if err != nil {
		return observation
	}
	client, err := s.dial(ctx, nntp.Endpoint{Address: net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), ServerName: endpoint.Host, TLS: endpoint.TLS, ConnectTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second, OverviewCommand: endpoint.OverviewCommand})
	if err != nil {
		return observation
	}
	defer client.Close()
	defer func() {
		if meter, ok := client.(nntp.TransferMeter); ok {
			observation.TransferBytes = meter.TransferBytes()
			if s.quota != nil && observation.TransferBytes > 0 {
				chargeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if err := s.quota.Consume(chargeCtx, account.ID, observation.TransferBytes); err != nil {
					if errors.Is(err, accounts.ErrQuotaExceeded) {
						observation.Outcome = "quota_reached"
					} else {
						observation.Outcome = "connection_failed"
					}
					observation.Article, observation.ArticleNumber, observation.ObservedDate = nil, nil, nil
				}
			}
		}
	}()
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
	observation.Outcome = "no_header_in_probe"
	var candidate *nntp.Overview
	for start := bounds.Low; start > 0 && start <= bounds.High && start < bounds.Low+probeWindow*maxProbeWindows; start += probeWindow {
		if check != nil {
			if err := check(ctx); err != nil {
				observation.ProbeError = err
				return observation
			}
		}
		end := min(start+probeWindow-1, bounds.High)
		if err := client.Overview(ctx, start, end, func(item nntp.Overview) error {
			if item.ArticleNumber >= start && item.ArticleNumber <= end && validMessageID(item.MessageID) && (candidate == nil || item.ArticleNumber < candidate.ArticleNumber) {
				copy := item
				candidate = &copy
			}
			return nil
		}); err != nil {
			observation.Outcome = "overview_failed"
			candidate = nil
			break
		}
		if candidate != nil {
			break
		}
	}
	if candidate != nil {
		observation.Article = candidate
		observation.Outcome = "found"
		if candidate.Date.IsZero() {
			observation.Outcome = "undated"
		}
	}
	if observation.Article != nil {
		number := observation.Article.ArticleNumber
		observation.ArticleNumber = &number
		if !observation.Article.Date.IsZero() {
			date := observation.Article.Date.UTC()
			observation.ObservedDate = &date
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
