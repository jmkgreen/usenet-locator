package jobs

import (
	"context"
	"errors"
	"testing"
)

type fakeClaimer struct {
	recovered bool
	job       Job
	claimed   bool
	err       error
}

func (f *fakeClaimer) RecoverInterrupted(context.Context) error { f.recovered = true; return f.err }
func (f *fakeClaimer) ClaimNext(context.Context) (Job, bool, error) {
	return f.job, f.claimed, f.err
}

func TestDispatcherRecoversWithoutExecutingWork(t *testing.T) {
	store := &fakeClaimer{}
	if err := NewDispatcher(store).Recover(context.Background()); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if !store.recovered {
		t.Fatal("Recover() did not call durable recovery")
	}
}

func TestDispatcherRunsOnlyClaimedJob(t *testing.T) {
	store := &fakeClaimer{job: Job{ID: "job-1"}, claimed: true}
	run := false
	claimed, err := NewDispatcher(store).RunOnce(context.Background(), func(_ context.Context, job Job) error {
		run = job.ID == "job-1"
		return nil
	})
	if err != nil || !claimed || !run {
		t.Fatalf("RunOnce() = claimed %v, ran %v, err %v", claimed, run, err)
	}
}

func TestDispatcherWrapsExecutorFailure(t *testing.T) {
	store := &fakeClaimer{job: Job{ID: "job-1"}, claimed: true}
	_, err := NewDispatcher(store).RunOnce(context.Background(), func(context.Context, Job) error { return errors.New("network down") })
	if err == nil || err.Error() != "execute job job-1: network down" {
		t.Fatalf("RunOnce() error = %v", err)
	}
}
