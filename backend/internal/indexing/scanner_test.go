package indexing

import (
	"context"
	"errors"
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
	partialFailure   bool
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
		if f.partialFailure {
			if item, ok := f.records[low]; ok {
				if err := receive(item); err != nil {
					return err
				}
			}
		}
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

func TestOverviewDiscardsRecordsFromFailedAttempt(t *testing.T) {
	client := &fakeClient{records: map[int64]nntp.Overview{1: {ArticleNumber: 1}}, overviewFailures: 1, partialFailure: true}
	var got []nntp.Overview
	err := (Scanner{Client: client, RetryBaseDelay: time.Nanosecond}).overview(context.Background(), 1, 1, func(item nntp.Overview) error { got = append(got, item); return nil })
	if err != nil || len(got) != 1 || client.overviewCalls != 2 {
		t.Fatalf("got %#v after %d calls, error %v", got, client.overviewCalls, err)
	}
}

func TestDiscoveryPropagatesQuotaFailure(t *testing.T) {
	client := &fakeClient{group: nntp.Group{Low: 1, High: 2}, records: map[int64]nntp.Overview{1: {ArticleNumber: 1}}}
	quotaError := errors.New("quota")
	calls := 0
	scanner := Scanner{Client: client, RecordTransfer: func(context.Context, int64) error {
		calls++
		if calls == 2 {
			return quotaError
		}
		return nil
	}}
	_, err := scanner.Discover(context.Background(), jobs.Job{Newsgroup: "example", StartDate: time.Now(), EndDate: time.Now()})
	if !errors.Is(err, quotaError) {
		t.Fatalf("expected quota error, got %v", err)
	}
}

func TestScannerStopsBeforeNextRequestWhenQuotaReached(t *testing.T) {
	client := &fakeClient{group:nntp.Group{Low:1, High:2}, records:map[int64]nntp.Overview{1:{ArticleNumber:1}}}
	quotaError := errors.New("quota reached")
	checks := 0
	scanner := Scanner{Client:client, Writer:&fakeWriter{}, BatchSize:1, CheckTransfer:func(context.Context) error {
		checks++
		if checks > 1 { return quotaError }
		return nil
	}}
	err := scanner.Scan(context.Background(),jobs.Job{ID:"job",Newsgroup:"comp.test",StartDate:time.Now(),EndDate:time.Now()})
	if !errors.Is(err,quotaError) || client.overviewCalls != 0 || client.transferBytes != 3 { t.Fatalf("err=%v overview calls=%d transfer=%d",err,client.overviewCalls,client.transferBytes) }
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
	if len(writer.batches) != 3 || writer.batches[0].RangeStart != 1 || writer.batches[2].RangeEnd != 5 || len(writer.batches[0].Overviews) != 1 || len(writer.batches[1].Overviews) != 1 {
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
