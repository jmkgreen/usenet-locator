// Package providers exposes safe configured endpoint metadata. It never reads
// or returns secret values.
package providers

import (
	"context"
	"sort"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
)

type Endpoint struct {
	ID                 string `json:"id"`
	AccountID          string `json:"account_id"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	TLS                bool   `json:"tls"`
	Primary            bool   `json:"primary"`
	Priority           int    `json:"priority"`
	ConnectionInUse    int    `json:"connection_in_use"`
	ConnectionLimit    int    `json:"connection_limit"`
	TransferUsedBytes  int64  `json:"transfer_used_bytes"`
	TransferLimitBytes *int64 `json:"transfer_limit_bytes"`
}
type Lister interface {
	List(context.Context) ([]Endpoint, error)
}
type Service struct {
	endpoints []Endpoint
	usage     accounts.UsageReader
	guard     accounts.Guard
}

func New(cfg config.Config, usage accounts.UsageReader, guard accounts.Guard) Service {
	endpoints := make([]Endpoint, 0, len(cfg.Endpoints))
	for _, source := range cfg.Endpoints {
		endpoints = append(endpoints, Endpoint{ID: source.ID, AccountID: source.AccountID, Host: source.Host, Port: source.Port, TLS: source.TLS, Primary: source.Primary, Priority: source.Priority})
	}
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Priority == endpoints[j].Priority {
			return endpoints[i].ID < endpoints[j].ID
		}
		return endpoints[i].Priority < endpoints[j].Priority
	})
	return Service{endpoints: endpoints, usage: usage, guard: guard}
}

func (s Service) List(ctx context.Context) ([]Endpoint, error) {
	items := append([]Endpoint(nil), s.endpoints...)
	for index := range items {
		items[index].ConnectionInUse, items[index].ConnectionLimit, _ = s.guard.ConnectionUsage(items[index].AccountID)
	}
	if s.usage == nil {
		return items, nil
	}
	usage, err := s.usage.ListUsage(ctx)
	if err != nil {
		return nil, err
	}
	byAccount := make(map[string]accounts.Usage, len(usage))
	for _, item := range usage {
		byAccount[item.AccountID] = item
	}
	for index := range items {
		if item, ok := byAccount[items[index].AccountID]; ok {
			items[index].TransferUsedBytes = item.TransferUsedBytes
			items[index].TransferLimitBytes = item.TransferLimitBytes
		}
	}
	return items, nil
}
