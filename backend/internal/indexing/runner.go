package indexing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/accounts"
	"github.com/jmkgreen/usenet-locator/backend/internal/config"
	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type DialFunc func(context.Context, nntp.Endpoint) (nntp.Client, error)

// Runner creates a bounded NNTP client for one durable job. Secrets are read
// only from the configured environment variables and are never stored or
// returned through the job/API boundary.
type Runner struct {
	Endpoints map[string]config.EndpointConfig
	Accounts  map[string]config.AccountConfig
	Writer    BatchWriter
	Finalizer jobs.Finalizer
	BatchSize int64
	Dial      DialFunc
	Guard     accounts.Guard
	Quota     accounts.QuotaConsumer
	JobQuota  jobs.TransferConsumer
}

func NewRunner(cfg config.Config, writer BatchWriter, finalizer jobs.Finalizer, guard accounts.Guard, quota accounts.QuotaConsumer, jobQuota jobs.TransferConsumer) Runner {
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
	return Runner{Endpoints: endpoints, Accounts: accounts, Writer: writer, Finalizer: finalizer, BatchSize: int64(cfg.Resources.BatchSize), Dial: nntp.Dial, Guard: guard, Quota: quota, JobQuota: jobQuota}
}

func (r Runner) Run(ctx context.Context, job jobs.Job) error {
	endpoint, ok := r.Endpoints[job.Endpoint]
	if !ok {
		return r.interrupt(ctx, job.ID, "configured endpoint is unavailable")
	}
	account, ok := r.Accounts[endpoint.AccountID]
	if !ok {
		return r.interrupt(ctx, job.ID, "configured account is unavailable")
	}
	release, err := r.Guard.Acquire(ctx, account.ID)
	if err != nil {
		return r.interrupt(ctx, job.ID, "NNTP account connection limit unavailable")
	}
	defer release()
	if r.Dial == nil || r.Writer == nil || r.Finalizer == nil || r.BatchSize < 1 {
		return r.interrupt(ctx, job.ID, "index worker is incomplete")
	}
	username, password, err := account.Credentials()
	if err != nil {
		return r.interrupt(ctx, job.ID, "NNTP credentials are unavailable")
	}
	client, err := r.Dial(ctx, nntp.Endpoint{Address: net.JoinHostPort(endpoint.Host, fmt.Sprint(endpoint.Port)), ServerName: endpoint.Host, TLS: endpoint.TLS, ConnectTimeout: 15 * time.Second, ReadTimeout: 30 * time.Second, OverviewCommand: endpoint.OverviewCommand})
	if err != nil {
		return r.interrupt(ctx, job.ID, "NNTP connection failed")
	}
	defer client.Close()
	charged := int64(0)
	if err := client.Authenticate(ctx, username, password); err != nil {
		return r.interrupt(ctx, job.ID, "NNTP authentication failed")
	}
	if _, err := client.Capabilities(ctx); err != nil {
		return r.interrupt(ctx, job.ID, "NNTP capability negotiation failed")
	}
	if err := client.ModeReader(ctx); err != nil {
		return r.interrupt(ctx, job.ID, "NNTP reader mode failed")
	}
	if err := r.recordTransfer(ctx, account.ID, job.ID, transferBytes(client)-charged); err != nil {
		return r.interrupt(ctx, job.ID, transferFailureReason(err))
	}
	if err := (Scanner{Client: client, Writer: r.Writer, BatchSize: r.BatchSize, RecordTransfer: func(ctx context.Context, bytes int64) error {
		return r.recordTransfer(ctx, account.ID, job.ID, bytes)
	}}).Scan(ctx, job); err != nil {
		if errors.Is(err, accounts.ErrQuotaExceeded) || errors.Is(err, jobs.ErrTransferLimitExceeded) {
			return r.interrupt(ctx, job.ID, transferFailureReason(err))
		}
		return r.interrupt(ctx, job.ID, scanFailureReason(err))
	}
	if err := r.Finalizer.Complete(ctx, job.ID); err != nil {
		return fmt.Errorf("complete indexing job: %w", err)
	}
	return nil
}

func scanFailureReason(err error) string {
	// Scanner errors are deliberately reduced to operational stages before they
	// reach persistent job status: provider response text may be untrusted and
	// must not be retained or displayed.
	message := err.Error()
	var overviewError *nntp.OverviewError
	if errors.As(err, &overviewError) {
		return fmt.Sprintf("NNTP %s returned %d", overviewError.Command, overviewError.Code)
	}
	switch {
	case strings.HasPrefix(message, "select newsgroup:"):
		return "NNTP newsgroup selection failed"
	case strings.HasPrefix(message, "read overview"):
		return "NNTP overview retrieval failed"
	default:
		return "NNTP scan interrupted"
	}
}

func transferBytes(client nntp.Client) int64 {
	if meter, ok := client.(nntp.TransferMeter); ok {
		return meter.TransferBytes()
	}
	return 0
}

func (r Runner) recordTransfer(ctx context.Context, accountID, jobID string, bytes int64) error {
	if bytes <= 0 {
		return nil
	}
	if r.Quota != nil {
		if err := r.Quota.Consume(ctx, accountID, bytes); err != nil {
			return err
		}
	}
	if r.JobQuota != nil {
		return r.JobQuota.ConsumeTransfer(ctx, jobID, bytes)
	}
	return nil
}

func transferFailureReason(err error) string {
	if errors.Is(err, jobs.ErrTransferLimitExceeded) {
		return "job transfer budget reached"
	}
	return "NNTP account transfer quota reached"
}

func (r Runner) interrupt(ctx context.Context, jobID, reason string) error {
	if r.Finalizer != nil {
		if err := r.Finalizer.Interrupt(ctx, jobID, reason); err != nil {
			return fmt.Errorf("interrupt indexing job: %w", err)
		}
	}
	return fmt.Errorf("%s", reason)
}
