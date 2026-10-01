package jobs

import (
	"testing"
	"time"
)

func request() CreateRequest {
	return CreateRequest{NewsgroupID: "comp.lang.go", EndpointID: "primary", StartDate: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), MarginDays: 2}
}

func TestCreateRequestAllowsInclusiveSingleDayRange(t *testing.T) {
	if err := request().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestCreateRequestRejectsReversedRange(t *testing.T) {
	r := request()
	r.StartDate = r.EndDate.AddDate(0, 0, 1)
	if err := r.Validate(); err == nil {
		t.Fatal("Validate() accepted reversed range")
	}
}

func TestCreateRequestRejectsNonMidnightUTCDate(t *testing.T) {
	r := request()
	r.EndDate = r.EndDate.Add(time.Hour)
	if err := r.Validate(); err == nil {
		t.Fatal("Validate() accepted a non-midnight UTC date")
	}
}

func TestCreateRequestRejectsNonPositiveTransferLimit(t *testing.T) {
	r := request()
	limit := int64(0)
	r.TransferLimitBytes = &limit
	if err := r.Validate(); err == nil {
		t.Fatal("Validate() accepted a non-positive transfer limit")
	}
}

func TestInterruptedJobsRequireManualResume(t *testing.T) {
	if !CanTransition(Interrupted, Queued) || CanTransition(Interrupted, Completed) {
		t.Fatal("unexpected interrupted-job transition policy")
	}
}
