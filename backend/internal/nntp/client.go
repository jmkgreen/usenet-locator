// Package nntp defines the bounded, reader-oriented NNTP boundary used by the
// indexer. Implementations may be third-party adapters or the narrow fallback.
package nntp

import (
	"context"
	"fmt"
	"io"
	"time"
)

// OverviewError preserves only the command and numeric NNTP response code.
// It intentionally excludes untrusted server response text from persistent
// status and logs.
type OverviewError struct {
	Command string
	Code    int
}

func (e *OverviewError) Error() string { return fmt.Sprintf("%s returned %d", e.Command, e.Code) }

type Client interface {
	Authenticate(context.Context, string, string) error
	Capabilities(context.Context) ([]string, error)
	ModeReader(context.Context) error
	Group(context.Context, string) (Group, error)
	Overview(context.Context, int64, int64, func(Overview) error) error
	Body(context.Context, int64, int64, io.Writer) error
	Close() error
}

// TransferMeter is optional so protocol fakes and alternative clients need not
// implement accounting. The wire client reports bytes sent and received over
// its NNTP connection.
type TransferMeter interface {
	TransferBytes() int64
}

// OverviewFormatClient is an optional diagnostic surface.  It deliberately
// reports only the numeric outcome and recognised standard field names, not
// arbitrary provider response text.
type OverviewFormatClient interface {
	OverviewFormat(context.Context) (OverviewFormat, error)
}

// StatClient is an optional diagnostic surface for a single, known Message-ID.
// It is not used by indexing, which must never infer an article range from an
// untrusted identifier.
type StatClient interface {
	Stat(context.Context, string) (int, error)
}

type OverviewFormat struct {
	Code   int
	Fields []string
}

type Group struct {
	Name string
	Low  int64
	High int64
}

type Overview struct {
	ArticleNumber int64
	Subject       string
	Author        string
	Date          time.Time
	RawDate       string
	MessageID     string
	References    string
	Bytes         int64
	Lines         int64
}

type Endpoint struct {
	Address         string
	ServerName      string
	TLS             bool
	ConnectTimeout  time.Duration
	ReadTimeout     time.Duration
	OverviewCommand string
}
