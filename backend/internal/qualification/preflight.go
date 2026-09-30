// Package qualification performs small, explicitly requested NNTP compatibility checks.
package qualification

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/james/usenet-locator/backend/internal/accounts"
	"github.com/james/usenet-locator/backend/internal/config"
	"github.com/james/usenet-locator/backend/internal/nntp"
)

type Result struct {
	Capabilities       []string `json:"capabilities"`
	OverviewFormatCode int      `json:"overview_format_code"`
	OverviewFields     []string `json:"overview_fields"`
	StatCode           int      `json:"stat_code"`
	GroupSelected      bool     `json:"group_selected"`
	OverviewCode       int      `json:"overview_code"`
	GroupLow           int64    `json:"group_low"`
	GroupHigh          int64    `json:"group_high"`
	OverviewRows       int      `json:"overview_rows"`
	OverviewDates      int      `json:"overview_dates"`
}
type Service struct {
	Endpoints map[string]config.EndpointConfig
	Accounts  map[string]config.AccountConfig
	Guard     accounts.Guard
	Quota     accounts.QuotaConsumer
	Dial      func(context.Context, nntp.Endpoint) (nntp.Client, error)
}

func New(cfg config.Config, guard accounts.Guard, quota accounts.QuotaConsumer) Service {
	s := Service{Endpoints: map[string]config.EndpointConfig{}, Accounts: map[string]config.AccountConfig{}, Guard: guard, Quota: quota, Dial: nntp.Dial}
	for _, e := range cfg.Endpoints {
		s.Endpoints[e.ID] = e
	}
	for _, a := range cfg.Accounts {
		s.Accounts[a.ID] = a
	}
	return s
}
func (s Service) Run(ctx context.Context, endpointID, messageID, group string, articleNumber int64) (result Result, err error) {
	e, ok := s.Endpoints[endpointID]
	if !ok {
		return result, fmt.Errorf("endpoint unavailable")
	}
	a, ok := s.Accounts[e.AccountID]
	if !ok {
		return result, fmt.Errorf("account unavailable")
	}
	release, err := s.Guard.Acquire(ctx, a.ID)
	if err != nil {
		return result, fmt.Errorf("connection limit unavailable")
	}
	defer release()
	u, p := os.Getenv(a.UsernameFromEnv), os.Getenv(a.PasswordFromEnv)
	if u == "" || p == "" {
		return result, fmt.Errorf("credentials unavailable")
	}
	c, err := s.Dial(ctx, nntp.Endpoint{Address: net.JoinHostPort(e.Host, fmt.Sprint(e.Port)), ServerName: e.Host, TLS: e.TLS, ConnectTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second})
	if err != nil {
		return result, fmt.Errorf("connection failed")
	}
	defer c.Close()
	defer func() {
		if m, ok := c.(nntp.TransferMeter); ok && s.Quota != nil {
			if qerr := s.Quota.Consume(ctx, a.ID, m.TransferBytes()); err == nil && qerr != nil {
				err = qerr
			}
		}
	}()
	if err = c.Authenticate(ctx, u, p); err != nil {
		return result, fmt.Errorf("authentication failed")
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return result, fmt.Errorf("capability negotiation failed")
	}
	result.Capabilities = recognisedCapabilities(caps)
	if err = c.ModeReader(ctx); err != nil {
		return result, fmt.Errorf("reader mode failed")
	}
	if f, ok := c.(nntp.OverviewFormatClient); ok {
		fresult, ferr := f.OverviewFormat(ctx)
		if ferr != nil {
			return result, fmt.Errorf("overview format check failed")
		}
		result.OverviewFormatCode = fresult.Code
		result.OverviewFields = fresult.Fields
	}
	if messageID != "" {
		stat, ok := c.(nntp.StatClient)
		if !ok {
			return result, fmt.Errorf("STAT unavailable")
		}
		_, serr := stat.Stat(ctx, messageID)
		if serr == nil {
			result.StatCode = 223
		} else if e, ok := serr.(*nntp.OverviewError); ok {
			result.StatCode = e.Code
		} else {
			return result, fmt.Errorf("STAT check failed")
		}
	}
	if group != "" {
		selected, groupErr := c.Group(ctx, group)
		if groupErr != nil {
			return result, fmt.Errorf("newsgroup selection failed")
		}
		result.GroupSelected = true
		result.GroupLow, result.GroupHigh = selected.Low, selected.High
		if selected.High < 1 {
			return result, fmt.Errorf("newsgroup has no articles")
		}
		if articleNumber < 1 {
			articleNumber = selected.High
		}
		if articleNumber < selected.Low || articleNumber > selected.High {
			return result, fmt.Errorf("article number outside selected group")
		}
		overviewErr := c.Overview(ctx, articleNumber, articleNumber, func(item nntp.Overview) error {
			result.OverviewRows++
			if !item.Date.IsZero() {
				result.OverviewDates++
			}
			return nil
		})
		if overviewErr == nil {
			result.OverviewCode = 224
		} else if protocolErr, ok := overviewErr.(*nntp.OverviewError); ok {
			result.OverviewCode = protocolErr.Code
		} else {
			return result, fmt.Errorf("overview check failed")
		}
	}
	return result, nil
}
func recognisedCapabilities(lines []string) []string {
	known := map[string]bool{"VERSION": true, "READER": true, "OVER": true, "HDR": true, "LIST": true}
	var out []string
	for _, line := range lines {
		f := strings.Fields(strings.ToUpper(line))
		if len(f) > 0 && known[f[0]] {
			out = append(out, f[0])
		}
	}
	return out
}
