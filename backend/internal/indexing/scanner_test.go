package indexing

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/jmkgreen/usenet-locator/backend/internal/jobs"
	"github.com/jmkgreen/usenet-locator/backend/internal/nntp"
)

type fakeClient struct {
	group            nntp.Group
	records          map[int64]nntp.Overview
	overviewFailures int
	overviewCalls    int
	transferBytes    int64
	authenticateErr  error
	capabilitiesErr  error
	modeReaderErr    error
}

func (f *fakeClient) Authenticate(context.Context, string, string) error { return f.authenticateErr }
func (f *fakeClient) Capabilities(context.Context) ([]string, error)     { return nil, f.capabilitiesErr }
func (f *fakeClient) ModeReader(context.Context) error                   { return f.modeReaderErr }
func (f *fakeClient) Group(context.Context, string) (nntp.Group, error) {
	f.transferBytes += 3
	return f.group, nil
}
func (f *fakeClient) Overview(_ context.Context, low, high int64, receive func(nntp.Overview) error) error {
	f.overviewCalls++
	f.transferBytes += 7
	if f.overviewFailures > 0 {
		f.overviewFailures--
		return fmt.Errorf("temporary overview failure")
	}
	for n := low; n <= high; n++ {
		if item, ok := f.records[n]; ok {
			if err := receive(item); err != nil {
				return err
			}
		}
	}
	return nil
}
func (f *fakeClient) Body(context.Context, int64, int64, io.Writer) error { return nil }
func (f *fakeClient) Close() error                                        { return nil }
func (f *fakeClient) TransferBytes() int64                                { return f.transferBytes }

type fakeWriter struct{ batches []Batch }

func (f *fakeWriter) PersistBatch(_ context.Context, batch Batch) (Result, error) {
	f.batches = append(f.batches, batch)
	return Result{}, nil
}

type skippingWriter struct {
	fakeWriter
	skipped []Batch
}

func (f *skippingWriter) IsRangeCovered(context.Context, string, int64, int64) (bool, error) {
	return true, nil
}
func (f *skippingWriter) AdvanceCovered(_ context.Context, batch Batch) error {
	f.skipped = append(f.skipped, batch)
	return nil
}

func TestScannerDiscoversAndFiltersInBoundedBatches(t *testing.T) {
	date := func(day int) time.Time { return time.Date(2020, 1, day, 0, 0, 0, 0, time.UTC) }
	client := &fakeClient{group: nntp.Group{Low: 1, High: 5}, records: map[int64]nntp.Overview{1: {ArticleNumber: 1, Date: date(1), MessageID: "<1@test>"}, 2: {ArticleNumber: 2, Date: date(2), MessageID: "<2@test>"}, 3: {ArticleNumber: 3, Date: date(3), MessageID: "<3@test>"}, 4: {ArticleNumber: 4, Date: date(4), MessageID: "<4@test>"}, 5: {ArticleNumber: 5, Date: date(5), MessageID: "<5@test>"}}}
	writer := &fakeWriter{}
	job := jobs.Job{ID: "job", Newsgroup: "example", StartDate: date(2), EndDate: date(3), MarginDays: 0}
	if err := (Scanner{Client: client, Writer: writer, BatchSize: 2}).Scan(context.Background(), job); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(writer.batches) != 1 || writer.batches[0].RangeStart != 2 || writer.batches[0].RangeEnd != 3 || len(writer.batches[0].Overviews) != 2 {
		t.Fatalf("batches = %#v", writer.batches)
	}
}

func TestScannerAdvancesAlreadyCoveredBatchWithoutDuplicateBatchRead(t *testing.T) {
	date := func(day int) time.Time { return time.Date(2020, 1, day, 0, 0, 0, 0, time.UTC) }
	client := &fakeClient{group: nntp.Group{Low: 1, High: 2}, records: map[int64]nntp.Overview{1: {ArticleNumber: 1, Date: date(1)}, 2: {ArticleNumber: 2, Date: date(2)}}}
	writer := &skippingWriter{}
	job := jobs.Job{ID: "job", Newsgroup: "example", StartDate: date(1), EndDate: date(2)}
	if err := (Scanner{Client: client, Writer: writer, BatchSize: 2}).Scan(context.Background(), job); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(writer.skipped) != 1 || len(writer.batches) != 0 {
		t.Fatalf("skipped = %#v, fetched = %#v", writer.skipped, writer.batches)
	}
}

func TestScannerRetriesTransientOverviewFailure(t *testing.T) {
	date := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	client := &fakeClient{group: nntp.Group{Low: 1, High: 1}, records: map[int64]nntp.Overview{1: {ArticleNumber: 1, Date: date}}, overviewFailures: 1}
	writer := &fakeWriter{}
	job := jobs.Job{ID: "job", Newsgroup: "example", StartDate: date, EndDate: date}
	if err := (Scanner{Client: client, Writer: writer, BatchSize: 1, RetryAttempts: 3, RetryBaseDelay: time.Nanosecond}).Scan(context.Background(), job); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if client.overviewCalls < 2 || len(writer.batches) != 1 {
		t.Fatalf("calls = %d, batches = %#v", client.overviewCalls, writer.batches)
	}
}

func TestScannerRecordsMeasuredProtocolTransfers(t *testing.T) {
	date := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	client := &fakeClient{group: nntp.Group{Low: 1, High: 1}, records: map[int64]nntp.Overview{1: {ArticleNumber: 1, Date: date}}}
	var charged int64
	scanner := Scanner{Client: client, Writer: &fakeWriter{}, BatchSize: 1, RecordTransfer: func(_ context.Context, bytes int64) error {
		charged += bytes
		return nil
	}}
	if err := scanner.Scan(context.Background(), jobs.Job{ID: "job", Newsgroup: "example", StartDate: date, EndDate: date}); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if charged != client.transferBytes || charged == 0 {
		t.Fatalf("charged = %d, measured = %d", charged, client.transferBytes)
	}
}
